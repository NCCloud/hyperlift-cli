package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/config"
	"github.com/nccloud/hyperlift-cli/internal/keyring"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// authStatus is the whoami view. It renders as a key/value table, as JSON, or as
// the masked key alone in quiet mode.
type authStatus struct {
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"` // masked
	Source   string `json:"source"` // env or config
	LoggedIn bool   `json:"loggedIn"`
}

func (a authStatus) Headers() []string { return []string{"FIELD", "VALUE"} }

func (a authStatus) Rows() [][]string {
	return [][]string{
		{"Base URL", a.BaseURL},
		{"API key", a.APIKey},
		{"Source", a.Source},
		{"Logged in", fmt.Sprintf("%t", a.LoggedIn)},
	}
}

// IDs implements output.IDer for quiet output. It prints the masked key.
func (a authStatus) IDs() []string {
	if a.APIKey == "" {
		return nil
	}

	return []string{a.APIKey}
}

func newCmdWhoami(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "whoami",
		Aliases: []string{"status"},
		Short:   "Show the active credentials",
		Long: `Show the configured base URL, the masked API key, and where the key comes
from (env or keyring). The command also checks whether the stored credentials
still authenticate against the API.

With --quiet, the command prints only the masked API key.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWhoami(cmd.Context(), f)
		},
	}

	return cmd
}

func runWhoami(ctx context.Context, f *cmdutil.Factory) error {
	ios := f.IOStreams

	cfg, err := f.Config()
	if err != nil {
		return err
	}

	baseURL := cfg.BaseURL()
	key := cfg.APIKey()

	secret, err := keyring.GetSecret()
	if err != nil {
		return fmt.Errorf("read stored secret: %w", err)
	}

	status := authStatus{
		BaseURL: baseURL,
		APIKey:  maskKey(key),
		Source:  keySource(),
	}

	if key == "" || secret == "" {
		return failLoggedOut(f, status)
	}

	c, err := f.Client()
	if err != nil {
		return err
	}

	var api authAPI = c

	ios.StartSpinner("Checking credentials")

	probeErr := api.Probe(ctx)

	ios.StopSpinner()

	loggedIn, scopeLimited, err := classifyWhoami(probeErr)
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			return failLoggedOut(f, status)
		}

		return err
	}

	status.LoggedIn = loggedIn
	if err := output.Render(ios, status, f.Output()); err != nil {
		return err
	}

	// A 403 means the key authenticates but lacks scopes.
	if f.Output() == output.FormatTable && scopeLimited {
		cs := ios.ColorScheme()
		_, _ = fmt.Fprintln(ios.ErrOut, cs.Yellow("!")+" credentials are recognized but lack required scopes")
	}

	return nil
}

// classifyWhoami reads the Probe result: nil and 403 both mean logged in (a
// 403 only lacks a scope, matching login), the rest pass through to the shared
// not-logged-in handling.
func classifyWhoami(probeErr error) (loggedIn, scopeLimited bool, err error) {
	if probeErr == nil {
		return true, false, nil
	}

	var apiErr *client.APIError
	if errors.As(probeErr, &apiErr) && apiErr.Status == http.StatusForbidden {
		return true, true, nil
	}

	return false, false, probeErr
}

// failLoggedOut ends whoami for missing or invalid credentials. --json still
// answers the question on stdout; the returned error then maps to the shared
// not-logged-in message and exit code.
func failLoggedOut(f *cmdutil.Factory, status authStatus) error {
	if f.Output() == output.FormatJSON {
		if err := output.Render(f.IOStreams, status, output.FormatJSON); err != nil {
			return err
		}
	}

	return cmdutil.ErrNotLoggedIn
}

// keySource reports where the active API key comes from. The keyring holds the
// secret, never the key, so a stored key always comes from the config file.
func keySource() string {
	if os.Getenv(config.EnvAPIKey) != "" {
		return "env"
	}

	return "config"
}

// maskKey shows only the last four characters of an API key.
func maskKey(key string) string {
	const reveal = 4

	if key == "" {
		return ""
	}

	if len(key) <= reveal {
		return "****"
	}

	return "****" + key[len(key)-reveal:]
}
