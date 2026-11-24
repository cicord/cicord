// Package setup implements Cicord's setup wizard (docs/onboarding-wizard.md): the guided
// path from nothing to a synchronized Discord server with a first plugin.
//
// The wizard is a small state machine persisted in the state store (KV scope
// "cicord:setup"), so it resumes after a restart. Every step is validated by the runtime
// before moving on: the bot token is checked against Discord, the server membership,
// permissions and role hierarchy are checked live, the imported configuration is planned
// (it must be empty: everything is adopted) before the runtime activates it, and the
// final checklist (Checker) is also the admin Diagnostics page.
//
// Access is protected by a one-time setup token printed in the logs, exchanged for a
// setup session (see Wizard.IssueToken / Exchange). The package has no HTTP code: the
// handlers live in internal/api.
package setup

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/gitauth"
	"github.com/cicord/cicord/internal/importer"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/version"
	"github.com/cicord/cicord/internal/writeback"
)

// Steps, in order.
const (
	StepWelcome    = "welcome"
	StepDiscord    = "discord"
	StepServer     = "server"
	StepRepository = "repository"
	StepLogin      = "login"
	StepVerify     = "verify"
	StepPlugin     = "plugin"
	StepDone       = "done"
)

// Steps lists the wizard steps in order.
var Steps = []string{StepWelcome, StepDiscord, StepServer, StepRepository, StepLogin, StepVerify, StepPlugin, StepDone}

// OAuthSecretName is the secret holding the Discord OAuth2 client secret.
const OAuthSecretName = "CICORD_DISCORD_CLIENT_SECRET" //nolint:gosec // G101: the name of a secret, not its value

const (
	stateScope = "cicord:setup"
	stateKey   = "state"
	// SessionMethod marks setup sessions in the session store.
	SessionMethod = "setup"
	// SessionUserID is the user ID of setup sessions.
	SessionUserID = "setup"
	sessionTTL    = 24 * time.Hour
)

// Error is a wizard error explained for people.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details string `json:"details,omitempty"`
	Link    string `json:"link,omitempty"`
	// Diagnostics of an invalid configuration (plugin install).
	Diagnostics config.Diagnostics `json:"diagnostics,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func errorf(status int, code, hint, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...), Hint: hint}
}

// ───────────────────────────────────────────────────────────── state

// BotState is the Discord application chosen in the Discord step.
type BotState struct {
	Key             string `json:"key"`
	SecretRef       string `json:"secretRef"`
	ApplicationID   string `json:"applicationId"`
	ApplicationName string `json:"applicationName"`
	UserID          string `json:"userId"`
	Username        string `json:"username"`
	AvatarURL       string `json:"avatarUrl,omitempty"`
	Flags           int    `json:"flags"`
}

// ServerChoice is the server picked in the Server step.
type ServerChoice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IconURL string `json:"iconUrl,omitempty"`
	Key     string `json:"key"`
}

// LoginState is the optional admin login step.
type LoginState struct {
	Configured bool   `json:"configured"`
	Skipped    bool   `json:"skipped"`
	TestedAs   string `json:"testedAs,omitempty"`
}

// PluginState is the first plugin installed.
type PluginState struct {
	Name     string `json:"name"`
	Instance string `json:"instance"`
	Command  string `json:"command,omitempty"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// State is the persisted wizard progress. It never contains secret values.
type State struct {
	Step            string          `json:"step"`
	Intent          string          `json:"intent,omitempty"` // "new" | "existing"
	Bot             *BotState       `json:"bot,omitempty"`
	Server          *ServerChoice   `json:"server,omitempty"`
	Repo            *RepoConfig     `json:"repo,omitempty"`
	RepoInitialized bool            `json:"repoInitialized,omitempty"`
	Activated       bool            `json:"activated,omitempty"`
	Revision        string          `json:"revision,omitempty"`
	FirstPlan       *reconcile.Plan `json:"firstPlan,omitempty"`
	Login           LoginState      `json:"login"`
	VerifyStarted   time.Time       `json:"verifyStarted,omitempty"`
	Plugin          *PluginState    `json:"plugin,omitempty"`
	// PublicURL is the URL the wizard ran on (serve): a restart without --public-url
	// keeps using it (OAuth redirect, portal links).
	PublicURL   string     `json:"publicUrl,omitempty"`
	Completed   bool       `json:"completed,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Sessions    []string   `json:"sessions,omitempty"` // setup session IDs (revoked on completion)
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// LoadState reads the persisted wizard state (zero State, false when none).
func LoadState(ctx context.Context, st state.Store) (State, bool, error) {
	raw, err := st.KV(stateScope).Get(ctx, stateKey)
	if errors.Is(err, state.ErrNotFound) {
		return State{Step: StepWelcome}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, false, fmt.Errorf("setup state: %w", err)
	}
	if s.Step == "" {
		s.Step = StepWelcome
	}
	return s, true, nil
}

func saveState(ctx context.Context, st state.Store, s State) error {
	s.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return st.KV(stateScope).Set(ctx, stateKey, data, 0)
}

// ───────────────────────────────────────────────────────────── wizard

// Activation is the running configuration after the Repository step.
type Activation struct {
	Runtime *runtime.Runtime
	Writer  writeback.Writer
	Checker *Checker
}

// Options configures a Wizard.
type Options struct {
	// Mode is "serve" or "dev".
	Mode      string
	Store     state.Store
	Secrets   *secrets.Store
	Connector discord.Connector
	Probe     Probe
	Catalog   *catalog.Catalog
	// Providers are the repository providers offered, the first one is the default.
	Providers []string
	DataDir   string
	// Dir is the directory `cicord dev` watches (local provider).
	Dir       string
	PublicURL string
	// AdminURL is the admin console URL when it differs from PublicURL (serve
	// --admin-url): the wizard and its login run there, so Discord must know both
	// callbacks.
	AdminURL string
	// Activate starts the runtime on a new repository (no process restart) and swaps the
	// HTTP handler to the full API.
	Activate func(ctx context.Context, repo RepoConfig) (*Activation, error)
	// Sim is the simulated Discord of `cicord dev --sim --setup`.
	Sim *SimEnv
	// Download fetches the server's files from the Discord CDN during the import
	// (default: the simulator's CDN with Sim, HTTP otherwise).
	Download importer.Downloader
	// Env looks environment variables up (default os.LookupEnv).
	Env func(string) (string, bool)
	// GitHubAppKeyFile is the GitHub App private key file of `cicord serve
	// --github-app-key-file` (it takes precedence over the stored key).
	GitHubAppKeyFile string
	// GitHubAPI overrides https://api.github.com and HTTPClient the client of the GitHub
	// App token exchange (tests).
	GitHubAPI  string
	HTTPClient *http.Client
	Logger     *slog.Logger
	// ActivationTimeout bounds the wait for a plugin install to become active (default 30s).
	ActivationTimeout time.Duration
}

// Wizard is safe for concurrent use; mutating operations are serialized.
type Wizard struct {
	opts Options
	log  *slog.Logger

	mu        sync.Mutex
	tokenHash [32]byte
	tokenSet  bool

	actMu sync.RWMutex
	act   *Activation

	sessMu   sync.Mutex
	sess     discord.Session
	sessHash string
}

// New returns a wizard.
func New(opts Options) *Wizard {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Env == nil {
		opts.Env = os.LookupEnv
	}
	if len(opts.Providers) == 0 {
		opts.Providers = []string{ProviderEmbedded, ProviderGitHub}
	}
	if opts.Catalog == nil {
		opts.Catalog = catalog.Official()
	}
	if opts.ActivationTimeout == 0 {
		opts.ActivationTimeout = 30 * time.Second
	}
	if opts.Download == nil {
		if opts.Sim != nil {
			opts.Download = opts.Sim.World.Download
		} else {
			opts.Download = importer.HTTPDownloader(nil)
		}
	}
	return &Wizard{opts: opts, log: opts.Logger}
}

// Resume attaches the running configuration after a restart and marks the setup
// activated when the repository had been created but the activation did not finish
// before the restart (the runtime now runs it).
func (w *Wizard) Resume(ctx context.Context, a *Activation) error {
	w.Attach(a)
	_, err := w.update(ctx, func(s *State) error {
		if !s.Activated && s.RepoInitialized {
			s.Activated = true
			if rev, ok := a.Runtime.Revision(); ok {
				s.Revision = rev.ID
			}
		}
		return nil
	})
	return err
}

// Attach sets the running configuration (boot after activation, before the wizard
// completed).
func (w *Wizard) Attach(a *Activation) {
	w.actMu.Lock()
	w.act = a
	w.actMu.Unlock()
}

func (w *Wizard) activation() *Activation {
	w.actMu.RLock()
	defer w.actMu.RUnlock()
	return w.act
}

// Simulator reports whether the wizard runs against the simulator.
func (w *Wizard) Simulator() bool { return w.opts.Sim != nil }

// SimEnv returns the simulator environment (nil outside `cicord dev --sim --setup`).
func (w *Wizard) SimEnv() *SimEnv { return w.opts.Sim }

// Catalog returns the plugin catalog.
func (w *Wizard) Catalog() *catalog.Catalog { return w.opts.Catalog }

func (w *Wizard) state(ctx context.Context) (State, error) {
	s, _, err := LoadState(ctx, w.opts.Store)
	return s, err
}

// Completed reports whether the wizard finished (setup endpoints answer 410).
func (w *Wizard) Completed(ctx context.Context) bool {
	s, err := w.state(ctx)
	return err == nil && s.Completed
}

// ───────────────────────────────────────────────────────────── setup token & sessions

// IssueToken mints a new one-time setup token (only its hash is kept) and returns it.
func (w *Wizard) IssueToken() string {
	tok := auth.RandomID(32)
	w.mu.Lock()
	w.tokenHash, w.tokenSet = sha256.Sum256([]byte(tok)), true
	w.mu.Unlock()
	return tok
}

// SetupURL is the ready-to-click URL printed in the logs.
func SetupURL(publicURL, token string) string {
	return strings.TrimRight(publicURL, "/") + "/admin/setup?token=" + url.QueryEscape(token)
}

// Exchange trades the setup token for a setup session (the token is consumed).
func (w *Wizard) Exchange(ctx context.Context, token string) (state.Session, error) {
	if w.Completed(ctx) {
		return state.Session{}, errorf(http.StatusGone, "gone", "", "Setup is already complete.")
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	w.mu.Lock()
	ok := w.tokenSet && subtle.ConstantTimeCompare(sum[:], w.tokenHash[:]) == 1
	if ok {
		w.tokenSet = false // one-time
	}
	w.mu.Unlock()
	if !ok {
		return state.Session{}, errorf(http.StatusUnauthorized, "unauthenticated",
			"Use the link printed in the Cicord logs when it started (each link works once). Restart Cicord to get a new one.",
			"This setup link is invalid or was already used.")
	}
	data, _ := json.Marshal(map[string]any{"method": SessionMethod, "username": "setup"})
	sess := state.Session{ID: auth.RandomID(32), UserID: SessionUserID, Data: data, ExpiresAt: time.Now().Add(sessionTTL)}
	if err := w.opts.Store.Sessions().Put(ctx, sess); err != nil {
		return state.Session{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.state(ctx)
	if err != nil {
		return state.Session{}, err
	}
	s.Sessions = append(s.Sessions, sess.ID)
	if len(s.Sessions) > 20 {
		s.Sessions = s.Sessions[len(s.Sessions)-20:]
	}
	return sess, saveState(ctx, w.opts.Store, s)
}

// ValidSession reports whether id is a live setup session of an unfinished setup.
func (w *Wizard) ValidSession(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	sess, err := w.opts.Store.Sessions().Get(ctx, id)
	if err != nil || sess.UserID != SessionUserID {
		return false
	}
	var d struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(sess.Data, &d)
	return d.Method == SessionMethod && !w.Completed(ctx)
}

// ───────────────────────────────────────────────────────────── view

// View is the wizard state for the UI (no secret values).
type View struct {
	Step      string   `json:"step"`
	Steps     []string `json:"steps"`
	Completed bool     `json:"completed"`
	Mode      string   `json:"mode"`
	Simulator bool     `json:"simulator"`
	Version   string   `json:"version"`
	Intent    string   `json:"intent,omitempty"`
	// Providers are the repository choices (first = default).
	Providers []string `json:"providers"`
	PublicURL string   `json:"publicUrl"`

	Bot       *BotView        `json:"bot,omitempty"`
	InviteURL string          `json:"inviteUrl,omitempty"`
	Server    *ServerChoice   `json:"server,omitempty"`
	Repo      *RepoView       `json:"repo,omitempty"`
	Activated bool            `json:"activated"`
	Revision  string          `json:"revision,omitempty"`
	FirstPlan *reconcile.Plan `json:"firstPlan,omitempty"`
	Login     LoginView       `json:"login"`
	Plugin    *PluginState    `json:"plugin,omitempty"`
}

// BotView describes the configured bot.
type BotView struct {
	BotState
	TokenStored bool   `json:"tokenStored"`
	PortalURL   string `json:"portalUrl"`
	Intents     Check  `json:"intents"`
}

// RepoView describes the chosen repository.
type RepoView struct {
	Provider    string `json:"provider"`
	URL         string `json:"url,omitempty"`
	Branch      string `json:"branch"`
	TokenStored bool   `json:"tokenStored,omitempty"`
	// Auth is "token" or "githubApp" (github provider); AppID is the GitHub App's ID.
	Auth  string `json:"auth,omitempty"`
	AppID string `json:"appId,omitempty"`
}

// LoginView describes the admin login step.
type LoginView struct {
	LoginState
	ClientID string `json:"clientId,omitempty"`
	// RedirectURL is the callback of the host the wizard runs on (the admin URL when
	// set); RedirectURLs every callback Discord must know (public and admin).
	RedirectURL  string   `json:"redirectUrl"`
	RedirectURLs []string `json:"redirectUrls"`
	PortalURL    string   `json:"portalUrl"`
	// FromEnv: the client secret comes from the environment (nothing to paste).
	FromEnv bool `json:"fromEnv"`
}

// InviteURL is the OAuth2 URL that adds the bot to a server with Administrator.
func InviteURL(appID string) string {
	q := url.Values{}
	q.Set("client_id", appID)
	q.Set("scope", "bot applications.commands")
	q.Set("permissions", "8")
	q.Set("integration_type", "0")
	return "https://discord.com/oauth2/authorize?" + q.Encode()
}

// Status returns the wizard view.
func (w *Wizard) Status(ctx context.Context) (View, error) {
	s, err := w.state(ctx)
	if err != nil {
		return View{}, err
	}
	return w.view(ctx, s), nil
}

func (w *Wizard) view(ctx context.Context, s State) View {
	v := View{Step: s.Step, Steps: Steps, Completed: s.Completed, Mode: w.opts.Mode, Simulator: w.opts.Sim != nil,
		Version: version.Version, Intent: s.Intent, Providers: w.opts.Providers, PublicURL: w.opts.PublicURL,
		Server: s.Server, Activated: s.Activated, Revision: s.Revision, FirstPlan: s.FirstPlan, Plugin: s.Plugin}
	if b := s.Bot; b != nil {
		v.Bot = &BotView{BotState: *b, TokenStored: w.opts.Secrets.Has(ctx, b.SecretRef), PortalURL: PortalBotURL(b.ApplicationID),
			Intents: IntentsCheck(b.ApplicationID, b.Flags, 0, RecommendedIntents)}
		v.InviteURL = InviteURL(b.ApplicationID)
	}
	if r := s.Repo; r != nil {
		v.Repo = w.repoView(ctx, *r)
	}
	v.Login = LoginView{LoginState: s.Login, RedirectURL: w.redirectURL(), RedirectURLs: w.redirectURLs()}
	if s.Bot != nil {
		v.Login.ClientID = s.Bot.ApplicationID
		v.Login.PortalURL = PortalOAuthURL(s.Bot.ApplicationID)
	}
	if val, ok := w.opts.Env(OAuthSecretName); ok && val != "" {
		v.Login.FromEnv, v.Login.Configured = true, true
	}
	return v
}

func (w *Wizard) update(ctx context.Context, fn func(*State) error) (View, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.state(ctx)
	if err != nil {
		return View{}, err
	}
	if s.Completed {
		return View{}, errorf(http.StatusGone, "gone", "", "Setup is already complete.")
	}
	if err := fn(&s); err != nil {
		return View{}, err
	}
	if err := saveState(ctx, w.opts.Store, s); err != nil {
		return View{}, err
	}
	return w.view(ctx, s), nil
}

// SetIntent records the Welcome choice ("new" community or "existing" server) and moves
// to the Discord step.
func (w *Wizard) SetIntent(ctx context.Context, intent string) (View, error) {
	if intent != "new" && intent != "existing" {
		return View{}, errorf(http.StatusBadRequest, "invalid", "", `intent must be "new" or "existing"`)
	}
	return w.update(ctx, func(s *State) error {
		s.Intent = intent
		if s.Step == StepWelcome {
			s.Step = StepDiscord
		}
		return nil
	})
}

// Goto moves to a step whose prerequisites are met.
func (w *Wizard) Goto(ctx context.Context, step string) (View, error) {
	if !slices.Contains(Steps, step) {
		return View{}, errorf(http.StatusBadRequest, "invalid", "", "unknown step %q", step)
	}
	// Server checks run outside the state lock (Discord calls).
	if step == StepRepository {
		s, err := w.state(ctx)
		if err != nil {
			return View{}, err
		}
		if s.Server == nil || s.Bot == nil {
			return View{}, errorf(http.StatusConflict, "conflict", "Pick the server first.", "No server is selected yet.")
		}
		if !s.Activated {
			checks, err := w.serverChecks(ctx, s)
			if err != nil {
				return View{}, err
			}
			for _, c := range checks {
				if c.Blocking() {
					return View{}, &Error{Status: http.StatusConflict, Code: "conflict", Message: c.Message, Hint: c.Hint}
				}
			}
		}
	}
	return w.update(ctx, func(s *State) error {
		idx := slices.Index(Steps, step)
		switch {
		case idx >= slices.Index(Steps, StepLogin) && !s.Activated:
			return errorf(http.StatusConflict, "conflict", "Finish the Repository step first.", "The configuration is not active yet.")
		case idx >= slices.Index(Steps, StepServer) && s.Bot == nil:
			return errorf(http.StatusConflict, "conflict", "Paste the bot token first.", "No Discord application is configured yet.")
		case idx <= slices.Index(Steps, StepRepository) && s.Activated && idx > slices.Index(Steps, StepWelcome):
			return errorf(http.StatusConflict, "conflict", "", "The configuration is already active: the earlier steps cannot be changed from the wizard.")
		}
		if step == StepVerify && s.VerifyStarted.IsZero() {
			s.VerifyStarted = time.Now().UTC()
		}
		if step == StepDone {
			if s.Plugin == nil {
				s.Plugin = &PluginState{Skipped: true}
			}
		}
		s.Step = step
		return nil
	})
}

// ───────────────────────────────────────────────────────────── Discord application

func (w *Wizard) secret(name string) (string, bool) {
	if v, ok := w.opts.Env(name); ok && v != "" {
		return v, true
	}
	v, err := w.opts.Secrets.Get(context.Background(), name)
	return v, err == nil
}

func secretRefFor(botKey string) string {
	return "DISCORD_TOKEN_" + strings.ToUpper(strings.ReplaceAll(botKey, "-", "_"))
}

func explainProbeErr(err error) *Error {
	switch {
	case errors.Is(err, discord.ErrInvalidToken):
		return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_token",
			Message: "Discord rejected this token.",
			Hint: "In the Developer Portal open your application → Bot, click “Reset Token”, copy the new token (Discord shows it only once) and paste it here. " +
				"Copy the Token itself, not the Application ID, the Public Key or the OAuth2 Client Secret.",
			Details: err.Error(), Link: "https://discord.com/developers/applications"}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Discord did not answer in time.",
			Hint: "Check the runtime's internet connection and try again.", Details: err.Error()}
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not reach Discord.",
		Hint: "Check the runtime's internet connection (and discordstatus.com), then try again.", Details: err.Error()}
}

// SubmitToken validates a bot token with Discord, derives the application and stores
// the token in the secret store under the secretRef the generated bot.yaml uses.
func (w *Wizard) SubmitToken(ctx context.Context, raw string) (View, error) {
	tok := CleanToken(raw)
	if len(tok) < 20 || strings.ContainsAny(tok, " \t\n") {
		return View{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_token",
			Message: "This does not look like a bot token.",
			Hint:    "A bot token is a long text with two dots, shown once after “Reset Token” on the Bot page of your application."}
	}
	id, err := w.opts.Probe.Identify(ctx, tok)
	if err != nil {
		if strings.Contains(err.Error(), "user account") {
			return View{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_token", Message: "This token belongs to a user account, not a bot.",
				Hint: "Copy the token from the Bot page of your application in the Developer Portal."}
		}
		return View{}, explainProbeErr(err)
	}
	return w.update(ctx, func(s *State) error {
		if s.Activated {
			return errorf(http.StatusConflict, "conflict", "", "The configuration is already active.")
		}
		key := importer.Slug(id.BotUsername, "bot")
		ref := secretRefFor(key)
		if old := s.Bot; old != nil && old.SecretRef != ref {
			_ = w.opts.Secrets.Delete(ctx, old.SecretRef)
		}
		if s.Bot != nil && s.Bot.ApplicationID != id.ApplicationID {
			s.Server = nil // another application: pick the server again
		}
		if err := w.opts.Secrets.Set(ctx, ref, tok); err != nil {
			return err
		}
		s.Bot = &BotState{Key: key, SecretRef: ref, ApplicationID: id.ApplicationID, ApplicationName: id.ApplicationName,
			UserID: id.BotUserID, Username: id.BotUsername, AvatarURL: id.BotAvatarURL, Flags: id.Flags}
		if slices.Index(Steps, s.Step) < slices.Index(Steps, StepDiscord) {
			s.Step = StepDiscord
		}
		w.closeSession()
		return nil
	})
}

// RecheckDiscord re-reads the application (privileged intents) with the stored token.
func (w *Wizard) RecheckDiscord(ctx context.Context) (View, error) {
	s, err := w.state(ctx)
	if err != nil {
		return View{}, err
	}
	if s.Bot == nil {
		return View{}, errorf(http.StatusConflict, "conflict", "Paste the bot token first.", "No Discord application is configured yet.")
	}
	tok, ok := w.secret(s.Bot.SecretRef)
	if !ok {
		return View{}, errorf(http.StatusConflict, "conflict", "Paste the bot token again.", "The bot token is no longer stored.")
	}
	id, err := w.opts.Probe.Identify(ctx, tok)
	if err != nil {
		return View{}, explainProbeErr(err)
	}
	return w.update(ctx, func(s *State) error {
		if s.Bot != nil {
			s.Bot.Flags, s.Bot.Username, s.Bot.AvatarURL, s.Bot.ApplicationName = id.Flags, id.BotUsername, id.BotAvatarURL, id.ApplicationName
		}
		return nil
	})
}

// ───────────────────────────────────────────────────────────── server

// session returns a REST session of the bot (no gateway), cached per token.
func (w *Wizard) session(ctx context.Context, s State) (discord.Session, error) {
	if s.Bot == nil {
		return nil, errorf(http.StatusConflict, "conflict", "Paste the bot token first.", "No Discord application is configured yet.")
	}
	tok, ok := w.secret(s.Bot.SecretRef)
	if !ok {
		return nil, errorf(http.StatusConflict, "conflict", "Paste the bot token again.", "The bot token is no longer stored.")
	}
	sum := sha256.Sum256([]byte(tok))
	h := hex.EncodeToString(sum[:])
	w.sessMu.Lock()
	defer w.sessMu.Unlock()
	if w.sess != nil && w.sessHash == h {
		return w.sess, nil
	}
	if w.sess != nil {
		_ = w.sess.Close(context.Background())
		w.sess = nil
	}
	sess, err := w.opts.Connector.Connect(ctx, tok)
	if err != nil {
		return nil, explainProbeErr(err)
	}
	w.sess, w.sessHash = sess, h
	return sess, nil
}

// closeSession closes the wizard's REST session (before the runtime connects the bot).
func (w *Wizard) closeSession() {
	w.sessMu.Lock()
	defer w.sessMu.Unlock()
	if w.sess != nil {
		_ = w.sess.Close(context.Background())
		w.sess = nil
	}
}

// Servers lists the servers the bot is in (polled by the UI while the invite is open).
func (w *Wizard) Servers(ctx context.Context) ([]ServerSummary, error) {
	s, err := w.state(ctx)
	if err != nil {
		return nil, err
	}
	if s.Bot == nil {
		return nil, errorf(http.StatusConflict, "conflict", "Paste the bot token first.", "No Discord application is configured yet.")
	}
	tok, ok := w.secret(s.Bot.SecretRef)
	if !ok {
		return nil, errorf(http.StatusConflict, "conflict", "Paste the bot token again.", "The bot token is no longer stored.")
	}
	out, err := w.opts.Probe.Servers(ctx, tok)
	if err != nil {
		return nil, explainProbeErr(err)
	}
	return out, nil
}

// ServerResult is the outcome of picking / re-checking the server.
type ServerResult struct {
	View   View    `json:"view"`
	Checks []Check `json:"checks"`
	OK     bool    `json:"ok"`
}

func (w *Wizard) serverChecks(ctx context.Context, s State) ([]Check, error) {
	sess, err := w.session(ctx, s)
	if err != nil {
		return nil, err
	}
	return ServerChecks(ctx, sess, s.Server.ID, s.Bot.UserID, s.Bot.Username), nil
}

// SelectServer picks the server to manage and checks it.
func (w *Wizard) SelectServer(ctx context.Context, serverID string) (ServerResult, error) {
	servers, err := w.Servers(ctx)
	if err != nil {
		return ServerResult{}, err
	}
	var pick *ServerSummary
	for i := range servers {
		if servers[i].ID == serverID {
			pick = &servers[i]
		}
	}
	if pick == nil {
		return ServerResult{}, errorf(http.StatusUnprocessableEntity, "invalid",
			"Open the invite link, choose the server and click Authorize. It appears here a few seconds later.",
			"The bot is not in this server yet.")
	}
	v, err := w.update(ctx, func(s *State) error {
		if s.Activated {
			return errorf(http.StatusConflict, "conflict", "", "The configuration is already active.")
		}
		s.Server = &ServerChoice{ID: pick.ID, Name: pick.Name, IconURL: pick.IconURL, Key: importer.Slug(pick.Name, "server")}
		if s.Step == StepDiscord {
			s.Step = StepServer
		}
		return nil
	})
	if err != nil {
		return ServerResult{}, err
	}
	return w.CheckServer(ctx, v)
}

// CheckServer re-runs the server checks (live re-check button).
func (w *Wizard) CheckServer(ctx context.Context, v ...View) (ServerResult, error) {
	s, err := w.state(ctx)
	if err != nil {
		return ServerResult{}, err
	}
	if s.Server == nil {
		return ServerResult{}, errorf(http.StatusConflict, "conflict", "Pick the server first.", "No server is selected yet.")
	}
	checks, err := w.serverChecks(ctx, s)
	if err != nil {
		return ServerResult{}, err
	}
	res := ServerResult{View: w.view(ctx, s), Checks: checks, OK: true}
	if len(v) > 0 {
		res.View = v[0]
	}
	for _, c := range checks {
		if c.Blocking() {
			res.OK = false
		}
	}
	return res, nil
}

// ───────────────────────────────────────────────────────────── repository

// RepoRequest is the Repository step form.
type RepoRequest struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Branch   string `json:"branch"`
	// Auth is "token" (default: a personal access token in Token) or "githubApp" (AppID,
	// PrivateKey and the optional InstallationID).
	Auth           string `json:"auth,omitempty"`
	Token          string `json:"token"`
	AppID          string `json:"appId,omitempty"`
	PrivateKey     string `json:"privateKey,omitempty"`
	InstallationID string `json:"installationId,omitempty"`
}

// ChooseRepo validates and records the repository choice. The git token is stored in
// the secret store (CICORD_GIT_TOKEN), or the GitHub App in CICORD_GITHUB_APP_*: an
// installation token is minted and the repository checked with it first.
func (w *Wizard) ChooseRepo(ctx context.Context, req RepoRequest) (View, error) {
	if !slices.Contains(w.opts.Providers, req.Provider) {
		return View{}, errorf(http.StatusBadRequest, "invalid", "", "this runtime offers: %s", strings.Join(w.opts.Providers, ", "))
	}
	// Refused before any secret is stored or removed: the repository in use keeps its
	// credentials.
	if s, err := w.state(ctx); err != nil {
		return View{}, err
	} else if s.RepoInitialized {
		return View{}, errorf(http.StatusConflict, "conflict", "", "The repository is already created.")
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = "main"
	}
	cfg := RepoConfig{Provider: req.Provider, Branch: branch}
	switch req.Provider {
	case ProviderLocal:
		cfg.Dir = w.opts.Dir
		if _, err := os.Stat(filepath.Join(cfg.Dir, "cicord.yaml")); err == nil {
			return View{}, errorf(http.StatusConflict, "conflict", "Remove it or run `cicord dev` in an empty folder.", "%s already contains a cicord.yaml.", cfg.Dir)
		}
	case ProviderEmbedded:
		cfg.Dir = filepath.Join(w.opts.DataDir, "repo.git")
	case ProviderGitHub:
		u := strings.TrimSpace(req.URL)
		if u == "" {
			return View{}, errorf(http.StatusUnprocessableEntity, "invalid", "Example: https://github.com/your-name/my-community", "Enter the repository URL.")
		}
		pu, err := url.Parse(u)
		local := filepath.IsAbs(u) || strings.HasPrefix(u, "file://")
		if !local && (err != nil || pu.Scheme != "https" || pu.Host == "") {
			return View{}, errorf(http.StatusUnprocessableEntity, "invalid", "Use the HTTPS URL of the repository, e.g. https://github.com/your-name/my-community (not the git@ SSH URL).", "This is not an HTTPS repository URL.")
		}
		cfg.URL = strings.TrimSuffix(u, "/")
		creds, err := w.repoCredentials(ctx, req, cfg.URL, local)
		if err != nil {
			return View{}, err
		}
		empty, hasCicord, err := CheckRemote(ctx, cfg.URL, branch, creds.tokens)
		if err != nil {
			return View{}, explainRemoteErr(err, creds.method)
		}
		if hasCicord {
			return View{}, errorf(http.StatusConflict, "conflict", "Pick an empty repository, or run `cicord serve` with this repository's cicord.yaml.",
				"This repository already contains a Cicord configuration.")
		}
		_ = empty
		if err := w.storeCredentials(ctx, creds); err != nil {
			return View{}, err
		}
		cfg.Auth = creds.method
		if creds.method == gitauth.MethodToken {
			cfg.TokenRef = GitTokenSecret
		}
	}
	return w.update(ctx, func(s *State) error {
		if s.RepoInitialized {
			return errorf(http.StatusConflict, "conflict", "", "The repository is already created.")
		}
		s.Repo = &cfg
		if s.Step == StepServer {
			s.Step = StepRepository
		}
		return nil
	})
}

// ImportResult is the outcome of the import.
type ImportResult struct {
	View View `json:"view"`
	// Plan is the preflight plan (before activation) or the first plan of the runtime.
	Plan *reconcile.Plan `json:"plan"`
	// NeedsConfirm: the preflight plan is not empty; call again with confirm to apply it.
	NeedsConfirm bool     `json:"needsConfirm"`
	Files        []string `json:"files,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// Import reads the chosen server, generates cicord.yaml / servers/<key>.yaml /
// bots/<bot>/bot.yaml, plans it (it must be empty: everything adopted), creates the
// repository with a first commit and activates the runtime without a restart.
func (w *Wizard) Import(ctx context.Context, confirm bool) (ImportResult, error) {
	s, err := w.state(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	if s.Completed {
		return ImportResult{}, errorf(http.StatusGone, "gone", "", "Setup is already complete.")
	}
	if s.Activated {
		return ImportResult{View: w.view(ctx, s), Plan: s.FirstPlan}, nil
	}
	if s.Bot == nil || s.Server == nil || s.Repo == nil {
		return ImportResult{}, errorf(http.StatusConflict, "conflict", "Complete the previous steps first.", "The bot, the server and the repository must be chosen first.")
	}
	res := ImportResult{}
	if !s.RepoInitialized {
		sess, err := w.session(ctx, s)
		if err != nil {
			return ImportResult{}, err
		}
		// Everything the importer knows (AutoMod, onboarding, widget, welcome screen,
		// expressions); those endpoints need Manage Server, so fall back to the core.
		snap, err := reconcile.FetchWith(ctx, sess, s.Server.ID, s.Bot.UserID, reconcile.FetchAll)
		var partial error
		if err != nil {
			partial = err
			snap, err = reconcile.Fetch(ctx, sess, s.Server.ID, s.Bot.UserID)
		}
		if err != nil {
			return ImportResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not read the server from Discord.",
				Hint: "Check that the bot is still in the server (Server step), then try again.", Details: err.Error()}
		}
		gen, err := importer.Generate(ctx, importer.Options{ServerKey: s.Server.Key, Snapshot: snap, Download: w.opts.Download})
		if err != nil {
			return ImportResult{}, err
		}
		res.Warnings = gen.Warnings
		if partial != nil {
			res.Warnings = append(res.Warnings, "AutoMod, onboarding, widget and welcome screen were not imported: "+partial.Error())
		}
		files, err := RepoFiles{Project: s.Server.Key, ServerKey: gen.Key, ServerYAML: gen.YAML, BotKey: s.Bot.Key,
			ApplicationID: s.Bot.ApplicationID, SecretRef: s.Bot.SecretRef, Repo: *s.Repo, PublicURL: publicURLForRepo(w.opts)}.Generate()
		if err != nil {
			return ImportResult{}, err
		}
		for p, data := range gen.FileMap() {
			files[p] = data
		}
		res.Files = catalog.SortedPaths(files)
		tmp, err := os.MkdirTemp("", "cicord-import-*")
		if err != nil {
			return ImportResult{}, err
		}
		defer os.RemoveAll(tmp)
		if err := writeTree(tmp, files); err != nil {
			return ImportResult{}, err
		}
		repo, ds := config.Load(tmp, config.LoadOptions{EngineVersion: version.Version})
		if ds.HasErrors() || repo == nil {
			return ImportResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
				Message: "The imported configuration does not validate (please report this bug).", Diagnostics: ds.Errors(), Details: ds.Err().Error()}
		}
		if err := replaceGitMappings(ctx, w.opts.Store, gen.Key, gen.Resources); err != nil {
			return ImportResult{}, err
		}
		if err := gen.RecordFiles(ctx, w.opts.Store.KV("reconcile:"+gen.Key)); err != nil {
			return ImportResult{}, err
		}
		rec := reconcile.New(reconcile.Options{Server: repo.Servers[gen.Key], RepoRoot: tmp, Client: sess,
			Resources: readOnlyResources{w.opts.Store.Resources()}, KV: w.opts.Store.KV("reconcile:" + gen.Key), BotUserID: s.Bot.UserID})
		plan, err := rec.Plan(ctx)
		rec.Close()
		if err != nil {
			return ImportResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not plan the imported configuration.",
				Hint: reconcileHint(err), Details: err.Error()}
		}
		res.Plan = &plan
		if !plan.Empty() && !confirm {
			res.NeedsConfirm = true
			res.View = w.view(ctx, s)
			return res, nil
		}
		w.closeSession() // the runtime opens its own gateway session
		tokens, err := w.repoTokens(*s.Repo)
		if err != nil {
			return ImportResult{}, &Error{Status: http.StatusConflict, Code: "conflict", Message: "The repository credentials are incomplete.",
				Hint: "Go back to the repository choice (Change) and enter them again.", Details: err.Error()}
		}
		msg := fmt.Sprintf("Set up Cicord for %s\n\nImported %d role(s) and %d channel(s) from Discord.", s.Server.Name, len(snap.Roles), len(snap.Channels))
		if _, err := InitRepository(ctx, *s.Repo, tokens, files, msg); err != nil {
			return ImportResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not create the repository.",
				Hint: "For GitHub, check that the token (or the GitHub App) has “Contents: Read and write” access; then try again.", Details: err.Error()}
		}
		if _, err := w.update(ctx, func(st *State) error { st.RepoInitialized = true; return nil }); err != nil {
			return ImportResult{}, err
		}
	}
	if w.opts.Activate == nil {
		return ImportResult{}, errors.New("setup: no activation function")
	}
	act, err := w.opts.Activate(ctx, *s.Repo)
	if err != nil {
		return ImportResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "The runtime could not activate the configuration.",
			Hint: "Look at the details below and the runtime logs, then click Import again.", Details: err.Error()}
	}
	w.Attach(act)
	rev, _ := act.Runtime.Revision()
	first, perr := act.Runtime.PlanServer(ctx, s.Server.Key)
	v, err := w.update(ctx, func(st *State) error {
		st.Activated, st.Revision = true, rev.ID
		st.PublicURL = publicURLForRepo(w.opts)
		if perr == nil {
			st.FirstPlan = &first
		}
		// Stay on the Repository step: the UI shows the first plan as proof, then moves on.
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	res.View = v
	if perr == nil {
		res.Plan = &first
	}
	return res, nil
}

func publicURLForRepo(o Options) string {
	if o.Mode != "serve" {
		return ""
	}
	u, err := url.Parse(o.PublicURL)
	if err != nil || u.Host == "" {
		return ""
	}
	h := u.Hostname()
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return ""
	}
	return strings.TrimRight(o.PublicURL, "/")
}

type readOnlyResources struct{ state.ResourceMap }

func (readOnlyResources) Put(context.Context, state.Resource) error { return nil }
func (readOnlyResources) Delete(context.Context, string, state.ResourceKind, string) error {
	return nil
}

func replaceGitMappings(ctx context.Context, st state.Store, server string, rs []state.Resource) error {
	cur, err := st.Resources().List(ctx, server)
	if err != nil {
		return err
	}
	for _, r := range cur {
		if r.Owner == state.OwnerGit {
			if err := st.Resources().Delete(ctx, server, r.Kind, r.Key); err != nil {
				return err
			}
		}
	}
	for _, r := range rs {
		r.Server, r.Owner = server, state.OwnerGit
		if err := st.Resources().Put(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// ───────────────────────────────────────────────────────────── admin login

// ClientSecretValidator checks an OAuth2 client secret with Discord (nil: no check).
type ClientSecretValidator interface {
	ValidateClientSecret(ctx context.Context, clientID, secret string) error
}

// SaveOAuth stores the OAuth2 client secret (admin login).
func (w *Wizard) SaveOAuth(ctx context.Context, secret string) (View, error) {
	secret = strings.TrimSpace(secret)
	s, err := w.state(ctx)
	if err != nil {
		return View{}, err
	}
	if s.Bot == nil {
		return View{}, errorf(http.StatusConflict, "conflict", "", "No Discord application is configured yet.")
	}
	if len(secret) < 16 || strings.ContainsAny(secret, " \t\n.") {
		return View{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "This does not look like a client secret.",
			Hint:    "In the Developer Portal open OAuth2, click “Reset Secret” and copy it. It is not the bot token (which has dots).",
			Link:    PortalOAuthURL(s.Bot.ApplicationID)}
	}
	if v, ok := w.opts.Probe.(ClientSecretValidator); ok {
		if err := v.ValidateClientSecret(ctx, s.Bot.ApplicationID, secret); err != nil {
			if errors.Is(err, discord.ErrInvalidToken) {
				return View{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Message: "Discord rejected this client secret.",
					Hint: "Click “Reset Secret” on the OAuth2 page of your application, copy the new secret and paste it here.",
					Link: PortalOAuthURL(s.Bot.ApplicationID), Details: err.Error()}
			}
			return View{}, explainProbeErr(err)
		}
	}
	if err := w.checkRedirect(ctx, s.Bot); err != nil {
		return View{}, err
	}
	if err := w.opts.Secrets.Set(ctx, OAuthSecretName, secret); err != nil {
		return View{}, err
	}
	return w.update(ctx, func(st *State) error { st.Login.Configured, st.Login.Skipped = true, false; return nil })
}

// redirectURL is where Discord sends people back after "Continue with Discord" on the
// host the wizard runs on: the admin URL when set, the public URL otherwise.
func (w *Wizard) redirectURL() string {
	base := w.opts.AdminURL
	if base == "" {
		base = w.opts.PublicURL
	}
	return strings.TrimRight(base, "/") + "/auth/callback"
}

// redirectURLs are every OAuth2 callback the Discord application must list: the public
// URL (portal logins) and, when set, the admin URL (admin console logins).
func (w *Wizard) redirectURLs() []string {
	out := []string{strings.TrimRight(w.opts.PublicURL, "/") + "/auth/callback"}
	if w.opts.AdminURL != "" {
		if a := strings.TrimRight(w.opts.AdminURL, "/") + "/auth/callback"; a != out[0] {
			out = append(out, a)
		}
	}
	return out
}

// checkRedirect fails when the application does not list each of Cicord's callbacks
// among its OAuth2 redirects (redirectURLs): Discord would answer "invalid redirect_uri"
// at the first login on that host. The error names the missing ones. It is skipped when
// the redirects cannot be read (simulator, probe error).
func (w *Wizard) checkRedirect(ctx context.Context, bot *BotState) error {
	tok, ok := w.secret(bot.SecretRef)
	if !ok {
		return nil
	}
	id, err := w.opts.Probe.Identify(ctx, tok)
	if err != nil {
		w.log.Debug("redirect check skipped: cannot read the application", "err", err)
		return nil
	}
	if id.RedirectURIs == nil {
		return nil
	}
	var missing []string
	for _, want := range w.redirectURLs() {
		if !slices.Contains(id.RedirectURIs, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	msg := "Discord does not know Cicord's login address yet."
	add := "add " + missing[0] + " exactly as written"
	if len(missing) > 1 {
		msg = "Discord does not know Cicord's login addresses yet."
		add = "add " + strings.Join(missing, " and ") + " (both, exactly as written)"
	}
	return &Error{Status: http.StatusUnprocessableEntity, Code: "redirect_missing", Message: msg,
		Hint: "In the Developer Portal open OAuth2 → Redirects, " + add + ", save, then try again.",
		Link: PortalOAuthURL(bot.ApplicationID), Details: "missing redirect URIs: " + strings.Join(missing, ", ")}
}

// LoginTested records a successful test login.
func (w *Wizard) LoginTested(ctx context.Context, username string) (View, error) {
	return w.update(ctx, func(st *State) error { st.Login.TestedAs = username; return nil })
}

// SkipLogin skips the admin login step.
func (w *Wizard) SkipLogin(ctx context.Context) (View, error) {
	return w.update(ctx, func(st *State) error {
		st.Login.Skipped = true
		if st.Step == StepLogin {
			st.Step = StepVerify
			if st.VerifyStarted.IsZero() {
				st.VerifyStarted = time.Now().UTC()
			}
		}
		return nil
	})
}

// OAuthClientSecret resolves the OAuth client secret (env first, then the store).
func OAuthClientSecret(store *secrets.Store, env func(string) (string, bool)) func() string {
	r := secrets.Resolver(store, env)
	return func() string {
		v, _ := r(OAuthSecretName)
		return v
	}
}

// ───────────────────────────────────────────────────────────── verify

// Verify runs the checklist on the configured server.
func (w *Wizard) Verify(ctx context.Context) (Report, error) {
	s, err := w.state(ctx)
	if err != nil {
		return Report{}, err
	}
	act := w.activation()
	if !s.Activated || act == nil || s.Server == nil {
		return Report{}, errorf(http.StatusConflict, "conflict", "Finish the Repository step first.", "The configuration is not active yet.")
	}
	return act.Checker.Run(ctx, s.Server.Key, s.VerifyStarted), nil
}

// ───────────────────────────────────────────────────────────── first plugin

// InstallRequest installs a catalog plugin.
type InstallRequest struct {
	Plugin string         `json:"plugin"`
	Config map[string]any `json:"config"`
}

// InstallResult is the outcome of an install.
type InstallResult struct {
	Instance       string `json:"instance"`
	Health         string `json:"health"`
	Message        string `json:"message,omitempty"`
	Command        string `json:"command,omitempty"`
	Revision       string `json:"revision,omitempty"`
	Mode           string `json:"mode"`
	PullRequestURL string `json:"pullRequestUrl,omitempty"`
}

// InstallPlugin installs a catalog plugin on the wizard's server.
func (w *Wizard) InstallPlugin(ctx context.Context, req InstallRequest) (InstallResult, View, error) {
	s, err := w.state(ctx)
	if err != nil {
		return InstallResult{}, View{}, err
	}
	act := w.activation()
	if !s.Activated || act == nil || s.Server == nil || s.Bot == nil {
		return InstallResult{}, View{}, errorf(http.StatusConflict, "conflict", "Finish the Repository step first.", "The configuration is not active yet.")
	}
	inst := Installer{Runtime: act.Runtime, Writer: act.Writer, Catalog: w.opts.Catalog, Probe: w.opts.Probe,
		Secrets: secrets.Resolver(w.opts.Secrets, w.opts.Env), Timeout: w.opts.ActivationTimeout}
	res, err := inst.Install(ctx, s.Server.Key, s.Bot.Key, req, "setup", "cicord setup")
	if err != nil {
		return InstallResult{}, View{}, err
	}
	v, err := w.update(ctx, func(st *State) error {
		st.Plugin = &PluginState{Name: req.Plugin, Instance: res.Instance, Command: res.Command}
		return nil
	})
	return res, v, err
}

// Finish completes the setup: the setup endpoints answer 410 and setup sessions are
// revoked.
func (w *Wizard) Finish(ctx context.Context) (View, error) {
	var sessions []string
	v, err := w.update(ctx, func(st *State) error {
		if !st.Activated {
			return errorf(http.StatusConflict, "conflict", "Finish the Repository step first.", "The configuration is not active yet.")
		}
		now := time.Now().UTC()
		st.Completed, st.CompletedAt, st.Step = true, &now, StepDone
		sessions, st.Sessions = st.Sessions, nil
		if st.Plugin == nil {
			st.Plugin = &PluginState{Skipped: true}
		}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	for _, id := range sessions {
		_ = w.opts.Store.Sessions().Delete(ctx, id)
	}
	w.mu.Lock()
	w.tokenSet = false
	w.mu.Unlock()
	v.Completed = true
	return v, nil
}

// ───────────────────────────────────────────────────────────── simulator actions

// SimCreateBot creates a simulated application and returns its token (dev --sim only).
func (w *Wizard) SimCreateBot(name string) (string, error) {
	if w.opts.Sim == nil {
		return "", errorf(http.StatusNotFound, "not_found", "", "not running against the simulator")
	}
	if name == "" {
		name = "Cicord"
	}
	tok, _ := w.opts.Sim.CreateBot(name)
	return tok, nil
}

func (w *Wizard) simBot(ctx context.Context) (State, string, error) {
	if w.opts.Sim == nil {
		return State{}, "", errorf(http.StatusNotFound, "not_found", "", "not running against the simulator")
	}
	s, err := w.state(ctx)
	if err != nil {
		return s, "", err
	}
	if s.Bot == nil {
		return s, "", errorf(http.StatusConflict, "conflict", "Create and paste a simulated bot token first.", "No bot yet.")
	}
	tok, ok := w.secret(s.Bot.SecretRef)
	if !ok {
		return s, "", errorf(http.StatusConflict, "conflict", "", "The bot token is no longer stored.")
	}
	return s, tok, nil
}

// SimSetIntents toggles the privileged intents of the simulated application.
func (w *Wizard) SimSetIntents(ctx context.Context, enabled bool) error {
	s, _, err := w.simBot(ctx)
	if err != nil {
		return err
	}
	var in discord.Intents
	if enabled {
		in = discord.PrivilegedIntents
	}
	return w.opts.Sim.SetIntents(s.Bot.ApplicationID, in)
}

// SimInvite simulates authorizing the invite on a simulated server.
func (w *Wizard) SimInvite(ctx context.Context, serverID string, low bool) error {
	_, tok, err := w.simBot(ctx)
	if err != nil {
		return err
	}
	if serverID == "" {
		if ss := w.opts.Sim.Servers(); len(ss) > 0 {
			serverID = ss[0].ID
		}
	}
	return w.opts.Sim.Invite(tok, serverID, low)
}

// SimRaiseRole simulates dragging the bot role to the top.
func (w *Wizard) SimRaiseRole(ctx context.Context) error {
	s, tok, err := w.simBot(ctx)
	if err != nil {
		return err
	}
	if s.Server == nil {
		return errorf(http.StatusConflict, "conflict", "", "Pick the server first.")
	}
	return w.opts.Sim.RaiseBotRole(tok, s.Server.ID)
}

// SimSendMessage posts a message as the server owner.
func (w *Wizard) SimSendMessage(ctx context.Context) error {
	if w.opts.Sim == nil {
		return errorf(http.StatusNotFound, "not_found", "", "not running against the simulator")
	}
	s, err := w.state(ctx)
	if err != nil {
		return err
	}
	if s.Server == nil {
		return errorf(http.StatusConflict, "conflict", "", "Pick the server first.")
	}
	return w.opts.Sim.SendMessage(s.Server.ID, "hello Cicord 👋")
}
