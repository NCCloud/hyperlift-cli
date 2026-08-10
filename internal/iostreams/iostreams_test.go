package iostreams

import (
	"strings"
	"testing"
)

func TestCanPromptNeedsBothTTYs(t *testing.T) {
	tests := []struct {
		name          string
		stdin, stdout bool
		want          bool
	}{
		{"both TTYs", true, true, true},
		{"stdin only", true, false, false},
		{"stdout only", false, true, false},
		{"neither", false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _, _ := Test()
			s.SetNeverPrompt(false)
			s.SetStdinTTY(tt.stdin)
			s.SetStdoutTTY(tt.stdout)

			if got := s.CanPrompt(); got != tt.want {
				t.Errorf("CanPrompt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTestStreamsCannotPrompt(t *testing.T) {
	s, _, _, _ := Test()
	if s.CanPrompt() {
		t.Error("Test() streams must not prompt by default")
	}
}

// TestPrintWordmark_SilentOffTTY pins the decoration rule: the logotype is
// never written to a redirected stream, where block characters would be noise.
func TestPrintWordmark_SilentOffTTY(t *testing.T) {
	ios, _, _, errOut := Test()

	ios.PrintWordmark()

	if errOut.String() != "" {
		t.Errorf("stderr = %q, want nothing off a TTY", errOut.String())
	}
}

// TestPrintWordmark_ShadesRows checks that a color terminal receives every
// shade in the ramp.
func TestPrintWordmark_ShadesRows(t *testing.T) {
	ios, _, _, errOut := Test()
	ios.SetStderrTTY(true)
	ios.SetStdoutTTY(true)

	ios.PrintWordmark()

	got := errOut.String()
	for _, want := range wordmarkRamp {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing the %q row shade", want)
		}
	}

	if lines := strings.Count(got, "\n"); lines != len(wordmark)+2 {
		t.Errorf("line count = %d, want %d rows framed by blank lines", lines, len(wordmark)+2)
	}
}
