package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeFile writes a test fixture file.
func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o755) //nolint:gosec // test fixture; exec bit is intentional
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.2.3", "1.2.3", false},
		{"2.0.0", "1.9.9", false},
		{"v1.0.0", "v1.0.1", true},     // a leading v is accepted
		{"dev", "0.0.1", true},         // dev always upgrades
		{"", "1.0.0", true},            // empty accepts any release
		{"1.0.0", "", false},           // there is no latest
		{"1.0.0-rc.1", "1.0.0", true},  // a release beats its pre-release
		{"1.0.0", "1.0.0-rc.1", false}, // a pre-release does not beat a release
		{"1.0.0-rc.1", "1.0.0-rc.2", true},
		{"1.10.0", "1.9.0", false}, // compare numbers, not text
		{"1.9.0", "1.10.0", true},
	}

	for _, c := range cases {
		if got := isNewer(c.current, c.latest); got != c.want {
			t.Errorf("isNewer(%q,%q)=%v want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte("" +
		"abc123  hyperlift_darwin_arm64.tar.gz\n" +
		"# a comment\n" +
		"\n" +
		"DEF456 *hyperlift_linux_amd64.tar.gz\n")

	got, err := parseChecksums(data)
	if err != nil {
		t.Fatalf("parseChecksums: %v", err)
	}

	if got["hyperlift_darwin_arm64.tar.gz"] != "abc123" {
		t.Errorf("darwin sum = %q", got["hyperlift_darwin_arm64.tar.gz"])
	}

	// The binary marker is removed and the hex digest is lowercase.
	if got["hyperlift_linux_amd64.tar.gz"] != "def456" {
		t.Errorf("linux sum = %q", got["hyperlift_linux_amd64.tar.gz"])
	}
}

func TestParseChecksums_Empty(t *testing.T) {
	if _, err := parseChecksums([]byte("# only comments\n\n")); err == nil {
		t.Fatal("want error for empty checksums")
	}
}

func TestAssetFor(t *testing.T) {
	r := release{
		TagName: "v1.0.0",
		Assets: []releaseAsset{
			{Name: "SHA256SUMS", URL: "u-sums"},
			// The metadata files sort before the archive, so a missing filter
			// would match them first.
			{Name: "hyperlift_darwin_arm64.tar.gz.sha256", URL: "u-sha"},
			{Name: "hyperlift_darwin_arm64.tar.gz.sig", URL: "u-sig"},
			{Name: "hyperlift_darwin_arm64.tar.gz.pem", URL: "u-pem"},
			{Name: "hyperlift_darwin_arm64.tar.gz.sigstore.json", URL: "u-bundle"},
			{Name: "hyperlift_darwin_arm64.tar.gz", URL: "u-darwin-arm64"},
			{Name: "hyperlift_darwin_amd64.tar.gz", URL: "u-darwin-amd64"},
			{Name: "hyperlift_linux_amd64.tar.gz", URL: "u-linux-amd64"},
		},
	}

	a, ok := r.AssetFor("darwin", "arm64")
	if !ok || a.URL != "u-darwin-arm64" {
		t.Fatalf("darwin/arm64 -> %+v ok=%v", a, ok)
	}

	a, ok = r.AssetFor("linux", "amd64")
	if !ok || a.URL != "u-linux-amd64" {
		t.Fatalf("linux/amd64 -> %+v ok=%v", a, ok)
	}

	if _, ok := r.AssetFor("windows", "386"); ok {
		t.Fatal("should not match windows/386")
	}
}

func TestContainsToken(t *testing.T) {
	if containsToken("hyperlift_linux_arm640.tar.gz", "arm64") {
		t.Error("arm64 must not match inside arm640")
	}

	if !containsToken("hyperlift_linux_arm64.tar.gz", "arm64") {
		t.Error("arm64 should match as a delimited token")
	}

	if !containsToken("hyperlift-darwin-amd64", "amd64") {
		t.Error("amd64 should match with dash delimiters")
	}
}

func TestGitHubReleases_Latest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/nccloud/hyperlift-cli/releases/latest" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v3.1.4","assets":[{"name":"SHA256SUMS","browser_download_url":"x"}]}`))
	}))
	defer srv.Close()

	g := newGitHubReleases()
	g.apiBaseURL = srv.URL

	rel, err := g.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}

	if rel.Version() != "3.1.4" {
		t.Fatalf("version = %q", rel.Version())
	}
}

func TestGitHubReleases_Latest_NoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	g := newGitHubReleases()
	g.apiBaseURL = srv.URL

	_, err := g.Latest(context.Background())
	if !errors.Is(err, errNoReleases) {
		t.Fatalf("want errNoReleases, got %v", err)
	}
}

func TestGitHubReleases_DownloadAndChecksums(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write([]byte("binary-bytes"))
		case "/sums":
			_, _ = w.Write([]byte("aa11  hyperlift_x.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	g := newGitHubReleases()

	b, err := g.Download(context.Background(), releaseAsset{Name: "x", URL: srv.URL + "/asset"})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if string(b) != "binary-bytes" {
		t.Fatalf("download = %q", b)
	}

	rel := release{Assets: []releaseAsset{{Name: "SHA256SUMS", URL: srv.URL + "/sums"}}}

	sums, err := g.Checksums(context.Background(), rel)
	if err != nil {
		t.Fatalf("Checksums: %v", err)
	}

	if sums["hyperlift_x.tar.gz"] != "aa11" {
		t.Fatalf("sums = %v", sums)
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()

	dst := filepath.Join(dir, "hyperlift")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil { //nolint:gosec // test fixture; exec bit is what we assert is preserved
		t.Fatal(err)
	}

	newBytes := []byte("new-binary")
	if err := replaceBinary(dst, newBytes); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	got, err := os.ReadFile(dst) //nolint:gosec // dst is a temp path created in this test
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(newBytes) {
		t.Fatalf("content = %q want %q", got, newBytes)
	}

	// Windows reports its own mode bits, so the executable bit means nothing
	// there.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}

		if fi.Mode().Perm()&0o100 == 0 {
			t.Fatalf("binary not executable: mode %v", fi.Mode())
		}
	}

	// No temp file may stay behind. Windows also keeps the .old backup, which
	// the next run removes.
	wantEntries := 1
	if runtime.GOOS == "windows" {
		wantEntries = 2
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != wantEntries {
		t.Fatalf("expected %d files, got %d: %v", wantEntries, len(entries), entries)
	}
}

// TestReplaceBinaryKeepsOriginalWhenRenameFails proves a failed move into place
// leaves the user with a working binary. On Windows that needs the rollback,
// because the original has already moved aside by then; elsewhere the original
// is simply never touched. One assertion covers both, so the Windows-only
// rollback is exercised in CI without a Windows-only test.
func TestReplaceBinaryKeepsOriginalWhenRenameFails(t *testing.T) {
	dir := t.TempDir()

	dst := filepath.Join(dir, "hyperlift")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}

	// Fail the move into place, and only that one: the move aside renames to
	// dst+".old", and the rollback renames back to dst and must succeed.
	errBlocked := errors.New("rename blocked")
	orig := renameFile
	blocked := false

	renameFile = func(from, to string) error {
		if to == dst && !blocked {
			blocked = true
			return errBlocked
		}

		return orig(from, to)
	}

	t.Cleanup(func() { renameFile = orig })

	err := replaceBinary(dst, []byte("new"))
	// The sentinel proves the failure came from the move into place. Without it
	// the test would also pass if replaceBinary failed earlier, before the
	// rollback it exists to cover.
	if !errors.Is(err, errBlocked) {
		t.Fatalf("err = %v, want the blocked rename", err)
	}

	if !blocked {
		t.Fatal("the move into place was never attempted")
	}

	got, err := os.ReadFile(dst) //nolint:gosec // dst is a temp path created in this test
	if err != nil {
		t.Fatalf("original binary is gone: %v", err)
	}

	if string(got) != "old" {
		t.Fatalf("content = %q, want the original", got)
	}
}
