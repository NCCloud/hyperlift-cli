package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	zk "github.com/zalando/go-keyring"

	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/testapi"
)

// TestJSONKeysAreCamelCase runs every JSON-emitting command against the contract
// mock through the real client, the only test that covers the full wiring across
// the command surface. The camelCase walk holds the README "Machine output"
// promise. `update` is excluded; its shape is pinned in internal/cmd/update.
func TestJSONKeysAreCamelCase(t *testing.T) {
	zk.MockInit()

	srv := httptest.NewServer(testapi.Server())
	defer srv.Close()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HYPERLIFT_BASE_URL", srv.URL)
	t.Setenv("HYPERLIFT_API_KEY", "k")
	t.Setenv("HYPERLIFT_API_SECRET", "s")
	t.Setenv("HYPERLIFT_NO_UPDATE_CHECK", "1")

	// Ordered: login stores the key that logout then removes.
	// Every new JSON-emitting leaf command must be added here.
	cases := []struct {
		args   []string
		ndjson bool
		// envKeys lists JSON paths of objects whose keys are environment
		// variable names: user data, exempt from the casing rule.
		envKeys []string
	}{
		{args: []string{"apps", "list"}},
		{args: []string{"apps", "get", "app_a1b2c3"}},
		{args: []string{"apps", "build", "app_a1b2c3"}},
		{args: []string{"apps", "start", "app_a1b2c3"}},
		{args: []string{"apps", "stop", "app_a1b2c3"}},
		{args: []string{"apps", "restart", "app_a1b2c3"}},
		{args: []string{"env", "get", "app_a1b2c3"}, envKeys: []string{"$"}},
		{args: []string{"env", "set", "app_a1b2c3", "CASING_PROBE=1"}, envKeys: []string{"$"}},
		{args: []string{"env", "unset", "app_a1b2c3", "CASING_PROBE"}, envKeys: []string{"$"}},
		{args: []string{"logs", "app_a1b2c3"}, ndjson: true},
		{args: []string{"metrics", "app_a1b2c3"}},
		{args: []string{"auth", "whoami"}},
		{args: []string{"auth", "login", "--key", "k", "--secret", "s"}},
		{args: []string{"auth", "logout", "--yes"}},
		{args: []string{"version"}},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			exempt := make(map[string]bool, len(tc.envKeys))
			for _, p := range tc.envKeys {
				exempt[p] = true
			}

			io, _, out, errOut := iostreams.Test()

			args := append(append([]string(nil), tc.args...), "--json")
			if code := Run(context.Background(), io, args); code != 0 {
				t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
			}

			docs := []string{out.String()}
			if tc.ndjson {
				docs = strings.Split(strings.TrimSpace(out.String()), "\n")
			}

			for _, doc := range docs {
				var v any
				if err := json.Unmarshal([]byte(doc), &v); err != nil {
					t.Fatalf("stdout is not JSON: %v\n%s", err, doc)
				}

				assertCamelKeys(t, "$", v, exempt)
			}
		})
	}
}

// camelKey is the shape of a contract field name.
var camelKey = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

// assertCamelKeys fails on any object key that is not lowerCamelCase. An
// object at an exempt path holds environment-variable names as keys; those are
// data, so only its values are walked.
func assertCamelKeys(t *testing.T, path string, v any, exempt map[string]bool) {
	t.Helper()

	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			if !exempt[path] && !camelKey.MatchString(k) {
				t.Errorf("non-camelCase key %q at %s", k, path)
			}

			assertCamelKeys(t, path+"."+k, vv, exempt)
		}
	case []any:
		for i, vv := range x {
			assertCamelKeys(t, fmt.Sprintf("%s[%d]", path, i), vv, exempt)
		}
	}
}
