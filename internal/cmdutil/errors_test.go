package cmdutil

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// TestFriendlyErrorCodeSuffixBehindDebug pins that the machine error-code
// suffix shows only under --debug.
func TestFriendlyErrorCodeSuffixBehindDebug(t *testing.T) {
	e := &client.APIError{
		Status: http.StatusUnprocessableEntity,
		Code:   "business.validationFailed",
		Detail: "take must be between 1 and 100",
	}

	msg, code := FriendlyError(e, false)
	if code != ExitError {
		t.Errorf("code = %d, want %d", code, ExitError)
	}

	if msg != "take must be between 1 and 100" {
		t.Errorf("msg = %q, want the detail without the code suffix", msg)
	}

	msg, _ = FriendlyError(e, true)
	if msg != "take must be between 1 and 100 (business.validationFailed)" {
		t.Errorf("debug msg = %q, want the code suffix kept", msg)
	}
}

// TestFriendlyErrorCodeOnlyStaysVisible: without a detail the code is the only
// information, so dropping it would leave nothing actionable.
func TestFriendlyErrorCodeOnlyStaysVisible(t *testing.T) {
	e := &client.APIError{Status: http.StatusUnprocessableEntity, Code: "business.validationFailed"}

	msg, _ := FriendlyError(e, false)
	if msg != "request failed: business.validationFailed" {
		t.Errorf("msg = %q, want the code kept when there is no detail", msg)
	}
}

func TestFriendlyErrorNotLoggedIn(t *testing.T) {
	msg, code := FriendlyError(fmt.Errorf("whoami: %w", ErrNotLoggedIn), false)
	if code != ExitAuth {
		t.Errorf("code = %d, want %d", code, ExitAuth)
	}

	if msg != "Not logged in. Run `hyperlift auth login`." {
		t.Errorf("msg = %q, want the shared not-logged-in message", msg)
	}
}
