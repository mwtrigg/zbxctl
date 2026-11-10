package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"golang.org/x/term"
)

// Exit codes.
const (
	ExitSuccess   = 0
	ExitError     = 1
	ExitNotFound  = 2
	ExitAuthError = 3
)

// Format is the output format.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatJSONL Format = "jsonl"
)

// Column describes a single output column.
type Column struct {
	Key    string // map key
	Header string // display name
}

// Print writes rows to stdout in the requested format.
func Print(format Format, columns []Column, rows []map[string]any) error {
	switch format {
	case FormatJSON:
		return printJSON(columns, rows)
	case FormatJSONL:
		return printJSONL(columns, rows)
	default:
		return printTable(columns, rows)
	}
}

func printTable(columns []Column, rows []map[string]any) error {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)

	// Minimal style: header separator, no box borders, space-separated columns.
	style := table.Style{
		Name: "zbxctl",
		Box: table.BoxStyle{
			PaddingLeft:      " ",
			PaddingRight:     " ",
			MiddleVertical:   " ",
			MiddleSeparator:  " ",
			MiddleHorizontal: "-",
		},
		Format: table.FormatOptions{
			Header: text.FormatUpper,
		},
		Options: table.Options{
			DrawBorder:      false,
			SeparateColumns: false,
			SeparateHeader:  true,
			SeparateRows:    false,
		},
	}
	t.SetStyle(style)

	// Header row.
	headerRow := make(table.Row, len(columns))
	for i, c := range columns {
		headerRow[i] = c.Header
	}
	t.AppendHeader(headerRow)

	// Data rows.
	for _, row := range rows {
		r := make(table.Row, len(columns))
		for i, c := range columns {
			r[i] = cell(row[c.Key])
		}
		t.AppendRow(r)
	}

	t.Render()
	return nil
}

func printJSON(_ []Column, rows []map[string]any) error {
	envelope := map[string]any{
		"data": rows,
		"meta": map[string]any{
			"count": len(rows),
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope)
}

func printJSONL(_ []Column, rows []map[string]any) error {
	enc := json.NewEncoder(os.Stdout)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

// filterColumns returns a new slice of maps containing only the specified columns.
func filterColumns(columns []Column, rows []map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		m := make(map[string]any, len(columns))
		for _, c := range columns {
			m[c.Key] = row[c.Key]
		}
		out[i] = m
	}
	return out
}

// PrintError writes a structured error to stderr.
func PrintError(format Format, msg string, code int) {
	switch format {
	case FormatJSON, FormatJSONL:
		enc := json.NewEncoder(os.Stderr)
		_ = enc.Encode(map[string]any{"error": msg, "code": code})
	default:
		fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	}
}

// DefaultFormat returns FormatTable if stdout is a TTY, FormatJSONL otherwise.
func DefaultFormat() Format {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return FormatTable
	}
	return FormatJSONL
}

// cell renders list values compactly for table output.
func cell(v any) any {
	if l, ok := v.([]string); ok {
		if len(l) == 0 {
			return "-"
		}
		return strings.Join(l, ",")
	}
	return v
}
