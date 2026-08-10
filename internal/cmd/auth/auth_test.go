package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	zk "github.com/zalando/go-keyring"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/client/clienttest"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/config"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/keyring"
)

// testEnv isolates one test. It points XDG_CONFIG_HOME at a temp dir, switches
// the keyring to the in-memory mock, and clears the credential env overrides.
func testEnv(t *testing.T) {
	t.Helper()
	zk.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvBaseURL, "")
	t.Setenv(config.EnvAPIKey, "")
	t.Setenv(keyring.EnvAPISecret, "")
}

// scriptedPrompter answers the logout confirmation, so a test can drive the
// interactive path without a real TTY.
type scriptedPrompter struct{ confirm bool }

func (s *scriptedPrompter) Input(string) (string, error)  { return "", nil }
func (s *scriptedPrompter) Secret(string) (string, error) { return "", nil }
func (s *scriptedPrompter) Confirm(string) (bool, error)  { return s.confirm, nil }

// apiErr builds a *client.APIError with the given status and code.
func apiErr(status int, code string) *client.APIError {
	return &client.APIError{Status: status, Code: code, Detail: "boom"}
}

// harness holds a Factory with Test IOStreams and a replaceable client.
type harness struct {
	f      *cmdutil.Factory
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	ios    *iostreams.IOStreams
}

// newHarness builds a Factory whose Client() returns the given mock. The
// login-time validator, the package variable newClient, returns the same mock.
func newHarness(t *testing.T, m *clienttest.Fake) *harness {
	t.Helper()

	ios, in, out, errOut := iostreams.Test()

	f := cmdutil.NewFactory(ios)
	f.Client = func() (client.Client, error) { return m, nil }

	newClient = func(client.Options) client.Client { return m }

	t.Cleanup(func() { newClient = client.New })

	return &harness{f: f, in: in, out: out, errOut: errOut, ios: ios}
}

// run executes the auth command named in args with the harness factory. It
// returns the message and exit code, the same way cli.Run does.
func (h *harness) run(t *testing.T, args ...string) (msg string, code int) {
	t.Helper()

	root := NewCmdAuth(h.f)
	// Copy the real root: register the persistent --json and --quiet flags, and
	// rewire f.Output to resolve them.
	cmdutil.AddOutputFlags(root, h.f)
	root.SetArgs(args)
	root.SetIn(h.ios.In)
	root.SetOut(h.ios.Out)
	root.SetErr(h.ios.ErrOut)
	root.SilenceErrors = true
	root.SilenceUsage = true

	err := root.ExecuteContext(context.Background())
	if err == nil {
		return "", cmdutil.ExitOK
	}

	if errors.Is(err, context.Canceled) {
		return "", cmdutil.ExitCancel
	}

	return cmdutil.FriendlyError(err, false)
}

// seedSecret is the fixed API secret seeded for login-dependent tests.
const seedSecret = "SK"

// seedLogin writes usable credentials, the key into the config and the secret
// into the keyring, so logout and whoami have something to act on.
func seedLogin(t *testing.T, key string) {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.StoredAPIKey = key
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if _, err := keyring.SetSecret(seedSecret); err != nil {
		t.Fatalf("set secret: %v", err)
	}
}

// --- login ---------------------------------------------------------------

func TestLogin(t *testing.T) {
	tests := []struct {
		name       string
		probe      func(context.Context) error
		args       []string
		stdin      string
		wantCode   int
		wantStored bool   // the key and secret are stored
		errSubstr  string // substring that must appear on any output stream
	}{
		{
			name:       "success with flags",
			probe:      func(context.Context) error { return nil },
			args:       []string{"login", "--key", "AK", "--secret", "SK"},
			wantCode:   cmdutil.ExitOK,
			wantStored: true,
			errSubstr:  "Logged in",
		},
		{
			name:       "success with secret on stdin",
			probe:      func(context.Context) error { return nil },
			args:       []string{"login", "--key", "AK", "--with-stdin"},
			stdin:      "SK-from-stdin\n",
			wantCode:   cmdutil.ExitOK,
			wantStored: true,
			errSubstr:  "Logged in",
		},
		{
			name:       "401 invalid credentials are not stored",
			probe:      func(context.Context) error { return apiErr(http.StatusUnauthorized, "") },
			args:       []string{"login", "--key", "AK", "--secret", "BAD"},
			wantCode:   cmdutil.ExitError, // a SilentError gives ExitError; the command prints the message to stderr
			wantStored: false,
			errSubstr:  "invalid API key or secret",
		},
		{
			name:       "403 stores creds but warns about scope",
			probe:      func(context.Context) error { return apiErr(http.StatusForbidden, "applications.read") },
			args:       []string{"login", "--key", "AK", "--secret", "SK"},
			wantCode:   cmdutil.ExitOK,
			wantStored: true,
			errSubstr:  "applications.read",
		},
		{
			name:      "missing secret non-interactive errors",
			probe:     func(context.Context) error { return nil },
			args:      []string{"login", "--key", "AK"},
			wantCode:  cmdutil.ExitError,
			errSubstr: "secret",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testEnv(t)

			m := &clienttest.Fake{ProbeFunc: tc.probe}

			h := newHarness(t, m)
			if tc.stdin != "" {
				h.in.WriteString(tc.stdin)
			}

			msg, code := h.run(t, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (msg=%q, err=%q)", code, tc.wantCode, msg, h.errOut.String())
			}

			combined := h.errOut.String() + msg
			if tc.errSubstr != "" && !strings.Contains(combined, tc.errSubstr) {
				t.Errorf("output %q does not contain %q", combined, tc.errSubstr)
			}

			// Check what was stored.
			cfg, _ := config.Load()
			storedKey := cfg.APIKey()
			storedSecret, _ := keyring.GetSecret()

			gotStored := storedKey != "" && storedSecret != ""
			if gotStored != tc.wantStored {
				t.Errorf("stored = %v (key=%q secret=%q), want %v", gotStored, storedKey, storedSecret, tc.wantStored)
			}
		})
	}
}

func TestLoginPromptPathStillStoresValidatedSecret(t *testing.T) {
	// A secret from stdin must be stored exactly, with the trailing newline
	// removed. This guards the stdin parsing.
	testEnv(t)

	m := &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }}
	h := newHarness(t, m)
	h.in.WriteString("super-secret\r\n")

	_, code := h.run(t, "login", "--key", "KEY123", "--with-stdin")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit = %d, want 0; err=%q", code, h.errOut.String())
	}

	got, _ := keyring.GetSecret()
	if got != "super-secret" {
		t.Errorf("stored secret = %q, want %q", got, "super-secret")
	}
}

func TestLoginJSONOutput(t *testing.T) {
	testEnv(t)

	m := &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }}
	h := newHarness(t, m)

	_, code := h.run(t, "login", "--key", "AK", "--secret", "SK", "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit = %d, want 0; err=%q", code, h.errOut.String())
	}

	var doc struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.out.String())
	}

	if !doc.LoggedIn {
		t.Errorf("loggedIn = false, want true: %s", h.out.String())
	}
}

func TestLoginKeyringFallbackWarns(t *testing.T) {
	testEnv(t)
	zk.MockInitWithError(errors.New("keyring backend unavailable"))

	m := &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }}
	h := newHarness(t, m)

	_, code := h.run(t, "login", "--key", "AK", "--secret", "SK")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit = %d, want 0; err=%q", code, h.errOut.String())
	}

	p, err := keyring.FallbackPath()
	if err != nil {
		t.Fatalf("FallbackPath: %v", err)
	}

	stderr := h.errOut.String()
	if !strings.Contains(stderr, "OS keyring unavailable") || !strings.Contains(stderr, p) {
		t.Errorf("stderr missing fallback warning with path %q: %q", p, stderr)
	}
}

func TestLoginEnvBaseURLWarnsOnlyWhenDiffering(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		stored   string // pre-seeded StoredBaseURL
		wantWarn bool
	}{
		{"env differs from default warns", "https://env-only.example/api/v1", "", true},
		{"env equals default is silent", config.DefaultBaseURL, "", false},
		{"env equals stored is silent", "https://stored.example/api/v1", "https://stored.example/api/v1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testEnv(t)

			if tc.stored != "" {
				cfg, err := config.Load()
				if err != nil {
					t.Fatalf("load config: %v", err)
				}

				cfg.StoredBaseURL = tc.stored
				if err := cfg.Save(); err != nil {
					t.Fatalf("save config: %v", err)
				}
			}

			t.Setenv(config.EnvBaseURL, tc.env)

			m := &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }}
			h := newHarness(t, m)

			_, code := h.run(t, "login", "--key", "AK", "--secret", "SK")
			if code != cmdutil.ExitOK {
				t.Fatalf("exit = %d, want 0; err=%q", code, h.errOut.String())
			}

			gotWarn := strings.Contains(h.errOut.String(), config.EnvBaseURL)
			if gotWarn != tc.wantWarn {
				t.Errorf("warning printed = %v, want %v: %q", gotWarn, tc.wantWarn, h.errOut.String())
			}

			cfg, _ := config.Load()
			if cfg.StoredBaseURL != tc.stored {
				t.Errorf("env base URL must not be persisted, got %q", cfg.StoredBaseURL)
			}
		})
	}
}

func TestLoginConfigSaveFailureStoresNoSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write permissions are not enforced this way on Windows")
	}

	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	testEnv(t)

	// Make the config dir read-only, so cfg.Save fails before the secret is
	// stored: a failed save must not leave a fresh secret next to a stale key.
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hyperlift")
	if err := os.MkdirAll(dir, 0o500); err != nil { //nolint:gosec // test path under t.TempDir
		t.Fatal(err)
	}

	m := &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }}
	h := newHarness(t, m)

	msg, code := h.run(t, "login", "--key", "AK", "--secret", "SK")
	if code == cmdutil.ExitOK {
		t.Fatal("login should fail when the config cannot be saved")
	}

	if !strings.Contains(msg, "save config") {
		t.Errorf("msg = %q, want a save-config error", msg)
	}

	if s, _ := keyring.GetSecret(); s != "" {
		t.Errorf("secret stored despite failed config save: %q", s)
	}
}

func TestLoginSecretStoreFailureRollsBackConfig(t *testing.T) {
	testEnv(t)
	seedLogin(t, "OLD-KEY")

	// Break both secret stores: the keyring mock fails every call, and a plain
	// file blocks the directory the fallback needs.
	zk.MockInitWithError(errors.New("keyring backend unavailable"))

	secretsDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hyperlift", "secrets")
	if err := os.WriteFile(secretsDir, []byte("not a dir"), 0o600); err != nil { //nolint:gosec // test path under t.TempDir
		t.Fatal(err)
	}

	m := &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }}
	h := newHarness(t, m)

	msg, code := h.run(t, "login", "--key", "NEW-KEY", "--secret", "SK-NEW")
	if code == cmdutil.ExitOK {
		t.Fatal("login should fail when the secret cannot be stored")
	}

	if !strings.Contains(msg, "store secret") {
		t.Errorf("msg = %q, want a store-secret error", msg)
	}

	// The config rollback keeps the prior key paired with the prior secret.
	cfg, _ := config.Load()
	if cfg.StoredAPIKey != "OLD-KEY" {
		t.Errorf("stored key = %q, want the prior key restored", cfg.StoredAPIKey)
	}
}

// --- logout --------------------------------------------------------------

func TestLogout(t *testing.T) {
	t.Run("--yes deletes creds", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "AK")
		h := newHarness(t, &clienttest.Fake{})

		msg, code := h.run(t, "logout", "--yes")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0 (msg=%q)", code, msg)
		}

		cfg, _ := config.Load()
		if cfg.APIKey() != "" {
			t.Errorf("api key not cleared: %q", cfg.APIKey())
		}

		if s, _ := keyring.GetSecret(); s != "" {
			t.Errorf("secret not deleted: %q", s)
		}

		if !strings.Contains(h.errOut.String(), "Logged out") {
			t.Errorf("missing success message: %q", h.errOut.String())
		}
	})

	t.Run("confirm declined cancels and keeps creds", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "AK")
		h := newHarness(t, &clienttest.Fake{})
		// Allow prompting, then decline.
		h.ios.SetStdinTTY(true)
		h.ios.SetStdoutTTY(true)
		h.ios.SetNeverPrompt(false)
		h.f.Prompter = &scriptedPrompter{confirm: false}

		_, code := h.run(t, "logout")
		// A decline gives a SilentError and ExitError, and keeps the credentials.
		if code != cmdutil.ExitError {
			t.Fatalf("exit = %d, want %d", code, cmdutil.ExitError)
		}

		if s, _ := keyring.GetSecret(); s != "SK" {
			t.Errorf("secret should be preserved, got %q", s)
		}
	})

	t.Run("confirm accepted deletes creds", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "AK")
		h := newHarness(t, &clienttest.Fake{})
		h.ios.SetStdinTTY(true)
		h.ios.SetStdoutTTY(true)
		h.ios.SetNeverPrompt(false)
		h.f.Prompter = &scriptedPrompter{confirm: true}

		_, code := h.run(t, "logout")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0", code)
		}

		if s, _ := keyring.GetSecret(); s != "" {
			t.Errorf("secret not deleted: %q", s)
		}
	})

	t.Run("--json emits loggedIn false", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "AK")
		h := newHarness(t, &clienttest.Fake{})

		_, code := h.run(t, "logout", "--yes", "--json")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0", code)
		}

		var doc struct {
			LoggedIn bool `json:"loggedIn"`
		}
		if err := json.Unmarshal(h.out.Bytes(), &doc); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, h.out.String())
		}

		if doc.LoggedIn {
			t.Errorf("loggedIn = true, want false: %s", h.out.String())
		}
	})

	t.Run("not logged in maps like every 401", func(t *testing.T) {
		testEnv(t)
		h := newHarness(t, &clienttest.Fake{})

		msg, code := h.run(t, "logout", "--yes")
		if code != cmdutil.ExitAuth {
			t.Fatalf("exit = %d, want %d", code, cmdutil.ExitAuth)
		}

		if msg != "Not logged in. Run `hyperlift auth login`." {
			t.Errorf("msg = %q, want the shared not-logged-in message", msg)
		}
	})

	t.Run("non-interactive without --yes refuses", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "AK")
		h := newHarness(t, &clienttest.Fake{}) // Test streams cannot prompt

		msg, code := h.run(t, "logout")
		if code != cmdutil.ExitError {
			t.Fatalf("exit = %d, want %d", code, cmdutil.ExitError)
		}

		if !strings.Contains(msg, "--yes") {
			t.Errorf("expected --yes hint, got %q", msg)
		}

		// The credentials must stay.
		if s, _ := keyring.GetSecret(); s != "SK" {
			t.Errorf("secret should be preserved, got %q", s)
		}
	})
}

// --- whoami --------------------------------------------------------------

func TestWhoami(t *testing.T) {
	t.Run("table success masks key", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "ABCDEFGH1234")
		h := newHarness(t, &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }})

		_, code := h.run(t, "whoami")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0", code)
		}

		out := h.out.String()
		if !strings.Contains(out, "****1234") {
			t.Errorf("masked key missing from output: %q", out)
		}

		if strings.Contains(out, "ABCDEFGH") {
			t.Errorf("raw key leaked into output: %q", out)
		}

		if !strings.Contains(out, "spaceship.dev") {
			t.Errorf("base URL missing: %q", out)
		}
	})

	t.Run("json output", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "ABCDEFGH1234")
		h := newHarness(t, &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }})

		_, code := h.run(t, "whoami", "--json")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0", code)
		}

		out := h.out.String()
		// The key lives in the config file; the keyring holds only the secret.
		for _, want := range []string{`"baseUrl"`, `"apiKey"`, `"loggedIn"`, `"source": "config"`, "****1234"} {
			if !strings.Contains(out, want) {
				t.Errorf("json missing %q in %q", want, out)
			}
		}
	})

	t.Run("key from the environment reports source env", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "STOREDKEY")
		t.Setenv(config.EnvAPIKey, "ENVKEY9999")
		h := newHarness(t, &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }})

		_, code := h.run(t, "whoami")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0", code)
		}

		out := h.out.String()
		if !strings.Contains(out, "Source") || !strings.Contains(out, "env") {
			t.Errorf("table missing env source: %q", out)
		}

		if !strings.Contains(out, "****9999") {
			t.Errorf("active key must be the env one: %q", out)
		}
	})

	t.Run("quiet outputs masked key only", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "ABCDEFGH1234")
		h := newHarness(t, &clienttest.Fake{ProbeFunc: func(context.Context) error { return nil }})

		_, code := h.run(t, "whoami", "--quiet")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0", code)
		}

		if got := strings.TrimSpace(h.out.String()); got != "****1234" {
			t.Errorf("quiet output = %q, want %q", got, "****1234")
		}
	})

	t.Run("not logged in maps like every 401", func(t *testing.T) {
		testEnv(t)
		h := newHarness(t, &clienttest.Fake{})

		msg, code := h.run(t, "whoami")
		if code != cmdutil.ExitAuth {
			t.Fatalf("exit = %d, want %d", code, cmdutil.ExitAuth)
		}

		if msg != "Not logged in. Run `hyperlift auth login`." {
			t.Errorf("msg = %q, want the shared not-logged-in message", msg)
		}
	})

	// The message and exit code must match what `apps list` produces for the
	// same 401; see TestErrorMapping in the apps package.
	t.Run("401 stored creds invalid maps like every 401", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "AK")
		h := newHarness(t, &clienttest.Fake{ProbeFunc: func(context.Context) error { return apiErr(http.StatusUnauthorized, "") }})

		msg, code := h.run(t, "whoami")
		if code != cmdutil.ExitAuth {
			t.Fatalf("exit = %d, want %d", code, cmdutil.ExitAuth)
		}

		if msg != "Not logged in. Run `hyperlift auth login`." {
			t.Errorf("msg = %q, want the shared not-logged-in message", msg)
		}
	})

	t.Run("401 with --json still answers the question", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "AK")
		h := newHarness(t, &clienttest.Fake{ProbeFunc: func(context.Context) error { return apiErr(http.StatusUnauthorized, "") }})

		_, code := h.run(t, "whoami", "--json")
		if code != cmdutil.ExitAuth {
			t.Fatalf("exit = %d, want %d", code, cmdutil.ExitAuth)
		}

		var doc struct {
			LoggedIn bool `json:"loggedIn"`
		}
		if err := json.Unmarshal(h.out.Bytes(), &doc); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, h.out.String())
		}

		if doc.LoggedIn {
			t.Errorf("loggedIn = true, want false: %s", h.out.String())
		}
	})

	t.Run("403 missing scope is logged in with warning", func(t *testing.T) {
		testEnv(t)
		seedLogin(t, "ABCDEFGH1234")
		h := newHarness(t, &clienttest.Fake{ProbeFunc: func(context.Context) error { return apiErr(http.StatusForbidden, "applications.read") }})

		_, code := h.run(t, "whoami")
		if code != cmdutil.ExitOK {
			t.Fatalf("exit = %d, want 0", code)
		}

		// A 403 means the key authenticates; only a scope is missing. The
		// table must read as logged in, matching login's 403 handling.
		out := h.out.String()
		if !strings.Contains(out, "true") || strings.Contains(out, "false") {
			t.Errorf("expected logged_in true in table: %q", out)
		}

		if !strings.Contains(h.errOut.String(), "scope") {
			t.Errorf("expected scope warning on stderr: %q", h.errOut.String())
		}
	})
}

// --- unit: maskKey / classifiers ----------------------------------------

func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"ab":           "****",
		"abcd":         "****",
		"abcde":        "****bcde",
		"ABCDEFGH1234": "****1234",
	}

	for in, want := range cases {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyProbe(t *testing.T) {
	t.Run("nil ok", func(t *testing.T) {
		w, err := classifyProbe(nil)
		if w != "" || err != nil {
			t.Fatalf("want empty/nil, got (%q, %v)", w, err)
		}
	})
	t.Run("401 silent error", func(t *testing.T) {
		_, err := classifyProbe(apiErr(http.StatusUnauthorized, ""))

		var se *cmdutil.SilentError
		if !errors.As(err, &se) {
			t.Fatalf("want SilentError, got %T (%v)", err, err)
		}
	})
	t.Run("403 warning", func(t *testing.T) {
		w, err := classifyProbe(apiErr(http.StatusForbidden, "applications.read"))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}

		if !strings.Contains(w, "applications.read") {
			t.Errorf("warning = %q, want scope name", w)
		}
	})
	t.Run("other surfaced", func(t *testing.T) {
		in := apiErr(http.StatusInternalServerError, "")

		_, err := classifyProbe(in)
		if err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("non-api error surfaced", func(t *testing.T) {
		in := errors.New("dial tcp: timeout")

		_, err := classifyProbe(in)
		if err != in {
			t.Fatalf("want passthrough, got %v", err)
		}
	})
}

func TestClassifyWhoami(t *testing.T) {
	t.Run("nil logged in", func(t *testing.T) {
		loggedIn, scopeLimited, err := classifyWhoami(nil)
		if !loggedIn || scopeLimited || err != nil {
			t.Fatalf("got (%v, %v, %v), want (true, false, nil)", loggedIn, scopeLimited, err)
		}
	})
	t.Run("401 passes through for the shared mapping", func(t *testing.T) {
		in := apiErr(http.StatusUnauthorized, "")

		loggedIn, _, err := classifyWhoami(in)
		if loggedIn || !errors.Is(err, in) {
			t.Fatalf("got (%v, %v), want the 401 unchanged", loggedIn, err)
		}
	})
	t.Run("403 logged in but scope-limited", func(t *testing.T) {
		loggedIn, scopeLimited, err := classifyWhoami(apiErr(http.StatusForbidden, "applications.read"))
		if !loggedIn || !scopeLimited || err != nil {
			t.Fatalf("got (%v, %v, %v), want (true, true, nil)", loggedIn, scopeLimited, err)
		}
	})
	t.Run("other surfaced", func(t *testing.T) {
		if _, _, err := classifyWhoami(apiErr(http.StatusInternalServerError, "")); err == nil {
			t.Fatal("want error")
		}
	})
}

// TestCommandTree checks that NewCmdAuth registers the three subcommands, and
// that whoami keeps its `status` alias.
func TestCommandTree(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	got := map[string]*cobra.Command{}
	for _, c := range NewCmdAuth(cmdutil.NewFactory(ios)).Commands() {
		got[c.Name()] = c
	}

	for _, want := range []string{"login", "logout", "whoami"} {
		if got[want] == nil {
			t.Errorf("missing subcommand %q", want)
		}
	}

	if w := got["whoami"]; w == nil || len(w.Aliases) == 0 || w.Aliases[0] != "status" {
		t.Errorf("whoami should alias status, got %+v", w)
	}
}
