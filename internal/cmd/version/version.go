// Package version holds the version command, which reports the CLI build
// metadata: the version, and with --verbose the commit, the build date and the
// Go runtime.
package version

import (
	"runtime"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/build"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// info is the shape the version command renders.
type info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	GoVersion string `json:"goVersion,omitempty"`
	Platform  string `json:"platform,omitempty"`
}

func (i info) Headers() []string { return []string{"FIELD", "VALUE"} }

// Rows implements output.Tabular. It shows only the fields that have a value, so
// the table without --verbose holds one Version row.
func (i info) Rows() [][]string {
	rows := [][]string{{"Version", i.Version}}
	if i.Commit != "" {
		rows = append(rows, []string{"Commit", i.Commit})
	}

	if i.Date != "" {
		rows = append(rows, []string{"Date", i.Date})
	}

	if i.GoVersion != "" {
		rows = append(rows, []string{"Go", i.GoVersion})
	}

	if i.Platform != "" {
		rows = append(rows, []string{"Platform", i.Platform})
	}

	return rows
}

// IDs implements output.IDer, so --quiet prints only the version string.
func (i info) IDs() []string { return []string{i.Version} }

// String returns the short version string for the --version flag of the root
// command, which cobra handles.
func String() string { return build.Version }

// NewCmdVersion returns the `version` command.
func NewCmdVersion(f *cmdutil.Factory) *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show CLI version information",
		Long: "Show the hyperlift CLI version.\n\n" +
			"With --verbose, the command also reports the git commit, the build\n" +
			"date, the Go runtime, and the platform of this binary.\n\n" +
			"With --quiet, the command prints only the version.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runVersion(f, verbose)
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Include the commit, build date, Go runtime, and platform")

	return cmd
}

func runVersion(f *cmdutil.Factory, verbose bool) error {
	io := f.IOStreams
	format := f.Output()

	i := info{Version: build.Version}
	// --json always carries the full detail. The human formats stay short until
	// --verbose is set.
	if verbose || format == output.FormatJSON {
		i.Commit = build.Commit
		i.Date = build.Date
		i.GoVersion = runtime.Version()
		i.Platform = runtime.GOOS + "/" + runtime.GOARCH
	}

	// In the default table format without --verbose, print one clear headline
	// instead of a FIELD/VALUE table with one row.
	if format == output.FormatTable && !verbose {
		cs := io.ColorScheme()
		_, err := io.Out.Write([]byte("hyperlift version " + cs.Bold(i.Version) + "\n"))

		return err
	}

	return output.Render(io, i, format)
}
