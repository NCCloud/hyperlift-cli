package apps

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// appList is the table view for `apps list`.
type appList struct {
	apps []client.Application
}

func (l appList) Headers() []string {
	return []string{"ID", "STATUS", "BUILD", "PLAN", "SCALE", "DOMAIN"}
}

func (l appList) Rows() [][]string {
	rows := make([][]string, 0, len(l.apps))
	for i := range l.apps {
		a := &l.apps[i]
		rows = append(rows, []string{a.ID, string(a.Status), string(a.BuildStatus), a.Plan, fmtScale(a.Scale), dash(a.Domain)})
	}

	return rows
}

func (l appList) IDs() []string {
	ids := make([]string, 0, len(l.apps))
	for i := range l.apps {
		ids = append(ids, l.apps[i].ID)
	}

	return ids
}

// listPageSize is the fixed page size that --all uses to walk every page. The
// external contract caps take at 100.
const listPageSize = 100

// fetchAll reads the caller's complete application list. skip advances by the
// page size, not by len(page): the server drops dead ids after paging, so a
// short page is normal and advancing by its length would re-fetch the overlap.
func fetchAll(ctx context.Context, api appsAPI) ([]client.Application, int, error) {
	var out []client.Application

	for skip := 0; ; skip += listPageSize {
		page, total, err := api.AppsList(ctx, listPageSize, skip)
		if err != nil {
			return nil, 0, err
		}

		out = append(out, page...)
		if len(page) == 0 || skip+listPageSize >= total {
			return out, total, nil
		}
	}
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var (
		take, skip int
		all        bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your applications",
		Long: "List your applications, one row per application.\n\n" +
			"The server pages the list; use --skip and --take to walk the pages, or\n" +
			"--all to fetch every page.\n\n" +
			"With --quiet, the command prints one application id per line.",
		Example: "  hyperlift apps list\n  hyperlift apps list --all --quiet",
		Aliases: []string{"ls"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --all ignores --take, so validate only where the value is used.
			if !all && (take < 1 || take > listPageSize) {
				return fmt.Errorf("--take must be between 1 and %d", listPageSize)
			}

			c, err := f.Client()
			if err != nil {
				return err
			}

			var api appsAPI = c

			f.IOStreams.StartSpinner("Loading applications")

			var (
				apps  []client.Application
				total int
			)
			if all {
				apps, total, err = fetchAll(cmd.Context(), api)
			} else {
				apps, total, err = api.AppsList(cmd.Context(), take, skip)
			}

			f.IOStreams.StopSpinner()

			if err != nil {
				return err
			}

			format := f.Output()
			if format == output.FormatJSON {
				return output.Render(f.IOStreams, map[string]any{"items": apps, "total": total}, format)
			}

			// An empty table prints nothing on stdout, so pipelines stay clean;
			// the notice goes to stderr.
			if format == output.FormatTable && len(apps) == 0 {
				_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "No applications found.")
				return nil
			}

			if err := output.Render(f.IOStreams, appList{apps: apps}, format); err != nil {
				return err
			}

			// --all reads every page, so hint only for manual paging. The hint is
			// prose on stdout, so table output only: --quiet promises one
			// identifier per line. The next page starts at the window the server
			// consumed, not at skip+len(apps).
			if next := skip + take; !all && format == output.FormatTable && total > next {
				_, _ = fmt.Fprintf(f.IOStreams.Out, "\nShowing %d-%d of %d. Use --skip %d for the next page (or --all).\n", skip+1, skip+len(apps), total, next)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&take, "take", listPageSize, "Maximum number of applications to return (1-100)")
	cmd.Flags().IntVar(&skip, "skip", 0, "Number of applications to skip (for paging)")
	cmd.Flags().BoolVar(&all, "all", false, "Fetch every page (ignores --take/--skip)")

	return cmd
}
