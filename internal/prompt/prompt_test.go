package prompt

import (
	"testing"

	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

// promptable returns Test streams that allow prompting, so the fallback
// (non-terminal) read paths run against buffers.
func promptable() (*iostreams.IOStreams, func(string)) {
	ios, in, _, _ := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	ios.SetNeverPrompt(false)

	return ios, func(s string) { in.WriteString(s) }
}

func TestSecretFallbackTrimsLikeTerminalPath(t *testing.T) {
	ios, feed := promptable()
	feed("  s3cr3t \r\n")

	got, err := New(ios).Secret("API secret:")
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}

	if got != "s3cr3t" {
		t.Errorf("Secret = %q, want %q (both paths trim surrounding whitespace)", got, "s3cr3t")
	}
}

func TestSecretRefusesWithoutTTY(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	if _, err := New(ios).Secret("API secret:"); err == nil {
		t.Fatal("Secret should refuse on non-interactive streams")
	}
}
