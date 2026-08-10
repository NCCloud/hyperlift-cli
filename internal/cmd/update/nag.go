package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/build"
	"github.com/nccloud/hyperlift-cli/internal/config"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// nagInterval is how often the background update check calls GitHub.
const nagInterval = 24 * time.Hour

// cacheFilename holds the daily-check state. It sits next to the config file.
const cacheFilename = "update-check.json"

// updateCache is the stored state of the last background update check.
type updateCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// NotifyConfig controls the background update check and its notice.
type NotifyConfig struct {
	IO     *iostreams.IOStreams
	Output func() string // resolved output format: table, json or quiet

	// CurrentCommand is the subcommand that runs, e.g. "apps list". The update
	// command itself prints no notice, to avoid a double message.
	CurrentCommand string

	// The fields below are test seams. An empty one falls back to its
	// production value: live GitHub, time.Now, the config dir, build.Version
	// and IO.IsStderrTTY.
	releases       releaseSource
	now            func() time.Time
	cacheDir       string
	currentVersion string
	stderrIsTTY    func() bool
}

// StartNotify starts the daily-cached "newer version available" check in the
// background. It returns a flush function that the root command calls afterwards
// to print the one-line notice to stderr. It tests the silencing rules in
// shouldNag before it starts any work. Every error is dropped on purpose: a
// failed update check must never break a real command.
func StartNotify(ctx context.Context, cfg NotifyConfig) (flush func()) {
	if cfg.now == nil {
		cfg.now = time.Now
	}

	if cfg.currentVersion == "" {
		cfg.currentVersion = build.Version
	}

	if !shouldNag(cfg) {
		return func() {}
	}

	// The notice prints only from a fresh cache, which is a local read, so the
	// command is never delayed. A stale cache starts a fire-and-forget refresh
	// whose result the NEXT run prints.
	if path, err := cachePath(cfg.cacheDir); err == nil {
		if latest, fresh := readCache(path, cfg.now()); fresh {
			return func() {
				if latest != "" && isNewer(cfg.currentVersion, latest) {
					printNag(cfg, latest)
				}
			}
		}
	}

	go checkForUpdate(ctx, cfg)

	return func() {}
}

// checkForUpdate refreshes the daily cache from GitHub when it is stale. It
// never reports a result: the NEXT run reads the cache and prints the notice.
func checkForUpdate(ctx context.Context, cfg NotifyConfig) {
	cachePath, err := cachePath(cfg.cacheDir)
	if err != nil {
		return
	}

	if _, fresh := readCache(cachePath, cfg.now()); fresh {
		return
	}

	// The cache is stale or missing, so refresh it from GitHub. This is best
	// effort.
	src := cfg.releases
	if src == nil {
		src = newGitHubReleases()
	}

	// Bound the check, so its goroutine cannot outlive the command by much.
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rel, rerr := src.Latest(cctx)
	if rerr != nil {
		// Record the attempt, so the CLI does not retry on every run when
		// it is offline or there are no releases.
		_ = writeCache(cachePath, updateCache{CheckedAt: cfg.now(), LatestVersion: ""})
		return
	}

	_ = writeCache(cachePath, updateCache{CheckedAt: cfg.now(), LatestVersion: rel.Version()})
}

// printNag writes the one-line "new release available" notice to stderr.
func printNag(cfg NotifyConfig, latest string) {
	cs := cfg.IO.ColorScheme()
	_, _ = fmt.Fprintf(cfg.IO.ErrOut,
		"\n%s A new release of hyperlift is available: %s -> %s\n",
		cs.Yellow("!"), display(cfg.currentVersion), cs.Cyan(latest))
	_, _ = fmt.Fprintf(cfg.IO.ErrOut, "  Run %s to update.\n\n", cs.Bold("hyperlift update"))
}

// shouldNag decides whether to print the notice. stderr must be a TTY, the
// output must be the human table format, the command must not be `update`, no CI
// marker may be set, and the binary must be a released build.
func shouldNag(cfg NotifyConfig) bool {
	if cfg.IO == nil {
		return false
	}
	// Any non-empty value of the env var turns the check off.
	if os.Getenv(config.EnvNoUpdateCheck) != "" {
		return false
	}

	isTTY := cfg.stderrIsTTY
	if isTTY == nil {
		isTTY = cfg.IO.IsStderrTTY
	}

	if !isTTY() {
		return false
	}

	if cfg.CurrentCommand == "update" {
		return false
	}

	if cfg.Output != nil {
		switch cfg.Output() {
		case output.FormatJSON, output.FormatQuiet:
			return false
		}
	}

	if isCI() {
		return false
	}

	// Never notify a local build. Source builds carry "dev" or a non-semver
	// stamp like "d7d15ec-dirty", and no released version is meaningfully newer
	// for a developer.
	if cfg.currentVersion == "" || cfg.currentVersion == "dev" || !validVersion(cfg.currentVersion) {
		return false
	}

	return true
}

// ciEnvVars are the environment markers that mean "this is a CI run".
var ciEnvVars = []string{"CI", "CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "GITHUB_ACTIONS"}

// isCI reports whether a common CI environment marker is set.
func isCI() bool {
	for _, k := range ciEnvVars {
		if v := os.Getenv(k); v != "" && v != "0" && v != "false" {
			return true
		}
	}

	return false
}

// cachePath resolves the update-check cache file. An empty dir means the config
// directory.
func cachePath(dir string) (string, error) {
	if dir == "" {
		d, err := config.Dir()
		if err != nil {
			return "", err
		}

		dir = d
	}

	return filepath.Join(dir, cacheFilename), nil
}

// readCache loads the cache. It reports it fresh when the last check happened
// inside nagInterval of now.
func readCache(path string, now time.Time) (latest string, fresh bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path is our own cache file under the config dir
	if err != nil {
		return "", false
	}

	var c updateCache
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}

	if now.Sub(c.CheckedAt) > nagInterval {
		return c.LatestVersion, false
	}

	return c.LatestVersion, true
}

// writeCache stores the cache atomically, with 0600 permissions.
func writeCache(path string, c updateCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(c)
	if err != nil {
		return err
	}

	// CreateTemp gives each writer its own 0600 temp file, so two concurrent
	// checks cannot interleave on one temp path.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

// clearUpdateCache removes the cached check state, so a new check runs after an
// explicit update. It is best effort.
func clearUpdateCache() error {
	path, err := cachePath("")
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}
