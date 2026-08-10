package apps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// progressHint follows every async verb except build; %s is the app id.
const progressHint = "Check progress: hyperlift apps get %s  (or pass --wait)"

// pollInterval is the pause between --wait polls. It is a package variable so
// tests can shorten it.
var pollInterval = defaultInterval

// lifecycleSpec parameterizes the shared build, start, stop and restart command.
type lifecycleSpec struct {
	use     string
	short   string
	long    string
	example string
	verb    string // present participle for the spinner, e.g. "Starting"
	done    string // past tense for the --wait success line, e.g. "Started"
	// triggered is the success line without --wait, e.g. "Start requested".
	triggered string
	// hint is the next-step line after triggered, with %s for the app id.
	hint string
	// wait selects the terminal condition that --wait polls toward.
	wait waitTarget
	op   func(api appsAPI, ctx context.Context, id string) (*client.OpRef, error)
}

func newCmdBuild(f *cmdutil.Factory) *cobra.Command {
	return newLifecycleCmd(f, lifecycleSpec{
		use:   "build <app-id>",
		short: "Trigger a build for an application",
		long: "Trigger a build of the application's repository.\n\n" +
			"The command returns as soon as the build is queued. With --wait, it\n" +
			"polls until buildStatus reaches built or failed, and fails on failed.\n" +
			"Follow the build output with `hyperlift logs <app-id> --build --follow`.\n\n" +
			"With --quiet, the command prints only the application id.",
		example:   "  hyperlift apps build app_123\n  hyperlift apps build app_123 --wait --timeout 15m",
		verb:      "Building",
		done:      "Build complete",
		triggered: "Build triggered",
		hint:      "Next: hyperlift logs %s --build --follow  (or pass --wait)",
		wait:      waitBuild,
		op: func(api appsAPI, ctx context.Context, id string) (*client.OpRef, error) {
			return api.AppsBuild(ctx, id)
		},
	})
}

func newCmdStart(f *cmdutil.Factory) *cobra.Command {
	return newLifecycleCmd(f, lifecycleSpec{
		use:   "start <app-id>",
		short: "Start an application",
		long: "Start the application (scale 1).\n\n" +
			"The command returns as soon as the start is requested. With --wait, it\n" +
			"polls until the status reaches running, and fails on failure.\n\n" +
			"With --quiet, the command prints only the application id.",
		example:   "  hyperlift apps start app_123 --wait",
		verb:      "Starting",
		done:      "Started",
		triggered: "Start requested",
		hint:      progressHint,
		wait:      waitStart,
		op: func(api appsAPI, ctx context.Context, id string) (*client.OpRef, error) {
			return api.AppsStart(ctx, id)
		},
	})
}

func newCmdStop(f *cmdutil.Factory) *cobra.Command {
	return newLifecycleCmd(f, lifecycleSpec{
		use:   "stop <app-id>",
		short: "Stop an application",
		long: "Stop the application (scale 0).\n\n" +
			"The command returns as soon as the stop is requested. With --wait, it\n" +
			"polls until the status reaches stopped, and fails on failure.\n\n" +
			"With --quiet, the command prints only the application id.",
		example:   "  hyperlift apps stop app_123 --wait",
		verb:      "Stopping",
		done:      "Stopped",
		triggered: "Stop requested",
		hint:      progressHint,
		wait:      waitStop,
		op: func(api appsAPI, ctx context.Context, id string) (*client.OpRef, error) {
			return api.AppsStop(ctx, id)
		},
	})
}

func newCmdRestart(f *cmdutil.Factory) *cobra.Command {
	return newLifecycleCmd(f, lifecycleSpec{
		use:   "restart <app-id>",
		short: "Restart an application",
		long: "Restart the application.\n\n" +
			"The command returns as soon as the restart is requested. With --wait,\n" +
			"it polls until the status leaves running and returns to it, and fails\n" +
			"on failure.\n\n" +
			"With --quiet, the command prints only the application id.",
		example:   "  hyperlift apps restart app_123 --wait",
		verb:      "Restarting",
		done:      "Restarted",
		triggered: "Restart requested",
		hint:      progressHint,
		wait:      waitRestart,
		op: func(api appsAPI, ctx context.Context, id string) (*client.OpRef, error) {
			return api.AppsRestart(ctx, id)
		},
	})
}

func newLifecycleCmd(f *cmdutil.Factory, spec lifecycleSpec) *cobra.Command {
	var (
		wait    bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:     spec.use,
		Short:   spec.short,
		Long:    spec.long,
		Example: spec.example,
		Args:    cmdutil.AppIDArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("timeout") && !wait {
				return errors.New("--timeout requires --wait")
			}

			id := args[0]

			c, err := f.Client()
			if err != nil {
				return err
			}

			var api appsAPI = c

			f.IOStreams.StartSpinner(spec.verb + " application")
			ref, err := spec.op(api, cmd.Context(), id)
			// The spinner keeps running only into a successful --wait poll.
			if !wait || err != nil {
				f.IOStreams.StopSpinner()
			}

			if err != nil {
				return err
			}

			refID := id
			if ref != nil && ref.ID != "" {
				refID = ref.ID
			}

			if !wait {
				// The --json contract returns the full application object on
				// every path, so refresh it; the mutation gave only an id.
				if f.Output() == output.FormatJSON {
					app, gerr := api.AppsGet(cmd.Context(), id)
					if gerr != nil {
						// The trigger already succeeded, so a failed refresh
						// only degrades the output to the mutation's own {id}.
						cs := f.IOStreams.ColorScheme()
						_, _ = fmt.Fprintln(f.IOStreams.ErrOut,
							cs.Yellow("Warning:")+" "+strings.Fields(spec.use)[0]+" was accepted, but fetching the result failed: "+gerr.Error())

						return output.Render(f.IOStreams, &client.OpRef{ID: refID}, output.FormatJSON)
					}

					return output.Render(f.IOStreams, app, output.FormatJSON)
				}

				return reportOp(f, refID, spec)
			}

			app, werr := waitForOutcome(cmd.Context(), func(ctx context.Context) (*client.Application, error) {
				return api.AppsGet(ctx, id)
			}, spec.wait, waitOptions{Interval: pollInterval, Timeout: timeout})

			f.IOStreams.StopSpinner()

			if werr != nil {
				// Only the wait's own timer earns the friendly rewrite.
				if errors.Is(werr, errWaitTimeout) {
					return fmt.Errorf("timed out after %s; the operation may still be running — check `hyperlift apps get %s`", timeout, id)
				}

				return werr
			}

			if failed := terminalFailure(app, spec.wait); failed != "" {
				msg := fmt.Sprintf("%s did not succeed: %s", strings.ToLower(spec.verb), failed)
				cmdutil.PrintError(f.IOStreams, msg)

				return cmdutil.NewSilentError(errors.New(msg))
			}

			return reportApp(f, app, spec.done)
		},
	}

	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the operation to reach a terminal state")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum time to wait (requires --wait)")

	return cmd
}

// terminalFailure returns a description when the settled state is a failure.
func terminalFailure(app *client.Application, target waitTarget) string {
	if app == nil {
		return ""
	}

	if target == waitBuild {
		if app.BuildStatus == client.BuildStatusFailed {
			return "build failed"
		}

		return ""
	}

	if app.Status == client.StatusFailure {
		return "status=failure"
	}

	return ""
}

// reportOp prints the success result of a triggered operation in quiet or table
// form; the JSON path renders the refreshed application before this runs.
func reportOp(f *cmdutil.Factory, id string, spec lifecycleSpec) error {
	if f.Output() == output.FormatQuiet {
		_, _ = fmt.Fprintln(f.IOStreams.Out, id)
		return nil
	}

	cs := f.IOStreams.ColorScheme()
	_, _ = fmt.Fprintf(f.IOStreams.Out, "%s %s\n", cs.Green("✓"), spec.triggered+" "+id)
	// The next step goes to stderr, so a piped stdout stays clean.
	_, _ = fmt.Fprintf(f.IOStreams.ErrOut, spec.hint+"\n", id)

	return nil
}

// reportApp prints a success result that carries a refreshed application.
func reportApp(f *cmdutil.Factory, app *client.Application, message string) error {
	format := f.Output()
	switch format {
	case output.FormatJSON:
		return output.Render(f.IOStreams, app, format)
	case output.FormatQuiet:
		_, _ = fmt.Fprintln(f.IOStreams.Out, app.ID)
		return nil
	default:
		cs := f.IOStreams.ColorScheme()
		_, _ = fmt.Fprintf(f.IOStreams.Out, "%s %s %s (status=%s, build=%s)\n",
			cs.Green("✓"), message, app.ID, app.Status, app.BuildStatus)

		return nil
	}
}
