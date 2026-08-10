package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/client/clienttest"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil/cmdutiltest"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// fastPoll shortens the --wait poll tick for one test. It mutates the
// package-level pollInterval, so a test that uses it must not call t.Parallel().
func fastPoll(t *testing.T) {
	t.Helper()

	old := pollInterval
	pollInterval = time.Millisecond

	t.Cleanup(func() { pollInterval = old })
}

// testFactory builds a Factory with Test IOStreams, the given mock client, a
// scripted prompter, and the requested output format.
func testFactory(c client.Client, format string) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	return cmdutiltest.NewFactory(c, format)
}

// run executes a new apps command tree with the given args. It returns the
// message and exit code the runner would produce.
func run(f *cmdutil.Factory, args ...string) (msg string, code int, err error) {
	cmd := NewCmdApps(f)
	cmd.SetArgs(args)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetIn(f.IOStreams.In)
	cmd.SetOut(f.IOStreams.Out)
	cmd.SetErr(f.IOStreams.ErrOut)

	err = cmd.ExecuteContext(context.Background())
	msg, code = cmdutil.FriendlyError(err, false)

	return msg, code, err
}

func sampleApp(id, status, build string) *client.Application {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	return &client.Application{
		ID:          id,
		Status:      client.AppStatus(status),
		BuildStatus: client.BuildStatus(build),
		Plan:        "hyperlift_micro",
		Domain:      new("demo.hyperlift.app"),
		Scale:       new(1),
		Branch:      new("main"),
		CreatedAt:   now,
		UpdatedAt:   new(now),
	}
}

func TestList(t *testing.T) {
	t.Run("table with pagination hint", func(t *testing.T) {
		var gotTake, gotSkip int

		c := &clienttest.Fake{AppsListFunc: func(_ context.Context, take, skip int) ([]client.Application, int, error) {
			gotTake, gotSkip = take, skip
			return []client.Application{*sampleApp("app_a1", "running", "built")}, 3, nil
		}}
		f, out, _ := testFactory(c, output.FormatTable)

		if _, _, err := run(f, "list", "--take", "1", "--skip", "0"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if gotTake != 1 || gotSkip != 0 {
			t.Fatalf("take/skip not forwarded: take=%d skip=%d", gotTake, gotSkip)
		}

		for _, want := range []string{"ID", "app_a1", "running", "Showing 1-1 of 3"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("stdout missing %q\ngot: %q", want, out.String())
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		c := &clienttest.Fake{AppsListFunc: func(context.Context, int, int) ([]client.Application, int, error) {
			return []client.Application{*sampleApp("app_a1", "running", "built")}, 1, nil
		}}
		f, out, _ := testFactory(c, output.FormatJSON)

		if _, _, err := run(f, "list"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, want := range []string{`"items"`, `"total"`, "app_a1"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("json missing %q\ngot: %q", want, out.String())
			}
		}
	})

	t.Run("quiet lists ids only", func(t *testing.T) {
		c := &clienttest.Fake{AppsListFunc: func(context.Context, int, int) ([]client.Application, int, error) {
			return []client.Application{
				*sampleApp("app_a1", "running", "built"),
				*sampleApp("app_b2", "stopped", "none"),
			}, 2, nil
		}}
		f, out, _ := testFactory(c, output.FormatQuiet)

		if _, _, err := run(f, "list"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if out.String() != "app_a1\napp_b2\n" {
			t.Errorf("quiet output = %q, want the ids only", out.String())
		}
	})

	t.Run("take out of range is rejected", func(t *testing.T) {
		for _, take := range []string{"0", "-1", "101"} {
			c := &clienttest.Fake{AppsListFunc: func(context.Context, int, int) ([]client.Application, int, error) {
				t.Fatal("the client must not be called for an invalid --take")
				return nil, 0, nil
			}}
			f, _, _ := testFactory(c, output.FormatTable)

			msg, code, _ := run(f, "list", "--take", take)

			if code != cmdutil.ExitError {
				t.Errorf("--take %s: exit = %d, want %d", take, code, cmdutil.ExitError)
			}

			if !strings.Contains(msg, "--take must be between 1 and 100") {
				t.Errorf("--take %s: msg = %q, want the range error", take, msg)
			}
		}
	})

	t.Run("all ignores an out-of-range take", func(t *testing.T) {
		c := &clienttest.Fake{AppsListFunc: func(context.Context, int, int) ([]client.Application, int, error) {
			return nil, 0, nil
		}}
		f, _, _ := testFactory(c, output.FormatQuiet)

		if _, _, err := run(f, "list", "--all", "--take", "500"); err != nil {
			t.Fatalf("--all must ignore --take, got %v", err)
		}
	})

	t.Run("all fetches every page", func(t *testing.T) {
		allApps := make([]client.Application, 250)
		for i := range allApps {
			allApps[i] = *sampleApp("app_x", "running", "built")
		}

		var calls int

		c := &clienttest.Fake{AppsListFunc: func(_ context.Context, take, skip int) ([]client.Application, int, error) {
			calls++
			lo := min(skip, len(allApps))
			hi := min(skip+take, len(allApps))

			return allApps[lo:hi], len(allApps), nil
		}}
		f, out, _ := testFactory(c, output.FormatJSON)

		if _, _, err := run(f, "list", "--all"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got := strings.Count(out.String(), `"id"`); got != 250 {
			t.Errorf("items rendered = %d, want 250", got)
		}

		if calls != 3 { // 100 + 100 + 50
			t.Errorf("pages fetched = %d, want 3", calls)
		}
	})
}

func TestGet(t *testing.T) {
	app := sampleApp("app_a1b2c3", "running", "built")

	t.Run("table", func(t *testing.T) {
		c := &clienttest.Fake{AppsGetFunc: func(_ context.Context, id string) (*client.Application, error) {
			if id != "app_a1b2c3" {
				t.Fatalf("got id %q", id)
			}

			return app, nil
		}}
		f, out, _ := testFactory(c, output.FormatTable)

		_, _, err := run(f, "get", "app_a1b2c3")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, want := range []string{"ID", "app_a1b2c3", "Status", "running", "Build status", "built"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("stdout missing %q\ngot: %q", want, out.String())
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		c := &clienttest.Fake{AppsGetFunc: func(context.Context, string) (*client.Application, error) {
			return app, nil
		}}
		f, out, _ := testFactory(c, output.FormatJSON)

		if _, _, err := run(f, "get", "app_a1b2c3"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(out.String(), `"id": "app_a1b2c3"`) {
			t.Errorf("json missing id\ngot: %q", out.String())
		}
	})

	t.Run("quiet", func(t *testing.T) {
		c := &clienttest.Fake{AppsGetFunc: func(context.Context, string) (*client.Application, error) {
			return app, nil
		}}
		f, out, _ := testFactory(c, output.FormatQuiet)

		if _, _, err := run(f, "get", "app_a1b2c3"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if out.String() != "app_a1b2c3\n" {
			t.Errorf("quiet output = %q, want the id only", out.String())
		}
	})

	t.Run("table renders nulls as dashes", func(t *testing.T) {
		// Every field the contract allows to be null is nil here.
		bare := &client.Application{
			ID:          "app_bare",
			Status:      client.StatusCreating,
			BuildStatus: client.BuildStatusNone,
			Plan:        "hyperlift_micro",
			CreatedAt:   time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		}
		c := &clienttest.Fake{AppsGetFunc: func(context.Context, string) (*client.Application, error) {
			return bare, nil
		}}
		f, out, _ := testFactory(c, output.FormatTable)

		if _, _, err := run(f, "get", "app_bare"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, want := range []string{
			"Scale                 -",
			"Domain                -",
			"Branch                -",
			"Repository            -",
			"GitHub installation   -",
			"Dockerfile path       -",
			"Automatic build       -",
			"Updated at            -",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("table missing %q\ngot:\n%s", want, out.String())
			}
		}
	})

	t.Run("not found maps to api error", func(t *testing.T) {
		c := &clienttest.Fake{AppsGetFunc: func(context.Context, string) (*client.Application, error) {
			return nil, &client.APIError{Status: http.StatusNotFound, Code: "business.notFound", Detail: "application not found"}
		}}
		f, _, _ := testFactory(c, output.FormatTable)

		msg, code, err := run(f, "get", "missing")
		if err == nil {
			t.Fatal("expected error")
		}

		if code != cmdutil.ExitError {
			t.Errorf("code = %d, want %d", code, cmdutil.ExitError)
		}

		if !strings.Contains(msg, "application not found") {
			t.Errorf("msg = %q", msg)
		}
	})
}

func TestLifecycleNoWait(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantHint string
		set      func(*clienttest.Fake, *string)
	}{
		{"build", []string{"build", "app_x"}, "Next: hyperlift logs app_x --build --follow  (or pass --wait)", func(m *clienttest.Fake, hit *string) {
			m.AppsBuildFunc = func(_ context.Context, id string) (*client.OpRef, error) {
				*hit = "build:" + id
				return &client.OpRef{ID: id}, nil
			}
		}},
		{"start", []string{"start", "app_x"}, "Check progress: hyperlift apps get app_x  (or pass --wait)", func(m *clienttest.Fake, hit *string) {
			m.AppsStartFunc = func(_ context.Context, id string) (*client.OpRef, error) {
				*hit = "start:" + id
				return &client.OpRef{ID: id}, nil
			}
		}},
		{"stop", []string{"stop", "app_x"}, "Check progress: hyperlift apps get app_x  (or pass --wait)", func(m *clienttest.Fake, hit *string) {
			m.AppsStopFunc = func(_ context.Context, id string) (*client.OpRef, error) {
				*hit = "stop:" + id
				return &client.OpRef{ID: id}, nil
			}
		}},
		{"restart", []string{"restart", "app_x"}, "Check progress: hyperlift apps get app_x  (or pass --wait)", func(m *clienttest.Fake, hit *string) {
			m.AppsRestartFunc = func(_ context.Context, id string) (*client.OpRef, error) {
				*hit = "restart:" + id
				return &client.OpRef{ID: id}, nil
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hit string

			c := &clienttest.Fake{}
			tc.set(c, &hit)
			f, out, errOut := testFactory(c, output.FormatTable)

			_, code, err := run(f, tc.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if code != cmdutil.ExitOK {
				t.Errorf("code = %d, want %d", code, cmdutil.ExitOK)
			}

			if hit != tc.name+":app_x" {
				t.Errorf("op hit = %q, want %s:app_x", hit, tc.name)
			}

			if !strings.Contains(out.String(), "app_x") {
				t.Errorf("stdout missing id\ngot: %q", out.String())
			}

			// The next-step hint goes to stderr, so stdout stays pipeable.
			if !strings.Contains(errOut.String(), tc.wantHint) {
				t.Errorf("stderr missing hint %q\ngot: %q", tc.wantHint, errOut.String())
			}

			if strings.Contains(out.String(), "or pass --wait") {
				t.Errorf("hint leaked to stdout: %q", out.String())
			}
		})
	}
}

// TestLifecycleJSONShape pins the --json contract: both the plain and the
// --wait path return the full application object.
func TestLifecycleJSONShape(t *testing.T) {
	fastPoll(t)

	for _, args := range [][]string{
		{"start", "app_x"},
		{"start", "app_x", "--wait", "--timeout", "5s"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c := &clienttest.Fake{
				AppsStartFunc: func(_ context.Context, id string) (*client.OpRef, error) {
					return &client.OpRef{ID: id}, nil
				},
				AppsGetFunc: func(_ context.Context, id string) (*client.Application, error) {
					return sampleApp(id, "running", "built"), nil
				},
			}
			f, out, _ := testFactory(c, output.FormatJSON)

			if _, _, err := run(f, args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, want := range []string{`"id": "app_x"`, `"status": "running"`, `"plan"`} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("json missing %q (want the full application object)\ngot: %q", want, out.String())
				}
			}
		})
	}
}

// TestLifecycleJSONRefreshFailureDegrades pins the degrade path: the trigger
// succeeded, so a failed refresh must not fail the command. It warns on stderr
// and falls back to the mutation's own {id} object.
func TestLifecycleJSONRefreshFailureDegrades(t *testing.T) {
	c := &clienttest.Fake{
		AppsStartFunc: func(_ context.Context, id string) (*client.OpRef, error) {
			return &client.OpRef{ID: id}, nil
		},
		AppsGetFunc: func(context.Context, string) (*client.Application, error) {
			return nil, errors.New("refresh boom")
		},
	}
	f, out, errOut := testFactory(c, output.FormatJSON)

	_, code, err := run(f, "start", "app_x")
	if err != nil {
		t.Fatalf("the accepted mutation must not fail on a refresh error: %v", err)
	}

	if code != cmdutil.ExitOK {
		t.Errorf("code = %d, want %d", code, cmdutil.ExitOK)
	}

	var got struct {
		ID string `json:"id"`
	}
	if uerr := json.Unmarshal(out.Bytes(), &got); uerr != nil {
		t.Fatalf("stdout is not the {id} fallback JSON: %v\ngot: %q", uerr, out.String())
	}

	if got.ID != "app_x" {
		t.Errorf("id = %q, want app_x", got.ID)
	}

	for _, want := range []string{"Warning:", "start was accepted, but fetching the result failed", "refresh boom"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr missing %q\ngot: %q", want, errOut.String())
		}
	}
}

func TestLifecycleWaitTimeoutMessage(t *testing.T) {
	fastPoll(t)

	c := &clienttest.Fake{
		AppsStartFunc: func(_ context.Context, id string) (*client.OpRef, error) {
			return &client.OpRef{ID: id}, nil
		},
		AppsGetFunc: func(_ context.Context, id string) (*client.Application, error) {
			return sampleApp(id, "instart", "built"), nil // never settles
		},
	}
	f, _, _ := testFactory(c, output.FormatTable)

	msg, code, err := run(f, "start", "app_x", "--wait", "--timeout", "50ms")
	if err == nil {
		t.Fatal("expected a timeout error")
	}

	if code != cmdutil.ExitError {
		t.Errorf("code = %d, want %d", code, cmdutil.ExitError)
	}

	for _, want := range []string{"timed out after 50ms", "the operation may still be running", "hyperlift apps get app_x"} {
		if !strings.Contains(msg, want) {
			t.Errorf("msg = %q, want contains %q", msg, want)
		}
	}

	if strings.Contains(msg, "context deadline exceeded") {
		t.Errorf("msg leaks the raw context error: %q", msg)
	}
}

// TestLifecycleWaitRequestTimeoutNotRewritten pins that only the wait's own
// timer earns the friendly timeout message. A poll failing with a
// DeadlineExceeded-wrapped request error, e.g. an HTTP client timeout,
// surfaces as itself.
func TestLifecycleWaitRequestTimeoutNotRewritten(t *testing.T) {
	fastPoll(t)

	c := &clienttest.Fake{
		AppsStartFunc: func(_ context.Context, id string) (*client.OpRef, error) {
			return &client.OpRef{ID: id}, nil
		},
		AppsGetFunc: func(context.Context, string) (*client.Application, error) {
			return nil, fmt.Errorf("Get \"/apps/app_x\": %w", context.DeadlineExceeded)
		},
	}
	f, _, _ := testFactory(c, output.FormatTable)

	msg, code, err := run(f, "start", "app_x", "--wait", "--timeout", "5m")
	if err == nil {
		t.Fatal("expected the request error")
	}

	if code != cmdutil.ExitError {
		t.Errorf("code = %d, want %d", code, cmdutil.ExitError)
	}

	if strings.Contains(msg, "timed out after") {
		t.Errorf("msg = %q, a request timeout must not masquerade as the --wait timeout", msg)
	}

	if !strings.Contains(msg, "/apps/app_x") {
		t.Errorf("msg = %q, want the poll's own error", msg)
	}
}

func TestTimeoutRequiresWait(t *testing.T) {
	c := &clienttest.Fake{AppsStartFunc: func(context.Context, string) (*client.OpRef, error) {
		t.Fatal("the client must not be called when the flags are invalid")
		return nil, nil
	}}
	f, _, _ := testFactory(c, output.FormatTable)

	msg, code, _ := run(f, "start", "app_x", "--timeout", "5s")

	if code != cmdutil.ExitError {
		t.Errorf("code = %d, want %d", code, cmdutil.ExitError)
	}

	if msg != "--timeout requires --wait" {
		t.Errorf("msg = %q, want %q", msg, "--timeout requires --wait")
	}
}

func TestMissingAppIDNamesTheToken(t *testing.T) {
	f, _, _ := testFactory(&clienttest.Fake{}, output.FormatTable)

	msg, code, _ := run(f, "get")

	if code != cmdutil.ExitError {
		t.Errorf("code = %d, want %d", code, cmdutil.ExitError)
	}

	if msg != "requires an application id (e.g. app_123)" {
		t.Errorf("msg = %q, want the named-token message", msg)
	}
}

func TestListEmptyPrintsNoticeToStderr(t *testing.T) {
	c := &clienttest.Fake{AppsListFunc: func(context.Context, int, int) ([]client.Application, int, error) {
		return nil, 0, nil
	}}
	f, out, errOut := testFactory(c, output.FormatTable)

	if _, _, err := run(f, "list"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty for pipelines", out.String())
	}

	if !strings.Contains(errOut.String(), "No applications found.") {
		t.Errorf("stderr = %q, want the empty notice", errOut.String())
	}
}

// TestLifecycleWait drives each verb's --wait through a scripted AppsGet
// sequence. wait_test.go covers the settle rules themselves; this pins that
// each verb watches the right field and reports the outcome. build must settle
// on build_status: its lifecycle status stays non-terminal to prove it. The
// build-failure case starts at "building", so the later "failed" reads as the
// outcome and not as a stale terminal status.
func TestLifecycleWait(t *testing.T) {
	type state struct{ status, build string }

	tests := []struct {
		name    string
		verb    string
		states  []state
		wantErr bool
		want    string // substring of stdout on success, of stderr on failure
	}{
		{"start reaches running", "start", []state{{"instart", "built"}, {"running", "built"}}, false, "status=running"},
		{"start failure", "start", []state{{"failure", "built"}}, true, "did not succeed"},
		{"build watches build status", "build", []state{{"deploying", "building"}, {"deploying", "built"}}, false, "build=built"},
		{"build failure", "build", []state{{"deploying", "building"}, {"deploying", "failed"}}, true, "build failed"},
		{"restart waits for the disruption", "restart", []state{{"restarting", "built"}, {"running", "built"}}, false, "status=running"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fastPoll(t)

			var calls int

			c := &clienttest.Fake{AppsGetFunc: func(_ context.Context, id string) (*client.Application, error) {
				st := tc.states[min(calls, len(tc.states)-1)]
				calls++

				return sampleApp(id, st.status, st.build), nil
			}}
			f, out, errOut := testFactory(c, output.FormatTable)

			_, code, err := run(f, tc.verb, "app_x", "--wait", "--timeout", "5s")

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error on the terminal failure")
				}

				if code != cmdutil.ExitError {
					t.Errorf("code = %d, want %d", code, cmdutil.ExitError)
				}
				// The --wait failure message goes to stderr; the error is silent.
				if !strings.Contains(errOut.String(), tc.want) {
					t.Errorf("stderr missing %q\ngot: %q", tc.want, errOut.String())
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if code != cmdutil.ExitOK {
				t.Errorf("code = %d, want %d", code, cmdutil.ExitOK)
			}

			if calls < len(tc.states) {
				t.Errorf("AppsGet calls = %d, want >= %d (the whole sequence observed)", calls, len(tc.states))
			}

			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("stdout missing %q\ngot: %q", tc.want, out.String())
			}
		})
	}
}

// TestAppsHelpTexts pins the help polish: every apps verb has a Long and an
// Example, --wait's terminal states are spelled out, and build points at the
// build logs.
func TestAppsHelpTexts(t *testing.T) {
	f, _, _ := testFactory(&clienttest.Fake{}, output.FormatTable)

	longs := map[string]string{}

	for _, c := range NewCmdApps(f).Commands() {
		if c.Long == "" || c.Example == "" {
			t.Errorf("%s: Long and Example are required", c.Name())
		}

		longs[c.Name()] = c.Long
	}

	for name, wants := range map[string][]string{
		"build":   {"built or failed", "logs <app-id> --build --follow"},
		"start":   {"running"},
		"stop":    {"stopped"},
		"restart": {"running"},
	} {
		for _, want := range wants {
			if !strings.Contains(longs[name], want) {
				t.Errorf("%s: Long help missing %q", name, want)
			}
		}
	}
}

// TestErrorMapping checks that 401, 403 and 429 produce the expected message and
// exit code in the apps commands.
func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		apiErr   *client.APIError
		wantMsg  string
		wantCode int
	}{
		{
			name:     "401 not logged in",
			apiErr:   &client.APIError{Status: http.StatusUnauthorized, Detail: "unauthorized"},
			wantMsg:  "Not logged in. Run `hyperlift auth login`.",
			wantCode: cmdutil.ExitAuth,
		},
		{
			// The error code is not a scope name, so the message must not present
			// it as one. It points at the API Manager instead.
			name:     "403 missing scope",
			apiErr:   &client.APIError{Status: http.StatusForbidden, Code: "application.forbidden"},
			wantMsg:  "lacks a scope this command needs",
			wantCode: cmdutil.ExitAuth,
		},
		{
			name:     "429 honors retry-after",
			apiErr:   &client.APIError{Status: http.StatusTooManyRequests, RetryAfter: 2 * time.Second},
			wantMsg:  "Retry after 2s",
			wantCode: cmdutil.ExitError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &clienttest.Fake{AppsGetFunc: func(context.Context, string) (*client.Application, error) {
				return nil, tc.apiErr
			}}
			f, _, _ := testFactory(c, output.FormatTable)

			msg, code, err := run(f, "get", "app_x")
			if err == nil {
				t.Fatal("expected error")
			}

			if !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("msg = %q, want contains %q", msg, tc.wantMsg)
			}

			if code != tc.wantCode {
				t.Errorf("code = %d, want %d", code, tc.wantCode)
			}
		})
	}
}

// TestListQuietOmitsPaginationHint pins the --quiet contract: only identifiers
// reach stdout. The hint is prose, so a script reading ids must never see it.
func TestListQuietOmitsPaginationHint(t *testing.T) {
	c := &clienttest.Fake{AppsListFunc: func(context.Context, int, int) ([]client.Application, int, error) {
		return []client.Application{*sampleApp("app_a1", "running", "built")}, 250, nil
	}}
	f, out, _ := testFactory(c, output.FormatQuiet)

	if _, _, err := run(f, "list", "--take", "1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := out.String(); got != "app_a1\n" {
		t.Errorf("stdout = %q, want only the application id", got)
	}
}
