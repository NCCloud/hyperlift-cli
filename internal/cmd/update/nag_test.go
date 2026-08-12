package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/config"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

func ttyTrue() bool  { return true }
func ttyFalse() bool { return false }

func tableOutput() string { return output.FormatTable }

// neutralizeCI clears every environment variable that isCI() reads, so a test
// that runs on a real CI runner is not treated as CI.
func neutralizeCI(t *testing.T) {
	t.Helper()

	for _, k := range ciEnvVars {
		t.Setenv(k, "")
	}
}

// notifyCfg builds the NotifyConfig every nag test uses: table output on a TTY
// stderr, version 1.0.0, and dir as the cache location. It also returns the
// captured stderr buffer.
func notifyCfg(dir string, src releaseSource) (NotifyConfig, *bytes.Buffer) {
	io, _, _, errOut := iostreams.Test()

	return NotifyConfig{
		IO:             io,
		Output:         tableOutput,
		CurrentCommand: "list",
		releases:       src,
		now:            time.Now,
		cacheDir:       dir,
		currentVersion: "1.0.0",
		stderrIsTTY:    ttyTrue,
	}, errOut
}

func TestStartNotify_PrintsFromCacheOnNextRun(t *testing.T) {
	neutralizeCI(t)

	cfg, errOut := notifyCfg(t.TempDir(), &fakeReleases{latest: release{TagName: "v9.9.9"}})

	// First run: the cache is empty, so the refresh runs in the background and
	// this run prints nothing.
	flush := StartNotify(context.Background(), cfg)
	flush()

	if errOut.String() != "" {
		t.Fatalf("first run must print nothing, got %q", errOut.String())
	}

	// Wait for the background refresh to write the cache. This also keeps the
	// write from racing the TempDir cleanup.
	cache := filepath.Join(cfg.cacheDir, cacheFilename)
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(cache); err == nil {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	if _, err := os.Stat(cache); err != nil {
		t.Fatal("background refresh never wrote its cache")
	}

	// Second run: the fresh cache resolves synchronously and flush prints.
	flush = StartNotify(context.Background(), cfg)
	flush()

	got := errOut.String()
	if !strings.Contains(got, "9.9.9") || !strings.Contains(got, "hyperlift update") {
		t.Fatalf("want nag mentioning 9.9.9 and 'hyperlift update', got %q", got)
	}
}

// TestShouldNag_SuppressionRules covers every rule that must silence the notice.
func TestShouldNag_SuppressionRules(t *testing.T) {
	io, _, _, _ := iostreams.Test()
	base := func() NotifyConfig {
		return NotifyConfig{
			IO:             io,
			Output:         tableOutput,
			CurrentCommand: "list",
			currentVersion: "1.0.0",
			stderrIsTTY:    ttyTrue,
		}
	}

	cases := []struct {
		name    string
		env     map[string]string
		mutate  func(*NotifyConfig)
		wantNag bool
	}{
		{name: "baseline nags", wantNag: true},
		{name: "non-TTY stderr", mutate: func(c *NotifyConfig) { c.stderrIsTTY = ttyFalse }},
		{name: "json output", mutate: func(c *NotifyConfig) { c.Output = func() string { return output.FormatJSON } }},
		{name: "quiet output", mutate: func(c *NotifyConfig) { c.Output = func() string { return output.FormatQuiet } }},
		{name: "update command itself", mutate: func(c *NotifyConfig) { c.CurrentCommand = "update" }},
		{name: "CI", env: map[string]string{"CI": "true"}},
		{name: "HYPERLIFT_NO_UPDATE_CHECK", env: map[string]string{config.EnvNoUpdateCheck: "1"}},
		{name: "dev build", mutate: func(c *NotifyConfig) { c.currentVersion = "dev" }},
		{name: "source build with git stamp", mutate: func(c *NotifyConfig) { c.currentVersion = "d7d15ec-dirty" }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			neutralizeCI(t)

			for k, v := range c.env {
				t.Setenv(k, v)
			}

			cfg := base()
			if c.mutate != nil {
				c.mutate(&cfg)
			}

			if got := shouldNag(cfg); got != c.wantNag {
				t.Fatalf("shouldNag = %v, want %v", got, c.wantNag)
			}
		})
	}
}

func TestCheckForUpdate_UsesFreshCache_NoNetwork(t *testing.T) {
	dir := t.TempDir()
	// Seed a fresh cache that reports an old version as the latest.
	p, _ := cachePath(dir)
	if err := writeCache(p, updateCache{CheckedAt: time.Now(), LatestVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	called := false
	cfg, _ := notifyCfg(dir, &recordingReleases{
		inner: &fakeReleases{latestErr: errors.New("network must not be called")},
		hit:   &called,
	})

	checkForUpdate(context.Background(), cfg)

	if called {
		t.Fatal("fresh cache must avoid network")
	}
}

func TestCheckForUpdate_RefreshesStaleCache(t *testing.T) {
	dir := t.TempDir()
	p, _ := cachePath(dir)
	// A stale cache, older than nagInterval.
	if err := writeCache(p, updateCache{
		CheckedAt:     time.Now().Add(-48 * time.Hour),
		LatestVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	cfg, _ := notifyCfg(dir, &fakeReleases{latest: release{TagName: "v2.0.0"}})

	checkForUpdate(context.Background(), cfg)

	// The cache must now hold the refreshed latest version.
	latest, fresh := readCache(p, time.Now())
	if !fresh || latest != "2.0.0" {
		t.Fatalf("cache not refreshed: latest=%q fresh=%v", latest, fresh)
	}
}

func TestCheckForUpdate_RecordsAttempt_OnNetworkError(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := notifyCfg(dir, &fakeReleases{latestErr: errors.New("offline")})

	checkForUpdate(context.Background(), cfg)

	// The attempt must be recorded, so the CLI does not retry on every run.
	p, _ := cachePath(dir)

	_, fresh := readCache(p, time.Now())
	if !fresh {
		t.Fatal("expected a recorded (fresh) attempt after network error")
	}
}

// recordingReleases records whether a caller used the inner source.
type recordingReleases struct {
	inner releaseSource
	hit   *bool
}

func (r *recordingReleases) Latest(ctx context.Context) (release, error) {
	*r.hit = true
	return r.inner.Latest(ctx)
}

func (r *recordingReleases) Download(ctx context.Context, a releaseAsset) ([]byte, error) {
	*r.hit = true
	return r.inner.Download(ctx, a)
}

func (r *recordingReleases) Checksums(ctx context.Context, rel release) (map[string]string, error) {
	*r.hit = true
	return r.inner.Checksums(ctx, rel)
}

// TestPrintNag_MajorGapWarns pins the two notice shapes: a minor gap stays the
// friendly one-liner, a major gap adds the red compatibility warning.
func TestPrintNag_MajorGapWarns(t *testing.T) {
	cases := []struct {
		name, current, latest string
		wantWarning           bool
	}{
		{"minor gap", "1.0.0", "v1.2.0", false},
		{"major gap", "1.2.3", "v2.0.0", true},
		{"two majors", "0.9.0", "v2.0.0", true},
		{"dev build never warns", "dev", "v9.0.0", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			io, _, _, errOut := iostreams.Test()
			printNag(NotifyConfig{IO: io, currentVersion: c.current}, c.latest)

			got := errOut.String()
			if !strings.Contains(got, "hyperlift update") {
				t.Fatalf("notice lost the update hint: %q", got)
			}

			warned := strings.Contains(got, "highly recommended")
			if warned != c.wantWarning {
				t.Fatalf("warning shown = %v, want %v; output %q", warned, c.wantWarning, got)
			}
		})
	}
}
