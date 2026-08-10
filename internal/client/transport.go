package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/build"
)

const (
	HeaderAPIKey    = "X-API-Key" //nolint:gosec // header name, not a credential
	HeaderAPISecret = "X-API-Secret"
)

// authTransport is an http.RoundTripper. It adds the auth and User-Agent
// headers, and retries a 429 when the wait is short. A Retry-After above
// maxBackoff surfaces the 429 at once: a rate-limit window cannot reset
// early, so an earlier retry is futile.
type authTransport struct {
	base       http.RoundTripper
	apiKey     string
	apiSecret  string
	maxRetries int
	// sleep waits for d, or until ctx is done, and returns ctx.Err() on cancel.
	// It is a field so tests do not wait.
	sleep func(ctx context.Context, d time.Duration) error
	// debug, when set, receives one redacted trace line per request.
	debug io.Writer
}

// newAuthTransport wraps base with auth. A nil base means http.DefaultTransport.
func newAuthTransport(base http.RoundTripper, apiKey, apiSecret string) *authTransport {
	if base == nil {
		base = http.DefaultTransport
	}

	return &authTransport{
		base:       base,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		maxRetries: 3,
		sleep:      sleepCtx,
	}
}

// RoundTrip implements http.RoundTripper.
func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request: never mutate the caller's copy.
	r := req.Clone(req.Context())
	if t.apiKey != "" {
		r.Header.Set(HeaderAPIKey, t.apiKey)
	}

	if t.apiSecret != "" {
		r.Header.Set(HeaderAPISecret, t.apiSecret)
	}

	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", build.UserAgent())
	}

	for attempt := 0; ; attempt++ {
		start := time.Now()
		resp, err := t.base.RoundTrip(r)
		t.trace(r, resp, err, time.Since(start))

		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests || attempt >= t.maxRetries {
			return resp, nil
		}

		// On 429, honor Retry-After. Without it, back off exponentially.
		wait := defaultBackoff(attempt)

		if ra := resp.Header.Get(HeaderRetryAfter); ra != "" {
			if d := parseRetryAfter(ra); d > 0 {
				if d > maxBackoff {
					// The server's wait is longer than a retry may sleep, so
					// return the 429; the error message carries the wait.
					return resp, nil
				}

				wait = d
			}
		}

		// Drain and close the body before the retry, so the keep-alive
		// connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		// Rewind the request body: the first attempt consumed it. GetBody, which
		// http.NewRequest sets for in-memory bodies, gives a fresh reader. On
		// failure the 429 response is already closed, so an error is the only exit.
		if r.GetBody != nil {
			body, gerr := r.GetBody()
			if gerr != nil {
				return nil, fmt.Errorf("retry after 429: rewind request body: %w", gerr)
			}

			r.Body = body
		}

		if serr := t.sleep(r.Context(), wait); serr != nil {
			return nil, serr
		}
	}
}

// trace writes one debug line per attempt: the method, the path without the
// query, the status and the duration.
func (t *authTransport) trace(r *http.Request, resp *http.Response, err error, dur time.Duration) {
	if t.debug == nil {
		return
	}

	switch {
	case err != nil:
		_, _ = fmt.Fprintf(t.debug, "DEBUG %s %s -> error (%s): %v\n", r.Method, r.URL.Path, dur.Round(time.Millisecond), err)
	default:
		_, _ = fmt.Fprintf(t.debug, "DEBUG %s %s -> %d (%s)\n", r.Method, r.URL.Path, resp.StatusCode, dur.Round(time.Millisecond))
	}
}

// sleepCtx waits for d, or until ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// backoffSteps is the exponential backoff schedule (1s, 2s, 4s, ...), capped at
// maxBackoff. A fixed table avoids an unbounded, unsafe bit shift.
var backoffSteps = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	maxBackoff,
}

const maxBackoff = 30 * time.Second

func defaultBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	if attempt >= len(backoffSteps) {
		return maxBackoff
	}

	return backoffSteps[attempt]
}
