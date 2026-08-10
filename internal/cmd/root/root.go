// Package root assembles the top-level `hyperlift` command and its subtree.
package root

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/cmd/apps"
	"github.com/nccloud/hyperlift-cli/internal/cmd/auth"
	"github.com/nccloud/hyperlift-cli/internal/cmd/env"
	"github.com/nccloud/hyperlift-cli/internal/cmd/logs"
	"github.com/nccloud/hyperlift-cli/internal/cmd/metrics"
	"github.com/nccloud/hyperlift-cli/internal/cmd/update"
	"github.com/nccloud/hyperlift-cli/internal/cmd/version"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
)

// Command group ids that organize the help output.
const (
	groupCore   = "core"
	groupConfig = "config"
)

// NewCmdRoot builds the root command and registers every subcommand.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	// Run every persistent hook in the chain, not only the nearest one. The
	// update nag hangs off the root's hooks; without this, the first subcommand
	// that defines its own PersistentPreRun would silently disable the nag for
	// its subtree.
	cobra.EnableTraverseRunHooks = true

	// The "new version available" notice is best effort and never delays the
	// command. update.StartNotify holds the rules that silence it.
	var flushUpdateNotice func()

	cmd := &cobra.Command{
		Use:   "hyperlift <command> <subcommand> [flags]",
		Short: "Manage Hyperlift container applications",
		Long: "hyperlift is the command-line interface for Hyperlift container\n" +
			"applications on the Spaceship platform.\n\n" +
			"Environment variables:\n" +
			"  HYPERLIFT_BASE_URL         Override the API base URL.\n" +
			"  HYPERLIFT_API_KEY          Override the stored API key.\n" +
			"  HYPERLIFT_API_SECRET       Override the stored API secret.\n" +
			"  HYPERLIFT_NO_UPDATE_CHECK  Disable the background update check.\n" +
			"  NO_COLOR                   Disable colored output.",
		Version:       version.String(),
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			// Rejected here, not with MarkFlagsMutuallyExclusive: cobra's
			// flag-group message is not user-friendly, and its validation does
			// not pass through SetFlagErrorFunc.
			pf := c.Root().PersistentFlags()
			jsonOn, _ := pf.GetBool("json")

			quietOn, _ := pf.GetBool("quiet")
			if jsonOn && quietOn {
				return errors.New("--json and --quiet cannot be combined")
			}

			// The debug trace and the spinner both write to stderr, so the
			// spinner stands down rather than animate across the trace.
			debugOn, _ := pf.GetBool("debug")
			f.IOStreams.SetNeverSpin(debugOn)

			flushUpdateNotice = update.StartNotify(c.Context(), update.NotifyConfig{
				IO:             f.IOStreams,
				Output:         f.Output,
				CurrentCommand: c.Name(),
			})

			return nil
		},
		PersistentPostRun: func(*cobra.Command, []string) {
			if flushUpdateNotice != nil {
				flushUpdateNotice()
			}
		},
	}

	cmd.SetVersionTemplate("hyperlift version {{.Version}}\n")

	// Persistent flags that every subcommand shares.
	pflags := cmd.PersistentFlags()
	pflags.Bool("debug", false, "Print a redacted one-line trace per HTTP request to stderr")
	// Resolve --debug at call time, from the inherited flags of the command that
	// runs.
	f.Debug = func() bool {
		b, _ := pflags.GetBool("debug")
		return b
	}

	// Register --json and --quiet. This also rewires f.Output.
	cmdutil.AddOutputFlags(cmd, f)

	// Help groups.
	cmd.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core commands:"},
		&cobra.Group{ID: groupConfig, Title: "Configuration commands:"},
	)

	// Core commands.
	addInGroup(cmd, groupCore,
		apps.NewCmdApps(f),
		env.NewCmdEnv(f),
		metrics.NewCmdMetrics(f),
		logs.NewCmdLogs(f),
	)

	// Configuration and meta commands.
	addInGroup(cmd, groupConfig,
		auth.NewCmdAuth(f),
		version.NewCmdVersion(f),
		update.NewCmdUpdate(f),
	)

	// cobra adds completion by itself. Put it in the configuration group.
	for _, c := range cmd.Commands() {
		if c.Name() == "completion" || c.Name() == "help" {
			c.GroupID = groupConfig
		}
	}

	return cmd
}

func addInGroup(parent *cobra.Command, groupID string, cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = groupID
		parent.AddCommand(c)
	}
}
