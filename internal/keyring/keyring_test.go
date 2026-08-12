package keyring

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	zk "github.com/zalando/go-keyring"
)

// useFallback makes every keyring-backend call fail, through the library mock,
// and points the config dir at a temp dir. A test then runs only the
// file-fallback path and never the real OS keyring. It returns the expected
// fallback file path.
func useFallback(t *testing.T) string {
	t.Helper()
	zk.MockInitWithError(errors.New("keyring backend unavailable"))

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	return filepath.Join(dir, "hyperlift", "secrets", account+".secret")
}

func TestFileFallbackRoundTrip(t *testing.T) {
	path := useFallback(t)
	t.Setenv(EnvAPISecret, "")

	usedFallback, err := SetSecret("s3cr3t")
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if !usedFallback {
		t.Error("SetSecret should report the file fallback when the keyring is unavailable")
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("fallback file not written: %v", err)
	}
	// POSIX only. Windows reports a writable file as 0666, whatever creation
	// mode the code requests.
	if perm := fi.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("fallback file mode = %o, want 600", perm)
	}

	got, err := GetSecret()
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}

	if got != "s3cr3t" {
		t.Errorf("GetSecret = %q, want %q", got, "s3cr3t")
	}
}

func TestSetSecretWithWorkingKeyring(t *testing.T) {
	zk.MockInit()
	t.Setenv(EnvAPISecret, "")

	usedFallback, err := SetSecret("in-keyring")
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if usedFallback {
		t.Error("SetSecret should not report the fallback when the keyring works")
	}
}

func TestSetSecretKeyringRecoveryRemovesFallback(t *testing.T) {
	path := useFallback(t)
	t.Setenv(EnvAPISecret, "")

	// The first write lands in the fallback file while the keyring is down.
	if _, err := SetSecret("old"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	// The keyring recovers: the next write must remove the fallback file, so
	// no stale plaintext copy lingers.
	zk.MockInit()

	usedFallback, err := SetSecret("new")
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if usedFallback {
		t.Error("SetSecret should use the keyring once it works")
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("fallback file still present after a keyring write: %v", err)
	}

	if got, err := GetSecret(); err != nil || got != "new" {
		t.Errorf("GetSecret = (%q, %v), want (new, nil)", got, err)
	}
}

func TestFileFallbackReplacesLooseModeFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced this way on Windows")
	}

	path := useFallback(t)
	t.Setenv(EnvAPISecret, "")

	// A pre-existing loose file must not keep its mode: SetSecret writes a
	// fresh 0600 temp file and renames it over the target.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec // the loose mode is the test fixture
		t.Fatal(err)
	}

	if _, err := SetSecret("new"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("fallback file mode = %o, want 600", perm)
	}

	if got, _ := GetSecret(); got != "new" {
		t.Errorf("GetSecret = %q, want %q", got, "new")
	}
}

func TestDeleteSecretRemovesFallback(t *testing.T) {
	path := useFallback(t)
	t.Setenv(EnvAPISecret, "")

	if _, err := SetSecret("gone-soon"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if err := DeleteSecret(); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("fallback file still present after delete: %v", err)
	}

	got, err := GetSecret()
	if err != nil {
		t.Fatalf("GetSecret after delete: %v", err)
	}

	if got != "" {
		t.Errorf("GetSecret = %q after delete, want empty", got)
	}

	// A second delete, with nothing stored, must not fail.
	if err := DeleteSecret(); err != nil {
		t.Errorf("DeleteSecret on empty store: %v", err)
	}
}

func TestGetSecretNothingStored(t *testing.T) {
	useFallback(t)
	t.Setenv(EnvAPISecret, "")

	got, err := GetSecret()
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}

	if got != "" {
		t.Errorf("GetSecret = %q, want empty when nothing is stored", got)
	}
}

func TestEnvOverrideWinsOverStored(t *testing.T) {
	useFallback(t)

	if _, err := SetSecret("stored"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	t.Setenv(EnvAPISecret, "from-env")

	got, err := GetSecret()
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}

	if got != "from-env" {
		t.Errorf("GetSecret = %q, want the %s override", got, EnvAPISecret)
	}
}
