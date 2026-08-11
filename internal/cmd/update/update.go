// Package update holds the self-update command, which replaces the running
// binary with the latest GitHub release. It also holds the daily "new version
// available" notice, which the root command starts and flushes around each run.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/build"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// updateOptions holds the wiring of one `update` run. The release source, the
// executable-path resolver and the replace and re-exec hooks are fields, so
// tests can run the command without a real filesystem, network or process.
type updateOptions struct {
	IO     *iostreams.IOStreams
	Output func() string

	releases       releaseSource
	currentVersion string

	executable func() (string, error)
	replace    func(dst string, newBin []byte) error
	reexec     func(path string, args []string) error

	// checkOnly returns before any download or replace.
	checkOnly bool
}

// NewCmdUpdate returns the `update` command, which self-updates or checks the
// version.
func NewCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	opts := &updateOptions{
		IO:             f.IOStreams,
		Output:         f.Output,
		releases:       newGitHubReleases(),
		currentVersion: build.Version,
		executable:     os.Executable,
		replace:        replaceBinary,
		reexec:         reexecProcess,
	}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for and install CLI updates",
		Long: "Check for the latest hyperlift release on GitHub.\n\n" +
			"If that release is newer than the running binary, the command downloads\n" +
			"it, verifies its SHA-256 checksum, replaces the current executable in\n" +
			"place, then re-execs it.\n\n" +
			"Use --check to report whether an update is available. With --check, the\n" +
			"command installs nothing.\n\n" +
			"With --json or --quiet, the command only reports, like --check, and\n" +
			"never installs. --quiet prints the new version when one exists, and\n" +
			"nothing when you are current.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.checkOnly, "check", false, "Check for an update without installing it")

	return cmd
}

// bestEffortExe resolves the running executable through symlinks, tolerating
// every failure: cleanup and the managed-install check must never block a run.
func bestEffortExe(opts *updateOptions) (string, bool) {
	if opts.executable == nil {
		return "", false
	}

	exe, err := opts.executable()
	if err != nil {
		return "", false
	}

	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}

	return exe, true
}

// runUpdate is the core of the command, which tests can call directly.
func runUpdate(ctx context.Context, opts *updateOptions) error {
	ios := opts.IO
	cs := ios.ColorScheme()

	format := output.FormatTable
	if opts.Output != nil {
		format = opts.Output()
	}

	// Remove a stale .old file left by a previous Windows update. replaceBinary
	// creates it next to the symlink-resolved path, so resolve here too.
	if exe, ok := bestEffortExe(opts); ok {
		_ = os.Remove(exe + ".old")
	}

	ios.StartSpinner("Checking for updates")

	rel, err := opts.releases.Latest(ctx)

	ios.StopSpinner()

	if err != nil {
		if errors.Is(err, errNoReleases) {
			return reportNoUpdate(ios, format, opts.currentVersion, "", "There are no releases yet.")
		}

		return fmt.Errorf("check for updates: %w", err)
	}

	latest := rel.Version()
	if !validVersion(latest) {
		return fmt.Errorf("latest release has a malformed version tag %q", latest)
	}

	if !isNewer(opts.currentVersion, latest) {
		return reportNoUpdate(ios, format, opts.currentVersion, latest,
			fmt.Sprintf("You are already on the latest version (%s).", display(opts.currentVersion)))
	}

	// An update is available. --check only reports; so do the non-table
	// formats: a non-interactive surface reports, and the caller installs
	// from a TTY.
	if opts.checkOnly || format == output.FormatJSON || format == output.FormatQuiet {
		return reportUpdateAvailable(ios, format, opts.currentVersion, latest)
	}

	// Never overwrite an install a package manager owns: the manager would fight
	// or revert an in-place swap.
	if exe, ok := bestEffortExe(opts); ok {
		if hint, managed := managedInstallHint(exe); managed {
			return fmt.Errorf("this binary is managed by a package manager; run `%s` instead", hint)
		}
	}

	_, _ = fmt.Fprintf(ios.Out, "Updating %s -> %s\n", display(opts.currentVersion), cs.Cyan(latest))

	asset, ok := rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("no release asset for %s/%s in %s", runtime.GOOS, runtime.GOARCH, latest)
	}

	// The replace writes a temp file next to the binary, so a root-owned
	// install directory (the install script's default) fails. Check first.
	if exe, ok := bestEffortExe(opts); ok {
		if err := writableDir(filepath.Dir(exe)); err != nil {
			if runtime.GOOS == "windows" {
				return fmt.Errorf("updating needs write permission to %s; re-run `hyperlift update` from an administrator terminal", filepath.Dir(exe))
			}

			return fmt.Errorf("updating needs write permission to %s; re-run as: `sudo hyperlift update`", filepath.Dir(exe))
		}
	}

	ios.StartSpinner(fmt.Sprintf("Downloading %s", asset.Name))
	archiveData, err := opts.releases.Download(ctx, asset)

	ios.StopSpinner()

	if err != nil {
		return fmt.Errorf("download %s: %w", asset.Name, err)
	}

	// Read the checksum and compare it with the downloaded archive; SHA256SUMS
	// lists the archive names. The self-updater checks only the SHA-256 against
	// a SHA256SUMS read over TLS. install.sh, or a manual check, verifies the
	// cosign signature on SHA256SUMS. That keeps a sigstore verifier out of the
	// binary's dependency tree.
	ios.StartSpinner("Verifying checksum")

	sums, err := opts.releases.Checksums(ctx, rel)
	if err != nil {
		ios.StopSpinner()
		return fmt.Errorf("download checksums: %w", err)
	}

	want, ok := sums[asset.Name]
	if !ok {
		ios.StopSpinner()
		return fmt.Errorf("no checksum for %s in SHA256SUMS", asset.Name)
	}

	got := sha256Hex(archiveData)

	ios.StopSpinner()

	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset.Name, want, got)
	}

	_, _ = fmt.Fprintln(ios.Out, cs.Green("Checksum verified."))

	binData, err := extractBinary(archiveData, asset.Name)
	if err != nil {
		return fmt.Errorf("extract binary from %s: %w", asset.Name, err)
	}

	exe, err := opts.executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	if err := opts.replace(exe, binData); err != nil {
		return fmt.Errorf("replace %s: %w", exe, err)
	}

	_, _ = fmt.Fprintf(ios.Out, "%s Installed %s\n", cs.Green("Updated."), cs.Cyan(latest))

	// Drop the cached "update available" notice, so the CLI does not report the
	// version it just installed. This is best effort.
	_ = clearUpdateCache()

	// Re-exec, so the user runs the new binary at once.
	if opts.reexec != nil {
		args := []string{exe, "version"}
		if err := opts.reexec(exe, args); err != nil {
			// A failed re-exec is not fatal: the binary is already updated.
			_, _ = fmt.Fprintf(ios.ErrOut, "%s re-exec failed (%v); restart hyperlift to use the new version.\n",
				cs.Yellow("Warning:"), err)
		}
	}

	return nil
}

// reportNoUpdate prints the "already current" or "no releases" result.
func reportNoUpdate(ios *iostreams.IOStreams, format, current, latest, msg string) error {
	switch format {
	case output.FormatJSON:
		return output.Render(ios, checkResult{
			CurrentVersion:  display(current),
			LatestVersion:   latest,
			UpdateAvailable: false,
		}, format)
	case output.FormatQuiet:
		// Print nothing: there is no id and no action.
		return nil
	default:
		_, _ = fmt.Fprintln(ios.Out, msg)
		return nil
	}
}

// reportUpdateAvailable prints the "newer version available" result.
func reportUpdateAvailable(ios *iostreams.IOStreams, format, current, latest string) error {
	cs := ios.ColorScheme()

	switch format {
	case output.FormatJSON:
		return output.Render(ios, checkResult{
			CurrentVersion:  display(current),
			LatestVersion:   latest,
			UpdateAvailable: true,
		}, format)
	case output.FormatQuiet:
		_, _ = fmt.Fprintln(ios.Out, latest)
		return nil
	default:
		_, _ = fmt.Fprintf(ios.Out,
			"A new version of hyperlift is available: %s -> %s\n",
			display(current), cs.Cyan(latest))
		_, _ = fmt.Fprintf(ios.Out, "Run %s to install it.\n", cs.Bold("hyperlift update"))

		return nil
	}
}

// checkResult is the JSON shape of `update --json` and `update --check --json`.
type checkResult struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// display renders a version for a person. A local "dev" build gets a readable
// label.
func display(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}

	return v
}

// managedInstallHint reports whether the executable path, already resolved
// through symlinks, belongs to a package manager. It also returns that
// manager's update command.
func managedInstallHint(exePath string) (hint string, managed bool) {
	// Replace the separators here, not with filepath.ToSlash, so a Windows path
	// works on every host platform.
	slashed := strings.ReplaceAll(exePath, `\`, "/")
	if strings.HasPrefix(slashed, "/opt/homebrew/") {
		return "brew upgrade hyperlift", true
	}

	// A custom Scoop root can have no "scoop" path element.
	if root := os.Getenv("SCOOP"); root != "" {
		root = strings.TrimSuffix(strings.ReplaceAll(root, `\`, "/"), "/")
		if strings.HasPrefix(slashed, root+"/") {
			return "scoop update hyperlift", true
		}
	}

	for elem := range strings.SplitSeq(slashed, "/") {
		// Formulae land under <prefix>/Cellar, casks under <prefix>/Caskroom.
		// Intel macOS prefixes /usr/local, which the /opt/homebrew check misses.
		if strings.EqualFold(elem, "cellar") || strings.EqualFold(elem, "caskroom") {
			return "brew upgrade hyperlift", true
		}

		if strings.EqualFold(elem, "scoop") {
			return "scoop update hyperlift", true
		}
	}

	return "", false
}

// renameFile is os.Rename. A test replaces it to fail the move into place,
// which is the only way to reach the rollback below.
var renameFile = os.Rename

// writableDir reports whether the process can create a file in dir, by doing
// it. A probe beats os.Stat modes, which say nothing under Windows ACLs.
func writableDir(dir string) error {
	probe, err := os.CreateTemp(dir, ".hyperlift-preflight-*")
	if err != nil {
		return err
	}

	name := probe.Name()
	_ = probe.Close()

	return os.Remove(name)
}

// replaceBinary replaces the file at dst with newBin atomically. It writes a
// temp file in the same directory, so the rename stays on one filesystem, makes
// it executable, then renames it over dst. Windows cannot overwrite a running
// executable, so there the binary first moves aside to dst+".old", which Windows
// does allow. The next update run removes that stale .old file.
func replaceBinary(dst string, newBin []byte) error {
	dir := filepath.Dir(dst)

	tmp, err := os.CreateTemp(dir, ".hyperlift-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	tmpName := tmp.Name()

	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(newBin); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write new binary: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Keep the mode of the existing binary when possible. The default is 0755.
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(dst); err == nil {
		mode = fi.Mode().Perm() | 0o100
	}

	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	// Windows cannot overwrite a running executable, so move it aside first.
	movedAside := ""

	if runtime.GOOS == "windows" {
		old := dst + ".old"

		_ = os.Remove(old)
		if err := renameFile(dst, old); err != nil {
			return fmt.Errorf("move running executable aside: %w", err)
		}

		movedAside = old
	}

	if err := renameFile(tmpName, dst); err != nil {
		// Put the old binary back, or the user is left with no CLI at all.
		if movedAside != "" {
			_ = renameFile(movedAside, dst)
		}

		return fmt.Errorf("rename into place: %w", err)
	}

	return nil
}

// isBinaryEntry reports whether name is the hyperlift binary at the archive
// root, under either name the platforms use.
func isBinaryEntry(name string) bool {
	base := path.Base(filepath.ToSlash(name))
	return base == "hyperlift" || base == "hyperlift.exe"
}

// extractBinary returns the bytes of the hyperlift binary in a GoReleaser
// archive. It reads .tar.gz and .tgz on unix, and .zip on Windows. assetName
// selects the format.
func extractBinary(archive []byte, assetName string) ([]byte, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractFromZip(archive)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractFromTarGz(archive)
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", assetName)
	}
}

func extractFromTarGz(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}

	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg || !isBinaryEntry(hdr.Name) {
			continue
		}

		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read binary entry: %w", err)
		}

		return b, nil
	}

	return nil, errors.New("no hyperlift binary found in archive")
}

func extractFromZip(archive []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !isBinaryEntry(f.Name) {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open binary entry: %w", err)
		}

		b, err := io.ReadAll(rc)
		_ = rc.Close()

		if err != nil {
			return nil, fmt.Errorf("read binary entry: %w", err)
		}

		return b, nil
	}

	return nil, errors.New("no hyperlift binary found in archive")
}
