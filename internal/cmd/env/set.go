package env

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "set <app-id> KEY=VALUE [KEY=VALUE...]",
		Short: "Set one or more environment variables",
		Long: "Set one or more environment variables on an application.\n\n" +
			"This is a read-modify-write. The command reads the current variables,\n" +
			"merges in the given KEY=VALUE pairs, then writes the complete map back.\n" +
			"A pair overwrites a key that already exists. Use KEY= to set an empty\n" +
			"value.\n\n" +
			"With --quiet, the command prints only the variable keys.",
		Example: "  hyperlift env set app_123 LOG_LEVEL=debug FEATURE_X=1",
		Args:    cmdutil.AppIDPlusArgs("KEY=VALUE pair"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSet(cmd.Context(), f, args[0], args[1:])
		},
	}
}

func runSet(ctx context.Context, f *cmdutil.Factory, id string, pairs []string) error {
	updates, err := parsePairs(pairs)
	if err != nil {
		return err
	}

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

	for k, v := range updates {
		env[k] = v
	}

	if err := api.EnvUpdate(ctx, id, env); err != nil {
		return err
	}

	notifyRestart(f)

	updated := refreshFromServer(ctx, f, api, id, env)

	return output.Render(f.IOStreams, envView(updated), f.Output())
}

// parsePairs checks the KEY=VALUE arguments and decodes them into a map. It
// normalizes each key the way the server stores it, in argument order, so
// aliases like `foo-bar=one FOO_BAR=two` always resolve to the last one. An
// empty value (KEY=) is valid. A missing '=' or an empty key is an error.
func parsePairs(pairs []string) (map[string]string, error) {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		key, val, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("invalid argument %q: expected KEY=VALUE", p)
		}

		if normalizeKey(key) == "" {
			return nil, fmt.Errorf("invalid argument %q: empty key", p)
		}

		out[normalizeKey(key)] = val
	}

	return out, nil
}
