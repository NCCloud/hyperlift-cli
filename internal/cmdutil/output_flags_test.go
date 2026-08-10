package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// writeConfig stores a config file with the given default_output in a fresh
// XDG_CONFIG_HOME, and returns the config path.
func writeConfig(t *testing.T, defaultOutput string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path := filepath.Join(dir, "hyperlift", "config.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("default_output: "+defaultOutput+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestUnknownDefaultOutputWarnsOnceAndFallsBackToTable(t *testing.T) {
	path := writeConfig(t, "yml")

	ios, _, _, errOut := iostreams.Test()
	f := NewFactory(ios)
	AddOutputFlags(&cobra.Command{Use: "root"}, f)

	if got := f.Output(); got != output.FormatTable {
		t.Errorf("Output() = %q, want table fallback", got)
	}

	f.Output() // a second resolve must not warn again

	stderr := errOut.String()
	if !strings.Contains(stderr, `"yml"`) || !strings.Contains(stderr, path) {
		t.Errorf("warning %q must name the bad value and the config file %q", stderr, path)
	}

	if strings.Count(stderr, "Warning:") != 1 {
		t.Errorf("want exactly one warning, got: %q", stderr)
	}
}

func TestValidDefaultOutputIsUsed(t *testing.T) {
	writeConfig(t, "json")

	ios, _, _, errOut := iostreams.Test()
	f := NewFactory(ios)
	AddOutputFlags(&cobra.Command{Use: "root"}, f)

	if got := f.Output(); got != output.FormatJSON {
		t.Errorf("Output() = %q, want json from config", got)
	}

	if errOut.Len() != 0 {
		t.Errorf("unexpected warning: %q", errOut.String())
	}
}
