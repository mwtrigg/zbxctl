package resources

import (
	"testing"
)

func TestGroupByValueType(t *testing.T) {
	items := []itemRef{
		{ItemID: "1", HostID: "10", Host: "web-01", Key: "cpu.load[1]", ValueType: "0"},
		{ItemID: "2", HostID: "10", Host: "web-01", Key: "agent.version", ValueType: "1"},
		{ItemID: "3", HostID: "11", Host: "web-02", Key: "cpu.load[1]", ValueType: "0"},
	}
	groups := groupByValueType(items)
	if len(groups[0]) != 2 {
		t.Errorf("expected 2 float items, got %d", len(groups[0]))
	}
	if len(groups[1]) != 1 {
		t.Errorf("expected 1 string item, got %d", len(groups[1]))
	}
}

func TestMergeAndSort(t *testing.T) {
	a := []HistoryRecord{
		{ItemID: "1", Clock: "100", ClockUnix: 100},
		{ItemID: "1", Clock: "300", ClockUnix: 300},
	}
	b := []HistoryRecord{
		{ItemID: "2", Clock: "200", ClockUnix: 200},
	}
	merged := mergeAndSort(a, b)
	if len(merged) != 3 {
		t.Fatalf("expected 3 records, got %d", len(merged))
	}
	if merged[0].ClockUnix != 100 || merged[1].ClockUnix != 200 || merged[2].ClockUnix != 300 {
		t.Errorf("records not sorted by clock: %v", merged)
	}
}
