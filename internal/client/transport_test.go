package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// rtFunc adapts a function to an http.RoundTripper.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newResp(status int, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}

	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("{}")),
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"2", 2 * time.Second},
		{"1.5", 1500 * time.Millisecond}, // fractional, accepted for safety
		{"0.05", 50 * time.Millisecond},
		{"-3", 0},
		{"", 0},
		{"garbage", 0},
		// Hostile values: non-finite gives 0, huge clamps to the ceiling.
		{"inf", 0},
		{"-inf", 0},
		{"nan", 0},
		{"1e9", time.Hour},
		{"3601", time.Hour},
		{"3600", time.Hour},
	}

	for _, c := range cases {
		if got := parseRetryAfter(c.in); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	// An HTTP-date in the past clamps to 0, one far in the future to the ceiling.
	if got := parseRetryAfter("Mon, 02 Jan 2006 15:04:05 GMT"); got != 0 {
		t.Errorf("past HTTP-date = %v, want 0", got)
	}

	future := time.Now().Add(48 * time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got != time.Hour {
		t.Errorf("far-future HTTP-date = %v, want the 1h ceiling", got)
	}
}

func TestRoundTrip_RetryCarriesBodyAndHonorsRetryAfter(t *testing.T) {
	const payload = `{"items":{"A":"1","B":"2"}}`

	var (
		mu     sync.Mutex
		bodies []string
	)

	attempts := 0
	base := rtFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)

		mu.Lock()

		bodies = append(bodies, string(b))
		attempts++
		n := attempts

		mu.Unlock()

		if n == 1 {
			return newResp(http.StatusTooManyRequests, map[string]string{HeaderRetryAfter: "2"}), nil
		}

		return newResp(http.StatusOK, nil), nil
	})

	tr := newAuthTransport(base, "k", "s")

	var waits []time.Duration

	tr.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, "http://example/hyperlift/app/environment", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	// The second attempt must carry the full body. This is verified behavior.
	if bodies[1] != payload {
		t.Errorf("retry body = %q, want %q", bodies[1], payload)
	}

	// The retry must honor the server's wait.
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Errorf("waits = %v, want [2s]", waits)
	}
}

// TestRoundTrip_LongRetryAfterFailsFast proves that a Retry-After above
// maxBackoff surfaces the 429 without a retry: the window cannot reset early,
// so sleeping the cap and retrying is futile.
func TestRoundTrip_LongRetryAfterFailsFast(t *testing.T) {
	attempts := 0
	base := rtFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return newResp(http.StatusTooManyRequests, map[string]string{HeaderRetryAfter: "600"}), nil
	})

	tr := newAuthTransport(base, "k", "s")
	tr.sleep = func(context.Context, time.Duration) error {
		t.Fatal("no sleep expected: a long Retry-After must not be retried")
		return nil
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example/hyperlift/applications", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 surfaced", resp.StatusCode)
	}

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRoundTrip_ContextCancelDuringBackoff(t *testing.T) {
	base := rtFunc(func(*http.Request) (*http.Response, error) {
		return newResp(http.StatusTooManyRequests, map[string]string{HeaderRetryAfter: "5"}), nil
	})
	tr := newAuthTransport(base, "k", "s") // real sleepCtx

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example/hyperlift/x", nil)

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	resp, err := tr.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}

	if err == nil {
		t.Fatal("want an error from cancelled backoff")
	}

	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestSleepCtx_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sleepCtx(ctx, time.Hour); err == nil {
		t.Fatal("want ctx error when already cancelled")
	}
}

func TestDebugHook_RedactsQueryAndPrintsStatus(t *testing.T) {
	base := rtFunc(func(*http.Request) (*http.Response, error) {
		return newResp(http.StatusOK, nil), nil
	})
	tr := newAuthTransport(base, "k", "s")

	var buf strings.Builder

	tr.debug = &buf

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example/hyperlift/app/metrics?startDate=SECRET&metrics=cpu", nil)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	out := buf.String()
	if !strings.Contains(out, "DEBUG GET /hyperlift/app/metrics -> 200") {
		t.Errorf("debug line = %q, want method/path/status", out)
	}

	// Query-parameter values must never appear.
	if strings.Contains(out, "SECRET") || strings.Contains(out, "startDate") || strings.Contains(out, "cpu") {
		t.Errorf("debug line leaked query data: %q", out)
	}
}
