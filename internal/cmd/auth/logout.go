package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/keyring"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

type logoutOptions struct {
	yes bool
}

func newCmdLogout(f *cmdutil.Factory) *cobra.Command {
	opts := &logoutOptions{}

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		Long: `Remove the stored API key and secret.

The command deletes the secret from the OS keyring and from any file fallback.
It also clears the API key from the config file.

With --quiet, the command prints nothing on success.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogout(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip the confirmation prompt")

	return cmd
}

func runLogout(_ context.Context, f *cmdutil.Factory, opts *logoutOptions) error {
	ios := f.IOStreams
	cs := ios.ColorScheme()

	cfg, err := f.Config()
	if err != nil {
		return err
	}

	secret, err := keyring.GetSecret()
	if err != nil {
		return fmt.Errorf("check stored secret: %w", err)
	}

	if cfg.StoredAPIKey == "" && secret == "" {
		return cmdutil.ErrNotLoggedIn
	}

	if !opts.yes {
		if !ios.CanPrompt() {
			return errors.New("refusing to log out without confirmation; pass --yes")
		}

		ok, perr := f.Prompter.Confirm("Remove stored credentials?")
		if perr != nil {
			return perr
		}

		if !ok {
			return cmdutil.NewSilentError(errors.New("logout cancelled"))
		}
	}

	if err := keyring.DeleteSecret(); err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}

	// Clear the API key, but keep the base URL and the output preference.
	cfg.StoredAPIKey = ""
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	switch f.Output() {
	case output.FormatJSON:
		return output.Render(ios, authResult{LoggedIn: false}, output.FormatJSON)
	case output.FormatQuiet:
		return nil
	default:
		_, _ = fmt.Fprintf(ios.ErrOut, "%s Logged out.\n", cs.Green("✓"))
		return nil
	}
}
