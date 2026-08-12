package cmdutil

import (
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/config"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// AddOutputFlags registers the persistent --json and --quiet flags on cmd, and
// rewires f.Output to resolve them. The order is json, quiet, the config
// default, then table. Call it on the root command, so every subcommand
// inherits the flags.
func AddOutputFlags(cmd *cobra.Command, f *Factory) {
	flags := cmd.PersistentFlags()
	flags.Bool("json", false, "Output as JSON")
	flags.Bool("quiet", false, "Output only the primary identifier per line")

	var warnBadDefault sync.Once

	f.Output = func() string {
		// Resolve from the inherited flags of the command that runs.
		if jsonOn, err := flags.GetBool("json"); err == nil && jsonOn {
			return output.FormatJSON
		}

		if quietOn, err := flags.GetBool("quiet"); err == nil && quietOn {
			return output.FormatQuiet
		}

		cfg, err := f.Config()
		if err == nil {
			switch o := cfg.DefaultOutput(); o {
			case "":
				// Fall through to table.
			case output.FormatTable, output.FormatJSON, output.FormatQuiet:
				return o
			default:
				// The value is only settable by hand-editing the config file,
				// so name the file: the render-time error would not.
				warnBadDefault.Do(func() {
					p, perr := config.Path()
					if perr != nil {
						p = "the config file"
					}

					_, _ = fmt.Fprintf(f.IOStreams.ErrOut,
						"Warning: unknown default_output %q in %s; using table\n", o, p)
				})
			}
		}

		return output.FormatTable
	}
}
