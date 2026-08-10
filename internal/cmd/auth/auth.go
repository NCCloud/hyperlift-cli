// Package auth holds the login, logout and whoami commands.
package auth

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
)

// authAPI is the client surface this package uses. It verifies credentials only.
type authAPI interface {
	Probe(ctx context.Context) error
}

// newClient is a package variable so login can validate the credentials the
// user just supplied instead of the ones the Factory stores, and so tests can
// replace it with a double.
var newClient = client.New

// NewCmdAuth returns the `auth` parent command of login, logout and whoami.
func NewCmdAuth(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth <command>",
		Short: "Authenticate with the Hyperlift API",
		Long:  "Manage the credentials that the CLI uses for the Hyperlift (Spaceship) API.",
	}

	cmd.AddCommand(
		newCmdLogin(f),
		newCmdLogout(f),
		newCmdWhoami(f),
	)

	return cmd
}
