// Package cli is the top-level entrypoint. It builds the Factory and the root
// command, runs it, and maps an error to a process exit code.
package cli

import (
	"context"
	"errors"

	"github.com/nccloud/hyperlift-cli/internal/cmd/root"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

// Run builds and executes the CLI, returning the process exit code.
func Run(ctx context.Context, io *iostreams.IOStreams, args []string) int {
	f := cmdutil.NewFactory(io)

	rootCmd := root.NewCmdRoot(f)
	rootCmd.SetArgs(args)
	rootCmd.SetIn(io.In)
	rootCmd.SetOut(io.Out)
	rootCmd.SetErr(io.ErrOut)
	// Run prints the errors and picks the exit codes itself.
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return cmdutil.ExitOK
	}

	// cobra returns a context cancellation as the wrapped error.
	if errors.Is(err, context.Canceled) {
		return cmdutil.ExitCancel
	}

	msg, code := cmdutil.FriendlyError(err, f.Debug())
	if msg != "" {
		cmdutil.PrintError(io, msg)
	}

	return code
}
