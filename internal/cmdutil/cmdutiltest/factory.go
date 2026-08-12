// Package cmdutiltest provides the shared Factory fixture for command tests.
package cmdutiltest

import (
	"bytes"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/prompt"
)

// NewFactory builds a Factory around c with Test IOStreams. format is what
// f.Output() returns. out and errOut capture stdout and stderr.
func NewFactory(c client.Client, format string) (f *cmdutil.Factory, out, errOut *bytes.Buffer) {
	io, _, out, errOut := iostreams.Test()
	f = &cmdutil.Factory{
		IOStreams: io,
		Prompter:  prompt.New(io),
		Client:    func() (client.Client, error) { return c, nil },
		Output:    func() string { return format },
	}

	return f, out, errOut
}
