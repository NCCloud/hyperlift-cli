package cmdutil

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

// Exit codes the CLI returns.
const (
	ExitOK    = 0
	ExitError = 1
	// ExitAuth means an authentication or authorization failure.
	ExitAuth = 2
	// ExitCancel means SIGINT or SIGTERM cancelled the context and stopped the
	// run. 130 follows the shell convention of 128+SIGINT. A declined
	// confirmation is a plain ExitError, not a cancel.
	ExitCancel = 130
)

// notLoggedInMsg is the shared message for an API 401 and for a local
// missing-credentials failure, so every command reports the state the same way.
const notLoggedInMsg = "Not logged in. Run `hyperlift auth login`."

// ErrNotLoggedIn marks a local not-logged-in failure, for example no stored
// credentials. FriendlyError maps it exactly like an API 401.
var ErrNotLoggedIn = errors.New("not logged in")

// SilentError wraps an error the user has already seen, or one to hide. Run()
// prints nothing for it, but still exits with a non-zero code.
type SilentError struct {
	Err error
}

func (e *SilentError) Error() string { return e.Err.Error() }
func (e *SilentError) Unwrap() error { return e.Err }

// NewSilentError wraps err, so the runner does not print it.
func NewSilentError(err error) error { return &SilentError{Err: err} }

// PrintError writes an "Error: <msg>" line to ErrOut, in the format of the
// top-level runner. A command calls it before it returns a SilentError, which
// the runner prints nothing for, so the message still reaches the user.
func PrintError(ios *iostreams.IOStreams, msg string) {
	if ios == nil {
		return
	}

	cs := ios.ColorScheme()
	_, _ = fmt.Fprintln(ios.ErrOut, cs.Red("Error:")+" "+msg)
}

// FriendlyError maps a known error to a message and an exit code. msg is the
// text to print on ErrOut; an empty msg means print nothing, as for a
// SilentError. debug keeps the machine error-code suffix in API error messages.
func FriendlyError(err error, debug bool) (msg string, code int) {
	if err == nil {
		return "", ExitOK
	}

	var silent *SilentError
	if errors.As(err, &silent) {
		return "", ExitError
	}

	if errors.Is(err, ErrNotLoggedIn) {
		return notLoggedInMsg, ExitAuth
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return friendlyAPIError(apiErr, debug)
	}

	return err.Error(), ExitError
}

func friendlyAPIError(e *client.APIError, debug bool) (string, int) {
	switch e.Status {
	case http.StatusUnauthorized:
		return notLoggedInMsg, ExitAuth
	case http.StatusForbidden:
		// The server names no scope, and e.Code is an error code, so the message
		// says where to fix it instead of guessing which scope is missing.
		msg := "Permission denied: your API key lacks a scope this command needs. " +
			"Add it to the key in the Spaceship API Manager."
		if debug && e.Code != "" {
			msg += fmt.Sprintf(" (%s)", e.Code)
		}

		return msg, ExitAuth
	case http.StatusTooManyRequests:
		if e.RetryAfter > 0 {
			return fmt.Sprintf("Rate limited. Retry after %s.", e.RetryAfter), ExitError
		}

		return "Rate limited. Please retry shortly.", ExitError
	}

	if e.Status >= 500 {
		if e.OperationID != "" {
			return fmt.Sprintf("Server error (%s). Reference operation id %s when contacting support.", e.Detail, e.OperationID), ExitError
		}

		return fmt.Sprintf("Server error: %s", e.Detail), ExitError
	}

	// The "(business.xxx)" suffix in Error() is machine detail; keep it only
	// under --debug.
	if !debug && e.Detail != "" {
		return e.Detail, ExitError
	}

	return e.Error(), ExitError
}
