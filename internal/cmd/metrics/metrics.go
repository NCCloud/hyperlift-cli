// Package metrics holds the `metrics` command. It reads and renders the
// time-series metrics of an application over a recent window.
package metrics

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// metricsAPI is the client surface this package uses.
type metricsAPI interface {
	Metrics(ctx context.Context, id string, q client.MetricsQuery) (*client.Metrics, error)
}

// defaultMetrics is the metric set to request when --metrics is absent. The
// values are the contract metric names; see GetApplicationMetricsQueryParams.
var defaultMetrics = []string{"cpuUsagePercentage", "memoryUsageBytes"}

const (
	defaultSince    = time.Hour
	defaultInterval = "5m"
)

// intervalRe is the --interval shape the server enforces, checked client-side
// for a clear error instead of a 422.
var intervalRe = regexp.MustCompile(`^\d+(s|m|h|d)$`)

type metricsOptions struct {
	id       string
	since    time.Duration
	interval string
	metrics  []string
}

// NewCmdMetrics returns the `metrics` command.
func NewCmdMetrics(f *cmdutil.Factory) *cobra.Command {
	opts := &metricsOptions{
		since:    defaultSince,
		interval: defaultInterval,
	}

	var metricsCSV string

	cmd := &cobra.Command{
		Use:   "metrics <app-id>",
		Short: "Show metrics for an application",
		Long: "Show recent time-series metrics for an application.\n\n" +
			"The window ends at the current time and goes back by --since. By\n" +
			"default, the command reports cpuUsagePercentage and memoryUsageBytes\n" +
			"at a 5m interval.\n\n" +
			"Available metrics: memoryUsageBytes, cpuUsagePercentage,\n" +
			"networkReceiveRateBytes, networkTransmitRateBytes,\n" +
			"ephemeralStorageUsedMebibytes, persistentStorageUsedMebibytes.\n\n" +
			"With --quiet, the command prints the returned series names.",
		Args: cmdutil.AppIDArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.id = args[0]
			opts.metrics = parseMetrics(metricsCSV)

			return runMetrics(cmd.Context(), f, opts)
		},
	}

	flags := cmd.Flags()
	flags.DurationVar(&opts.since, "since", opts.since, "How far back to query (e.g. 30m, 6h, 24h)")
	flags.StringVar(&opts.interval, "interval", opts.interval, "Sampling interval (e.g. 1m, 5m, 1h)")
	flags.StringVar(&metricsCSV, "metrics", "", "Comma-separated metrics to fetch (default cpuUsagePercentage,memoryUsageBytes)")

	return cmd
}

// parseMetrics splits the --metrics CSV into a list, trimmed and de-duplicated,
// keeping the order. Empty input gives the default set. Unknown names pass
// through on purpose, so the CLI keeps working when the contract gains metrics.
func parseMetrics(csv string) []string {
	parts := strings.Split(csv, ",")
	seen := make(map[string]bool, len(parts))

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}

		seen[name] = true
		out = append(out, name)
	}

	if len(out) == 0 {
		return append([]string(nil), defaultMetrics...)
	}

	return out
}

func runMetrics(ctx context.Context, f *cmdutil.Factory, opts *metricsOptions) error {
	if opts.since <= 0 {
		return fmt.Errorf("--since must be positive")
	}

	if !intervalRe.MatchString(opts.interval) {
		return fmt.Errorf("--interval must be a number followed by s, m, h or d (e.g. 5m), got %q", opts.interval)
	}

	c, err := f.Client()
	if err != nil {
		return err
	}

	var api metricsAPI = c

	end := time.Now()
	start := end.Add(-opts.since)
	q := client.MetricsQuery{
		Start:    start,
		End:      end,
		Interval: opts.interval,
		Metrics:  opts.metrics,
	}

	io := f.IOStreams
	io.StartSpinner("Fetching metrics")

	m, err := api.Metrics(ctx, opts.id, q)

	io.StopSpinner()

	if err != nil {
		return err
	}

	if m == nil {
		m = &client.Metrics{}
	}

	return output.Render(io, newView(m), f.Output())
}
