package version

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/nccloud/hyperlift-cli/internal/build"
	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/client/clienttest"
	"github.com/nccloud/hyperlift-cli/internal/cmdutil"
	"github.com/nccloud/hyperlift-cli/internal/iostreams"
	"github.com/nccloud/hyperlift-cli/internal/output"
)

// newTestFactory builds a Factory with Test IOStreams. The mock Client fails the
// test if the version command ever calls the network.
func newTestFactory(t *testing.T, format string) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	io, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: io,
		Client: func() (client.Client, error) {
			return &clienttest.Fake{
				ProbeFunc: func(_ context.Context) error {
					t.Fatalf("version must not call the client")
					return nil
				},
			}, nil
		},
		Output: func() string { return format },
	}

	return f, out, errOut
}

func TestVersion(t *testing.T) {
	// Pin the build metadata, so the assertions hold whatever link flags built
	// the test binary.
	origV, origC, origD := build.Version, build.Commit, build.Date
	build.Version, build.Commit, build.Date = "1.2.3", "abc1234", "2026-06-25T00:00:00Z"

	t.Cleanup(func() { build.Version, build.Commit, build.Date = origV, origC, origD })

	plat := runtime.GOOS + "/" + runtime.GOARCH
	goVer := runtime.Version()

	tests := []struct {
		name        string
		format      string
		verbose     bool
		wantOut     []string // substrings that must appear on stdout
		wantNotOut  []string // substrings that must not appear on stdout
		wantErrOut  string   // exact stderr, always empty for version
		wantQuietEq string   // when set, stdout must equal this exactly
	}{
		{
			name:       "default table terse",
			format:     output.FormatTable,
			wantOut:    []string{"hyperlift version", "1.2.3"},
			wantNotOut: []string{"abc1234", goVer, "Commit"},
		},
		{
			name:    "verbose table includes commit date go platform",
			format:  output.FormatTable,
			verbose: true,
			wantOut: []string{"1.2.3", "abc1234", "2026-06-25T00:00:00Z", goVer, plat, "Commit", "Date"},
		},
		{
			name:    "json carries full detail even without verbose",
			format:  output.FormatJSON,
			wantOut: []string{`"version": "1.2.3"`, `"commit": "abc1234"`, `"date": "2026-06-25T00:00:00Z"`},
		},
		{
			name:        "quiet prints only the version",
			format:      output.FormatQuiet,
			wantQuietEq: "1.2.3\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, out, errOut := newTestFactory(t, tt.format)

			cmd := NewCmdVersion(f)

			args := []string{}
			if tt.verbose {
				args = append(args, "--verbose")
			}

			cmd.SetArgs(args)
			cmd.SetOut(out)
			cmd.SetErr(errOut)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}

			if errOut.Len() != 0 {
				t.Errorf("stderr = %q, want empty", errOut.String())
			}

			gotOut := out.String()
			if tt.wantQuietEq != "" && gotOut != tt.wantQuietEq {
				t.Errorf("stdout = %q, want exactly %q", gotOut, tt.wantQuietEq)
			}

			for _, sub := range tt.wantOut {
				if !strings.Contains(gotOut, sub) {
					t.Errorf("stdout = %q, want to contain %q", gotOut, sub)
				}
			}

			for _, sub := range tt.wantNotOut {
				if strings.Contains(gotOut, sub) {
					t.Errorf("stdout = %q, must NOT contain %q", gotOut, sub)
				}
			}
		})
	}
}

// TestVersionJSONDecodes checks that --json writes one valid object, with the
// documented fields filled in.
func TestVersionJSONDecodes(t *testing.T) {
	origV, origC, origD := build.Version, build.Commit, build.Date
	build.Version, build.Commit, build.Date = "9.9.9", "deadbeef", "2026-01-01T00:00:00Z"

	t.Cleanup(func() { build.Version, build.Commit, build.Date = origV, origC, origD })

	f, out, _ := newTestFactory(t, output.FormatJSON)

	cmd := NewCmdVersion(f)
	cmd.SetArgs(nil)
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got info
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}

	if got.Version != "9.9.9" || got.Commit != "deadbeef" {
		t.Errorf("decoded = %+v, want version=9.9.9 commit=deadbeef", got)
	}

	if got.GoVersion != runtime.Version() {
		t.Errorf("go_version = %q, want %q", got.GoVersion, runtime.Version())
	}

	if got.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("platform = %q, want %q", got.Platform, runtime.GOOS+"/"+runtime.GOARCH)
	}
}

// TestVersionRejectsArgs confirms that version takes no positional argument.
func TestVersionRejectsArgs(t *testing.T) {
	f, _, _ := newTestFactory(t, output.FormatTable)

	cmd := NewCmdVersion(f)
	cmd.SetArgs([]string{"unexpected"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unexpected positional arg, got nil")
	}
}

// TestString confirms that the source of the root --version flag is the build
// version.
func TestString(t *testing.T) {
	orig := build.Version
	build.Version = "7.7.7"

	t.Cleanup(func() { build.Version = orig })

	if got := String(); got != "7.7.7" {
		t.Errorf("String() = %q, want 7.7.7", got)
	}
}
