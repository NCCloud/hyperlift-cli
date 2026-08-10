package root

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

// newTestRoot builds the full command tree with Test IOStreams and an isolated
// config dir. The returned buffer captures stdout.
func newTestRoot(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HYPERLIFT_NO_UPDATE_CHECK", "1")

	ios, _, out, _ := iostreams.Test()

	root := NewCmdRoot(cmdutil.NewFactory(ios))
	root.SetIn(ios.In)
	root.SetOut(ios.Out)
	root.SetErr(ios.ErrOut)

	return root, out
}

func TestJSONAndQuietCannotBeCombined(t *testing.T) {
	root, _ := newTestRoot(t)
	// A subcommand inherits the pair, so the conflict must trip there too.
	root.SetArgs([]string{"version", "--json", "--quiet"})
	root.SilenceErrors = true
	root.SilenceUsage = true

	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("--json --quiet together should be an error")
	}

	if got := err.Error(); got != "--json and --quiet cannot be combined" {
		t.Errorf("err = %q, want the friendly conflict message", got)
	}
}

func TestQuietFlagDescription(t *testing.T) {
	root, _ := newTestRoot(t)

	fl := root.PersistentFlags().Lookup("quiet")
	if fl == nil {
		t.Fatal("--quiet flag not registered")
	}

	if fl.Usage != "Output only the primary identifier per line" {
		t.Errorf("usage = %q, want the primary-identifier wording", fl.Usage)
	}
}

func TestRootHelpListsEnvironmentVariables(t *testing.T) {
	root, outBuf := newTestRoot(t)
	root.SetArgs([]string{"--help"})

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute --help: %v", err)
	}

	out := outBuf.String()
	if !strings.Contains(out, "Environment variables:") {
		t.Fatalf("help missing the environment-variables block:\n%s", out)
	}

	for _, v := range []string{
		"HYPERLIFT_BASE_URL",
		"HYPERLIFT_API_KEY",
		"HYPERLIFT_API_SECRET",
		"HYPERLIFT_NO_UPDATE_CHECK",
		"NO_COLOR",
	} {
		if !strings.Contains(out, v) {
			t.Errorf("help missing %s", v)
		}
	}
}

// TestSubcommandHookKeepsRootHook guards the update nag: cobra runs only the
// nearest PersistentPreRun in the chain unless EnableTraverseRunHooks is set,
// so a subcommand with its own hook would otherwise silently disable the
// root's.
func TestSubcommandHookKeepsRootHook(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ios, _, _, _ := iostreams.Test()
	f := cmdutil.NewFactory(ios)
	root := NewCmdRoot(f)

	rootRan := false
	rootHook := root.PersistentPreRunE
	root.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		rootRan = true
		return rootHook(c, args)
	}

	childRan := false
	child := &cobra.Command{
		Use:              "probe",
		PersistentPreRun: func(*cobra.Command, []string) { childRan = true },
		RunE:             func(*cobra.Command, []string) error { return nil },
	}
	root.AddCommand(child)

	root.SetArgs([]string{"probe"})
	root.SetIn(ios.In)
	root.SetOut(ios.Out)
	root.SetErr(ios.ErrOut)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !childRan {
		t.Error("the subcommand's own PersistentPreRun did not run")
	}

	if !rootRan {
		t.Error("the root's PersistentPreRun did not run; the update nag is disabled for this subtree")
	}
}

// TestDebugSilencesSpinner pins the stderr contract: the debug trace and the
// spinner share the stream, so --debug must stand the spinner down. A TTY
// stderr is what would otherwise let it start.
func TestDebugSilencesSpinner(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HYPERLIFT_NO_UPDATE_CHECK", "1")

	ios, _, _, errOut := iostreams.Test()
	ios.SetStderrTTY(true)

	f := cmdutil.NewFactory(ios)
	root := NewCmdRoot(f)
	root.SetArgs([]string{"version", "--debug"})
	root.SetIn(ios.In)
	root.SetOut(ios.Out)
	root.SetErr(ios.ErrOut)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Any spinner frame would leave an escape sequence on stderr.
	ios.StartSpinner("working")
	ios.StopSpinner()

	if errOut.String() != "" {
		t.Errorf("stderr = %q, want no spinner output under --debug", errOut.String())
	}
}
