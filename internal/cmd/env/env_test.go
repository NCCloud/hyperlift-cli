package env

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
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

// testFactory is the shared fixture; kept as a local name for brevity.
func testFactory(t *testing.T, m *clienttest.Fake, format string) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	return cmdutiltest.NewFactory(m, format)
}

// execEnv runs the env command tree with the given args. It returns the message
// and exit code the CLI runner would produce.
func execEnv(t *testing.T, f *cmdutil.Factory, args ...string) (msg string, code int) {
	t.Helper()

	cmd := NewCmdEnv(f)
	cmd.SetArgs(args)
	cmd.SetIn(f.IOStreams.In)
	cmd.SetOut(f.IOStreams.Out)
	cmd.SetErr(f.IOStreams.ErrOut)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return "", cmdutil.ExitOK
	}

	return cmdutil.FriendlyError(err, false)
}

func apiErr(status int, code string, retryAfter time.Duration) *client.APIError {
	return &client.APIError{
		Status:      status,
		Code:        code,
		Detail:      "boom",
		OperationID: "op-1",
		RetryAfter:  retryAfter,
	}
}

func TestEnvGet(t *testing.T) {
	tests := []struct {
		name       string
		format     string
		env        map[string]string
		getErr     error
		wantCode   int
		wantOut    []string // substrings that must appear in stdout, in any order
		wantOutEq  string   // exact stdout, when set
		wantErrSub string   // substring that must appear in the friendly error message
	}{
		{
			name:    "table sorted",
			format:  output.FormatTable,
			env:     map[string]string{"ZED": "1", "ALPHA": "2"},
			wantOut: []string{"KEY", "VALUE", "ALPHA", "ZED"},
		},
		{
			name:      "json",
			format:    output.FormatJSON,
			env:       map[string]string{"A": "1"},
			wantOutEq: "{\n  \"A\": \"1\"\n}\n",
		},
		{
			name:      "quiet lists keys only",
			format:    output.FormatQuiet,
			env:       map[string]string{"B": "2", "A": "1"},
			wantOutEq: "A\nB\n",
		},
		{
			name:      "empty env json",
			format:    output.FormatJSON,
			env:       map[string]string{},
			wantOutEq: "{}\n",
		},
		{
			name:       "401 maps to auth message",
			format:     output.FormatTable,
			getErr:     apiErr(http.StatusUnauthorized, "auth.unauthorized", 0),
			wantCode:   cmdutil.ExitAuth,
			wantErrSub: "Not logged in",
		},
		{
			name:       "403 points at the API Manager",
			format:     output.FormatTable,
			getErr:     apiErr(http.StatusForbidden, "application.forbidden", 0),
			wantCode:   cmdutil.ExitAuth,
			wantErrSub: "lacks a scope this command needs",
		},
		{
			name:       "429 honors retry-after",
			format:     output.FormatTable,
			getErr:     apiErr(http.StatusTooManyRequests, "rate.limited", 2*time.Second),
			wantCode:   cmdutil.ExitError,
			wantErrSub: "Retry after 2s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &clienttest.Fake{
				EnvGetFunc: func(_ context.Context, id string) (map[string]string, error) {
					if id != "app_123" {
						t.Fatalf("EnvGet got id %q, want app_123", id)
					}

					return tt.env, tt.getErr
				},
			}

			f, out, _ := testFactory(t, m, tt.format)
			msg, code := execEnv(t, f, "get", "app_123")

			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (msg=%q)", code, tt.wantCode, msg)
			}

			if tt.wantErrSub != "" && !strings.Contains(msg, tt.wantErrSub) {
				t.Fatalf("error message %q does not contain %q", msg, tt.wantErrSub)
			}

			if tt.wantOutEq != "" && out.String() != tt.wantOutEq {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.wantOutEq)
			}

			for _, sub := range tt.wantOut {
				if !strings.Contains(out.String(), sub) {
					t.Fatalf("stdout %q missing %q", out.String(), sub)
				}
			}
		})
	}
}

func TestEnvSet(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		format     string
		existing   map[string]string
		getErr     error
		updateErr  error
		wantCode   int
		wantPut    map[string]string // map EnvUpdate must receive; nil means no call
		wantOutEq  string
		wantOut    []string
		wantErrSub string
	}{
		{
			name:     "merge overwrites and adds",
			args:     []string{"set", "app_123", "B=new", "C=3"},
			format:   output.FormatQuiet,
			existing: map[string]string{"A": "1", "B": "2"},
			wantPut:  map[string]string{"A": "1", "B": "new", "C": "3"},
			// quiet prints the sorted keys of the resulting full map
			wantOutEq: "A\nB\nC\n",
		},
		{
			name:      "empty value allowed",
			args:      []string{"set", "app_123", "EMPTY="},
			format:    output.FormatJSON,
			existing:  map[string]string{},
			wantPut:   map[string]string{"EMPTY": ""},
			wantOutEq: "{\n  \"EMPTY\": \"\"\n}\n",
		},
		{
			name:     "value containing equals sign",
			args:     []string{"set", "app_123", "DSN=postgres://u:p@h/db?x=1"},
			format:   output.FormatQuiet,
			existing: map[string]string{},
			wantPut:  map[string]string{"DSN": "postgres://u:p@h/db?x=1"}, //nolint:gosec // placeholder DSN, not a credential
			wantOut:  []string{"DSN"},
		},
		{
			// A key is normalized the way the server stores it, so a dashed
			// spelling overwrites the stored underscore name and does not
			// duplicate it.
			name:      "dashed key overwrites stored underscore name",
			args:      []string{"set", "app_123", "foo-bar=new"},
			format:    output.FormatQuiet,
			existing:  map[string]string{"FOO_BAR": "old"},
			wantPut:   map[string]string{"FOO_BAR": "new"},
			wantOutEq: "FOO_BAR\n",
		},
		{
			name:       "missing equals is rejected before any call",
			args:       []string{"set", "app_123", "NOEQUALS"},
			format:     output.FormatTable,
			existing:   map[string]string{"A": "1"},
			wantCode:   cmdutil.ExitError,
			wantErrSub: "expected KEY=VALUE",
		},
		{
			name:       "empty key is rejected",
			args:       []string{"set", "app_123", "=val"},
			format:     output.FormatTable,
			wantCode:   cmdutil.ExitError,
			wantErrSub: "empty key",
		},
		{
			name:       "get failure surfaces and skips update",
			args:       []string{"set", "app_123", "A=1"},
			format:     output.FormatTable,
			getErr:     apiErr(http.StatusUnauthorized, "auth", 0),
			wantCode:   cmdutil.ExitAuth,
			wantErrSub: "Not logged in",
		},
		{
			name:       "update failure surfaces",
			args:       []string{"set", "app_123", "A=1"},
			format:     output.FormatTable,
			existing:   map[string]string{},
			updateErr:  apiErr(http.StatusForbidden, "application.forbidden", 0),
			wantCode:   cmdutil.ExitAuth,
			wantPut:    map[string]string{"A": "1"},
			wantErrSub: "lacks a scope this command needs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPut map[string]string

			putCalled := false
			// The fake keeps state: a successful PUT becomes the state the
			// re-fetch returns, and the re-fetch drives the displayed result.
			current := maps.Clone(tt.existing)
			m := &clienttest.Fake{
				EnvGetFunc: func(_ context.Context, _ string) (map[string]string, error) {
					return maps.Clone(current), tt.getErr
				},
				EnvUpdateFunc: func(_ context.Context, _ string, env map[string]string) error {
					putCalled = true

					gotPut = env
					if tt.updateErr == nil {
						current = maps.Clone(env)
					}

					return tt.updateErr
				},
			}

			f, out, _ := testFactory(t, m, tt.format)
			msg, code := execEnv(t, f, tt.args...)

			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (msg=%q)", code, tt.wantCode, msg)
			}

			if tt.wantErrSub != "" && !strings.Contains(msg, tt.wantErrSub) {
				t.Fatalf("error %q missing %q", msg, tt.wantErrSub)
			}

			if tt.wantPut == nil {
				if putCalled {
					t.Fatalf("EnvUpdate was called unexpectedly with %v", gotPut)
				}
			} else {
				if !putCalled {
					t.Fatalf("EnvUpdate was not called; expected %v", tt.wantPut)
				}

				if !maps.Equal(gotPut, tt.wantPut) {
					t.Fatalf("EnvUpdate got %v, want %v", gotPut, tt.wantPut)
				}
			}

			if tt.wantOutEq != "" && out.String() != tt.wantOutEq {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.wantOutEq)
			}

			for _, sub := range tt.wantOut {
				if !strings.Contains(out.String(), sub) {
					t.Fatalf("stdout %q missing %q", out.String(), sub)
				}
			}
		})
	}
}

func TestEnvUnset(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		format      string
		existing    map[string]string
		updateErr   error
		wantCode    int
		wantPut     map[string]string // nil means EnvUpdate must not be called
		wantNoPut   bool
		wantOutEq   string
		wantErrSub  string // friendly error of a non-zero exit
		wantWarnSub string // substring that must appear on stderr
	}{
		{
			name:      "remove existing key writes back remainder",
			args:      []string{"unset", "app_123", "B"},
			format:    output.FormatQuiet,
			existing:  map[string]string{"A": "1", "B": "2"},
			wantPut:   map[string]string{"A": "1"},
			wantOutEq: "A\n",
		},
		{
			name:      "remove multiple keys",
			args:      []string{"unset", "app_123", "A", "C"},
			format:    output.FormatQuiet,
			existing:  map[string]string{"A": "1", "B": "2", "C": "3"},
			wantPut:   map[string]string{"B": "2"},
			wantOutEq: "B\n",
		},
		{
			name:        "unknown key warns and skips write",
			args:        []string{"unset", "app_123", "MISSING"},
			format:      output.FormatQuiet,
			existing:    map[string]string{"A": "1"},
			wantNoPut:   true,
			wantOutEq:   "A\n",
			wantWarnSub: "\"MISSING\" is not set",
		},
		{
			// The server stores foo-bar as FOO_BAR. unset must use the same
			// normalization, and not warn that "foo-bar" is not set.
			name:      "dashed key removes stored underscore name",
			args:      []string{"unset", "app_123", "foo-bar"},
			format:    output.FormatQuiet,
			existing:  map[string]string{"FOO_BAR": "1", "B": "2"},
			wantPut:   map[string]string{"B": "2"},
			wantOutEq: "B\n",
		},
		{
			name:        "mix of known and unknown writes once and warns",
			args:        []string{"unset", "app_123", "A", "MISSING"},
			format:      output.FormatQuiet,
			existing:    map[string]string{"A": "1", "B": "2"},
			wantPut:     map[string]string{"B": "2"},
			wantOutEq:   "B\n",
			wantWarnSub: "\"MISSING\" is not set",
		},
		{
			name:       "update failure surfaces",
			args:       []string{"unset", "app_123", "A"},
			format:     output.FormatTable,
			existing:   map[string]string{"A": "1"},
			updateErr:  apiErr(http.StatusTooManyRequests, "rate", 5*time.Second),
			wantCode:   cmdutil.ExitError,
			wantPut:    map[string]string{},
			wantErrSub: "Retry after 5s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPut map[string]string

			putCalled := false
			// The fake keeps state: a successful PUT becomes the state the
			// re-fetch returns, and the re-fetch drives the displayed result.
			current := maps.Clone(tt.existing)
			m := &clienttest.Fake{
				EnvGetFunc: func(_ context.Context, _ string) (map[string]string, error) {
					return maps.Clone(current), nil
				},
				EnvUpdateFunc: func(_ context.Context, _ string, env map[string]string) error {
					putCalled = true

					gotPut = env
					if tt.updateErr == nil {
						current = maps.Clone(env)
					}

					return tt.updateErr
				},
			}

			f, out, errOut := testFactory(t, m, tt.format)
			msg, code := execEnv(t, f, tt.args...)

			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (msg=%q)", code, tt.wantCode, msg)
			}

			if tt.wantErrSub != "" && !strings.Contains(msg, tt.wantErrSub) {
				t.Fatalf("error %q missing %q", msg, tt.wantErrSub)
			}

			if tt.wantNoPut && putCalled {
				t.Fatalf("EnvUpdate was called unexpectedly with %v", gotPut)
			}

			if tt.wantPut != nil {
				if !putCalled {
					t.Fatalf("EnvUpdate not called; expected %v", tt.wantPut)
				}

				if !maps.Equal(gotPut, tt.wantPut) {
					t.Fatalf("EnvUpdate got %v, want %v", gotPut, tt.wantPut)
				}
			}

			if tt.wantOutEq != "" && out.String() != tt.wantOutEq {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.wantOutEq)
			}

			if tt.wantWarnSub != "" && !strings.Contains(errOut.String(), tt.wantWarnSub) {
				t.Fatalf("stderr %q missing warning %q", errOut.String(), tt.wantWarnSub)
			}
			// A successful write prints the restart note; nothing else may
			// reach stderr.
			stderr := errOut.String()
			if tt.wantPut != nil && tt.wantErrSub == "" {
				if !strings.Contains(stderr, "restarting") {
					t.Fatalf("stderr %q missing the restart note", stderr)
				}

				stderr = strings.ReplaceAll(stderr, "Variables updated; the application is restarting to apply them.\n", "")
			}

			if tt.wantWarnSub == "" && len(stderr) > 0 {
				t.Fatalf("unexpected stderr output: %q", stderr)
			}
		})
	}
}

// TestEnvSetDisplaysServerState pins that set renders the re-fetched server
// state and not the locally merged map. The GET after the PUT returns extra
// server-side data, a default the server adds, which must appear in the output.
func TestEnvSetDisplaysServerState(t *testing.T) {
	gets := 0
	m := &clienttest.Fake{
		EnvGetFunc: func(_ context.Context, _ string) (map[string]string, error) {
			gets++
			if gets == 1 {
				return map[string]string{}, nil
			}

			return map[string]string{"A": "1", "APPLICATION_PORT": "8080"}, nil
		},
	}

	f, out, _ := testFactory(t, m, output.FormatQuiet)

	msg, code := execEnv(t, f, "set", "app_123", "A=1")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit code = %d (msg=%q)", code, msg)
	}

	if gets != 2 {
		t.Fatalf("EnvGet called %d times, want 2 (read-modify-write + re-fetch)", gets)
	}

	if out.String() != "APPLICATION_PORT\nA\n" && out.String() != "A\nAPPLICATION_PORT\n" {
		t.Fatalf("stdout = %q, want the re-fetched server state", out.String())
	}
}

// TestEnvUnsetDisplaysServerState is the unset counterpart. The displayed map
// comes from the GET after the PUT, not from the locally edited one.
func TestEnvUnsetDisplaysServerState(t *testing.T) {
	gets := 0
	m := &clienttest.Fake{
		EnvGetFunc: func(_ context.Context, _ string) (map[string]string, error) {
			gets++
			if gets == 1 {
				return map[string]string{"A": "1", "B": "2"}, nil
			}

			return map[string]string{"B": "2", "APPLICATION_PORT": "8080"}, nil
		},
	}

	f, out, _ := testFactory(t, m, output.FormatQuiet)

	msg, code := execEnv(t, f, "unset", "app_123", "A")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit code = %d (msg=%q)", code, msg)
	}

	if gets != 2 {
		t.Fatalf("EnvGet called %d times, want 2 (read-modify-write + re-fetch)", gets)
	}

	if !strings.Contains(out.String(), "APPLICATION_PORT") {
		t.Fatalf("stdout = %q, want the re-fetched server state", out.String())
	}
}

// TestNormalizeKey pins the copy of the server-side name normalization.
func TestNormalizeKey(t *testing.T) {
	for in, want := range map[string]string{
		"foo-bar":    "FOO_BAR",
		" log level": "LOG_LEVEL",
		"A_B":        "A_B",
		"a b-c":      "A_B_C",
	} {
		if got := normalizeKey(in); got != want {
			t.Errorf("normalizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEnvArgValidation checks the argument count of each subcommand.
func TestEnvArgValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"get needs an id", []string{"get"}, "requires an application id (e.g. app_123)"},
		{"set needs an id", []string{"set"}, "requires an application id (e.g. app_123)"},
		{"set needs a pair", []string{"set", "app_123"}, "requires at least one KEY=VALUE pair"},
		{"unset needs a key", []string{"unset", "app_123"}, "requires at least one KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &clienttest.Fake{}
			f, _, _ := testFactory(t, m, output.FormatTable)

			msg, code := execEnv(t, f, tt.args...)
			if code == cmdutil.ExitOK {
				t.Fatalf("expected non-zero exit for %v", tt.args)
			}

			if msg != tt.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tt.wantMsg)
			}
		})
	}
}

func TestEnvClientBuildError(t *testing.T) {
	io, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: io,
		Client:    func() (client.Client, error) { return nil, errors.New("no credentials") },
		Output:    func() string { return output.FormatTable },
	}

	msg, code := execEnv(t, f, "get", "app_123")
	if code != cmdutil.ExitError {
		t.Fatalf("exit code = %d, want %d", code, cmdutil.ExitError)
	}

	if !strings.Contains(msg, "no credentials") {
		t.Fatalf("error %q missing %q", msg, "no credentials")
	}
}
