package output

import "testing"

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
