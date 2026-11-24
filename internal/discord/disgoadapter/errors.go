package disgoadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/disgoorg/disgo/rest"
	"github.com/gorilla/websocket"

	"github.com/npjonath/cicord/internal/discord"
)

// Gateway close codes (https://discord.com/developers/docs/topics/opcodes-and-status-codes).
const (
	closeAuthenticationFailed = 4004
	closeInvalidIntents       = 4013
	closeDisallowedIntents    = 4014
)

// mapError converts an error returned by disgo's REST client into an error that
// wraps one of the contract sentinels, keeping Discord's message in the text:
//
//	401                          → discord.ErrInvalidToken
//	403, codes 50001 / 50013     → discord.ErrForbidden
//	404, codes 10001..10999      → discord.ErrNotFound
//	400 (incl. 50035, 40060)     → discord.ErrBadRequest
//
// Other errors (5xx, exhausted 429 retries, transport errors) are wrapped as-is.
// Context cancellation is returned unchanged so errors.Is(err, context.Canceled) works.
func mapError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("discord %s: %w", op, err)
	}
	var re *rest.Error
	if !errors.As(err, &re) {
		return fmt.Errorf("discord %s: %w", op, err)
	}
	status := 0
	if re.Response != nil {
		status = re.Response.StatusCode
	}
	msg := restErrorText(re, status)
	code := int(re.Code)
	var sentinel error
	switch {
	case status == http.StatusUnauthorized:
		sentinel = discord.ErrInvalidToken
	case status == http.StatusForbidden || code == 50001 || code == 50013:
		sentinel = discord.ErrForbidden
	case status == http.StatusNotFound || (code >= 10001 && code <= 10999):
		sentinel = discord.ErrNotFound
	case status == http.StatusBadRequest:
		sentinel = discord.ErrBadRequest
	}
	if sentinel == nil {
		return &wrappedError{text: fmt.Sprintf("discord %s: %s", op, msg), err: err}
	}
	return fmt.Errorf("discord %s: %w (%s)", op, sentinel, msg)
}

// restErrorText renders a rest.Error without the request URL (interaction and
// webhook URLs embed tokens) and without dereferencing a nil response.
func restErrorText(re *rest.Error, status int) string {
	prefix := ""
	if status != 0 {
		prefix = fmt.Sprintf("HTTP %d ", status)
	}
	if re.Code != 0 || re.Message != "" {
		s := fmt.Sprintf("%s%d: %s", prefix, re.Code, re.Message)
		if len(re.Errors) > 0 && string(re.Errors) != "null" {
			s += " " + truncate(string(re.Errors), 500)
		}
		return s
	}
	return prefix + truncate(string(re.RsBody), 500)
}

// wrappedError keeps the *rest.Error in the chain without calling its Error method
// (which dereferences the response and includes nothing we don't already show).
type wrappedError struct {
	text string
	err  error
}

func (e *wrappedError) Error() string { return e.text }
func (e *wrappedError) Unwrap() error { return e.err }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mapGatewayError maps a gateway open failure (websocket close codes) to sentinels.
func mapGatewayError(err error) error {
	if err == nil {
		return nil
	}
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case closeAuthenticationFailed:
			return fmt.Errorf("discord gateway: %w (close %d: %s)", discord.ErrInvalidToken, ce.Code, ce.Text)
		case closeDisallowedIntents:
			return fmt.Errorf("discord gateway: %w: privileged intents not enabled in the developer portal (close %d: %s)",
				discord.ErrForbidden, ce.Code, ce.Text)
		case closeInvalidIntents:
			return fmt.Errorf("discord gateway: %w: invalid intents (close %d: %s)", discord.ErrBadRequest, ce.Code, ce.Text)
		}
	}
	return fmt.Errorf("discord gateway: %w", err)
}
