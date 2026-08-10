// Package testapi holds a stateful HTTP stand-in for the Hyperlift endpoints of
// the Spaceship External API: the real wire shapes, headers and problem+json
// errors. cmd/mock and the tests use it. contract_validation_test.go validates
// every response against the pinned copy of the published OpenAPI document, so
// the wire shape is proven real, not self-declared.
package testapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// ptr takes the address of a literal, for the contract's nullable fields.
func ptr[T any](v T) *T { return &v }

// SeedApps returns the mock's two sample applications in the contract shape.
// Tests assert against these values instead of repeating literals.
func SeedApps() map[string]*client.Application {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	return map[string]*client.Application{
		"app_a1b2c3": {
			ID:                       "app_a1b2c3",
			Status:                   client.StatusRunning,
			BuildStatus:              client.BuildStatusBuilt,
			Plan:                     "hyperlift_micro",
			Domain:                   ptr("demo.hyperlift.app"),
			Scale:                    ptr(1),
			Branch:                   ptr("main"),
			GithubInstallationID:     ptr[int64](12345678),
			GithubRepositoryFullName: ptr("acme/web"),
			DockerfilePath:           ptr("Dockerfile"),
			AutomaticBuildEnabled:    ptr(true),
			CreatedAt:                now,
			UpdatedAt:                ptr(now.Add(time.Hour)),
		},
		"app_d4e5f6": {
			ID:                       "app_d4e5f6",
			Status:                   client.StatusStopped,
			BuildStatus:              client.BuildStatusNone,
			Plan:                     "hyperlift_large",
			Domain:                   nil, // renders as "domain": null
			Scale:                    ptr(0),
			Branch:                   ptr("develop"),
			GithubInstallationID:     ptr[int64](67890),
			GithubRepositoryFullName: ptr("acme/api"),
			DockerfilePath:           ptr("docker/Dockerfile"),
			AutomaticBuildEnabled:    ptr(false),
			CreatedAt:                now.Add(-48 * time.Hour),
			UpdatedAt:                ptr(now.Add(-time.Hour)),
		},
	}
}

// SeedEnv returns the mock's initial environment variables per application.
func SeedEnv() map[string]map[string]string {
	return map[string]map[string]string{
		"app_a1b2c3": {"NODE_ENV": "production", "PORT": "8080"},
		"app_d4e5f6": {"GO_ENV": "staging"},
	}
}

// mockServer is the stateful handler behind Server().
type mockServer struct {
	mu   sync.Mutex
	apps map[string]*client.Application
	env  map[string]map[string]string
	// pending holds one unsettled transition per application id. The next read
	// settles it, so `--wait` polling sees a transient state, then a terminal one.
	pending map[string]transition
}

// transition records the terminal state a pending operation settles into, and
// the earliest time a GET may apply it.
type transition struct {
	status      client.AppStatus   // terminal application status; empty leaves it as is
	buildStatus client.BuildStatus // terminal build_status; empty leaves it as is
	settleAt    time.Time          // stays transient until now is at or past settleAt
}

// Server returns an http.Handler that stands in for the v1/hyperlift namespace
// of the real External API gateway. It mounts at /hyperlift, so a base URL such
// as http://localhost:8080 composes exactly as the real https://gateway/api/v1
// does.
func Server() http.Handler {
	s := &mockServer{
		apps:    SeedApps(),
		env:     SeedEnv(),
		pending: map[string]transition{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.route)

	return mux
}

const prefix = "/hyperlift/applications"

// rateLimitID is a sentinel application id that always answers 429, so the
// standalone mock can exercise the rate-limit handling.
const rateLimitID = "app_429"

// mockOperationID is the operation id in every mock error. The contract types it
// as exactly 16 lowercase alphanumeric characters.
const mockOperationID = "0f8c2b47a91d3e6c"

// asyncSettleDelay is how long build, restart and scale stay transient before a
// GET settles them. It is long enough to watch `--wait` work.
const asyncSettleDelay = 3 * time.Second

func (s *mockServer) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == prefix || path == prefix+"/" {
		s.requireMethod(w, r, http.MethodGet, func() { s.list(w, r) })
		return
	}

	rest, ok := strings.CutPrefix(path, prefix+"/")

	rest = strings.Trim(rest, "/")
	if !ok || rest == "" {
		s.problem(w, http.StatusNotFound, "business.notFound", "resource not found")
		return
	}

	parts := strings.Split(rest, "/")
	id := parts[0]

	// The rate-limit sentinel always answers 429.
	if id == rateLimitID {
		s.rateLimited(w)
		return
	}

	// /hyperlift/applications/{id}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.get(w, id)
		default:
			s.problem(w, http.StatusMethodNotAllowed, "business.methodNotAllowed", "method not allowed")
		}

		return
	}

	// /hyperlift/applications/{id}/{sub}
	if len(parts) != 2 {
		s.problem(w, http.StatusNotFound, "business.notFound", "resource not found")
		return
	}

	sub := parts[1]
	switch sub {
	case "build", "restart":
		s.requireMethod(w, r, http.MethodPost, func() { s.lifecycle(w, id, sub) })
	case "scale":
		s.requireMethod(w, r, http.MethodPut, func() { s.scale(w, r, id) })
	case "environment":
		s.environment(w, r, id)
	case "metrics":
		s.requireMethod(w, r, http.MethodGet, func() { s.metrics(w, r, id) })
	case "logs":
		s.requireMethod(w, r, http.MethodGet, func() { s.logs(w, r, id, "app") })
	case "build-logs":
		s.requireMethod(w, r, http.MethodGet, func() { s.logs(w, r, id, "build") })
	default:
		s.problem(w, http.StatusNotFound, "business.notFound", "resource not found")
	}
}

// requireMethod runs fn when the request uses method, and answers 405 otherwise.
func (s *mockServer) requireMethod(w http.ResponseWriter, r *http.Request, method string, fn func()) {
	if r.Method != method {
		s.problem(w, http.StatusMethodNotAllowed, "business.methodNotAllowed", "method not allowed")
		return
	}

	fn()
}

// settle applies a due pending transition. The caller must hold s.mu.
func (s *mockServer) settle(id string) {
	tr, pending := s.pending[id]
	if !pending || time.Now().Before(tr.settleAt) {
		return
	}

	app := s.apps[id]
	if tr.status != "" {
		app.Status = tr.status
	}

	if tr.buildStatus != "" {
		app.BuildStatus = tr.buildStatus
	}

	delete(s.pending, id)
}

func (s *mockServer) get(w http.ResponseWriter, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	app, ok := s.apps[id]
	if !ok {
		s.notFound(w, id)
		return
	}

	s.settle(id)
	s.writeJSON(w, app)
}

// list returns one page of the caller's applications, as {items,total}, paged by
// offset. It rejects an out-of-range take or skip like the real backend, so a
// test with a contract-breaking sender cannot pass.
func (s *mockServer) list(w http.ResponseWriter, r *http.Request) {
	take, err := queryInt(r, "take", 100)
	if err != nil || take < 1 || take > 100 {
		s.validationFailed(w, "take", "take must be between 1 and 100")
		return
	}

	skip, err := queryInt(r, "skip", 0)
	if err != nil || skip < 0 {
		s.validationFailed(w, "skip", "skip must be >= 0")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, len(s.apps))
	for id := range s.apps {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	total := len(ids)
	lo := min(skip, total)
	hi := min(skip+take, total)

	items := make([]*client.Application, 0, hi-lo)
	for _, id := range ids[lo:hi] {
		// The list settles due transitions too, so `apps list` against the
		// standalone mock does not show building/restarting forever.
		s.settle(id)
		items = append(items, s.apps[id])
	}

	s.writeJSON(w, map[string]any{"items": items, "total": total})
}

// lifecycle handles build and restart. It answers a synchronous 200 with {id},
// and records a transition from the transient state to the terminal one.
func (s *mockServer) lifecycle(w http.ResponseWriter, id, action string) {
	s.mu.Lock()

	app, ok := s.apps[id]
	if ok {
		settle := time.Now().Add(asyncSettleDelay)

		switch action {
		case "build":
			app.BuildStatus = client.BuildStatusBuilding
			s.pending[id] = transition{buildStatus: client.BuildStatusBuilt, settleAt: settle}
		case "restart":
			app.Status = client.StatusRestarting
			s.pending[id] = transition{status: client.StatusRunning, settleAt: settle}
		}
	}

	s.mu.Unlock()

	if !ok {
		s.notFound(w, id)
		return
	}

	s.writeJSON(w, client.OpRef{ID: id})
}

// scale handles PUT /{id}/scale with the body {"scale":0} or {"scale":1}. 1 runs
// the application and 0 stops it.
func (s *mockServer) scale(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Scale int `json:"scale"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.validationFailed(w, "scale", "invalid body")
		return
	}

	if body.Scale != 0 && body.Scale != 1 {
		s.validationFailed(w, "scale", "scale must be 0 or 1")
		return
	}

	s.mu.Lock()

	app, ok := s.apps[id]
	if ok {
		app.Scale = ptr(body.Scale)
		settle := time.Now().Add(asyncSettleDelay)

		if body.Scale == 1 {
			app.Status = client.StatusInStart
			s.pending[id] = transition{status: client.StatusRunning, settleAt: settle}
		} else {
			app.Status = client.StatusInStop
			s.pending[id] = transition{status: client.StatusStopped, settleAt: settle}
		}
	}

	s.mu.Unlock()

	if !ok {
		s.notFound(w, id)
		return
	}

	s.writeJSON(w, client.OpRef{ID: id})
}

// envNameOK applies the contract's environment-variable name rule.
func envNameOK(name string) bool {
	if len(name) > 128 {
		return false
	}

	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToUpper(strings.TrimSpace(name)))

	return envNameRe.MatchString(normalized)
}

var envNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func (s *mockServer) environment(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.apps[id]; !ok {
		s.notFound(w, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items := s.env[id]
		if items == nil {
			items = map[string]string{}
		}
		// The contract response is {id, items}.
		s.writeJSON(w, map[string]any{"id": id, "items": items})
	case http.MethodPut:
		var body struct {
			Items map[string]string `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.validationFailed(w, "items", "invalid body")
			return
		}

		for name := range body.Items {
			if !envNameOK(name) {
				s.validationFailed(w, "items", "invalid environment variable name: "+name)
				return
			}
		}

		s.env[id] = body.Items // replaces the whole map
		s.writeJSON(w, client.OpRef{ID: id})
	default:
		s.problem(w, http.StatusMethodNotAllowed, "business.methodNotAllowed", "method not allowed")
	}
}

// metricUnits holds the contract metric names the mock accepts, with the unit of
// each.
var metricUnits = map[string]string{
	"memoryUsageBytes":               "bytes",
	"cpuUsagePercentage":             "percent",
	"networkReceiveRateBytes":        "bytesPerSecond",
	"networkTransmitRateBytes":       "bytesPerSecond",
	"ephemeralStorageUsedMebibytes":  "mebibytes",
	"persistentStorageUsedMebibytes": "mebibytes",
}

func (s *mockServer) metrics(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	_, ok := s.apps[id]
	s.mu.Unlock()

	if !ok {
		s.notFound(w, id)
		return
	}

	// Check the required query parameters, per GetApplicationMetricsQueryParams.
	q := r.URL.Query()
	for _, p := range []string{"startDate", "endDate", "interval", "metrics"} {
		if q.Get(p) == "" {
			s.validationFailed(w, p, fmt.Sprintf("missing query parameter %q", p))
			return
		}
	}

	requested := strings.Split(q.Get("metrics"), ",")
	for _, name := range requested {
		if _, ok := metricUnits[strings.TrimSpace(name)]; !ok {
			s.validationFailed(w, "metrics", fmt.Sprintf("unknown metric %q", name))
			return
		}
	}

	// Copy the real backend: one series per requested metric, in request order,
	// on one shared timestamp grid, where a bucket with no seeded value reports 0.
	seeded := map[string][]float64{
		"memoryUsageBytes":   {536870912, 557842432},
		"cpuUsagePercentage": {12.5, 18.0},
	}
	timestamps := []string{"2026-06-01T12:00:00.000Z", "2026-06-01T12:05:00.000Z"}
	quotaValues := map[string]float64{"memoryUsageBytes": 1073741824}

	series := make([]map[string]any, 0, len(requested))
	for _, name := range requested {
		name = strings.TrimSpace(name)

		samples := make([]map[string]any, len(timestamps))
		for i, ts := range timestamps {
			var value float64
			if values, ok := seeded[name]; ok {
				value = values[i]
			}

			samples[i] = map[string]any{"timestamp": ts, "value": value}
		}

		entry := map[string]any{"name": name, "unit": metricUnits[name], "samples": samples}
		if quota, ok := quotaValues[name]; ok {
			entry["quota"] = quota
		}

		series = append(series, entry)
	}

	s.writeJSON(w, map[string]any{"metrics": series})
}

// mockLogTotal is the fixed number of log lines the mock serves per kind.
const mockLogTotal = 3

func (s *mockServer) logs(w http.ResponseWriter, r *http.Request, id, kind string) {
	s.mu.Lock()
	_, ok := s.apps[id]
	s.mu.Unlock()

	if !ok {
		s.notFound(w, id)
		return
	}

	take, err := queryInt(r, "take", 100)
	if err != nil || take < 1 || take > 100 {
		s.validationFailed(w, "take", "take must be between 1 and 100")
		return
	}

	// The cursor holds the number of lines the caller has seen, in the
	// contract's shape of 24 hex characters. A cursor past the fixed history is
	// well-formed but stale; it yields an empty finished page, like a real
	// server, not a validation error.
	seen := 0

	if cur := r.URL.Query().Get("cursor"); cur != "" {
		n, err := strconv.ParseInt(cur, 16, 64)
		if len(cur) != 24 || err != nil || n < 0 {
			s.validationFailed(w, "cursor", "invalid cursor")
			return
		}

		seen = min(int(n), mockLogTotal)
	}

	items := make([]map[string]any, 0)
	for i := seen; i < mockLogTotal && len(items) < take; i++ {
		items = append(items, map[string]any{
			"message":   fmt.Sprintf("%s line %d", kind, i+1),
			"timestamp": fmt.Sprintf("2026-01-15T12:00:%02dZ", i),
		})
	}

	seen += len(items)

	s.writeJSON(w, map[string]any{
		"items":  items,
		"cursor": fmt.Sprintf("%024x", seen),
		// The mock history is fixed, so it reports finished once the caller reads
		// it all. A real running application would leave the page open.
		"finished": seen >= mockLogTotal,
	})
}

// queryInt parses an integer query parameter. An absent parameter gives def.
func queryInt(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}

	return strconv.Atoi(v)
}

func (s *mockServer) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *mockServer) notFound(w http.ResponseWriter, id string) {
	s.problem(w, http.StatusNotFound, "business.notFound", fmt.Sprintf("application %q not found", id))
}

// validationFailed writes a 422. Its body carries the general detail and the
// per-field `data` list the contract requires.
func (s *mockServer) validationFailed(w http.ResponseWriter, field, detail string) {
	s.writeProblem(w, http.StatusUnprocessableEntity, "business.validationFailed", map[string]any{
		"detail": detail,
		"data":   []map[string]string{{"field": field, "details": detail}},
	})
}

// rateLimited writes a 429 with the rate-limit headers and a problem+json body.
func (s *mockServer) rateLimited(w http.ResponseWriter) {
	w.Header().Set("X-RateLimit-Limit", "300")
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(2*time.Second).Unix(), 10))
	// Integer seconds: the schema types it that way, and so does the live gateway.
	// The mock also sends the X-RateLimit-* headers the gateway omits.
	w.Header().Set(client.HeaderRetryAfter, "2")

	s.writeProblem(w, http.StatusTooManyRequests, "business.rateLimited", map[string]any{
		"detail": "too many requests",
	})
}

// problem writes an application/problem+json error that carries only a detail.
func (s *mockServer) problem(w http.ResponseWriter, status int, code, detail string) {
	s.writeProblem(w, status, code, map[string]any{"detail": detail})
}

// writeProblem writes an application/problem+json body, with the error-code and
// operation-id headers.
func (s *mockServer) writeProblem(w http.ResponseWriter, status int, code string, body map[string]any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set(client.HeaderErrorCode, code)
	w.Header().Set(client.HeaderOperationID, mockOperationID)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
