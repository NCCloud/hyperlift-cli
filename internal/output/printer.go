// Package output renders a command result as a table, as JSON, or in quiet form.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/nccloud/hyperlift-cli/internal/iostreams"
)

// Output format identifiers.
const (
	FormatTable = "table"
	FormatJSON  = "json"
	FormatQuiet = "quiet"
)

// Tabular is a type that can present itself as a table, with a header and rows.
// Whatever a command renders in table format must implement it. There is no
// generic fallback.
type Tabular interface {
	Headers() []string
	Rows() [][]string
}

// IDer is a type that can report its ids for quiet output. Whatever a command
// renders in quiet format must implement it.
type IDer interface {
	IDs() []string
}

// Render writes data to io.Out in the given format. An empty format means table.
func Render(io *iostreams.IOStreams, data any, format string) error {
	switch format {
	case "", FormatTable:
		return renderTable(io, data)
	case FormatJSON:
		return renderJSON(io, data)
	case FormatQuiet:
		return renderQuiet(io, data)
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
}

func renderJSON(io *iostreams.IOStreams, data any) error {
	enc := json.NewEncoder(io.Out)
	enc.SetIndent("", "  ")

	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}

	return nil
}

func renderQuiet(io *iostreams.IOStreams, data any) error {
	d, ok := data.(IDer)
	if !ok {
		return fmt.Errorf("internal: %T has no id representation", data)
	}

	for _, id := range d.IDs() {
		_, _ = fmt.Fprintln(io.Out, id)
	}

	return nil
}

func renderTable(io *iostreams.IOStreams, data any) error {
	t, ok := data.(Tabular)
	if !ok {
		return fmt.Errorf("internal: %T has no table representation", data)
	}

	return writeTable(io, t.Headers(), t.Rows())
}

func writeTable(io *iostreams.IOStreams, headers []string, rows [][]string) error {
	// Pad first, colorize after: ANSI escape bytes count toward tabwriter's
	// measured cell widths, so a bolded cell misaligns the columns. Render the
	// plain table into a buffer, then bold the already-padded header line.
	var buf bytes.Buffer

	tw := tabwriter.NewWriter(&buf, 0, 2, 3, ' ', 0)
	if len(headers) > 0 {
		_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))
	}

	for _, row := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}

	table := buf.String()
	if len(headers) > 0 {
		if i := strings.IndexByte(table, '\n'); i >= 0 {
			cs := io.ColorScheme()
			table = cs.Bold(table[:i]) + table[i:]
		}
	}

	if _, err := fmt.Fprint(io.Out, table); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	return nil
}
