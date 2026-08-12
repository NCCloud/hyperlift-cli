package apps

import (
	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// appDetail is the key/value table view that `get` renders for one application.
type appDetail struct {
	app *client.Application
}

func (d appDetail) Headers() []string { return []string{"FIELD", "VALUE"} }

func (d appDetail) Rows() [][]string {
	a := d.app

	return [][]string{
		{"ID", a.ID},
		{"Status", string(a.Status)},
		{"Build status", string(a.BuildStatus)},
		{"Plan", a.Plan},
		{"Scale", fmtScale(a.Scale)},
		{"Domain", dash(a.Domain)},
		{"Branch", dash(a.Branch)},
		{"Repository", dash(a.GithubRepositoryFullName)},
		{"GitHub installation", fmtInstallID(a.GithubInstallationID)},
		{"Dockerfile path", dash(a.DockerfilePath)},
		{"Automatic build", fmtBool(a.AutomaticBuildEnabled)},
		{"Created at", fmtTime(&a.CreatedAt)},
		{"Updated at", fmtTime(a.UpdatedAt)},
	}
}

func (d appDetail) IDs() []string { return []string{d.app.ID} }

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-id>",
		Short: "Show details for an application",
		Long: "Show the details of one application as a field/value table.\n\n" +
			"With --json, the command prints the full application object. With\n" +
			"--quiet, it prints only the application id.",
		Example: "  hyperlift apps get app_123\n  hyperlift apps get app_123 --json",
		Args:    cmdutil.AppIDArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.Client()
			if err != nil {
				return err
			}

			var api appsAPI = c

			f.IOStreams.StartSpinner("Loading application")

			app, err := api.AppsGet(cmd.Context(), args[0])

			f.IOStreams.StopSpinner()

			if err != nil {
				return err
			}

			format := f.Output()
			if format == output.FormatJSON {
				return output.Render(f.IOStreams, app, format)
			}

			return output.Render(f.IOStreams, appDetail{app: app}, format)
		},
	}
}
