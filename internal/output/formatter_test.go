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
