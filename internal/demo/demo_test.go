package demo

import "testing"

// The demo must hold the story its help text points at.
func TestDemoData(t *testing.T) {
	s := &server{}
	s.seed()
	if n := len(s.call("problem.get", object{}).([]object)); n != 7 {
		t.Errorf("open problems: %d, want 7", n)
	}
	trig := s.call("trigger.get", object{"triggerids": []any{"4741"}}).([]object)
	if len(trig) != 1 || trig[0]["items"].([]object)[0]["status"] != "1" {
		t.Errorf("trigger 4741 must exist with its item disabled: %v", trig)
	}
	hosts := s.call("host.get", object{"filter": object{"host": []any{"web-03"}}}).([]object)
	if len(hosts) != 1 || hosts[0]["status"] != "0" {
		t.Fatalf("host web-03 must exist and be monitored: %v", hosts)
	}
	s.call("host.update", object{"hostid": "10503", "status": 1})
	if got := s.call("host.get", object{"hostids": []any{"10503"}}).([]object)[0]["status"]; got != "1" {
		t.Errorf("a change is kept in memory: status %v", got)
	}
	if v := s.call("apiinfo.version", object{}); v != "7.4.0" {
		t.Errorf("version: %v", v)
	}
	if out, ok := s.call("no.such.method", object{}).([]any); !ok || len(out) != 0 {
		t.Error("an unknown method answers with an empty list")
	}
}
