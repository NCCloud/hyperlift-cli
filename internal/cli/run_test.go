package cli

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	zk "github.com/zalando/go-keyring"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/testapi"
)

// mockEnv points the CLI at a fresh contract mock and an isolated config dir.
func mockEnv(t *testing.T) {
	t.Helper()
	zk.MockInit()

	srv := httptest.NewServer(testapi.Server())
	t.Cleanup(srv.Close)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HYPERLIFT_BASE_URL", srv.URL)
	t.Setenv("HYPERLIFT_API_KEY", "k")
	t.Setenv("HYPERLIFT_API_SECRET", "s")
	t.Setenv("HYPERLIFT_NO_UPDATE_CHECK", "1")
}

// TestRun_ErrorContract pins Run's documented error behavior: a failing command
// prints one "Error: ..." line to stderr and exits with the mapped code.
func TestRun_ErrorContract(t *testing.T) {
	mockEnv(t)

	io, _, out, errOut := iostreams.Test()

	code := Run(context.Background(), io, []string{"apps", "get", "app_missing"})

	if code != cmdutil.ExitError {
		t.Errorf("exit = %d, want %d", code, cmdutil.ExitError)
	}

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty on error", out.String())
	}

	if !strings.HasPrefix(errOut.String(), "Error: ") {
		t.Errorf("stderr = %q, want an Error: line", errOut.String())
	}
}

// TestRun_CancelExitsWithCancelCode pins the Ctrl-C contract: a canceled
// context maps to ExitCancel (130) with no error line.
func TestRun_CancelExitsWithCancelCode(t *testing.T) {
	mockEnv(t)

	io, _, _, errOut := iostreams.Test()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if code := Run(ctx, io, []string{"apps", "list"}); code != cmdutil.ExitCancel {
		t.Errorf("exit = %d, want %d, stderr: %s", code, cmdutil.ExitCancel, errOut.String())
	}
}

// TestRun_SuccessExitsZero pins the success path through the same entrypoint.
func TestRun_SuccessExitsZero(t *testing.T) {
	mockEnv(t)

	io, _, out, errOut := iostreams.Test()

	if code := Run(context.Background(), io, []string{"apps", "list"}); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}

	if !strings.Contains(out.String(), "app_a1b2c3") {
		t.Errorf("stdout = %q, want the mock's application listed", out.String())
	}
}
