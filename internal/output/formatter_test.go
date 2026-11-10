package output

import (
	"strings"
	"testing"
)

func TestStripRawHidesRawAndUnixUnlessRequested(t *testing.T) {
	rows := []map[string]any{{"status": "monitored", "status_raw": "0", "clock": "2026-01-01T00:00:00Z", "clock_unix": "1"}}
	Raw = false
	got := stripRaw(rows)[0]
	if _, ok := got["status_raw"]; ok || got["status"] != "monitored" || got["clock_unix"] != nil {
		t.Fatalf("raw not stripped: %v", got)
	}
	Raw = true
	t.Cleanup(func() { Raw = false })
	if got := stripRaw(rows)[0]; got["status_raw"] != "0" {
		t.Fatalf("raw dropped with --raw: %v", got)
	}
}

func TestDefaultFormatIsJSON(t *testing.T) {
	if DefaultFormat() != FormatJSON {
		t.Fatal("default must be json")
	}
}

func TestPrintableMakesControlCharactersVisible(t *testing.T) {
	if got := Printable("plain text\twith\nnewlines"); got != "plain text\twith\nnewlines" {
		t.Fatalf("clean text must be untouched: %q", got)
	}
	got := Printable("ok\x1b[2K\rhidden\x07")
	for _, c := range []string{"\x1b", "\r", "\x07"} {
		if strings.Contains(got, c) {
			t.Fatalf("control character %q reached the output: %q", c, got)
		}
	}
	if !strings.Contains(got, `\x1b`) || !strings.Contains(got, `\x0d`) {
		t.Fatalf("escapes must be shown: %q", got)
	}
}

func TestPrintableEscapesBidiAndInvisibleCharacters(t *testing.T) {
	for name, in := range map[string]string{
		"right-to-left override": "ok\u202egnp.exe",
		"isolate":                "a\u2066b\u2069",
		"zero-width space":       "pay\u200bpal",
		"zero-width joiner":      "a\u200db",
		"byte order mark":        "\ufeffhost",
		"line separator":         "a\u2028b",
		"left-to-right mark":     "a\u200eb",
	} {
		got := Printable(in)
		if got == in || strings.ContainsAny(got, "\u202e\u2066\u2069\u200b\u200d\ufeff\u2028\u200e") {
			t.Errorf("%s: invisible or reordering characters must be shown as escapes: %q", name, got)
		}
		if !strings.Contains(got, `\u`) {
			t.Errorf("%s: want a visible \\uXXXX escape: %q", name, got)
		}
	}
	if got := Printable("café — ünïcode 日本語 ok"); got != "café — ünïcode 日本語 ok" {
		t.Errorf("ordinary text, accents and other scripts stay as they are: %q", got)
	}
}
