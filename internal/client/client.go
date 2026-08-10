package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// httpClient is the real REST implementation of Client.
type httpClient struct {
	baseURL string
	http    *http.Client
}

// Options configure a new HTTP client.
type Options struct {
	BaseURL   string
	APIKey    string
	APISecret string
	// HTTPClient replaces the default client, mainly for tests. The auth
	// transport wraps its transport, or the default transport.
	HTTPClient *http.Client
	// Debug prints one redacted trace line per HTTP request to DebugOut. It
	// never prints headers, bodies, or query-parameter values.
	Debug bool
	// DebugOut receives the debug traces, usually stderr. It does nothing unless
	// Debug is set.
	DebugOut io.Writer
}

// New constructs an HTTP-backed Client.
func New(opts Options) Client {
	base := &http.Client{Timeout: 30 * time.Second}

	if opts.HTTPClient != nil {
		// Copy the struct: the auth transport must not mutate the caller's
		// client.
		c := *opts.HTTPClient
		base = &c
	}

	tr := newAuthTransport(base.Transport, opts.APIKey, opts.APISecret)
	if opts.Debug {
		tr.debug = opts.DebugOut
	}

	base.Transport = tr

	return &httpClient{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		http:    base,
	}
}

// appsPath is the applications collection; one application lives at appsPath/{id}.
const appsPath = "/hyperlift/applications"

func (c *httpClient) url(parts ...string) string {
	return c.baseURL + "/" + strings.TrimLeft(strings.Join(parts, "/"), "/")
}

// do sends a request to the full url. It decodes a 2xx JSON body into out when
// out is not nil, and turns a non-2xx response into an *APIError. A 202 is an
// error too: every Hyperlift mutation is a synchronous 200.
func (c *httpClient) do(ctx context.Context, method, reqURL string, body, out any) error {
	var reader io.Reader

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}

		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// A *url.Error repeats the URL the wrap below already carries.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}

		return fmt.Errorf("%s %s: %w", method, reqURL, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusAccepted {
		// Build the error like every other one, so the Code and OperationID
		// headers populate and the body is read, then override the detail.
		e := parseError(resp)
		e.Detail = "unexpected 202 Accepted from a synchronous endpoint"

		return e
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseError(resp)
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// randomUUID returns a random RFC-4122 v4 UUID string.
// Avoids importing UUID dependency for just one call.
func randomUUID() string {
	var b [16]byte

	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Probe verifies the credentials. The External API has no identity endpoint, so
// Probe does a GET on a random application id: a 404 (problem+json) means the
// credentials are valid. It returns a 401/403 *APIError unchanged, so callers can
// tell a bad key (401) from a valid key without the read scope (403).
func (c *httpClient) Probe(ctx context.Context) error {
	err := c.do(ctx, http.MethodGet, c.url(appsPath, randomUUID()), nil, nil)
	if err == nil {
		return nil
	}

	// A 404 means the credentials authenticated and the random id simply does
	// not exist. Require the gateway's own error code, so a 404 from a typo'd
	// base URL or an unrelated host cannot pass as a successful login.
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Code == codeNotFound {
		return nil
	}

	return err
}

func (c *httpClient) AppsList(ctx context.Context, take, skip int) ([]Application, int, error) {
	u, err := url.Parse(c.url(appsPath))
	if err != nil {
		return nil, 0, fmt.Errorf("build list url: %w", err)
	}

	q := u.Query()
	q.Set("take", strconv.Itoa(take))
	q.Set("skip", strconv.Itoa(skip))
	u.RawQuery = q.Encode()

	var env struct {
		Items []Application `json:"items"`
		Total int           `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, u.String(), nil, &env); err != nil {
		return nil, 0, err
	}

	return env.Items, env.Total, nil
}

func (c *httpClient) AppsGet(ctx context.Context, id string) (*Application, error) {
	var app Application
	if err := c.do(ctx, http.MethodGet, c.url(appsPath, url.PathEscape(id)), nil, &app); err != nil {
		return nil, err
	}

	return &app, nil
}

// mutate calls a mutation subresource and decodes the {id}
// ApplicationMutationResponse.
func (c *httpClient) mutate(ctx context.Context, method, path string, body any) (*OpRef, error) {
	var ref OpRef
	if err := c.do(ctx, method, path, body, &ref); err != nil {
		return nil, err
	}

	return &ref, nil
}

func (c *httpClient) AppsBuild(ctx context.Context, id string) (*OpRef, error) {
	return c.mutate(ctx, http.MethodPost, c.url(appsPath, url.PathEscape(id), "build"), nil)
}

func (c *httpClient) AppsRestart(ctx context.Context, id string) (*OpRef, error) {
	return c.mutate(ctx, http.MethodPost, c.url(appsPath, url.PathEscape(id), "restart"), nil)
}

// AppsStart and AppsStop use the scale endpoint: 1 runs the application, 0 stops
// it. Scale sets a desired state, so the contract makes it a PUT.
func (c *httpClient) AppsStart(ctx context.Context, id string) (*OpRef, error) {
	return c.mutate(ctx, http.MethodPut, c.url(appsPath, url.PathEscape(id), "scale"), scaleRequest{Scale: 1})
}

func (c *httpClient) AppsStop(ctx context.Context, id string) (*OpRef, error) {
	return c.mutate(ctx, http.MethodPut, c.url(appsPath, url.PathEscape(id), "scale"), scaleRequest{Scale: 0})
}

func (c *httpClient) EnvGet(ctx context.Context, id string) (map[string]string, error) {
	var env envEnvelope
	if err := c.do(ctx, http.MethodGet, c.url(appsPath, url.PathEscape(id), "environment"), nil, &env); err != nil {
		return nil, err
	}

	if env.Items == nil {
		env.Items = map[string]string{}
	}

	return env.Items, nil
}

func (c *httpClient) EnvUpdate(ctx context.Context, id string, vars map[string]string) error {
	// The PUT replaces the complete {items} map.
	return c.do(ctx, http.MethodPut, c.url(appsPath, url.PathEscape(id), "environment"), envEnvelope{Items: vars}, nil)
}

func (c *httpClient) Metrics(ctx context.Context, id string, q MetricsQuery) (*Metrics, error) {
	u, err := url.Parse(c.url(appsPath, url.PathEscape(id), "metrics"))
	if err != nil {
		return nil, fmt.Errorf("build metrics url: %w", err)
	}

	query := u.Query()
	query.Set("startDate", isoDate(q.Start))
	query.Set("endDate", isoDate(q.End))
	query.Set("interval", q.Interval)
	query.Set("metrics", strings.Join(q.Metrics, ","))
	u.RawQuery = query.Encode()

	var m Metrics
	if err := c.do(ctx, http.MethodGet, u.String(), nil, &m); err != nil {
		return nil, err
	}

	return &m, nil
}

// isoDate formats t as a UTC ISO-8601 datetime with milliseconds. This matches
// the contract's isoDate scalar, e.g. 2024-01-15T00:00:00.000Z.
func isoDate(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func (c *httpClient) Logs(ctx context.Context, lr LogsRequest) (*LogsPage, error) {
	return c.logsPage(ctx, lr, "logs")
}

func (c *httpClient) BuildLogs(ctx context.Context, lr LogsRequest) (*LogsPage, error) {
	return c.logsPage(ctx, lr, "build-logs")
}

// logsPage reads one page from GET {appsPath}/{id}/logs or .../build-logs. The
// gateway rewrites the verb to POST and moves the query into the internal body.
func (c *httpClient) logsPage(ctx context.Context, lr LogsRequest, sub string) (*LogsPage, error) {
	logsURL := c.url(appsPath, url.PathEscape(lr.ID), sub)

	q := url.Values{}
	if lr.Take > 0 {
		q.Set("take", strconv.Itoa(lr.Take))
	}

	if lr.Cursor != "" {
		q.Set("cursor", lr.Cursor)
	}

	if enc := q.Encode(); enc != "" {
		logsURL += "?" + enc
	}

	var page LogsPage
	if err := c.do(ctx, http.MethodGet, logsURL, nil, &page); err != nil {
		return nil, err
	}

	return &page, nil
}
