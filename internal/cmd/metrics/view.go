package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// view adapts a *client.Metrics for rendering. It implements output.Tabular
// with a timestamp column and one column per returned series in request order,
// plus output.IDer and json.Marshaler.
type view struct {
	m *client.Metrics
}

func newView(m *client.Metrics) *view {
	return &view{m: m}
}

// MarshalJSON writes the metrics payload itself, so --json shows the wire shape
// — the series with an optional per-series quota — not the table internals.
func (v *view) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.m)
}

// Headers returns the table header: TIMESTAMP plus one column per series. A
// series with a plan quota carries it in the header — a quota is constant, so
// a column would repeat it on every row.
func (v *view) Headers() []string {
	h := make([]string, 0, len(v.m.Series)+1)
	h = append(h, "TIMESTAMP")

	for i := range v.m.Series {
		s := &v.m.Series[i]

		name := columnName(s.Name)
		if s.Quota != nil {
			name += " (LIMIT " + formatMetricValue(*s.Quota, s.Unit) + ")"
		}

		h = append(h, name)
	}

	return h
}

// Rows returns one row per timestamp bucket. Each row holds each series' value,
// in column order. The contract is dense: all series share one timestamp grid,
// so sample i is the same bucket in every series. The grid comes from the
// longest series, so a short first series cannot truncate a longer one; a
// missing or mismatched sample renders as "-".
func (v *view) Rows() [][]string {
	if len(v.m.Series) == 0 {
		return nil
	}

	longest := 0
	for i := range v.m.Series {
		if len(v.m.Series[i].Samples) > len(v.m.Series[longest].Samples) {
			longest = i
		}
	}

	grid := v.m.Series[longest].Samples

	rows := make([][]string, 0, len(grid))
	for i := range grid {
		row := make([]string, 0, len(v.m.Series)+1)
		row = append(row, formatTimestamp(grid[i].Timestamp))

		for j := range v.m.Series {
			s := &v.m.Series[j]
			if i >= len(s.Samples) || s.Samples[i].Timestamp != grid[i].Timestamp {
				row = append(row, "-")
			} else {
				row = append(row, formatMetricValue(s.Samples[i].Value, s.Unit))
			}
		}

		rows = append(rows, row)
	}

	return rows
}

// IDs implements output.IDer. A metrics response has no id of its own, so the
// quiet form lists the returned series names, the metrics reported.
func (v *view) IDs() []string {
	names := make([]string, 0, len(v.m.Series))
	for i := range v.m.Series {
		names = append(names, v.m.Series[i].Name)
	}

	return names
}

// columnName turns a camelCase metric name into an upper-snake column label,
// e.g. cpuUsagePercentage becomes CPU_USAGE_PERCENTAGE.
func columnName(metric string) string {
	var b strings.Builder

	for i, r := range metric {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}

		b.WriteRune(unicode.ToUpper(r))
	}

	return b.String()
}

// formatTimestamp renders a sample timestamp. It shows a known ISO-8601 or
// RFC3339 string in UTC with a Z suffix, like apps get and --json, and passes
// anything else through unchanged. An empty timestamp becomes "-".
func formatTimestamp(ts string) string {
	if ts == "" {
		return "-"
	}

	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}

	return ts
}

// formatMetricValue renders one value in the series' unit. The byte units
// convert to the largest binary unit; percent and mebibytes keep their value
// and gain a label. An unknown unit passes through as a plain number on
// purpose, for forward compatibility.
func formatMetricValue(val float64, unit string) string {
	switch unit {
	case "bytes":
		return formatBytes(val, "")
	case "bytesPerSecond":
		return formatBytes(val, "/s")
	case "percent":
		return formatValue(val) + "%"
	case "mebibytes":
		return formatValue(val) + " MiB"
	}

	return formatValue(val)
}

// formatBytes renders a byte count in the largest binary unit, one decimal,
// with suffix after the unit, e.g. "/s" for a rate. A negative value scales by
// magnitude and keeps its sign.
func formatBytes(v float64, suffix string) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	mag := math.Abs(v)

	i := 0
	for mag >= 1024 && i < len(units)-1 {
		mag /= 1024
		i++
	}

	if v < 0 {
		mag = -mag
	}

	return fmt.Sprintf("%.1f %s%s", mag, units[i], suffix)
}

// formatValue renders a metric value compactly. A whole value has no trailing
// ".0". A fractional value keeps at most four decimals.
func formatValue(val float64) string {
	if val == float64(int64(val)) {
		return fmt.Sprintf("%d", int64(val))
	}

	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", val), "0"), ".")
}
