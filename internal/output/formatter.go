package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// Exit codes.
const (
	ExitSuccess   = 0
	ExitError     = 1
	ExitNotFound  = 2
	ExitAuthError = 3
	ExitConnect   = 4 // cannot reach Zabbix
	ExitUsage     = 5 // invalid arguments or flags
	ExitVersion   = 6 // the Zabbix server is older than zbxctl supports
	ExitUnknown   = 7 // a write was sent but its outcome is not known (timeout, 502/504): check before retrying
)

// Raw includes the raw API values (*_raw, *_unix) next to the readable ones.
var Raw bool

// Field, when set, makes Print emit just that field of each row, one value
// per line (list values one element per line).
var Field string

// Command is the command path reported in the JSON envelope's meta.
var Command string

// KindFor maps an exit code to a stable machine-readable error kind.
func KindFor(code int) string {
	switch code {
	case ExitNotFound:
		return "not_found"
	case ExitAuthError:
		return "auth"
	case ExitConnect:
		return "connection"
	case ExitUsage:
		return "usage"
	case ExitVersion:
		return "unsupported_version"
	case ExitUnknown:
		return "unknown_outcome"
	default:
		return "error"
	}
}

// stripRaw drops the raw duplicates unless --raw was given.
func stripRaw(rows []map[string]any) []map[string]any {
	if Raw {
		return rows
	}
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		m := make(map[string]any, len(row))
		for k, v := range row {
			if strings.HasSuffix(k, "_raw") || strings.HasSuffix(k, "_unix") {
				continue
			}
			m[k] = v
		}
		out[i] = m
	}
	return out
}

// Format is the output format.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatJSONL Format = "jsonl"
	FormatCSV   Format = "csv"
)

// Column describes a single output column.
type Column struct {
	Key    string // map key
	Header string // display name
}

// Print writes rows to stdout in the requested format.
func Print(format Format, columns []Column, rows []map[string]any) error {
	if Field != "" {
		return printField(rows)
	}
	rows = stripRaw(rows)
	columns = visibleColumns(columns)
	switch format {
	case FormatJSON:
		return printJSON(columns, rows)
	case FormatJSONL:
		return printJSONL(columns, rows)
	case FormatCSV:
		return printCSV(columns, rows)
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
			"count":   len(rows),
			"command": Command,
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope)
}

func printJSONL(_ []Column, rows []map[string]any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
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
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]any{"error": msg, "kind": KindFor(code), "code": code})
	default:
		fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	}
}

// DefaultFormat is JSON: agents are the primary users. Use -o table for humans.
func DefaultFormat() Format { return FormatJSON }

// cell renders list values compactly for table output.
func cell(v any) any {
	if v == nil {
		return "-"
	}
	if l, ok := v.([]string); ok {
		if len(l) == 0 {
			return "-"
		}
		return strings.Join(l, ",")
	}
	return v
}

// FieldError is returned when --field names a field that does not exist.
type FieldError struct{ Msg string }

func (e *FieldError) Error() string { return e.Msg }

func printField(rows []map[string]any) error {
	for _, row := range rows {
		v, ok := row[Field]
		if !ok {
			keys := make([]string, 0, len(row))
			for k := range row {
				if !strings.HasSuffix(k, "_raw") && !strings.HasSuffix(k, "_unix") {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			return &FieldError{fmt.Sprintf("no such field %q (available: %s)", Field, strings.Join(keys, ", "))}
		}
		switch t := v.(type) {
		case nil:
		case []string:
			for _, e := range t {
				fmt.Println(e)
			}
		default:
			fmt.Println(t)
		}
	}
	return nil
}

func printCSV(columns []Column, rows []map[string]any) error {
	w := csv.NewWriter(os.Stdout)
	head := make([]string, len(columns))
	for i, c := range columns {
		head[i] = c.Key
	}
	if err := w.Write(head); err != nil {
		return err
	}
	for _, row := range rows {
		rec := make([]string, len(columns))
		for i, c := range columns {
			switch v := row[c.Key].(type) {
			case nil:
			case []string:
				rec[i] = strings.Join(v, ";")
			default:
				rec[i] = fmt.Sprint(v)
			}
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// visibleColumns drops the raw-value columns unless --raw was given, so tables
// and CSV do not show empty columns for fields that were stripped.
func visibleColumns(cols []Column) []Column {
	if Raw {
		return cols
	}
	out := make([]Column, 0, len(cols))
	for _, c := range cols {
		if strings.HasSuffix(c.Key, "_raw") || strings.HasSuffix(c.Key, "_unix") {
			continue
		}
		out = append(out, c)
	}
	return out
}
