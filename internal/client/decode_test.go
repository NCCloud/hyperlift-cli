package client_test

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// The testdata fixtures are wire JSON, written by hand from the examples in the
// published OpenAPI document. They are never marshalled from the Go types, so
// they can disagree with them and fail these tests.
//
//go:embed testdata/*.json
var fixtures embed.FS

// serving returns a real client. A server answers every request with the given
// fixture, so decoding runs through the real HTTP path.
func serving(t *testing.T, status int, fixture string, header map[string]string) client.Client {
	t.Helper()

	body, err := fixtures.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		for k, v := range header {
			w.Header().Set(k, v)
		}

		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return client.New(client.Options{BaseURL: srv.URL, APIKey: "k", APISecret: "s"})
}

func TestDecode_Application(t *testing.T) {
	c := serving(t, http.StatusOK, "application.json", nil)

	app, err := c.AppsGet(context.Background(), "3f8b9a2e-5c41-4d6a-9f0e-7b2c8d1a4e5f")
	if err != nil {
		t.Fatalf("AppsGet: %v", err)
	}

	if app.ID != "3f8b9a2e-5c41-4d6a-9f0e-7b2c8d1a4e5f" {
		t.Errorf("ID = %q", app.ID)
	}

	if app.Status != client.StatusRunning || app.BuildStatus != client.BuildStatusBuilt {
		t.Errorf("status/build = %s/%s, want running/built", app.Status, app.BuildStatus)
	}

	if app.Domain == nil || *app.Domain != "app.example.com" {
		t.Errorf("Domain = %v, want app.example.com", app.Domain)
	}

	if app.Scale == nil || *app.Scale != 1 {
		t.Errorf("Scale = %v, want 1", app.Scale)
	}

	// The wire value is the contract's int32 maximum. It must survive intact.
	if app.GithubInstallationID == nil || *app.GithubInstallationID != 2147483647 {
		t.Errorf("GithubInstallationID = %v, want 2147483647", app.GithubInstallationID)
	}

	if app.Branch == nil || *app.Branch != "main" {
		t.Errorf("Branch = %v, want main", app.Branch)
	}

	if app.GithubRepositoryFullName == nil || *app.GithubRepositoryFullName != "acme/web-app" {
		t.Errorf("GithubRepositoryFullName = %v, want acme/web-app", app.GithubRepositoryFullName)
	}

	if app.DockerfilePath == nil || *app.DockerfilePath != "docker/Dockerfile" {
		t.Errorf("DockerfilePath = %v, want docker/Dockerfile", app.DockerfilePath)
	}

	if app.AutomaticBuildEnabled == nil || !*app.AutomaticBuildEnabled {
		t.Errorf("AutomaticBuildEnabled = %v, want true", app.AutomaticBuildEnabled)
	}

	if want := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC); !app.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", app.CreatedAt, want)
	}

	if want := time.Date(2026, 1, 16, 8, 30, 0, 0, time.UTC); app.UpdatedAt == nil || !app.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", app.UpdatedAt, want)
	}
}

// TestDecode_ApplicationNullable pins that every nullable contract field stays
// nil and does not collapse to a zero value. --json re-marshals this struct, so
// a zero value there would report something false.
func TestDecode_ApplicationNullable(t *testing.T) {
	c := serving(t, http.StatusOK, "application_nullable.json", nil)

	app, err := c.AppsGet(context.Background(), "app_a1b2c3")
	if err != nil {
		t.Fatalf("AppsGet: %v", err)
	}

	if app.Domain != nil {
		t.Errorf("Domain = %q, want nil", *app.Domain)
	}

	if app.Scale != nil {
		t.Errorf("Scale = %d, want nil (desired scale not yet known)", *app.Scale)
	}

	if app.Branch != nil {
		t.Errorf("Branch = %q, want nil", *app.Branch)
	}

	if app.GithubRepositoryFullName != nil {
		t.Errorf("GithubRepositoryFullName = %q, want nil", *app.GithubRepositoryFullName)
	}

	if app.DockerfilePath != nil {
		t.Errorf("DockerfilePath = %q, want nil", *app.DockerfilePath)
	}

	if app.GithubInstallationID != nil {
		t.Errorf("GithubInstallationID = %d, want nil", *app.GithubInstallationID)
	}

	if app.AutomaticBuildEnabled != nil {
		t.Errorf("AutomaticBuildEnabled = %t, want nil", *app.AutomaticBuildEnabled)
	}

	if app.UpdatedAt != nil {
		t.Errorf("UpdatedAt = %v, want nil", *app.UpdatedAt)
	}

	// Re-marshalling, which is what --json does, must put the nulls back.
	out, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for _, want := range []string{
		`"domain":null`, `"scale":null`, `"branch":null`,
		`"githubInstallationId":null`, `"githubRepositoryFullName":null`,
		`"dockerfilePath":null`, `"automaticBuildEnabled":null`, `"updatedAt":null`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("--json output %s is missing %s", out, want)
		}
	}
}

func TestDecode_Metrics(t *testing.T) {
	c := serving(t, http.StatusOK, "metrics.json", nil)

	m, err := c.Metrics(context.Background(), "app_a1b2c3", client.MetricsQuery{
		Start:    time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC),
		Interval: "5m",
		Metrics:  []string{"memoryUsageBytes", "cpuUsagePercentage", "persistentStorageUsedMebibytes"},
	})
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}

	if len(m.Series) != 3 {
		t.Fatalf("series = %d, want 3", len(m.Series))
	}

	for i, want := range []struct {
		name string
		unit string
	}{
		{"memoryUsageBytes", "bytes"},
		{"cpuUsagePercentage", "percent"},
		{"persistentStorageUsedMebibytes", "mebibytes"},
	} {
		if m.Series[i].Name != want.name || m.Series[i].Unit != want.unit {
			t.Errorf("series[%d] = %s/%s, want %s/%s", i, m.Series[i].Name, m.Series[i].Unit, want.name, want.unit)
		}

		if len(m.Series[i].Samples) != 2 {
			t.Errorf("series[%d] samples = %d, want 2", i, len(m.Series[i].Samples))
		}
	}

	// quota appears only where the plan defines one.
	if m.Series[0].Quota == nil || *m.Series[0].Quota != 1073741824 {
		t.Errorf("memoryUsageBytes quota = %v, want 1073741824", m.Series[0].Quota)
	}

	if m.Series[1].Quota != nil {
		t.Errorf("cpuUsagePercentage quota = %v, want none", *m.Series[1].Quota)
	}

	if got := m.Series[0].Samples[0]; got.Value != 536870912 || got.Timestamp != "2026-01-15T00:00:00.000Z" {
		t.Errorf("first sample = %+v", got)
	}

	// A zero-valued bucket is a real sample, not a gap.
	if got := m.Series[1].Samples[1]; got.Value != 0 || got.Timestamp == "" {
		t.Errorf("zero-filled bucket = %+v, want a timestamped 0", got)
	}
}

func TestDecode_LogsPage(t *testing.T) {
	c := serving(t, http.StatusOK, "logs_page.json", nil)

	page, err := c.Logs(context.Background(), client.LogsRequest{ID: "app_a1b2c3", Take: 2})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}

	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(page.Items))
	}

	if page.Items[0].Message != "Server listening on port 3000" || page.Items[0].Timestamp != "2026-01-15T12:00:00Z" {
		t.Errorf("first line = %+v", page.Items[0])
	}

	// The timestamp is absent when the source line has none.
	if page.Items[1].Timestamp != "" {
		t.Errorf("second line timestamp = %q, want empty", page.Items[1].Timestamp)
	}

	if page.Cursor != "665f20b3a1b2c3d4e5f60718" {
		t.Errorf("Cursor = %q, want the resume cursor", page.Cursor)
	}

	if page.Finished {
		t.Error("Finished = true, want false (more lines may follow)")
	}
}

func TestDecode_ErrorEnvelope(t *testing.T) {
	c := serving(t, http.StatusNotFound, "error.json", map[string]string{
		"Content-Type":           "application/problem+json",
		client.HeaderErrorCode:   "business.notFound",
		client.HeaderOperationID: "op7f3a9c1b2d4e5f",
	})
	_, err := c.AppsGet(context.Background(), "3f8b9a2e-5c41-4d6a-9f0e-7b2c8d1a4e5f")

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *client.APIError", err, err)
	}

	if apiErr.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", apiErr.Status)
	}

	if apiErr.Code != "business.notFound" || apiErr.OperationID != "op7f3a9c1b2d4e5f" {
		t.Errorf("code/operation = %q/%q", apiErr.Code, apiErr.OperationID)
	}

	if apiErr.Detail != "Application 3f8b9a2e-5c41-4d6a-9f0e-7b2c8d1a4e5f was not found." {
		t.Errorf("Detail = %q, want the problem detail", apiErr.Detail)
	}
}

func TestDecode_Unexpected202CarriesHeaders(t *testing.T) {
	c := serving(t, http.StatusAccepted, "error.json", map[string]string{
		client.HeaderErrorCode:   "application.async",
		client.HeaderOperationID: "op7f3a9c1b2d4e5f",
	})
	_, err := c.AppsBuild(context.Background(), "app_1")

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *client.APIError", err, err)
	}

	if apiErr.Status != http.StatusAccepted {
		t.Errorf("Status = %d, want 202", apiErr.Status)
	}

	// The synthetic error goes through parseError, so the headers populate.
	if apiErr.Code != "application.async" || apiErr.OperationID != "op7f3a9c1b2d4e5f" {
		t.Errorf("code/operation = %q/%q, want the response headers", apiErr.Code, apiErr.OperationID)
	}

	if apiErr.Detail != "unexpected 202 Accepted from a synchronous endpoint" {
		t.Errorf("Detail = %q, want the fixed 202 detail", apiErr.Detail)
	}
}

// TestConnectionErrorShowsURLOnce pins that a dial failure names the URL once:
// the *url.Error text, which repeats it, is unwrapped before the wrap.
func TestConnectionErrorShowsURLOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close() // the port now refuses connections

	c := client.New(client.Options{BaseURL: base, APIKey: "k", APISecret: "s"})

	_, err := c.AppsGet(context.Background(), "app_x")
	if err == nil {
		t.Fatal("want a connection error")
	}

	msg := err.Error()
	if got := strings.Count(msg, base); got != 1 {
		t.Errorf("URL appears %d times in %q, want exactly once", got, msg)
	}
}

// TestProbe_RejectsForeign404 pins the login guard: only the gateway's own
// not-found code proves the credentials authenticated. Any other host that
// answers 404, such as a typo'd base URL, must fail.
func TestProbe_RejectsForeign404(t *testing.T) {
	t.Run("bare 404 is not a valid login", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", APISecret: "s"})
		if err := c.Probe(context.Background()); err == nil {
			t.Error("a 404 without the gateway's error code must not pass")
		}
	})

	t.Run("the gateway's 404 is a valid login", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(client.HeaderErrorCode, "business.notFound")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"resource not found"}`))
		}))
		defer srv.Close()

		c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", APISecret: "s"})
		if err := c.Probe(context.Background()); err != nil {
			t.Errorf("the gateway's own 404 must pass: %v", err)
		}
	})
}
