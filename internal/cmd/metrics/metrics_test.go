package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/client/clienttest"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil/cmdutiltest"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// testFactory wraps the shared fixture, dropping the unused stderr buffer.
func testFactory(t *testing.T, mc *clienttest.Fake, format string) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()

	f, out, _ := cmdutiltest.NewFactory(mc, format)

	return f, out
}

// run executes the metrics command with args. It returns the message and exit
// code from FriendlyError, like the real runner does.
func run(t *testing.T, f *cmdutil.Factory, args ...string) (msg string, code int) {
	t.Helper()

	cmd := NewCmdMetrics(f)

	if args == nil {
		args = []string{} // nil would make cobra fall back to os.Args
	}

	cmd.SetArgs(args)
	cmd.SetOut(f.IOStreams.Out)
	cmd.SetErr(f.IOStreams.ErrOut)

	err := cmd.ExecuteContext(context.Background())

	return cmdutil.FriendlyError(err, false)
}

// staticMetrics returns a Fake whose Metrics call always answers with m.
func staticMetrics(m *client.Metrics) *clienttest.Fake {
	return &clienttest.Fake{
		MetricsFunc: func(context.Context, string, client.MetricsQuery) (*client.Metrics, error) {
			return m, nil
		},
	}
}

func sampleMetrics() *client.Metrics {
	return &client.Metrics{
		Series: []client.MetricSeries{
			{
				Name: "cpuUsagePercentage",
				Unit: "percent",
				Samples: []client.MetricSample{
					{Timestamp: "2026-06-01T12:00:00Z", Value: 0.12},
					{Timestamp: "2026-06-01T12:05:00Z", Value: 0.18},
				},
			},
			{
				Name:  "memoryUsageBytes",
				Unit:  "furlongsPerFortnight", // an unknown future unit must pass through untouched
				Quota: new(float64(512)),
				Samples: []client.MetricSample{
					{Timestamp: "2026-06-01T12:00:00Z", Value: 128},
					{Timestamp: "2026-06-01T12:05:00Z", Value: 156},
				},
			},
		},
	}
}

func TestMetrics_DefaultQuery(t *testing.T) {
	var (
		got   client.MetricsQuery
		gotID string
	)

	before := time.Now()
	mc := &clienttest.Fake{
		MetricsFunc: func(_ context.Context, id string, q client.MetricsQuery) (*client.Metrics, error) {
			gotID, got = id, q
			return sampleMetrics(), nil
		},
	}
	f, _ := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_123"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d, want %d", code, cmdutil.ExitOK)
	}

	after := time.Now()

	if gotID != "app_123" {
		t.Errorf("id = %q, want app_123", gotID)
	}

	if got.Interval != defaultInterval {
		t.Errorf("interval = %q, want %q", got.Interval, defaultInterval)
	}

	if !slices.Equal(got.Metrics, defaultMetrics) {
		t.Errorf("metrics = %v, want %v", got.Metrics, defaultMetrics)
	}

	// end is about now, and start is about one hour before now.
	if got.End.Before(before) || got.End.After(after) {
		t.Errorf("end %v not within [%v,%v]", got.End, before, after)
	}

	if d := got.End.Sub(got.Start); d < defaultSince-time.Second || d > defaultSince+time.Second {
		t.Errorf("window = %v, want ~%v", d, defaultSince)
	}
}

func TestMetrics_SinceAndInterval(t *testing.T) {
	var got client.MetricsQuery

	mc := &clienttest.Fake{
		MetricsFunc: func(_ context.Context, _ string, q client.MetricsQuery) (*client.Metrics, error) {
			got = q
			return sampleMetrics(), nil
		},
	}
	f, _ := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_1", "--since", "6h", "--interval", "1m"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	if got.Interval != "1m" {
		t.Errorf("interval = %q, want 1m", got.Interval)
	}

	if d := got.End.Sub(got.Start); d < 6*time.Hour-time.Second || d > 6*time.Hour+time.Second {
		t.Errorf("window = %v, want ~6h", d)
	}
}

func TestMetrics_CustomMetrics(t *testing.T) {
	var got client.MetricsQuery

	mc := &clienttest.Fake{
		MetricsFunc: func(_ context.Context, _ string, q client.MetricsQuery) (*client.Metrics, error) {
			got = q
			return &client.Metrics{}, nil
		},
	}
	f, _ := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_1", "--metrics", " cpu , latency ,cpu, "); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	// The names are trimmed and de-duplicated, and the order stays.
	if want := []string{"cpu", "latency"}; !slices.Equal(got.Metrics, want) {
		t.Errorf("metrics = %v, want %v", got.Metrics, want)
	}
}

func TestMetrics_TableOutput(t *testing.T) {
	mc := staticMetrics(sampleMetrics())
	f, out := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_1"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	s := out.String()
	for _, want := range []string{"TIMESTAMP", "CPU_USAGE_PERCENTAGE", "MEMORY_USAGE_BYTES", "0.12", "128", "156"} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing %q\n--- output ---\n%s", want, s)
		}
	}

	// The series share one grid, so each row carries one bucket.
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d\n--- output ---\n%s", len(lines), s)
	}

	if !strings.Contains(lines[1], "0.12") || !strings.Contains(lines[1], "128") {
		t.Errorf("first row = %q, want the 12:00 bucket of both series", lines[1])
	}

	// The memory value 128 must render without a trailing .0
	if strings.Contains(s, "128.0") {
		t.Errorf("expected whole float trimmed, got %q", s)
	}
}

func TestMetrics_QuietOutput(t *testing.T) {
	mc := staticMetrics(sampleMetrics())
	f, out := testFactory(t, mc, output.FormatQuiet)

	if _, code := run(t, f, "app_1"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	// A metrics response has no id, so quiet reports the series names.
	if want := "cpuUsagePercentage\nmemoryUsageBytes\n"; out.String() != want {
		t.Errorf("quiet output = %q, want %q", out.String(), want)
	}
}

func TestMetrics_JSONOutput(t *testing.T) {
	mc := staticMetrics(sampleMetrics())
	f, out := testFactory(t, mc, output.FormatJSON)

	if _, code := run(t, f, "app_1"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	var decoded client.Metrics
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid Metrics JSON: %v\n%s", err, out.String())
	}

	if len(decoded.Series) != 2 {
		t.Fatalf("series = %d, want 2", len(decoded.Series))
	}

	if len(decoded.Series[0].Samples) != 2 {
		t.Errorf("samples = %d, want 2", len(decoded.Series[0].Samples))
	}

	if decoded.Series[0].Unit != "percent" || decoded.Series[1].Unit != "furlongsPerFortnight" {
		t.Errorf("units = %q/%q, want percent and the unknown unit preserved", decoded.Series[0].Unit, decoded.Series[1].Unit)
	}

	if decoded.Series[1].Quota == nil {
		t.Errorf("quota missing from JSON: %+v", decoded.Series[1])
	}
}

func TestMetrics_EmptySamples(t *testing.T) {
	mc := staticMetrics(&client.Metrics{})
	f, out := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_1"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	// The header still prints, but there are no data rows.
	if !strings.Contains(out.String(), "TIMESTAMP") {
		t.Errorf("expected header, got %q", out.String())
	}
}

func TestMetrics_NilMetricsResponse(t *testing.T) {
	mc := staticMetrics(nil)
	f, _ := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_1"); code != cmdutil.ExitOK {
		t.Fatalf("nil metrics should not error, exit = %d", code)
	}
}

func TestMetrics_InvalidSince(t *testing.T) {
	mc := &clienttest.Fake{}
	f, _ := testFactory(t, mc, output.FormatTable)

	msg, code := run(t, f, "app_1", "--since", "0")
	if code != cmdutil.ExitError {
		t.Fatalf("exit = %d, want %d", code, cmdutil.ExitError)
	}

	if !strings.Contains(msg, "since") {
		t.Errorf("msg = %q, want mention of since", msg)
	}
}

func TestMetrics_InvalidInterval(t *testing.T) {
	for _, bad := range []string{"5x", "m", "5", "-5m", "5 m", ""} {
		t.Run(bad, func(t *testing.T) {
			mc := &clienttest.Fake{}
			f, _ := testFactory(t, mc, output.FormatTable)

			msg, code := run(t, f, "app_1", "--interval", bad)
			if code != cmdutil.ExitError {
				t.Fatalf("exit = %d, want %d", code, cmdutil.ExitError)
			}

			if !strings.Contains(msg, "interval") {
				t.Errorf("msg = %q, want mention of interval", msg)
			}
		})
	}
}

func TestMetrics_ValidIntervals(t *testing.T) {
	for _, good := range []string{"30s", "5m", "1h", "1d"} {
		t.Run(good, func(t *testing.T) {
			mc := staticMetrics(sampleMetrics())
			f, _ := testFactory(t, mc, output.FormatTable)

			if msg, code := run(t, f, "app_1", "--interval", good); code != cmdutil.ExitOK {
				t.Fatalf("exit = %d (%q), want 0", code, msg)
			}
		})
	}
}

// TestMetrics_HumanizedTableWithQuota pins the table polish: byte-unit values
// humanize to binary units with one decimal, a rate series gets the "/s"
// suffix, a quota-bearing series carries its LIMIT in the header, and
// timestamps render in UTC with a Z suffix.
func TestMetrics_HumanizedTableWithQuota(t *testing.T) {
	mc := staticMetrics(&client.Metrics{
		Series: []client.MetricSeries{
			{
				Name:  "memoryUsageBytes",
				Unit:  "bytes",
				Quota: new(float64(1073741824)),
				Samples: []client.MetricSample{
					{Timestamp: "2026-06-01T12:00:00.000Z", Value: 536870912},
				},
			},
			{
				Name: "networkEgressBytesPerSecond",
				Unit: "bytesPerSecond",
				Samples: []client.MetricSample{
					{Timestamp: "2026-06-01T12:00:00.000Z", Value: 524288},
				},
			},
		},
	})
	f, out := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_1"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	s := out.String()
	for _, want := range []string{"(LIMIT 1.0 GiB)", "512.0 MiB", "512.0 KiB/s", "2026-06-01T12:00:00Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing %q\n--- output ---\n%s", want, s)
		}
	}
}

func TestFormatMetricValue(t *testing.T) {
	tests := []struct {
		val  float64
		unit string
		want string
	}{
		{536870912, "bytes", "512.0 MiB"},
		{524288, "bytesPerSecond", "512.0 KiB/s"},
		{-536870912, "bytes", "-512.0 MiB"}, // a delta scales by magnitude and keeps its sign
		{1125899906842624, "bytes", "1.0 PiB"},
		{1152921504606846976, "bytes", "1024.0 PiB"}, // beyond the largest unit it stays in PiB
		{12.5, "percent", "12.5%"},
		{512, "mebibytes", "512 MiB"},
		{512, "furlongsPerFortnight", "512"}, // an unknown unit passes through untouched
	}

	for _, tt := range tests {
		if got := formatMetricValue(tt.val, tt.unit); got != tt.want {
			t.Errorf("formatMetricValue(%v, %q) = %q, want %q", tt.val, tt.unit, got, tt.want)
		}
	}
}

func TestMetrics_NoQuotaMeansNoLimitColumn(t *testing.T) {
	mc := staticMetrics(&client.Metrics{
		Series: []client.MetricSeries{
			{
				Name:    "cpuUsagePercentage",
				Unit:    "percent",
				Samples: []client.MetricSample{{Timestamp: "2026-06-01T12:00:00Z", Value: 12.5}},
			},
		},
	})
	f, out := testFactory(t, mc, output.FormatTable)

	if _, code := run(t, f, "app_1"); code != cmdutil.ExitOK {
		t.Fatalf("exit = %d", code)
	}

	if strings.Contains(out.String(), "LIMIT") {
		t.Errorf("LIMIT must appear only with a quota\n--- output ---\n%s", out.String())
	}
}

func TestMetrics_MissingAppIDNamesTheToken(t *testing.T) {
	f, _ := testFactory(t, &clienttest.Fake{}, output.FormatTable)

	msg, code := run(t, f)
	if code != cmdutil.ExitError {
		t.Fatalf("exit = %d, want %d", code, cmdutil.ExitError)
	}

	if msg != "requires an application id (e.g. app_123)" {
		t.Errorf("msg = %q, want the named-token message", msg)
	}
}

func TestRows_GridComesFromLongestSeries(t *testing.T) {
	// The second series is longer than the first: the extra bucket must render
	// as a row, with "-" for the series that lacks it.
	v := newView(&client.Metrics{
		Series: []client.MetricSeries{
			{
				Name: "cpuUsagePercentage",
				Samples: []client.MetricSample{
					{Timestamp: "2026-06-01T12:00:00Z", Value: 0.12},
				},
			},
			{
				Name: "memoryUsageBytes",
				Samples: []client.MetricSample{
					{Timestamp: "2026-06-01T12:00:00Z", Value: 128},
					{Timestamp: "2026-06-01T12:05:00Z", Value: 156},
				},
			},
		},
	})

	rows := v.Rows()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (grid from the longest series)", len(rows))
	}

	if rows[1][1] != "-" {
		t.Errorf("missing cpu sample = %q, want -", rows[1][1])
	}

	if rows[1][2] != "156" {
		t.Errorf("memory sample = %q, want 156", rows[1][2])
	}
}

func TestMetrics_ErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		apiErr   *client.APIError
		wantCode int
		wantMsg  string
	}{
		{
			name:     "401 unauthorized",
			apiErr:   &client.APIError{Status: http.StatusUnauthorized, Code: "auth.unauthorized"},
			wantCode: cmdutil.ExitAuth,
			wantMsg:  "Not logged in",
		},
		{
			name:     "403 forbidden points at the API Manager",
			apiErr:   &client.APIError{Status: http.StatusForbidden, Code: "application.forbidden"},
			wantCode: cmdutil.ExitAuth,
			wantMsg:  "lacks a scope this command needs",
		},
		{
			name:     "429 honors retry-after",
			apiErr:   &client.APIError{Status: http.StatusTooManyRequests, RetryAfter: 30 * time.Second},
			wantCode: cmdutil.ExitError,
			wantMsg:  "Retry after 30s",
		},
		{
			name:     "500 shows operation id",
			apiErr:   &client.APIError{Status: http.StatusInternalServerError, Detail: "boom", OperationID: "op_42"},
			wantCode: cmdutil.ExitError,
			wantMsg:  "op_42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &clienttest.Fake{
				MetricsFunc: func(_ context.Context, _ string, _ client.MetricsQuery) (*client.Metrics, error) {
					return nil, tt.apiErr
				},
			}

			f, _ := testFactory(t, mc, output.FormatTable)

			msg, code := run(t, f, "app_1")
			if code != tt.wantCode {
				t.Errorf("code = %d, want %d", code, tt.wantCode)
			}

			if !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("msg = %q, want contains %q", msg, tt.wantMsg)
			}
		})
	}
}

func TestMetrics_ClientConstructionError(t *testing.T) {
	io, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: io,
		Client:    func() (client.Client, error) { return nil, errors.New("no credentials") },
		Output:    func() string { return output.FormatTable },
	}

	msg, code := run(t, f, "app_1")
	if code != cmdutil.ExitError {
		t.Errorf("code = %d, want %d", code, cmdutil.ExitError)
	}

	if !strings.Contains(msg, "no credentials") {
		t.Errorf("msg = %q", msg)
	}
}

func TestParseMetrics(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", defaultMetrics},
		{"   ", defaultMetrics},
		{"cpuUsagePercentage", []string{"cpuUsagePercentage"}},
		{"cpuUsagePercentage,memoryUsageBytes", []string{"cpuUsagePercentage", "memoryUsageBytes"}},
		{" cpuUsagePercentage , memoryUsageBytes ", []string{"cpuUsagePercentage", "memoryUsageBytes"}},
		{"cpuUsagePercentage,cpuUsagePercentage,memoryUsageBytes", []string{"cpuUsagePercentage", "memoryUsageBytes"}},
		{",,", defaultMetrics},
	}

	for _, tt := range tests {
		if got := parseMetrics(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("parseMetrics(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{128.0, "128"},
		{0.12, "0.12"},
		{1.5, "1.5"},
		{0.123456, "0.1235"}, // at most four decimals
		{0, "0"},
	}

	for _, tt := range tests {
		if got := formatValue(tt.in); got != tt.want {
			t.Errorf("formatValue(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestColumnName(t *testing.T) {
	for in, want := range map[string]string{
		"memoryUsageBytes":               "MEMORY_USAGE_BYTES",
		"cpuUsagePercentage":             "CPU_USAGE_PERCENTAGE",
		"persistentStorageUsedMebibytes": "PERSISTENT_STORAGE_USED_MEBIBYTES",
	} {
		if got := columnName(in); got != want {
			t.Errorf("columnName(%q) = %q, want %q", in, got, want)
		}
	}
}
