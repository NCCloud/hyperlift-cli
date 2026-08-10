package env

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

func newCmdUnset(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "unset <app-id> KEY [KEY...]",
		Short: "Remove one or more environment variables",
		Long: "Remove one or more environment variables from an application.\n\n" +
			"This is a read-modify-write. The command reads the current variables,\n" +
			"removes the named keys, then writes the complete map back. A key that\n" +
			"is not set causes a warning, and the command ignores it.\n\n" +
			"With --quiet, the command prints only the variable keys.",
		Example: "  hyperlift env unset app_123 LOG_LEVEL FEATURE_X",
		Args:    cmdutil.AppIDPlusArgs("KEY"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnset(cmd.Context(), f, args[0], args[1:])
		},
	}
}

func runUnset(ctx context.Context, f *cmdutil.Factory, id string, keys []string) error {
	c, err := f.Client()
	if err != nil {
		return err
	}

	var api envAPI = c

	env, err := api.EnvGet(ctx, id)
	if err != nil {
		return err
	}

	if env == nil {
		env = map[string]string{}
	}

	cs := f.IOStreams.ColorScheme()
	removed := 0

	for _, k := range keys {
		// Match the name form the server stores, so `unset foo-bar` removes the
		// stored FOO_BAR.
		nk := normalizeKey(k)
		if nk == "" {
			_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "%s %q is not a valid variable name\n", cs.Yellow("Warning:"), k)
			continue
		}

		if _, ok := env[nk]; ok {
			delete(env, nk)

			removed++
		} else {
			_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "%s %q is not set\n", cs.Yellow("Warning:"), k)
		}
	}

	// Write only after a real change, to avoid a PUT that does nothing.
	if removed > 0 {
		if err := api.EnvUpdate(ctx, id, env); err != nil {
			return err
		}

		notifyRestart(f)

		env = refreshFromServer(ctx, f, api, id, env)
	}

	return output.Render(f.IOStreams, envView(env), f.Output())
}
