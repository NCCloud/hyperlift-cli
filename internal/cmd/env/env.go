// Package env holds the environment-variable commands.
//
// An update replaces the whole map on the wire, so set and unset read the
// current variables, apply the change locally, then PUT the complete map.
package env

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
)

// envAPI is the client surface this package uses.
type envAPI interface {
	EnvGet(ctx context.Context, id string) (map[string]string, error)
	EnvUpdate(ctx context.Context, id string, env map[string]string) error
}

// NewCmdEnv returns the `env` parent command with its subcommands: get, set and
// unset.
func NewCmdEnv(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env <command>",
		Short: "Manage application environment variables",
		Long: "Manage the environment variables of a Hyperlift application.\n\n" +
			"An update replaces the complete map on the wire. The set and unset\n" +
			"commands read the current variables, apply your change, then write the\n" +
			"complete map back.",
	}

	cmd.AddCommand(
		newCmdGet(f),
		newCmdSet(f),
		newCmdUnset(f),
	)

	return cmd
}

// normalizeKey copies the variable-name normalization the server documents for a
// write: trim, upper-case, and turn dashes and spaces into underscores. The same
// rule runs locally, so set and unset address the key the server stores.
func normalizeKey(key string) string {
	key = strings.ToUpper(strings.TrimSpace(key))

	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return '_'
		}

		return r
	}, key)
}

// notifyRestart tells the user a successful env update restarts the
// application. The server rejects further env updates until it runs again.
func notifyRestart(f *cmdutil.Factory) {
	_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "Variables updated; the application is restarting to apply them.")
}

// refreshFromServer re-reads the stored variables after a successful update, so
// the output shows the state the server holds and not the locally edited map.
// The update already succeeded, so a failed refresh only degrades the display:
// it warns and returns local.
func refreshFromServer(ctx context.Context, f *cmdutil.Factory, api envAPI, id string, local map[string]string) map[string]string {
	updated, err := api.EnvGet(ctx, id)
	if err != nil {
		cs := f.IOStreams.ColorScheme()
		_, _ = fmt.Fprintln(f.IOStreams.ErrOut,
			cs.Yellow("Warning:")+" variables were updated, but refreshing their stored form failed: "+err.Error())

		return local
	}

	return updated
}

// envView renders an environment map three ways: a sorted KEY/VALUE table, raw
// JSON of the map itself, and the keys alone for quiet output.
type envView map[string]string

func (v envView) keys() []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func (v envView) Headers() []string { return []string{"KEY", "VALUE"} }

func (v envView) Rows() [][]string {
	rows := make([][]string, 0, len(v))
	for _, k := range v.keys() {
		rows = append(rows, []string{k, v[k]})
	}

	return rows
}

// IDs implements output.IDer. For env, the ids are the variable keys.
func (v envView) IDs() []string { return v.keys() }
