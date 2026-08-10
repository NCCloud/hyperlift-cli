package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// tarGzArchive builds an in-memory .tar.gz that holds the given files.
func tarGzArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, data := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}

		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return buf.Bytes()
}

// zipArchive builds an in-memory .zip that holds the given files.
func zipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}

		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	return buf.Bytes()
}

// fakeReleases is an in-memory releaseSource for tests.
type fakeReleases struct {
	latest    release
	latestErr error

	downloads map[string][]byte // asset URL to bytes
	dlErr     error

	checksums    map[string]string // filename to hex digest
	checksumsErr error
}

func (f *fakeReleases) Latest(context.Context) (release, error) {
	return f.latest, f.latestErr
}

func (f *fakeReleases) Download(_ context.Context, a releaseAsset) ([]byte, error) {
	if f.dlErr != nil {
		return nil, f.dlErr
	}

	b, ok := f.downloads[a.URL]
	if !ok {
		return nil, errors.New("no such asset")
	}

	return b, nil
}

func (f *fakeReleases) Checksums(context.Context, release) (map[string]string, error) {
	if f.checksumsErr != nil {
		return nil, f.checksumsErr
	}

	return f.checksums, nil
}

// TestRunUpdate_ReportModes covers every path that only reports: no releases,
// already current, --check, and the non-interactive --json and --quiet formats.
// None of them may touch the binary.
func TestRunUpdate_ReportModes(t *testing.T) {
	cases := []struct {
		name       string
		format     string
		current    string
		latest     string // empty means the repository has no releases
		checkOnly  bool
		want       []string
		wantExact  string
		wantSilent bool
	}{
		{name: "no releases", format: output.FormatTable, current: "1.0.0", want: []string{"no releases"}},
		{name: "already latest", format: output.FormatTable, current: "1.2.3", latest: "v1.2.3", want: []string{"already on the latest"}},
		{name: "older release is never installed", format: output.FormatTable, current: "2.0.0", latest: "v1.0.0", want: []string{"already on the latest"}},
		{name: "check reports the new version", format: output.FormatTable, current: "1.0.0", latest: "v2.0.0", checkOnly: true, want: []string{"new version", "2.0.0"}},
		{name: "dev build is offered any release", format: output.FormatTable, current: "dev", latest: "v0.0.1", checkOnly: true, want: []string{"0.0.1"}},
		{name: "json available", format: output.FormatJSON, current: "1.0.0", latest: "v2.0.0", want: []string{`"currentVersion": "1.0.0"`, `"latestVersion": "2.0.0"`, `"updateAvailable": true`}},
		{name: "json current", format: output.FormatJSON, current: "1.0.0", latest: "v1.0.0", checkOnly: true, want: []string{`"currentVersion": "1.0.0"`, `"latestVersion": "1.0.0"`, `"updateAvailable": false`}},
		{name: "quiet available prints the version alone", format: output.FormatQuiet, current: "1.0.0", latest: "v2.0.0", checkOnly: true, wantExact: "2.0.0"},
		{name: "quiet current prints nothing", format: output.FormatQuiet, current: "1.0.0", latest: "v1.0.0", checkOnly: true, wantSilent: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			io, _, out, errOut := iostreams.Test()

			src := &fakeReleases{latest: release{TagName: c.latest}}
			if c.latest == "" {
				src.latestErr = errNoReleases
			}

			replaced := false
			opts := &updateOptions{
				IO:             io,
				Output:         func() string { return c.format },
				releases:       src,
				currentVersion: c.current,
				checkOnly:      c.checkOnly,
				executable:     func() (string, error) { return "/x", nil },
				replace:        func(string, []byte) error { replaced = true; return nil },
				reexec:         func(string, []string) error { return nil },
			}

			if err := runUpdate(context.Background(), opts); err != nil {
				t.Fatalf("runUpdate: %v", err)
			}

			if replaced {
				t.Fatal("a report-only run must never replace the binary")
			}

			if c.wantSilent && (out.String() != "" || errOut.String() != "") {
				t.Fatalf("want no output, got stdout %q stderr %q", out.String(), errOut.String())
			}

			if c.wantExact != "" && strings.TrimSpace(out.String()) != c.wantExact {
				t.Fatalf("stdout = %q, want exactly %q", out.String(), c.wantExact)
			}

			for _, w := range c.want {
				if !strings.Contains(out.String(), w) {
					t.Fatalf("stdout = %q, missing %q", out.String(), w)
				}
			}
		})
	}
}

func TestRunUpdate_FullInstall_Success(t *testing.T) {
	// The downloaded asset is a real GoReleaser-style archive. The updater must
	// check the checksum of the archive, then extract and install the binary.
	binData := []byte("brand-new-binary-bytes")
	archive := tarGzArchive(t, map[string][]byte{
		"hyperlift": binData,
		"README.md": []byte("readme"),
		"LICENSE":   []byte("license"),
	})
	name := "hyperlift_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	url := "https://example/" + name

	io, _, out, _ := iostreams.Test()

	var (
		replacedPath  string
		replacedBytes []byte
		reexecPath    string
	)

	opts := &updateOptions{
		IO:     io,
		Output: func() string { return output.FormatTable },
		releases: &fakeReleases{
			latest: release{
				TagName: "v2.0.0",
				Assets: []releaseAsset{
					{Name: name, URL: url},
					{Name: checksumsFilename, URL: "https://example/SHA256SUMS"},
				},
			},
			downloads: map[string][]byte{url: archive},
			checksums: map[string]string{name: sha256Hex(archive)},
		},
		currentVersion: "1.0.0",
		replace: func(dst string, b []byte) error {
			replacedPath = dst
			replacedBytes = b

			return nil
		},
		reexec: func(path string, _ []string) error {
			reexecPath = path
			return nil
		},
	}

	// Use a real path that exists, so EvalSymlinks succeeds.
	tmpExe := t.TempDir() + "/hyperlift"
	if err := writeFile(tmpExe, []byte("old")); err != nil {
		t.Fatal(err)
	}

	opts.executable = func() (string, error) { return tmpExe, nil }

	resolvedExe, err := filepath.EvalSymlinks(tmpExe)
	if err != nil {
		t.Fatal(err)
	}

	if err := runUpdate(context.Background(), opts); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}

	if replacedPath != resolvedExe {
		t.Fatalf("replaced path = %q, want %q", replacedPath, resolvedExe)
	}

	// The written bytes must be the extracted binary, not the raw archive.
	if string(replacedBytes) != string(binData) {
		t.Fatalf("replaced bytes = %q, want extracted binary %q", replacedBytes, binData)
	}

	if reexecPath != resolvedExe {
		t.Fatalf("reexec path = %q, want %q", reexecPath, resolvedExe)
	}

	if !strings.Contains(out.String(), "Checksum verified") {
		t.Fatalf("want 'Checksum verified' in output, got %q", out.String())
	}

	if !strings.Contains(out.String(), "Updated") {
		t.Fatalf("want 'Updated' in output, got %q", out.String())
	}
}

func TestRunUpdate_ChecksumMismatch(t *testing.T) {
	io, _, _, _ := iostreams.Test()

	name := "hyperlift_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	url := "https://example/" + name
	archive := tarGzArchive(t, map[string][]byte{"hyperlift": []byte("real-bytes")})

	tmpExe := t.TempDir() + "/hyperlift"
	if err := writeFile(tmpExe, []byte("old")); err != nil {
		t.Fatal(err)
	}

	replaced := false
	opts := &updateOptions{
		IO:     io,
		Output: func() string { return output.FormatTable },
		releases: &fakeReleases{
			latest: release{
				TagName: "v2.0.0",
				Assets: []releaseAsset{
					{Name: name, URL: url},
					{Name: checksumsFilename, URL: "https://example/SHA256SUMS"},
				},
			},
			downloads: map[string][]byte{url: archive},
			checksums: map[string]string{name: sha256Hex([]byte("DIFFERENT"))},
		},
		currentVersion: "1.0.0",
		executable:     func() (string, error) { return tmpExe, nil },
		replace:        func(string, []byte) error { replaced = true; return nil },
		reexec:         func(string, []string) error { return nil },
	}

	err := runUpdate(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch error, got %v", err)
	}

	if replaced {
		t.Fatal("binary must NOT be replaced on checksum mismatch")
	}
}

// TestExtractBinary covers both real archive formats, on any host platform.
func TestExtractBinary(t *testing.T) {
	want := []byte("the-binary")

	t.Run("tar.gz", func(t *testing.T) {
		archive := tarGzArchive(t, map[string][]byte{
			"hyperlift": want,
			"README.md": []byte("x"),
			"LICENSE":   []byte("y"),
		})

		got, err := extractBinary(archive, "hyperlift_linux_amd64.tar.gz")
		if err != nil {
			t.Fatalf("extractBinary: %v", err)
		}

		if string(got) != string(want) {
			t.Fatalf("extracted %q, want %q", got, want)
		}
	})

	t.Run("zip", func(t *testing.T) {
		archive := zipArchive(t, map[string][]byte{
			"hyperlift.exe": want,
			"README.md":     []byte("x"),
		})

		got, err := extractBinary(archive, "hyperlift_windows_amd64.zip")
		if err != nil {
			t.Fatalf("extractBinary: %v", err)
		}

		if string(got) != string(want) {
			t.Fatalf("extracted %q, want %q", got, want)
		}
	})

	t.Run("missing binary errors", func(t *testing.T) {
		archive := tarGzArchive(t, map[string][]byte{"README.md": []byte("x")})

		if _, err := extractBinary(archive, "hyperlift_linux_amd64.tar.gz"); err == nil {
			t.Fatal("want error when no binary present")
		}
	})

	t.Run("unsupported format errors", func(t *testing.T) {
		if _, err := extractBinary([]byte("x"), "hyperlift_linux_amd64.rar"); err == nil {
			t.Fatal("want error for unsupported format")
		}
	})

	// A corrupt archive that passed the checksum (a mis-built release) must
	// error, never yield garbage bytes.
	t.Run("corrupt tar.gz errors", func(t *testing.T) {
		if _, err := extractBinary([]byte("not gzip at all"), "hyperlift_linux_amd64.tar.gz"); err == nil {
			t.Fatal("want error for corrupt gzip data")
		}
	})

	t.Run("truncated tar.gz errors", func(t *testing.T) {
		archive := tarGzArchive(t, map[string][]byte{"hyperlift": []byte("the-binary")})

		if _, err := extractBinary(archive[:len(archive)/2], "hyperlift_linux_amd64.tar.gz"); err == nil {
			t.Fatal("want error for truncated archive")
		}
	})

	t.Run("corrupt zip errors", func(t *testing.T) {
		if _, err := extractBinary([]byte("not a zip"), "hyperlift_windows_amd64.zip"); err == nil {
			t.Fatal("want error for corrupt zip data")
		}
	})
}

// TestNewCmdUpdate_Wiring pins the command surface: the --check flag exists and
// positional arguments are rejected. It never runs the command, so the real
// GitHub release source is never called.
func TestNewCmdUpdate_Wiring(t *testing.T) {
	io, _, _, _ := iostreams.Test()
	cmd := NewCmdUpdate(&cmdutil.Factory{IOStreams: io})

	if cmd.Use != "update" {
		t.Errorf("Use = %q, want update", cmd.Use)
	}

	fl := cmd.Flags().Lookup("check")
	if fl == nil {
		t.Fatal("--check flag not registered")
	}

	if fl.DefValue != "false" {
		t.Errorf("--check default = %q, want false", fl.DefValue)
	}

	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("positional arguments must be rejected")
	}
}

func TestManagedInstallHint(t *testing.T) {
	cases := []struct {
		path     string
		wantHint string
	}{
		{"/opt/homebrew/Cellar/hyperlift/1.2.3/bin/hyperlift", "brew upgrade hyperlift"},
		{"/opt/homebrew/bin/hyperlift", "brew upgrade hyperlift"},
		{"/usr/local/Cellar/hyperlift/1.2.3/bin/hyperlift", "brew upgrade hyperlift"},
		{"/home/linuxbrew/.linuxbrew/Cellar/hyperlift/1.2.3/bin/hyperlift", "brew upgrade hyperlift"},
		{`C:\Users\me\scoop\apps\hyperlift\current\hyperlift.exe`, "scoop update hyperlift"},
		{`C:\Users\me\Scoop\shims\hyperlift.exe`, "scoop update hyperlift"},
		{"/usr/local/bin/hyperlift", ""},
		{"/home/me/scooper/hyperlift", ""}, // "scoop" must match one whole path element
		{"/home/me/.local/bin/hyperlift", ""},
	}

	for _, c := range cases {
		hint, managed := managedInstallHint(c.path)
		if managed != (c.wantHint != "") || hint != c.wantHint {
			t.Errorf("managedInstallHint(%q) = (%q, %v), want hint %q", c.path, hint, managed, c.wantHint)
		}
	}
}

func TestManagedInstallHintCustomScoopRoot(t *testing.T) {
	t.Setenv("SCOOP", `D:\tools\pkg`)

	hint, managed := managedInstallHint(`D:\tools\pkg\apps\hyperlift\current\hyperlift.exe`)
	if !managed || hint != "scoop update hyperlift" {
		t.Errorf("managedInstallHint under custom SCOOP root = (%q, %v)", hint, managed)
	}
}

func TestRunUpdate_NoAssetForPlatform(t *testing.T) {
	io, _, _, _ := iostreams.Test()
	opts := &updateOptions{
		IO:     io,
		Output: func() string { return output.FormatTable },
		releases: &fakeReleases{
			latest: release{
				TagName: "v2.0.0",
				Assets: []releaseAsset{
					{Name: "hyperlift_plan9_sparc.tar.gz", URL: "https://example/x"},
					{Name: checksumsFilename, URL: "https://example/SHA256SUMS"},
				},
			},
		},
		currentVersion: "1.0.0",
		executable:     func() (string, error) { return "/x", nil },
		replace:        func(string, []byte) error { return nil },
		reexec:         func(string, []string) error { return nil },
	}

	err := runUpdate(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "no release asset") {
		t.Fatalf("want 'no release asset' error, got %v", err)
	}
}
