package output

import (
	"strings"
	"testing"

	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

// tableView implements Tabular and IDer, as every command view does.
type tableView struct{}

func (tableView) Headers() []string { return []string{"ID", "STATUS"} }
func (tableView) Rows() [][]string  { return [][]string{{"app_a1", "running"}} }
func (tableView) IDs() []string     { return []string{"app_a1"} }

// plain implements neither. It stands in for a raw wire type.
type plain struct {
	ID string `json:"id"`
}

// wideView has data cells wider than their headers, the shape that exposed the
// bold-header misalignment.
type wideView struct{}

func (wideView) Headers() []string { return []string{"ID", "STATUS", "DOMAIN"} }
func (wideView) Rows() [][]string {
	return [][]string{{"app_a1b2c3d4e5", "running", "very-long-domain.example.com"}}
}

// TestBoldHeadersKeepAlignment renders with color on and off. Stripped of the
// ANSI codes, both outputs must be byte-identical: the bold must never change
// the padding tabwriter computed.
func TestBoldHeadersKeepAlignment(t *testing.T) {
	plainIO, _, plainOut, _ := iostreams.Test()
	if err := Render(plainIO, wideView{}, FormatTable); err != nil {
		t.Fatalf("Render (plain): %v", err)
	}

	colorIO, _, colorOut, _ := iostreams.Test()
	colorIO.SetStdoutTTY(true) // enables color

	if err := Render(colorIO, wideView{}, FormatTable); err != nil {
		t.Fatalf("Render (color): %v", err)
	}

	colored := colorOut.String()
	if !strings.Contains(colored, "\033[1m") {
		t.Fatal("color output carries no bold escape; the test would prove nothing")
	}

	stripped := strings.NewReplacer("\033[1m", "", "\033[0m", "").Replace(colored)
	if stripped != plainOut.String() {
		t.Errorf("bold changed the table layout\ncolor (stripped):\n%s\nplain:\n%s", stripped, plainOut.String())
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		data    any
		format  string
		wantOut []string
		wantErr string
	}{
		{
			name:    "table via Tabular",
			data:    tableView{},
			format:  FormatTable,
			wantOut: []string{"ID", "STATUS", "app_a1", "running"},
		},
		{
			name:    "quiet via IDer",
			data:    tableView{},
			format:  FormatQuiet,
			wantOut: []string{"app_a1"},
		},
		{
			name:    "json takes any value",
			data:    plain{ID: "app_a1"},
			format:  FormatJSON,
			wantOut: []string{`"id": "app_a1"`},
		},
		{
			name:    "table without Tabular is a programming error",
			data:    plain{ID: "app_a1"},
			format:  FormatTable,
			wantErr: "no table representation",
		},
		{
			name:    "quiet without IDer is a programming error",
			data:    plain{ID: "app_a1"},
			format:  FormatQuiet,
			wantErr: "no id representation",
		},
		{
			name:    "unknown format",
			data:    tableView{},
			format:  "yaml",
			wantErr: `unknown output format "yaml"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, out, _ := iostreams.Test()

			err := Render(io, tt.data, tt.format)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("stdout %q missing %q", out.String(), want)
				}
			}
		})
	}
}
