package iostreams

import "fmt"

var wordmark = []string{
	"█ █ █ █ █▀█ █▀▀ █▀█ █   ▀█▀ █▀▀ ▀█▀",
	"█▀█ ▀█▀ █▀▀ █▀▀ █▀▄ █    █  █▀▀  █ ",
	"▀ ▀  ▀  ▀   ▀▀▀ ▀ ▀ ▀▀▀ ▀▀▀ ▀    ▀ ",
}

// wordmarkRamp fades the rows into the logo's amber, #f0b020. The 4-bit set the
// color scheme uses has no amber, so these are 256-color escapes.
var wordmarkRamp = []string{"\033[38;5;222m", "\033[38;5;214m", "\033[38;5;172m"}

// PrintWordmark writes the logotype to ErrOut between two blank lines, so it
// does not sit flush against the shell prompt. It writes nothing unless stderr
// is a terminal: the blocks are decoration, and a redirected stream stays
// readable without them.
func (s *IOStreams) PrintWordmark() {
	if !s.stderrTTY {
		return
	}

	color := s.ColorEnabled()

	_, _ = fmt.Fprintln(s.ErrOut)

	for i, line := range wordmark {
		if color {
			line = wordmarkRamp[i%len(wordmarkRamp)] + line + escReset
		}

		_, _ = fmt.Fprintln(s.ErrOut, "  "+line)
	}

	_, _ = fmt.Fprintln(s.ErrOut)
}
