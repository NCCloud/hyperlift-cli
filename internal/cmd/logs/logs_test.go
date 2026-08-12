package logs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/client/clienttest"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil/cmdutiltest"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// testFactory wraps the shared fixture, returning closures that read the
// captured stdout and stderr.
func testFactory(t *testing.T, mc *clienttest.Fake, format string) (*cmdutil.Factory, func() string, func() string) {
	t.Helper()

	f, out, errOut := cmdutiltest.NewFactory(mc, format)

	return f, out.String, errOut.String
}

// fastPolling shortens the --follow pause for the rest of the test.
func fastPolling(t *testing.T) {
	t.Helper()

	old := followPollInterval
	followPollInterval = 5 * time.Millisecond

	t.Cleanup(func() { followPollInterval = old })
}

// pagesOf returns a LogsFunc that serves the given pages in order, then repeats
// the last one. It records every request it receives.
func pagesOf(reqs *[]client.LogsRequest, pages ...client.LogsPage) func(context.Context, client.LogsRequest) (*client.LogsPage, error) {
	i := 0

	return func(_ context.Context, req client.LogsRequest) (*client.LogsPage, error) {
		if reqs != nil {
			*reqs = append(*reqs, req)
		}

		page := pages[i]
		if i < len(pages)-1 {
			i++
		}

		return &page, nil
	}
}

// fullPage builds a page with exactly logsPageTake items, which reads as "still
// catching up".
func fullPage(cursor string) client.LogsPage {
	items := make([]client.LogEntry, logsPageTake)
	for i := range items {
		items[i] = client.LogEntry{Message: fmt.Sprintf("bulk %d", i)}
	}

	return client.LogsPage{Items: items, Cursor: cursor}
}

func TestLogs_OneShot_PagesUntilCaughtUpAndPassesCursor(t *testing.T) {
	var reqs []client.LogsRequest

	mc := &clienttest.Fake{
		LogsFunc: pagesOf(&reqs,
			fullPage("c-1"), // full page: read the next one at once
			client.LogsPage{Items: []client.LogEntry{{Message: "tail line"}}, Cursor: "c-2"}, // partial: caught up, exit
		),
	}
	f, out, errOut := testFactory(t, mc, output.FormatTable)

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-123"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out(), "\n"), "\n")
	if len(lines) != logsPageTake+1 {
		t.Fatalf("expected %d printed lines, got %d", logsPageTake+1, len(lines))
	}

	if lines[len(lines)-1] != "tail line" {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], "tail line")
	}

	if errOut() != "" {
		t.Fatalf("stderr = %q, want empty", errOut())
	}

	if len(reqs) != 2 {
		t.Fatalf("expected 2 page requests (full page then partial), got %d", len(reqs))
	}

	if reqs[0].Cursor != "" || reqs[0].Take != logsPageTake {
		t.Errorf("first request = %+v, want empty cursor and take=%d", reqs[0], logsPageTake)
	}

	if reqs[1].Cursor != "c-1" {
		t.Errorf("second request cursor = %q, want c-1 (resume after first page)", reqs[1].Cursor)
	}
}

func TestLogs_BuildFlagUsesBuildLogsEndpoint(t *testing.T) {
	var reqs []client.LogsRequest

	mc := &clienttest.Fake{
		BuildLogsFunc: pagesOf(&reqs, client.LogsPage{Finished: true}),
		LogsFunc: func(context.Context, client.LogsRequest) (*client.LogsPage, error) {
			t.Fatal("--build must call BuildLogs, not Logs")
			return nil, nil
		},
	}
	f, _, _ := testFactory(t, mc, output.FormatTable)

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-xyz", "--build"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if reqs[0].ID != "app-xyz" {
		t.Errorf("req.ID = %q, want app-xyz", reqs[0].ID)
	}
}

func TestLogs_Follow_StopsWhenFinished(t *testing.T) {
	fastPolling(t)

	// A finished log, for example a completed build, must end --follow by itself.
	mc := &clienttest.Fake{
		BuildLogsFunc: pagesOf(nil,
			client.LogsPage{Items: []client.LogEntry{{Message: "step 1"}}, Cursor: "c-1"},
			client.LogsPage{Items: []client.LogEntry{{Message: "done"}}, Cursor: "c-2", Finished: true},
		),
	}
	f, out, _ := testFactory(t, mc, output.FormatTable)

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-1", "--follow", "--build"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := out(), "step 1\ndone\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestLogs_Follow_TransientErrorRetriesWithWarning(t *testing.T) {
	fastPolling(t)

	calls := 0
	mc := &clienttest.Fake{
		LogsFunc: func(_ context.Context, _ client.LogsRequest) (*client.LogsPage, error) {
			calls++
			if calls == 1 {
				return nil, &client.APIError{Status: 502}
			}

			return &client.LogsPage{Items: []client.LogEntry{{Message: "recovered"}}, Finished: true}, nil
		},
	}
	f, out, errOut := testFactory(t, mc, output.FormatTable)

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-1", "--follow"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out(), "recovered") {
		t.Errorf("stdout = %q, want to contain the post-retry line", out())
	}

	if !strings.Contains(errOut(), "warning:") {
		t.Errorf("stderr = %q, want a retry warning", errOut())
	}
}

func TestLogs_Follow_AuthErrorIsFatal(t *testing.T) {
	wantErr := &client.APIError{Status: 401}
	mc := &clienttest.Fake{
		LogsFunc: func(_ context.Context, _ client.LogsRequest) (*client.LogsPage, error) {
			return nil, wantErr
		},
	}
	f, _, _ := testFactory(t, mc, output.FormatTable)

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-1", "--follow"})

	err := cmd.ExecuteContext(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the 401 to end --follow", err)
	}
}

func TestLogs_JSONOutput_EmitsEntries(t *testing.T) {
	mc := &clienttest.Fake{
		LogsFunc: pagesOf(nil,
			client.LogsPage{
				Items: []client.LogEntry{
					{Message: "ok", Timestamp: "2026-01-15T12:00:00Z"},
					{Message: "no timestamp"},
				},
				Cursor:   "c-1",
				Finished: true,
			},
		),
	}
	f, out, _ := testFactory(t, mc, output.FormatJSON)

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %q", len(lines), out())
	}

	if lines[0] != `{"message":"ok","timestamp":"2026-01-15T12:00:00Z"}` {
		t.Errorf("line[0] = %q, want full entry object", lines[0])
	}
	// An absent timestamp disappears; it is not an empty string.
	if lines[1] != `{"message":"no timestamp"}` {
		t.Errorf("line[1] = %q, want message-only object", lines[1])
	}
}

func TestLogs_Follow_CancelReturnsContextError(t *testing.T) {
	// Poll faster, so the test runs a few wait cycles quickly.
	fastPolling(t)

	first := true
	mc := &clienttest.Fake{
		LogsFunc: func(_ context.Context, _ client.LogsRequest) (*client.LogsPage, error) {
			if first {
				first = false
				return &client.LogsPage{Items: []client.LogEntry{{Message: "first line"}}, Cursor: "c-1"}, nil
			}
			// Caught up but never finished, so --follow keeps polling.
			return &client.LogsPage{Cursor: "c-1"}, nil
		},
	}
	f, out, _ := testFactory(t, mc, output.FormatTable)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-1", "--follow"})

	err := cmd.ExecuteContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// The line read before the cancel must appear.
	if !strings.Contains(out(), "first line") {
		t.Errorf("stdout = %q, want to contain %q", out(), "first line")
	}
}

func TestLogs_ClientBuildError_Propagates(t *testing.T) {
	wantErr := errors.New("no credentials")
	f := &cmdutil.Factory{
		IOStreams: func() *iostreams.IOStreams { io, _, _, _ := iostreams.Test(); return io }(),
		Client: func() (client.Client, error) {
			return nil, wantErr
		},
		Output: func() string { return output.FormatTable },
	}

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{"app-1"})

	err := cmd.ExecuteContext(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestLogs_APIErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		apiErr   *client.APIError
		wantCode int
		wantMsg  string
	}{
		{
			name:     "401 unauthorized -> auth exit + login hint",
			apiErr:   &client.APIError{Status: 401},
			wantCode: cmdutil.ExitAuth,
			wantMsg:  "Not logged in. Run `hyperlift auth login`.",
		},
		{
			name:     "403 forbidden -> auth exit + points at the API Manager",
			apiErr:   &client.APIError{Status: 403, Code: "application.forbidden"},
			wantCode: cmdutil.ExitAuth,
			wantMsg:  "lacks a scope this command needs",
		},
		{
			name:     "429 rate limited -> error exit + retry-after honored",
			apiErr:   &client.APIError{Status: 429, RetryAfter: 5 * time.Second},
			wantCode: cmdutil.ExitError,
			wantMsg:  "Retry after 5s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &clienttest.Fake{
				LogsFunc: func(_ context.Context, _ client.LogsRequest) (*client.LogsPage, error) {
					return nil, tt.apiErr
				},
			}
			f, out, _ := testFactory(t, mc, output.FormatTable)

			cmd := NewCmdLogs(f)
			cmd.SetArgs([]string{"app-1"})

			err := cmd.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}

			// Repeat the mapping of cli.Run to check the message and exit code.
			msg, code := cmdutil.FriendlyError(err, false)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}

			if !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("message = %q, want to contain %q", msg, tt.wantMsg)
			}

			if out() != "" {
				t.Errorf("stdout = %q, want empty on error", out())
			}
		})
	}
}

func TestLogs_RequiresExactlyOneArg(t *testing.T) {
	mc := &clienttest.Fake{}
	f, _, _ := testFactory(t, mc, output.FormatTable)

	cmd := NewCmdLogs(f)
	cmd.SetArgs([]string{}) // no app id
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for missing app-id, got nil")
	}

	if got := err.Error(); got != "requires an application id (e.g. app_123)" {
		t.Errorf("err = %q, want the named-token message", got)
	}
}

func TestLogs_TimestampsDefaultOnAndOptOut(t *testing.T) {
	page := client.LogsPage{
		Items: []client.LogEntry{
			{Message: "ok", Timestamp: "2026-01-15T12:00:00Z"},
			{Message: "no timestamp"},
		},
		Finished: true,
	}

	t.Run("default prefixes the wire timestamp", func(t *testing.T) {
		mc := &clienttest.Fake{LogsFunc: pagesOf(nil, page)}
		f, out, _ := testFactory(t, mc, output.FormatTable)

		cmd := NewCmdLogs(f)
		cmd.SetArgs([]string{"app-1"})

		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// A line without a wire timestamp stays bare.
		if want := "2026-01-15T12:00:00Z ok\nno timestamp\n"; out() != want {
			t.Errorf("stdout = %q, want %q", out(), want)
		}
	})

	t.Run("--no-timestamps restores bare messages", func(t *testing.T) {
		mc := &clienttest.Fake{LogsFunc: pagesOf(nil, page)}
		f, out, _ := testFactory(t, mc, output.FormatTable)

		cmd := NewCmdLogs(f)
		cmd.SetArgs([]string{"app-1", "--no-timestamps"})

		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if want := "ok\nno timestamp\n"; out() != want {
			t.Errorf("stdout = %q, want %q", out(), want)
		}
	})
}
