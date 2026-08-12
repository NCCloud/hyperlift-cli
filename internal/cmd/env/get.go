package env

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-id>",
		Short: "List environment variables",
		Long: "List the environment variables of an application.\n\n" +
			"The table output shows a KEY column and a VALUE column. With --json,\n" +
			"the command prints the raw map. With --quiet, it prints only the\n" +
			"variable keys.",
		Args: cmdutil.AppIDArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(cmd.Context(), f, args[0])
		},
	}
}

func runGet(ctx context.Context, f *cmdutil.Factory, id string) error {
	c, err := f.Client()
	if err != nil {
		return err
	}

	var api envAPI = c

	env, err := api.EnvGet(ctx, id)
	if err != nil {
		return err
	}

	return output.Render(f.IOStreams, envView(env), f.Output())
}
