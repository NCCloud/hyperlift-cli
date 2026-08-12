// Package logs holds the logs command, which reads bounded pages from the
// cursor-paged logs endpoint. --follow keeps polling, because the External API
// has no streaming. The cursor guarantees no gaps or duplicates between pages.
package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// logsAPI is the client surface this package uses.
type logsAPI interface {
	Logs(ctx context.Context, req client.LogsRequest) (*client.LogsPage, error)
	BuildLogs(ctx context.Context, req client.LogsRequest) (*client.LogsPage, error)
}

// followPollInterval is the pause between polls after the log is caught up. It
// is a package variable so tests can shorten it.
var followPollInterval = 3 * time.Second

const followMaxBackoff = 30 * time.Second

const logsPageTake = 100

// logsOptions holds the resolved inputs of one logs run.
type logsOptions struct {
	appID        string
	follow       bool
	build        bool
	noTimestamps bool
}

// NewCmdLogs returns the `logs` command.
func NewCmdLogs(f *cmdutil.Factory) *cobra.Command {
	opts := &logsOptions{}

	cmd := &cobra.Command{
		Use:   "logs <app-id>",
		Short: "Print logs for an application",
		Long: "Print runtime logs for an application.\n\n" +
			"Without --follow, the command prints the log lines that are available\n" +
			"now, then exits. With --follow, it polls for new lines every few seconds.\n" +
			"It continues until the log finishes, or until you interrupt it. Use\n" +
			"--build to read the build logs instead of the runtime logs.\n\n" +
			"Each line starts with the UTC timestamp the API reports, then the\n" +
			"message. Pass --no-timestamps for the bare messages.\n\n" +
			"The --quiet flag has no effect on this command. The output is already\n" +
			"plain log lines.",
		Args: cmdutil.AppIDArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.appID = args[0]
			return runLogs(cmd.Context(), f, opts)
		},
	}

	flags := cmd.Flags()
	flags.BoolVarP(&opts.follow, "follow", "f", false, "Keep polling and print new log lines as they arrive")
	flags.BoolVar(&opts.build, "build", false, "Read build logs instead of runtime logs")
	flags.BoolVar(&opts.noTimestamps, "no-timestamps", false, "Print log lines without the timestamp prefix")

	return cmd
}

func runLogs(ctx context.Context, f *cmdutil.Factory, opts *logsOptions) error {
	c, err := f.Client()
	if err != nil {
		return err
	}

	var api logsAPI = c

	asJSON := f.Output() == output.FormatJSON
	req := client.LogsRequest{
		ID:   opts.appID,
		Take: logsPageTake,
	}

	fetch := api.Logs
	if opts.build {
		fetch = api.BuildLogs
	}

	backoff := followPollInterval

	for {
		page, err := fetch(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if !opts.follow || fatalLogsErr(err) {
				return err
			}

			// A transient error during --follow can be a 5xx, a network blip,
			// or exhausted 429 retries. Keep the session alive: warn, back off,
			// and retry with the same cursor.
			_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "warning: %v (retrying)\n", err)

			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}

			backoff = min(backoff*2, followMaxBackoff)

			continue
		}

		backoff = followPollInterval

		for _, entry := range page.Items {
			if err := printEntry(f, entry, asJSON, !opts.noTimestamps); err != nil {
				return err
			}
		}

		if page.Cursor != "" {
			req.Cursor = page.Cursor
		}

		if page.Finished {
			// No more lines: the build completed or the application stopped.
			return nil
		}

		if len(page.Items) == logsPageTake {
			// A full page means the reader is still catching up, so read the
			// next page at once. Pausing only on a partial page stops a steady
			// producer from causing a tight request loop.
			continue
		}

		if !opts.follow {
			return nil
		}

		if !sleepCtx(ctx, followPollInterval) {
			// The user interrupted, which is the normal way out of --follow.
			return ctx.Err()
		}
	}
}

// fatalLogsErr reports whether a --follow session must stop instead of retry. A
// client error does not heal if the same request is sent again. In the 4xx
// family, only 408 (timeout) and 429 (rate limit) are worth a retry.
func fatalLogsErr(err error) bool {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return false
		}

		return apiErr.Status >= 400 && apiErr.Status < 500
	}

	return false
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// printEntry writes one log line to stdout: a compact NDJSON object in JSON
// mode, otherwise the wire's UTC timestamp, one space, and the message. Without
// withTimestamps, or without a wire timestamp, the line is the bare message.
func printEntry(f *cmdutil.Factory, entry client.LogEntry, asJSON, withTimestamps bool) error {
	out := f.IOStreams.Out

	if asJSON {
		b, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("encode log line: %w", err)
		}

		_, err = fmt.Fprintln(out, string(b))

		return err
	}

	line := entry.Message
	if withTimestamps && entry.Timestamp != "" {
		line = entry.Timestamp + " " + entry.Message
	}

	_, err := fmt.Fprintln(out, line)

	return err
}
