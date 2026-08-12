// Package prompt holds an interactive prompter that reads stdin and uses no
// third-party library. IOStreams.CanPrompt() gates it.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

// ErrCannotPrompt means the caller asked for input, but the environment is not
// interactive, for example a piped stdin or --no-input.
var ErrCannotPrompt = errors.New("cannot prompt: not a terminal (provide the value via flag/env instead)")

// Prompter is the interactive-input contract the commands use.
type Prompter interface {
	Confirm(msg string) (bool, error)
	Input(msg string) (string, error)
	Secret(msg string) (string, error)
}

// stdinPrompter reads from IOStreams and masks a secret with x/term.
type stdinPrompter struct {
	io *iostreams.IOStreams
	// reader buffers io.In across the prompts. One shared reader is essential.
	// A new bufio.Reader per prompt can swallow input it read ahead of the
	// current line, and the next prompt then loses it.
	reader *bufio.Reader
}

// New returns a Prompter that uses the given streams.
func New(io *iostreams.IOStreams) Prompter {
	return &stdinPrompter{io: io, reader: bufio.NewReader(io.In)}
}

func (p *stdinPrompter) guard() error {
	if !p.io.CanPrompt() {
		return ErrCannotPrompt
	}

	return nil
}

func (p *stdinPrompter) Input(msg string) (string, error) {
	if err := p.guard(); err != nil {
		return "", err
	}

	_, _ = fmt.Fprintf(p.io.ErrOut, "%s ", msg)

	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}

	return strings.TrimRight(line, "\r\n"), nil
}

func (p *stdinPrompter) Confirm(msg string) (bool, error) {
	if err := p.guard(); err != nil {
		return false, err
	}

	ans, err := p.Input(msg + " [y/N]:")
	if err != nil {
		return false, err
	}

	ans = strings.ToLower(strings.TrimSpace(ans))

	return ans == "y" || ans == "yes", nil
}

func (p *stdinPrompter) Secret(msg string) (string, error) {
	if err := p.guard(); err != nil {
		return "", err
	}

	_, _ = fmt.Fprintf(p.io.ErrOut, "%s ", msg)

	// Mask the input only when it comes from the real terminal stdin.
	if f, ok := p.io.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))

		_, _ = fmt.Fprintln(p.io.ErrOut)

		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}

		return strings.TrimSpace(string(b)), nil
	}

	// Otherwise read one unmasked line, as a test does. Trim like the terminal
	// path, so a secret comes out the same on both paths.
	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read secret: %w", err)
	}

	return strings.TrimSpace(line), nil
}
