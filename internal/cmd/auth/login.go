package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/config"
	"github.com/nccloud/hyperlift-cli/internal/keyring"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// authResult is the minimal --json document of login and logout.
type authResult struct {
	LoggedIn bool `json:"loggedIn"`
}

type loginOptions struct {
	key       string
	secret    string
	baseURL   string
	withStdin bool
}

func newCmdLogin(f *cmdutil.Factory) *cobra.Command {
	opts := &loginOptions{}

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in with an API key and secret",
		Long: `Authenticate the CLI with a Spaceship API key and secret.

Supply the credentials interactively, with flags, or on stdin:

  # interactive prompt (TTY only)
  hyperlift auth login

  # explicit flags
  hyperlift auth login --key KEY --secret SECRET

  # secret piped on stdin (keeps it out of shell history)
  printf '%s' "$SECRET" | hyperlift auth login --key KEY --with-stdin

The CLI stores the API key in the config file, and the base URL too when you
pass --base-url. It stores the secret in the OS keyring. If no keyring is
available, it uses a 0600 file instead.

With --quiet, the command prints nothing on success.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.key, "key", "", "API key (X-API-Key)")
	cmd.Flags().StringVar(&opts.secret, "secret", "", "API secret (X-API-Secret); use --with-stdin to keep it out of shell history")
	cmd.Flags().StringVar(&opts.baseURL, "base-url", "", "API base URL (defaults to the configured value or the built-in URL)")
	cmd.Flags().BoolVar(&opts.withStdin, "with-stdin", false, "Read the API secret from stdin")

	return cmd
}

func runLogin(ctx context.Context, f *cmdutil.Factory, opts *loginOptions) error {
	ios := f.IOStreams
	cs := ios.ColorScheme()

	cfg, err := f.Config()
	if err != nil {
		return err
	}

	key, secret, err := resolveCredentials(f, opts)
	if err != nil {
		return err
	}

	if key == "" {
		return errors.New("an API key is required (use --key or run interactively)")
	}

	if secret == "" {
		return errors.New("an API secret is required (use --secret, --with-stdin, or run interactively)")
	}

	// Resolve the base URL. The flag wins; otherwise use env, config or default.
	baseURL := opts.baseURL
	if baseURL == "" {
		baseURL = cfg.BaseURL()
	}

	// Validate against the API with the credentials the user just supplied, not
	// with the Factory's stored client.
	var api authAPI = newClient(client.Options{
		BaseURL:   baseURL,
		APIKey:    key,
		APISecret: secret,
	})

	ios.StartSpinner("Verifying credentials")

	probeErr := api.Probe(ctx)

	ios.StopSpinner()

	scopeWarning, err := classifyProbe(probeErr)
	if err != nil {
		var silent *cmdutil.SilentError
		if errors.As(err, &silent) {
			cmdutil.PrintError(ios, silent.Error())
		}

		return err
	}

	prevKey, prevBaseURL := cfg.StoredAPIKey, cfg.StoredBaseURL

	cfg.StoredAPIKey = key
	if opts.baseURL != "" {
		cfg.StoredBaseURL = opts.baseURL
	}

	// Save the config before the secret: the config write fails more often
	// than the keyring write with its file fallback, so this order shrinks the
	// window where a new secret pairs with a stale key.
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	usedFallback, err := keyring.SetSecret(secret)
	if err != nil {
		// Roll back the config, so the new key does not pair with the old
		// secret. The rollback is best effort.
		cfg.StoredAPIKey, cfg.StoredBaseURL = prevKey, prevBaseURL
		if rerr := cfg.Save(); rerr != nil {
			return fmt.Errorf("store secret: %w (config rollback also failed: %v)", err, rerr)
		}

		return fmt.Errorf("store secret: %w", err)
	}

	if usedFallback {
		p, _ := keyring.FallbackPath()
		_, _ = fmt.Fprintf(ios.ErrOut, "%s OS keyring unavailable; secret stored in %s (0600)\n",
			cs.Yellow("Warning:"), p)
	}

	if scopeWarning != "" {
		_, _ = fmt.Fprintln(ios.ErrOut, cs.Yellow("!")+" "+scopeWarning)
	}

	// The env var is a per-shell override, never persisted: warn that the next
	// shell without it talks to a different host than the one just validated.
	if env := os.Getenv(config.EnvBaseURL); opts.baseURL == "" && env != "" {
		stored := cfg.StoredBaseURL
		if stored == "" {
			stored = config.DefaultBaseURL
		}

		if env != stored {
			_, _ = fmt.Fprintf(ios.ErrOut, "%s validation used %s from %s; this URL is not stored in the config\n",
				cs.Yellow("Warning:"), baseURL, config.EnvBaseURL)
		}
	}

	switch f.Output() {
	case output.FormatJSON:
		return output.Render(ios, authResult{LoggedIn: true}, output.FormatJSON)
	case output.FormatQuiet:
		return nil
	default:
		ios.PrintWordmark()

		_, _ = fmt.Fprintf(ios.ErrOut, "%s Logged in to %s.\n",
			cs.Green("✓"), cs.Bold(baseURL))
		_, _ = fmt.Fprintf(ios.ErrOut, "\n%s\n", cs.Gray("Next: hyperlift apps list"))

		return nil
	}
}

// classifyProbe maps the Probe error of a login:
//   - nil           -> the credentials are good
//   - 401           -> a hard failure, silent, with a message already worded
//   - 403           -> valid credentials without a scope, so return a warning
//   - anything else -> return the error unchanged
func classifyProbe(probeErr error) (warning string, err error) {
	if probeErr == nil {
		return "", nil
	}

	var apiErr *client.APIError
	if !errors.As(probeErr, &apiErr) {
		return "", probeErr
	}

	switch apiErr.Status {
	case http.StatusUnauthorized:
		return "", cmdutil.NewSilentError(errors.New("invalid API key or secret"))
	case http.StatusForbidden:
		scope := apiErr.Code
		if scope == "" {
			scope = "a required scope"
		}

		return fmt.Sprintf("your API key is missing %s; some commands may fail", scope), nil
	default:
		return "", probeErr
	}
}

// resolveCredentials collects the API key and secret. It reads the flags first,
// then stdin when --with-stdin is set, then the interactive prompts.
func resolveCredentials(f *cmdutil.Factory, opts *loginOptions) (key, secret string, err error) {
	ios := f.IOStreams
	key = strings.TrimSpace(opts.key)
	secret = opts.secret

	if opts.withStdin {
		data, rerr := io.ReadAll(ios.In)
		if rerr != nil {
			return "", "", fmt.Errorf("read secret from stdin: %w", rerr)
		}

		secret = strings.TrimRight(string(data), "\r\n")
	}

	// Prompt for whatever is still missing, if prompting is possible.
	if key == "" {
		if !ios.CanPrompt() {
			return "", "", errors.New("no API key provided and stdin is not a terminal (use --key)")
		}

		key, err = f.Prompter.Input("API key:")
		if err != nil {
			return "", "", err
		}

		key = strings.TrimSpace(key)
	}

	if secret == "" {
		if !ios.CanPrompt() {
			return "", "", errors.New("no API secret provided and stdin is not a terminal (use --secret or --with-stdin)")
		}

		secret, err = f.Prompter.Secret("API secret:")
		if err != nil {
			return "", "", err
		}
	}

	return key, secret, nil
}
