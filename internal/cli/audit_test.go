package cli

import (
	"strings"
	"testing"
)

// Every command in the manifest that changes Zabbix (it has an apply flag) must
// be wired for the audit trail: it takes --no-state and --state-dir, which is how
// a before-state file gets written. A new write command that forgets the audit
// fails here instead of shipping with the README's promise broken.
func TestEveryWriteCommandIsWiredForTheAuditTrail(t *testing.T) {
	// Writes that are not about a Zabbix object, or that keep their own record.
	exempt := map[string]string{
		"state prune":     "deletes local files; it is its own record",
		"upgrade":         "replaces the binary",
		"run script":      "runs a script; guarded by its own confirmation and allowlist",
		"context use":     "changes the config file",
		"export template": "writes files",
		"export host":     "writes files",
	}
	checked := 0
	for _, c := range capabilities {
		if !isWriteCommand(c) {
			continue
		}
		if why, ok := exempt[c.Command]; ok {
			t.Logf("skipping %q: %s", c.Command, why)
			continue
		}
		cmd, _, err := rootCmd.Find(strings.Fields(c.Command))
		if err != nil || cmd == rootCmd {
			t.Errorf("%q is in the manifest but is not a command", c.Command)
			continue
		}
		checked++
		for _, flag := range []string{"no-state", "state-dir"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("%q changes Zabbix but has no --%s: it is not wired for the before-state file", c.Command, flag)
			}
		}
	}
	if checked < 30 {
		t.Fatalf("only %d write commands checked; the manifest walk is broken", checked)
	}
}

// Each single-object write saves exactly one before-state file under --apply and
// none in a dry run, and the file marks the target applied.
func TestSingleObjectWritesSaveABeforeState(t *testing.T) {
	handler := func(call rpcCall) fakeReply {
		switch call.Method {
		case "user.get":
			return fakeReply{Result: `[{"userid":"7","username":"jdoe","name":"J","surname":"D","roleid":"1","role":{"name":"User role"}}]`}
		case "user.checkAuthentication":
			return fakeReply{Result: `{"userid":"1","username":"Admin"}`}
		case "user.delete":
			return fakeReply{Result: `{"userids":["7"]}`}
		case "hostgroup.get":
			return fakeReply{Result: `[]`}
		case "hostgroup.create":
			return fakeReply{Result: `{"groupids":["50"]}`}
		case "host.get":
			return fakeReply{Result: `[{"hostid":"10","host":"web1","name":"Web 1","status":"0","tags":[{"tag":"a","value":"b"}],"hostgroups":[],"parentTemplates":[],"interfaces":[],"tags":[],"macros":[]}]`}
		case "host.update":
			return fakeReply{Result: `{"hostids":["10"]}`}
		case "usermacro.get":
			return fakeReply{Result: `[]`}
		case "usermacro.create":
			return fakeReply{Result: `{"hostmacroids":["3"]}`}
		}
		return fakeReply{}
	}
	cases := map[string][]string{
		"delete user":  {"delete", "user", "jdoe"},
		"create group": {"create", "group", "--name", "Linux/New"},
		"tag host":     {"tag", "host", "web1", "--tag", "env", "--value", "prod"},
		"set macro":    {"set", "macro", "--host", "web1", "--macro", "{$X}", "--value", "1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeZabbix{Handler: handler}
			if _, err := runFake(t, f, args...); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if got := xdgStateFiles(t); len(got) != 0 {
				t.Fatalf("a dry run must not write a before-state file: %v", got)
			}
			if _, err := runFake(t, f, append(args, "--apply")...); err != nil {
				t.Fatalf("apply: %v", err)
			}
			d := readOnlyStateDoc(t)
			if len(d.Items) != 1 || d.Items[0].Status != "applied" {
				t.Fatalf("the record must exist and say applied: %+v", d)
			}
		})
	}
}
