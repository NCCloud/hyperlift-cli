package client_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/testapi"
)

// recordedReq holds the wire details of one request the client sent.
type recordedReq struct {
	method string
	path   string
	query  url.Values
	body   []byte
}

// recorder wraps a handler. It records the method, path, query and body of each
// request before it delegates, so a test can assert the exact wire contract.
type recorder struct {
	h    http.Handler
	mu   sync.Mutex
	reqs []recordedReq
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(body))

	r.mu.Lock()
	r.reqs = append(r.reqs, recordedReq{
		method: req.Method,
		path:   req.URL.Path,
		query:  req.URL.Query(),
		body:   body,
	})
	r.mu.Unlock()

	r.h.ServeHTTP(w, req)
}

func (r *recorder) last() recordedReq {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.reqs) == 0 {
		return recordedReq{}
	}

	return r.reqs[len(r.reqs)-1]
}

// newMock starts the contract mock behind a recorder. It returns a real client
// that points at the mock, plus the recorder.
func newMock(t *testing.T) (client.Client, *recorder) {
	t.Helper()

	rec := &recorder{h: testapi.Server()}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", APISecret: "s"})

	return c, rec
}

func TestContract_AppsList(t *testing.T) {
	c, rec := newMock(t)

	apps, total, err := c.AppsList(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("AppsList: %v", err)
	}

	got := rec.last()
	if got.method != http.MethodGet || got.path != "/hyperlift/applications" {
		t.Fatalf("wire = %s %s, want GET /hyperlift/applications", got.method, got.path)
	}

	if got.query.Get("take") != "1" || got.query.Get("skip") != "1" {
		t.Errorf("query = %v, want take=1 skip=1", got.query)
	}

	// An external GET must have an empty body: the gateway rejects a body.
	if len(got.body) != 0 {
		t.Errorf("GET body = %q, want empty", got.body)
	}

	// The {items,total} envelope holds one of the two seeded apps, but total
	// counts both.
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}

	if len(apps) != 1 || apps[0].ID != "app_d4e5f6" {
		t.Fatalf("apps = %+v, want the second seeded app only", apps)
	}
}

func TestContract_AppsList_TakeBoundsRejected(t *testing.T) {
	c, _ := newMock(t)

	_, _, err := c.AppsList(context.Background(), 101, 0)

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("err = %v, want 422 APIError", err)
	}

	if apiErr.Code != "business.validationFailed" {
		t.Errorf("code = %q, want business.validationFailed", apiErr.Code)
	}
}

// TestContract_AppsGet proves decode fidelity: the mock serializes a seed
// application to the wire, and the client must decode it back to the exact
// same struct, nullable pointers included.
func TestContract_AppsGet(t *testing.T) {
	c, rec := newMock(t)

	app, err := c.AppsGet(context.Background(), "app_a1b2c3")
	if err != nil {
		t.Fatalf("AppsGet: %v", err)
	}

	if got := rec.last(); got.method != http.MethodGet || got.path != "/hyperlift/applications/app_a1b2c3" {
		t.Fatalf("wire = %s %s, want GET /hyperlift/applications/app_a1b2c3", got.method, got.path)
	}

	assertAppEqualsSeed(t, app, testapi.SeedApps()["app_a1b2c3"])
}

// TestContract_AppsGet_NullableDomain covers the null-carrying seed: a wire
// "domain": null must decode to a nil pointer, not a zero value.
func TestContract_AppsGet_NullableDomain(t *testing.T) {
	c, _ := newMock(t)

	app, err := c.AppsGet(context.Background(), "app_d4e5f6")
	if err != nil {
		t.Fatalf("AppsGet: %v", err)
	}

	assertAppEqualsSeed(t, app, testapi.SeedApps()["app_d4e5f6"])
}

// assertAppEqualsSeed compares a decoded application with its seed field by
// field. Timestamps compare with time.Equal, which JSON round-trips preserve.
func assertAppEqualsSeed(t *testing.T, got, want *client.Application) {
	t.Helper()

	if got.ID != want.ID || got.Status != want.Status || got.BuildStatus != want.BuildStatus || got.Plan != want.Plan {
		t.Errorf("core fields = %s/%s/%s/%s, want %s/%s/%s/%s",
			got.ID, got.Status, got.BuildStatus, got.Plan, want.ID, want.Status, want.BuildStatus, want.Plan)
	}

	assertPtrEqual(t, "domain", got.Domain, want.Domain)
	assertPtrEqual(t, "scale", got.Scale, want.Scale)
	assertPtrEqual(t, "branch", got.Branch, want.Branch)
	assertPtrEqual(t, "githubInstallationId", got.GithubInstallationID, want.GithubInstallationID)
	assertPtrEqual(t, "githubRepositoryFullName", got.GithubRepositoryFullName, want.GithubRepositoryFullName)
	assertPtrEqual(t, "dockerfilePath", got.DockerfilePath, want.DockerfilePath)
	assertPtrEqual(t, "automaticBuildEnabled", got.AutomaticBuildEnabled, want.AutomaticBuildEnabled)

	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("createdAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}

	switch {
	case (got.UpdatedAt == nil) != (want.UpdatedAt == nil):
		t.Errorf("updatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	case got.UpdatedAt != nil && !got.UpdatedAt.Equal(*want.UpdatedAt):
		t.Errorf("updatedAt = %v, want %v", *got.UpdatedAt, *want.UpdatedAt)
	}
}

// assertPtrEqual compares two nullable fields: both nil, or equal values.
func assertPtrEqual[T comparable](t *testing.T, name string, got, want *T) {
	t.Helper()

	switch {
	case (got == nil) != (want == nil):
		t.Errorf("%s = %v, want %v", name, got, want)
	case got != nil && *got != *want:
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

func TestContract_AppsGet_NotFound(t *testing.T) {
	c, _ := newMock(t)

	_, err := c.AppsGet(context.Background(), "does_not_exist")

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want 404 APIError", err)
	}

	if apiErr.Code != "business.notFound" {
		t.Errorf("code = %q, want business.notFound", apiErr.Code)
	}
}

func TestContract_AppsBuild(t *testing.T) {
	c, rec := newMock(t)

	ref, err := c.AppsBuild(context.Background(), "app_a1b2c3")
	if err != nil {
		t.Fatalf("AppsBuild: %v", err)
	}

	if got := rec.last(); got.method != http.MethodPost || got.path != "/hyperlift/applications/app_a1b2c3/build" {
		t.Fatalf("wire = %s %s, want POST /hyperlift/applications/app_a1b2c3/build", got.method, got.path)
	}

	if ref.ID != "app_a1b2c3" {
		t.Errorf("ref.ID = %q", ref.ID)
	}
}

func TestContract_AppsRestart(t *testing.T) {
	c, rec := newMock(t)

	if _, err := c.AppsRestart(context.Background(), "app_a1b2c3"); err != nil {
		t.Fatalf("AppsRestart: %v", err)
	}

	if got := rec.last(); got.method != http.MethodPost || got.path != "/hyperlift/applications/app_a1b2c3/restart" {
		t.Fatalf("wire = %s %s, want POST /hyperlift/applications/app_a1b2c3/restart", got.method, got.path)
	}
}

func TestContract_AppsStartStop_UseScale(t *testing.T) {
	c, rec := newMock(t)

	if _, err := c.AppsStart(context.Background(), "app_d4e5f6"); err != nil {
		t.Fatalf("AppsStart: %v", err)
	}

	got := rec.last()
	if got.method != http.MethodPut || got.path != "/hyperlift/applications/app_d4e5f6/scale" {
		t.Fatalf("start wire = %s %s, want PUT /hyperlift/applications/app_d4e5f6/scale", got.method, got.path)
	}

	if string(bytes.TrimSpace(got.body)) != `{"scale":1}` {
		t.Errorf("start body = %s, want {\"scale\":1}", got.body)
	}

	if _, err := c.AppsStop(context.Background(), "app_a1b2c3"); err != nil {
		t.Fatalf("AppsStop: %v", err)
	}

	got = rec.last()
	if got.method != http.MethodPut || got.path != "/hyperlift/applications/app_a1b2c3/scale" {
		t.Fatalf("stop wire = %s %s, want PUT /hyperlift/applications/app_a1b2c3/scale", got.method, got.path)
	}

	if string(bytes.TrimSpace(got.body)) != `{"scale":0}` {
		t.Errorf("stop body = %s, want {\"scale\":0}", got.body)
	}
}

func TestContract_EnvUpdate_InvalidNameRejected(t *testing.T) {
	c, _ := newMock(t)

	err := c.EnvUpdate(context.Background(), "app_a1b2c3", map[string]string{"db.user": "x"})

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("err = %v, want 422 APIError", err)
	}

	if apiErr.Code != "business.validationFailed" {
		t.Errorf("code = %q, want business.validationFailed", apiErr.Code)
	}
}

func TestContract_EnvGetAndUpdate(t *testing.T) {
	c, rec := newMock(t)

	got, err := c.EnvGet(context.Background(), "app_a1b2c3")
	if err != nil {
		t.Fatalf("EnvGet: %v", err)
	}

	if r := rec.last(); r.method != http.MethodGet || r.path != "/hyperlift/applications/app_a1b2c3/environment" {
		t.Fatalf("wire = %s %s, want GET /hyperlift/applications/app_a1b2c3/environment", r.method, r.path)
	}

	if want := testapi.SeedEnv()["app_a1b2c3"]; !maps.Equal(got, want) {
		t.Errorf("env = %v, want the seed %v", got, want)
	}

	next := map[string]string{"NODE_ENV": "staging", "NEW": "1"}
	if err := c.EnvUpdate(context.Background(), "app_a1b2c3", next); err != nil {
		t.Fatalf("EnvUpdate: %v", err)
	}

	put := rec.last()
	if put.method != http.MethodPut || put.path != "/hyperlift/applications/app_a1b2c3/environment" {
		t.Fatalf("wire = %s %s, want PUT /hyperlift/applications/app_a1b2c3/environment", put.method, put.path)
	}

	if !bytes.Contains(put.body, []byte(`"items"`)) {
		t.Errorf("PUT body missing items envelope: %s", put.body)
	}

	// The next read shows the full replacement.
	after, err := c.EnvGet(context.Background(), "app_a1b2c3")
	if err != nil {
		t.Fatalf("EnvGet after update: %v", err)
	}

	if after["NODE_ENV"] != "staging" || after["NEW"] != "1" {
		t.Errorf("env after update = %v", after)
	}

	if _, ok := after["PORT"]; ok {
		t.Errorf("PORT should have been replaced away, got %v", after)
	}
}

func TestContract_Metrics(t *testing.T) {
	c, rec := newMock(t)
	q := client.MetricsQuery{
		Start:    time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
		Interval: "5m",
		Metrics:  []string{"cpuUsagePercentage", "memoryUsageBytes", "networkReceiveRateBytes"},
	}

	m, err := c.Metrics(context.Background(), "app_a1b2c3", q)
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}

	got := rec.last()
	if got.method != http.MethodGet || got.path != "/hyperlift/applications/app_a1b2c3/metrics" {
		t.Fatalf("wire = %s %s, want GET /hyperlift/applications/app_a1b2c3/metrics", got.method, got.path)
	}

	if got.query.Get("startDate") != "2026-06-01T12:00:00.000Z" {
		t.Errorf("startDate = %q", got.query.Get("startDate"))
	}

	if got.query.Get("interval") != "5m" {
		t.Errorf("interval = %q", got.query.Get("interval"))
	}

	// The client sends the metrics comma-joined, as camelCase contract tokens.
	if got.query.Get("metrics") != "cpuUsagePercentage,memoryUsageBytes,networkReceiveRateBytes" {
		t.Errorf("metrics = %q", got.query.Get("metrics"))
	}

	// One series per requested metric, in request order.
	if len(m.Series) != 3 {
		t.Fatalf("series = %d, want 3: %+v", len(m.Series), m.Series)
	}

	for i, want := range []string{"cpuUsagePercentage", "memoryUsageBytes", "networkReceiveRateBytes"} {
		if m.Series[i].Name != want {
			t.Errorf("series[%d].Name = %q, want %q", i, m.Series[i].Name, want)
		}
	}

	if s := m.Series[0].Samples; len(s) == 0 || s[0].Value != 12.5 {
		t.Errorf("cpuUsagePercentage samples = %+v, want first value 12.5", s)
	}

	// The grid is dense: an unobserved metric still gets one sample per bucket,
	// with the value 0.
	rx := m.Series[2].Samples
	if len(rx) != len(m.Series[0].Samples) {
		t.Errorf("networkReceiveRateBytes samples = %d, want %d (shared grid)", len(rx), len(m.Series[0].Samples))
	}

	if len(rx) == 0 || rx[0].Value != 0 || rx[0].Timestamp != m.Series[0].Samples[0].Timestamp {
		t.Errorf("networkReceiveRateBytes[0] = %+v, want zero-filled on the shared grid", rx)
	}

	// Only a quota-bearing metric with a defined limit carries a quota.
	if m.Series[1].Quota == nil || *m.Series[1].Quota != 1073741824 {
		t.Errorf("memoryUsageBytes quota = %v, want 1073741824", m.Series[1].Quota)
	}

	if m.Series[0].Quota != nil {
		t.Errorf("cpuUsagePercentage quota = %v, want none", *m.Series[0].Quota)
	}
}

func TestContract_Logs_RuntimeAndBuildAreSeparatePaths(t *testing.T) {
	c, rec := newMock(t)

	if _, err := c.Logs(context.Background(), client.LogsRequest{ID: "app_a1b2c3", Take: 2}); err != nil {
		t.Fatalf("Logs: %v", err)
	}

	got := rec.last()
	if got.method != http.MethodGet || got.path != "/hyperlift/applications/app_a1b2c3/logs" {
		t.Fatalf("wire = %s %s, want GET /hyperlift/applications/app_a1b2c3/logs", got.method, got.path)
	}

	if got.query.Get("take") != "2" {
		t.Errorf("take = %q, want 2", got.query.Get("take"))
	}

	// The build selector is a path, never a query parameter.
	if _, ok := got.query["build"]; ok {
		t.Errorf("runtime logs query = %v, want no build param", got.query)
	}

	page, err := c.BuildLogs(context.Background(), client.LogsRequest{ID: "app_a1b2c3"})
	if err != nil {
		t.Fatalf("BuildLogs: %v", err)
	}

	got = rec.last()
	if got.method != http.MethodGet || got.path != "/hyperlift/applications/app_a1b2c3/build-logs" {
		t.Fatalf("wire = %s %s, want GET /hyperlift/applications/app_a1b2c3/build-logs", got.method, got.path)
	}

	if _, ok := got.query["build"]; ok {
		t.Errorf("build logs query = %v, want no build param", got.query)
	}

	// Build logs use the same page schema as runtime logs.
	if len(page.Items) == 0 || page.Cursor == "" || !page.Finished {
		t.Errorf("build page = %+v, want items + cursor + finished", page)
	}
}

func TestContract_Metrics_MissingParamsRejected(t *testing.T) {
	c, _ := newMock(t)
	// An empty interval must fail the mock's query validation.
	q := client.MetricsQuery{
		Start:    time.Now(),
		End:      time.Now(),
		Interval: "",
		Metrics:  []string{"cpuUsagePercentage"},
	}

	_, err := c.Metrics(context.Background(), "app_a1b2c3", q)

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("err = %v, want 422 APIError", err)
	}
}

// TestContract_OldPathsRejected pins that the mock, which stands in for the
// contract, returns 404 for the old paths the CLI no longer uses.
func TestContract_OldPathsRejected(t *testing.T) {
	rec := &recorder{h: testapi.Server()}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	for _, p := range []string{
		"/hyperlift",            // old flat collection
		"/hyperlift/app_a1b2c3", // old flat item path
		"/hyperlift/applications/app_a1b2c3/start", // start is now scale
		"/hyperlift/applications/app_a1b2c3/stop",  // stop is now scale
		"/hyperlift/applications/app_a1b2c3/env",   // renamed to environment
	} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+p, nil)
		if err != nil {
			t.Fatalf("build request %s: %v", p, err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s -> %d, want 404", p, resp.StatusCode)
		}
	}
}

// --- Probe -----------------------------------------------------------------

func TestProbe_ValidWhen404(t *testing.T) {
	// The contract mock returns 404 for a random uuid, so the credentials are
	// valid.
	c, rec := newMock(t)

	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v, want nil (404 => valid)", err)
	}

	if got := rec.last(); got.method != http.MethodGet || !bytes.HasPrefix([]byte(got.path), []byte("/hyperlift/applications/")) {
		t.Errorf("probe wire = %s %s", got.method, got.path)
	}
}

func TestProbe_InvalidOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(client.HeaderErrorCode, "auth.unauthorized")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"bad key"}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", APISecret: "s"})

	err := c.Probe(context.Background())

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want 401 APIError", err)
	}
}

func TestProbe_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens now

	c := client.New(client.Options{BaseURL: url, APIKey: "k", APISecret: "s"})

	if err := c.Probe(context.Background()); err == nil {
		t.Fatal("want a network error from Probe")
	}
}
