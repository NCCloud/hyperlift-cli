package client

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Response header names that carry error and operation metadata.
const (
	HeaderErrorCode   = "spaceship-error-code"
	HeaderOperationID = "spaceship-operation-id"
	HeaderRetryAfter  = "Retry-After"
)

// codeNotFound is the gateway's error code for a missing resource. Probe uses
// it to tell a real gateway apart from any host that answers 404.
const codeNotFound = "business.notFound"

// APIError is the typed error built from an application/problem+json response.
type APIError struct {
	Status      int
	Code        string
	OperationID string
	Detail      string
	RetryAfter  time.Duration
}

// problemBody is the part of the application/problem+json document the client
// reads.
type problemBody struct {
	Detail string `json:"detail"`
	Title  string `json:"title"`
}

// Error returns the message for the error interface.
func (e *APIError) Error() string {
	switch {
	case e.Detail != "" && e.Code != "":
		return fmt.Sprintf("%s (%s)", e.Detail, e.Code)
	case e.Detail != "":
		return e.Detail
	case e.Code != "":
		return fmt.Sprintf("request failed: %s", e.Code)
	default:
		return fmt.Sprintf("request failed with status %d", e.Status)
	}
}

// parseError builds an *APIError from a non-2xx HTTP response. It reads the
// body. Call it only for error responses.
func parseError(resp *http.Response) *APIError {
	e := &APIError{
		Status:      resp.StatusCode,
		Code:        resp.Header.Get(HeaderErrorCode),
		OperationID: resp.Header.Get(HeaderOperationID),
	}

	if ra := resp.Header.Get(HeaderRetryAfter); ra != "" {
		e.RetryAfter = parseRetryAfter(ra)
	}

	if resp.Body != nil {
		// Buffer the body, because the raw text is the last-resort message when
		// it is not problem+json. The limit bounds what a proxy or CDN can send.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

		var pb problemBody
		if json.Unmarshal(body, &pb) == nil {
			switch {
			case pb.Detail != "":
				e.Detail = pb.Detail
			case pb.Title != "":
				e.Detail = pb.Title
			}
		}

		if e.Detail == "" && len(body) > 0 {
			e.Detail = string(body)
		}
	}

	return e
}

// maxRetryAfter caps a parsed Retry-After. A larger value is a hostile or
// broken header, and the duration also ends up in a user-facing message.
const maxRetryAfter = time.Hour

// parseRetryAfter reads both Retry-After forms: delta-seconds and HTTP-date. It
// also accepts fractional seconds ("1.5") for safety, although the gateway sends
// integers. Non-finite or negative values give 0; the result is capped at
// maxRetryAfter.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)

	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if math.IsNaN(secs) || math.IsInf(secs, 0) || secs < 0 {
			return 0
		}

		if secs > maxRetryAfter.Seconds() {
			return maxRetryAfter
		}

		return time.Duration(secs * float64(time.Second))
	}

	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, maxRetryAfter)
		}
	}

	return 0
}
