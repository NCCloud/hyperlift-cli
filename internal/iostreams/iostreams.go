// Package iostreams holds the process I/O in one place: the three streams, the
// TTY detection, the color gate, and a small spinner.
package iostreams

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// IOStreams holds the standard streams and what they support.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	// stdinTTY, stdoutTTY and stderrTTY are resolved once, at construction.
	stdinTTY  bool
	stdoutTTY bool
	stderrTTY bool

	// colorDisabled turns color off whatever the TTY says, e.g. for NO_COLOR.
	colorDisabled bool

	// neverSpin turns the spinner off, so nothing animates over another writer
	// that shares stderr.
	neverSpin bool
	// neverPrompt turns interactive prompts off, e.g. on a non-TTY or with
	// --no-input.
	neverPrompt bool

	spinner *spinner
}

// System returns IOStreams that use the real process streams.
func System() *IOStreams {
	_, noColor := os.LookupEnv("NO_COLOR")
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	stderrTTY := term.IsTerminal(int(os.Stderr.Fd()))

	// Windows consoles process ANSI escapes only after opting in; disable
	// color when the console cannot. No-op elsewhere.
	if stdoutTTY && !enableVirtualTerminal(os.Stdout.Fd()) {
		noColor = true
	}

	if stderrTTY {
		_ = enableVirtualTerminal(os.Stderr.Fd())
	}

	return &IOStreams{
		In:            os.Stdin,
		Out:           os.Stdout,
		ErrOut:        os.Stderr,
		stdinTTY:      stdinTTY,
		stdoutTTY:     stdoutTTY,
		stderrTTY:     stderrTTY,
		colorDisabled: noColor,
		neverPrompt:   !stdinTTY || !stdoutTTY,
	}
}

// Test returns IOStreams that write to buffers, plus the three buffers.
func Test() (streams *IOStreams, in, out, errOut *bytes.Buffer) {
	in = &bytes.Buffer{}
	out = &bytes.Buffer{}
	errOut = &bytes.Buffer{}

	streams = &IOStreams{
		In:          in,
		Out:         out,
		ErrOut:      errOut,
		neverPrompt: true,
	}

	return streams, in, out, errOut
}

// IsStderrTTY reports whether stderr is an interactive terminal.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrTTY }

// ColorEnabled reports whether to write ANSI color. It needs stdout to be a TTY
// and NO_COLOR to be unset.
func (s *IOStreams) ColorEnabled() bool {
	return !s.colorDisabled && s.stdoutTTY
}

// CanPrompt reports whether interactive prompts are allowed. A prompt reads
// stdin and writes its question to the terminal, so both must be TTYs.
func (s *IOStreams) CanPrompt() bool {
	return !s.neverPrompt && s.stdinTTY && s.stdoutTTY
}

// SetNeverSpin turns the spinner off. --debug uses it, because the trace writes
// to stderr too and would print across the spinner's line.
func (s *IOStreams) SetNeverSpin(v bool) { s.neverSpin = v }

// SetNeverPrompt turns prompting off. A --no-input style flag uses it.
func (s *IOStreams) SetNeverPrompt(v bool) { s.neverPrompt = v }

// SetStdinTTY overrides the resolved stdin-TTY value. It is a test helper.
func (s *IOStreams) SetStdinTTY(v bool) { s.stdinTTY = v }

// SetStderrTTY overrides the resolved stderr-TTY value. A test uses it to reach
// the spinner, which stays silent on a non-TTY stderr.
func (s *IOStreams) SetStderrTTY(v bool) { s.stderrTTY = v }

// SetStdoutTTY overrides the resolved stdout-TTY value. A test uses it to run
// the interactive prompt and color paths against buffer-backed streams.
func (s *IOStreams) SetStdoutTTY(v bool) { s.stdoutTTY = v }

// StartSpinner starts a spinner on ErrOut with the given label. It does nothing
// when ErrOut is not a TTY, so a pipe stays clean.
func (s *IOStreams) StartSpinner(label string) {
	if !s.stderrTTY || s.neverSpin {
		return
	}

	if s.spinner != nil {
		s.spinner.Stop()
	}

	s.spinner = newSpinner(s.ErrOut, label)
	s.spinner.Start()
}

// StopSpinner stops any running spinner.
func (s *IOStreams) StopSpinner() {
	if s.spinner != nil {
		s.spinner.Stop()
		s.spinner = nil
	}
}

// spinner is a small Braille-frame spinner that writes to one writer.
type spinner struct {
	w     io.Writer
	label string
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newSpinner(w io.Writer, label string) *spinner {
	return &spinner{
		w:     w,
		label: label,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

func (sp *spinner) Start() {
	frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

	go func() {
		defer close(sp.done)

		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()

		i := 0

		for {
			select {
			case <-sp.stop:
				// Clear the spinner line.
				_, _ = fmt.Fprint(sp.w, "\r\033[K")
				return
			case <-ticker.C:
				_, _ = fmt.Fprintf(sp.w, "\r%c %s", frames[i%len(frames)], sp.label)
				i++
			}
		}
	}()
}

func (sp *spinner) Stop() {
	sp.once.Do(func() {
		close(sp.stop)
		<-sp.done
	})
}
