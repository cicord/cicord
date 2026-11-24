// Package disgoadapter implements discord.Connector / discord.Session on top of
// github.com/disgoorg/disgo.
//
// It is the only package in Cicord where the Discord API word "guild" appears
// (ADR-0009): everything it returns uses Cicord's vocabulary ("server").
//
// Layering (see wire.go): disgo's rest.Client carries every REST call (auth,
// endpoint definitions, per-route and global rate limiting with 429 retries) and
// disgo's gateway.Gateway carries the websocket (identify, heartbeats, resume,
// reconnect with backoff, zstd compression). Payloads are encoded/decoded with the
// adapter's own wire structs. disgo's bot, voice, sharding and httpserver packages
// are deliberately not imported: they pull voice / DAVE code Cicord does not need,
// and the binary must stay CGO_ENABLED=0 friendly.
package disgoadapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"

	"github.com/cicord/cicord/internal/discord"
)

const userAgent = "DiscordBot (https://github.com/cicord/cicord, 0)"

// Option configures a Connector.
type Option func(*config)

type config struct {
	logger         *slog.Logger
	restURL        string
	httpClient     *http.Client
	gatewayURL     string
	memberChunking *bool
	maxRetries     int
	// gatewayCompression overrides disgo's default (zstd-stream); tests only.
	gatewayCompression *gateway.CompressionType
}

// WithLogger sets the logger used by the adapter and by disgo. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(c *config) { c.logger = l } }

// WithRESTURL overrides the REST base URL (default https://discord.com/api/v10).
// Useful to point the adapter at an httptest server.
func WithRESTURL(u string) Option {
	return func(c *config) { c.restURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the HTTP client used for REST calls (default: 20s timeout).
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.httpClient = h } }

// WithGatewayURL overrides the gateway websocket URL (default wss://gateway.discord.gg).
func WithGatewayURL(u string) Option { return func(c *config) { c.gatewayURL = u } }

// WithMemberChunking forces member chunking on or off. By default the adapter
// requests the full member list of every server on GUILD_CREATE when the
// ServerMembers intent is enabled, so MemberUpdateEvent.Previous is available for
// every member; without it, Previous is only set for members seen since connect.
func WithMemberChunking(enabled bool) Option {
	return func(c *config) { c.memberChunking = &enabled }
}

// WithMaxRetries sets how many times a request answered with 429 is retried by
// disgo's rate limiter (default 10).
func WithMaxRetries(n int) Option { return func(c *config) { c.maxRetries = n } }

type connector struct{ cfg config }

// NewConnector returns a discord.Connector backed by disgo.
func NewConnector(opts ...Option) discord.Connector {
	cfg := config{}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	return &connector{cfg: cfg}
}

// Connect validates the token (GET /users/@me and /oauth2/applications/@me) and
// returns a session whose gateway is not opened yet.
func (c *connector) Connect(ctx context.Context, token string) (discord.Session, error) {
	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bot "))
	if token == "" {
		return nil, fmt.Errorf("discord connect: %w: empty token", discord.ErrInvalidToken)
	}
	s := newSession(c.cfg, token)
	var me wireUser
	if err := s.do(ctx, "Connect", rest.GetCurrentUser.Compile(nil), nil, &me); err != nil {
		s.rest.Close(context.Background())
		return nil, err
	}
	var app wireApplication
	if err := s.do(ctx, "Connect", rest.GetBotApplicationInfo.Compile(nil), nil, &app); err != nil {
		s.rest.Close(context.Background())
		return nil, err
	}
	s.self = userFromWire(me)
	s.appID, s.appFlags = app.ID, app.Flags
	return s, nil
}

// session implements discord.Session.
type session struct {
	cfg    config
	token  string
	logger *slog.Logger
	rest   rest.Client

	self     discord.User
	appID    string
	appFlags int

	members *memberCache

	mu       sync.Mutex
	gw       gateway.Gateway
	queue    *eventQueue
	done     chan struct{} // closed when the dispatcher exits
	presence *gateway.MessageDataPresenceUpdate
	chunk    bool
	closed   bool
}

var (
	_ discord.Session                = (*session)(nil)
	_ discord.ApplicationFlagsReader = (*session)(nil)
)

func newSession(cfg config, token string) *session {
	logger := cfg.logger.With(slog.String("component", "discord"))
	ropts := []rest.ClientConfigOpt{
		rest.WithLogger(logger),
		rest.WithUserAgent(userAgent),
		rest.WithRateLimiterConfigOpts(rest.WithRateLimiterLogger(logger)),
	}
	if cfg.restURL != "" {
		ropts = append(ropts, rest.WithURL(cfg.restURL))
	}
	if cfg.httpClient != nil {
		ropts = append(ropts, rest.WithHTTPClient(cfg.httpClient))
	}
	if cfg.maxRetries > 0 {
		ropts = append(ropts, rest.WithRateLimiterConfigOpts(rest.WithMaxRetries(cfg.maxRetries)))
	}
	return &session{
		cfg:     cfg,
		token:   token,
		logger:  logger,
		rest:    rest.NewClient(token, ropts...),
		members: newMemberCache(),
	}
}

func (s *session) Self() discord.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.self
}
func (s *session) ApplicationID() string { return s.appID }

// ApplicationFlags are the application flags read by Connect (privileged intents).
func (s *session) ApplicationFlags() int { return s.appFlags }

// do performs one REST call through disgo's client (which blocks on rate limits
// and retries 429s) and maps the error.
func (s *session) do(ctx context.Context, op string, ep *rest.CompiledEndpoint, body, out any, reason ...string) error {
	opts := []rest.RequestOpt{rest.WithCtx(ctx)}
	if len(reason) > 0 && reason[0] != "" {
		opts = append(opts, rest.WithReason(reason[0]))
	}
	return mapError(op, s.rest.Do(ep, body, out, opts...))
}

// ───────────────────────────────────────────────────────────── gateway

// Open connects to the gateway with the given intents and blocks until READY (or
// ctx is done). ctx only bounds the handshake: the connection then lives until
// Close, reconnecting and resuming on its own. Events are translated on disgo's
// reader goroutine, pushed to an unbounded FIFO queue, and handed to handler one
// at a time from a single dispatcher goroutine, so a slow handler never stalls the
// websocket reader and ordering is preserved.
func (s *session) Open(ctx context.Context, intents discord.Intents, handler func(discord.Event)) error {
	if handler == nil {
		return fmt.Errorf("discord open: %w: nil handler", discord.ErrBadRequest)
	}
	if intents > math.MaxInt64 {
		return fmt.Errorf("discord open: %w: invalid intents %#x", discord.ErrBadRequest, uint64(intents))
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("discord open: session closed")
	}
	if s.gw != nil {
		s.mu.Unlock()
		return errors.New("discord open: gateway already open")
	}
	s.chunk = intents.Has(discord.MustIntents("ServerMembers"))
	if s.cfg.memberChunking != nil {
		s.chunk = s.chunk && *s.cfg.memberChunking
	}
	q := newEventQueue()
	done := make(chan struct{})
	go func() {
		defer close(done)
		q.run(handler, s.logger)
	}()

	gopts := []gateway.ConfigOpt{
		gateway.WithLogger(s.logger),
		gateway.WithIntents(gateway.Intents(intents)),
		gateway.WithEnableRawEvents(true),
		gateway.WithOS("linux"),
		gateway.WithBrowser("cicord"),
		gateway.WithDevice("cicord"),
	}
	if s.cfg.gatewayURL != "" {
		gopts = append(gopts, gateway.WithURL(s.cfg.gatewayURL))
	}
	if s.cfg.gatewayCompression != nil {
		gopts = append(gopts, gateway.WithCompression(*s.cfg.gatewayCompression))
	}
	if s.presence != nil {
		p := *s.presence
		gopts = append(gopts, gateway.WithPresenceOpts(func(u *gateway.MessageDataPresenceUpdate) { *u = p }))
	}
	// Only called for non-resumable closes (e.g. 4004 after a token reset, 4014
	// after intents were revoked): the session is then permanently disconnected.
	gopts = append(gopts, gateway.WithCloseHandler(func(_ gateway.Gateway, err error, _ bool) {
		s.logger.Error("discord: gateway closed permanently", slog.Any("err", mapGatewayError(err)))
	}))
	gw := gateway.New(s.token, s.onGatewayEvent, gopts...)
	s.gw, s.queue, s.done = gw, q, done
	s.mu.Unlock()

	if err := gw.Open(ctx); err != nil {
		s.mu.Lock()
		s.gw, s.queue, s.done = nil, nil, nil
		s.mu.Unlock()
		q.close()
		<-done
		return mapGatewayError(err)
	}
	return nil
}

// onGatewayEvent runs on disgo's reader goroutine. Only raw payloads are used:
// they are the exact Discord JSON, decoded into wire structs by translate.
func (s *session) onGatewayEvent(gw gateway.Gateway, t gateway.EventType, _ int, data gateway.EventData) {
	if t != gateway.EventTypeRaw {
		return
	}
	raw, ok := data.(gateway.EventRaw)
	if !ok || raw.Payload == nil {
		return
	}
	payload, err := readAll(raw.Payload)
	if err != nil {
		s.logger.Warn("discord: reading gateway payload", slog.Any("err", err))
		return
	}
	evs, err := s.translate(string(raw.EventType), payload)
	if err != nil {
		s.logger.Warn("discord: decoding gateway event", slog.String("event", string(raw.EventType)), slog.Any("err", err))
		return
	}
	s.mu.Lock()
	q := s.queue
	s.mu.Unlock()
	if q == nil {
		return
	}
	for _, e := range evs {
		q.push(e)
	}
}

// requestMembers asks the gateway for the full member list of a server
// (results arrive as GUILD_MEMBERS_CHUNK and only feed the member cache).
func (s *session) requestMembers(serverID string) {
	s.mu.Lock()
	gw := s.gw
	s.mu.Unlock()
	if gw == nil {
		return
	}
	id, err := parseSnowflake(serverID)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		query, limit := "", 0
		// The gateway is READY by the time GUILD_CREATE is dispatched, but Send may
		// race with a reconnect; a failed request only means Previous is unavailable.
		if err := gw.Send(ctx, gateway.OpcodeRequestGuildMembers, gateway.MessageDataRequestGuildMembers{
			GuildID: id,
			Query:   &query,
			Limit:   &limit,
		}); err != nil {
			s.logger.Debug("discord: member chunk request failed", slog.String("server", serverID), slog.Any("err", err))
		}
	}()
}

// SetPresence updates the bot presence. Before Open, it is stored and sent with
// IDENTIFY.
func (s *session) SetPresence(ctx context.Context, p discord.Presence) error {
	data, err := presenceToGateway(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.presence = &data
	gw := s.gw
	s.mu.Unlock()
	if gw == nil {
		return nil
	}
	if err := gw.Send(ctx, gateway.OpcodePresenceUpdate, data); err != nil {
		return fmt.Errorf("discord SetPresence: %w", err)
	}
	return nil
}

// Close closes the gateway, stops the dispatcher (pending events are dropped) and
// releases the REST client. It is idempotent. It waits for an in-flight handler
// call to return, bounded by ctx; do not call it with a context that never ends
// from inside the event handler.
func (s *session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	gw, q, done := s.gw, s.queue, s.done
	s.gw, s.queue = nil, nil
	s.mu.Unlock()

	if gw != nil {
		gw.Close(ctx)
	}
	if q != nil {
		q.close()
	}
	var err error
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	s.rest.Close(ctx)
	return err
}
