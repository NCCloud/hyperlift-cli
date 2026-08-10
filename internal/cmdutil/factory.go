// Package cmdutil holds what every command shares: the Factory of common
// dependencies, the output flags, and the error mapping.
package cmdutil

import (
	"sync"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/config"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/keyring"
	"github.com/nccloud/hyperlift-cli/internal/output"
	"github.com/nccloud/hyperlift-cli/internal/prompt"
)

// Factory is the dependency container that every NewCmdX receives. The config
// and client fields are functions, so they build late and honor the persistent
// flags the root command parses.
type Factory struct {
	IOStreams *iostreams.IOStreams
	Config    func() (*config.Config, error)
	Client    func() (client.Client, error)
	Prompter  prompt.Prompter

	// Output returns the resolved output format: table, json or quiet. It
	// reflects the persistent --json and --quiet flags and the config default.
	Output func() string

	// Debug reports whether the global --debug flag is set. The root command
	// rewires it after it parses the flags.
	Debug func() bool
}

// NewFactory builds a Factory with the real implementations.
func NewFactory(io *iostreams.IOStreams) *Factory {
	f := &Factory{
		IOStreams: io,
		Prompter:  prompt.New(io),
		Debug:     func() bool { return false },
	}

	f.Config = sync.OnceValues(config.Load)

	f.Client = func() (client.Client, error) {
		cfg, err := f.Config()
		if err != nil {
			return nil, err
		}

		secret, err := keyring.GetSecret()
		if err != nil {
			return nil, err
		}

		debug := f.Debug != nil && f.Debug()

		return client.New(client.Options{
			BaseURL:   cfg.BaseURL(),
			APIKey:    cfg.APIKey(),
			APISecret: secret,
			Debug:     debug,
			DebugOut:  io.ErrOut,
		}), nil
	}

	// The default resolver, until AddOutputFlags replaces it.
	f.Output = func() string {
		cfg, err := f.Config()
		if err != nil {
			return output.FormatTable
		}

		if o := cfg.DefaultOutput(); o != "" {
			return o
		}

		return output.FormatTable
	}

	return f
}
