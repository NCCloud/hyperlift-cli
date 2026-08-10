package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tempConfigDir points XDG_CONFIG_HOME at a new temp dir, so each test works on
// its own config file. It returns the resolved config path.
func tempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	return filepath.Join(dir, "hyperlift", "config.yml")
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := tempConfigDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load (missing file): %v", err)
	}

	cfg.StoredBaseURL = "https://api.example.test/v1"
	cfg.StoredAPIKey = "key-123"

	cfg.StoredDefaultOutput = "json"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	// POSIX only. Windows reports a writable file as 0666, whatever creation
	// mode the code requests.
	if perm := fi.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("config file mode = %o, want 600", perm)
	}

	// The write is atomic, so no temp file may stay behind.
	if leftovers, _ := filepath.Glob(path + ".tmp*"); len(leftovers) != 0 {
		t.Errorf("temp files left behind after Save: %v", leftovers)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.StoredBaseURL != cfg.StoredBaseURL ||
		got.StoredAPIKey != cfg.StoredAPIKey ||
		got.StoredDefaultOutput != cfg.StoredDefaultOutput {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, cfg)
	}
}

func TestSaveOverwritesExisting(t *testing.T) {
	tempConfigDir(t)

	cfg, _ := Load()

	cfg.StoredAPIKey = "first"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg.StoredAPIKey = "second"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save (overwrite): %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.StoredAPIKey != "second" {
		t.Errorf("StoredAPIKey = %q, want %q", got.StoredAPIKey, "second")
	}
}

func TestLoadMissingFileIsEmptyConfig(t *testing.T) {
	tempConfigDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.StoredBaseURL != "" || cfg.StoredAPIKey != "" || cfg.StoredDefaultOutput != "" {
		t.Errorf("missing file must load as empty config, got %+v", cfg)
	}

	if cfg.BaseURL() != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want default %q", cfg.BaseURL(), DefaultBaseURL)
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	path := tempConfigDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("{not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("want parse error, got %v", err)
	}
}

func TestEnvPrecedence(t *testing.T) {
	tempConfigDir(t)

	cfg := &Config{
		StoredBaseURL: "https://from-config.test",
		StoredAPIKey:  "config-key",
	}

	t.Setenv(EnvBaseURL, "https://from-env.test")
	t.Setenv(EnvAPIKey, "env-key")

	if got := cfg.BaseURL(); got != "https://from-env.test" {
		t.Errorf("BaseURL = %q, want env value", got)
	}

	if got := cfg.APIKey(); got != "env-key" {
		t.Errorf("APIKey = %q, want env value", got)
	}

	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "")

	if got := cfg.BaseURL(); got != "https://from-config.test" {
		t.Errorf("BaseURL = %q, want config value", got)
	}

	if got := cfg.APIKey(); got != "config-key" {
		t.Errorf("APIKey = %q, want config value", got)
	}
}

func TestDefaultOutput(t *testing.T) {
	cfg := &Config{}
	if got := cfg.DefaultOutput(); got != "" {
		t.Errorf("DefaultOutput = %q, want empty (caller falls back to table)", got)
	}

	cfg.StoredDefaultOutput = "quiet"
	if got := cfg.DefaultOutput(); got != "quiet" {
		t.Errorf("DefaultOutput = %q, want quiet", got)
	}
}
