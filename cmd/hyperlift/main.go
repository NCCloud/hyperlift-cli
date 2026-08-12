// Command hyperlift is the customer CLI that manages Hyperlift container
// applications through the Spaceship External API.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/nccloud/hyperlift-cli/internal/cli"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

func main() {
	os.Exit(run())
}

// run returns the exit code. os.Exit stays in main, so the deferred signal stop
// runs before the process exits.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Run(ctx, iostreams.System(), os.Args[1:])
}
