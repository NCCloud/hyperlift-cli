// Package apps holds the application lifecycle commands: list, get, build,
// start, stop and restart.
package apps

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
)

// appsAPI is the client surface this package uses.
type appsAPI interface {
	AppsList(ctx context.Context, take, skip int) ([]client.Application, int, error)
	AppsGet(ctx context.Context, id string) (*client.Application, error)
	AppsBuild(ctx context.Context, id string) (*client.OpRef, error)
	AppsStart(ctx context.Context, id string) (*client.OpRef, error)
	AppsStop(ctx context.Context, id string) (*client.OpRef, error)
	AppsRestart(ctx context.Context, id string) (*client.OpRef, error)
}

// NewCmdApps returns the `apps` parent command.
func NewCmdApps(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "apps <command>",
		Short:   "Manage Hyperlift applications",
		Aliases: []string{"app"},
	}

	cmd.AddCommand(
		newCmdList(f),
		newCmdGet(f),
		newCmdBuild(f),
		newCmdStart(f),
		newCmdStop(f),
		newCmdRestart(f),
	)

	return cmd
}
