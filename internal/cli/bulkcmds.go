package cli

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// hostBefore is the state worth keeping before a host is changed or deleted.
func hostBefore(h *resources.Host) map[string]any {
	tags := make([]string, len(h.Tags))
	for i, t := range h.Tags {
		tags[i] = tagLabel(t)
	}
	macros := make([]string, len(h.Macros))
	for i, m := range h.Macros {
		if m.Type == "1" {
			macros[i] = m.Macro + "=******"
		} else {
			macros[i] = m.Macro + "=" + m.Value
		}
	}
	ifaces := make([]string, len(h.Interfaces))
	for i, f := range h.Interfaces {
		addr := f.IP
		if addr == "" {
			addr = f.DNS
		}
		ifaces[i] = addr + ":" + f.Port
	}
	return map[string]any{
		"hostid": h.HostID, "host": h.Host, "name": h.Name, "status": hostStatusLabel(h.Status),
		"groups": h.GroupNames(), "templates": h.TemplateNames(), "tags": tags, "macros": macros, "interfaces": ifaces,
	}
}

var hostCols = []output.Column{
	{Key: "hostid", Header: "HOST_ID"}, {Key: "host", Header: "HOST"}, {Key: "name", Header: "NAME"}, {Key: "status", Header: "STATUS"},
}

func hostStatusSpec(enable bool) bulkSpec {
	verb, want := "disable", "unmonitored"
	if enable {
		verb, want = "enable", "monitored"
	}
	return bulkSpec{
		Name: verb + "-host", Tier: "operator", Cols: hostCols,
		Prepare: func(c *api.Client, target string) (*step, error) {
			h, err := resources.GetHost(c, target)
			if err != nil {
				return nil, err
			}
			return &step{
				Key: h.HostID, Desc: fmt.Sprintf("%s host %q (%s)", verb, h.Name, h.Host), Before: hostBefore(h),
				Apply: func() error {
					if enable {
						return resources.EnableHost(c, h.HostID)
					}
					return resources.DisableHost(c, h.HostID)
				},
				Verify: func() error {
					now, err := resources.GetHost(c, h.HostID)
					if err != nil {
						return err
					}
					if got := hostStatusLabel(now.Status); got != want {
						return fmt.Errorf("status is %s, expected %s", got, want)
					}
					return nil
				},
				Row: map[string]any{"hostid": h.HostID, "host": h.Host, "name": h.Name, "status": want},
			}, nil
		},
	}
}

func triggerStatusSpec(enable bool) bulkSpec {
	verb, want := "disable", "1"
	label := "disabled"
	if enable {
		verb, want, label = "enable", "0", "enabled"
	}
	return bulkSpec{
		Name: verb + "-trigger", Tier: "operator",
		Cols: []output.Column{{Key: "triggerid", Header: "TRIGGER_ID"}, {Key: "host", Header: "HOST"}, {Key: "description", Header: "DESCRIPTION"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, target string) (*step, error) {
			t, err := resources.GetTriggerByID(c, target)
			if err != nil {
				return nil, err
			}
			host := ""
			if len(t.Hosts) > 0 {
				host = t.Hosts[0].Host
			}
			return &step{
				Key: t.TriggerID, Desc: fmt.Sprintf("%s trigger %s %q (host: %s)", verb, t.TriggerID, t.Description, host),
				Before: map[string]any{"triggerid": t.TriggerID, "description": t.Description, "host": host, "status": enabledLabel(t.Status)},
				Apply: func() error {
					if enable {
						return resources.EnableTrigger(c, t.TriggerID)
					}
					return resources.DisableTrigger(c, t.TriggerID)
				},
				Verify: func() error {
					now, err := resources.GetTriggerByID(c, t.TriggerID)
					if err != nil {
						return err
					}
					if now.Status != want {
						return fmt.Errorf("trigger is not %s", label)
					}
					return nil
				},
				Row: map[string]any{"triggerid": t.TriggerID, "host": host, "description": t.Description, "status": label},
			}, nil
		},
	}
}

func deleteHostSpec() bulkSpec {
	return bulkSpec{
		Name: "delete-host", Tier: "write", Cols: hostCols,
		Prepare: func(c *api.Client, target string) (*step, error) {
			h, err := resources.GetHost(c, target)
			if err != nil {
				return nil, err
			}
			return &step{
				Key: h.HostID, Desc: fmt.Sprintf("delete host %q (id: %s)", h.Host, h.HostID), Before: hostBefore(h),
				Apply: func() error { _, err := resources.DeleteHost(c, h.HostID); return err },
				Verify: func() error {
					_, err := resources.GetHost(c, h.HostID)
					var nfe *resources.NotFoundError
					if errors.As(err, &nfe) {
						return nil
					}
					if err != nil {
						return err
					}
					return errors.New("host still exists")
				},
				Row: map[string]any{"hostid": h.HostID, "host": h.Host, "name": h.Name, "status": "deleted"},
			}, nil
		},
	}
}

func maintenanceDeleteSpec() bulkSpec {
	return bulkSpec{
		Name: "maintenance-delete", Tier: "operator",
		Cols: []output.Column{{Key: "maintenanceid", Header: "MAINTENANCE_ID"}, {Key: "name", Header: "NAME"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, target string) (*step, error) {
			m, err := resources.ResolveMaintenance(c, target)
			if err != nil {
				return nil, err
			}
			return &step{
				Key: m.MaintenanceID, Desc: fmt.Sprintf("delete maintenance %q (%s)", m.Name, m.MaintenanceID),
				Before: map[string]any{"maintenanceid": m.MaintenanceID, "name": m.Name, "active_since": fmtUnix(m.ActiveSince), "active_till": fmtUnix(m.ActiveTill), "description": m.Description},
				Apply:  func() error { return resources.DeleteMaintenance(c, m.MaintenanceID) },
				Verify: func() error {
					_, err := resources.ResolveMaintenance(c, m.MaintenanceID)
					var nfe *resources.NotFoundError
					if errors.As(err, &nfe) {
						return nil
					}
					if err != nil {
						return err
					}
					return errors.New("maintenance still exists")
				},
				Row: map[string]any{"maintenanceid": m.MaintenanceID, "name": m.Name, "status": "deleted"},
			}, nil
		},
	}
}

// maintenanceEndSpec extends or shortens one-time maintenance windows. Either
// by a duration (delta) or to an absolute end time (until).
func maintenanceEndSpec(verb string, by time.Duration, until int64) bulkSpec {
	return bulkSpec{
		Name: "maintenance-" + verb, Tier: "operator",
		Cols: []output.Column{{Key: "maintenanceid", Header: "MAINTENANCE_ID"}, {Key: "name", Header: "NAME"}, {Key: "active_till", Header: "NEW_END"}},
		Prepare: func(c *api.Client, target string) (*step, error) {
			m, err := resources.ResolveMaintenance(c, target)
			if err != nil {
				return nil, err
			}
			oldTill, _ := strconv.ParseInt(m.ActiveTill, 10, 64)
			newTill := until
			if newTill == 0 {
				switch verb {
				case "extend":
					newTill = oldTill + int64(by.Seconds())
				default:
					newTill = oldTill - int64(by.Seconds())
				}
			}
			if newTill == oldTill {
				return nil, &usageError{fmt.Sprintf("maintenance %q already ends at %s", m.Name, fmtUnix(m.ActiveTill))}
			}
			newLabel := fmtUnix(strconv.FormatInt(newTill, 10))
			return &step{
				Key:    m.MaintenanceID,
				Desc:   fmt.Sprintf("%s maintenance %q from %s to %s", verb, m.Name, fmtUnix(m.ActiveTill), newLabel),
				Before: map[string]any{"maintenanceid": m.MaintenanceID, "name": m.Name, "active_since": fmtUnix(m.ActiveSince), "active_till": fmtUnix(m.ActiveTill), "hosts": m.HostNames(), "groups": m.GroupNames()},
				Apply:  func() error { return resources.SetMaintenanceEnd(c, m, newTill) },
				Verify: func() error {
					now, err := resources.ResolveMaintenance(c, m.MaintenanceID)
					if err != nil {
						return err
					}
					if now.ActiveTill != strconv.FormatInt(newTill, 10) {
						return fmt.Errorf("end is %s, expected %s", fmtUnix(now.ActiveTill), newLabel)
					}
					return nil
				},
				Row: map[string]any{"maintenanceid": m.MaintenanceID, "name": m.Name, "active_till": newLabel, "previous_end": fmtUnix(m.ActiveTill)},
			}, nil
		},
	}
}

// ackSpec builds the acknowledge flow from the validated flags in p.
func ackSpec(p resources.AcknowledgeParams, verb string) bulkSpec {
	return bulkSpec{
		Name: "acknowledge", Tier: "operator",
		Cols: []output.Column{{Key: "eventid", Header: "EVENT_ID"}, {Key: "action", Header: "ACTION"}, {Key: "outcome", Header: "OUTCOME"}},
		Prepare: func(c *api.Client, target string) (*step, error) {
			st, err := resources.GetEventAckState(c, target)
			if err != nil {
				return nil, err
			}
			q := p
			q.EventID = st.EventID
			outcome := string(q.Action)
			desc := fmt.Sprintf("%s event %s (%s)", verb, st.EventID, st.Name)
			// Closing only works on triggers that allow it; otherwise acknowledge instead.
			if q.Action == resources.ActionAcknowledge && q.Close {
				if st.Closed {
					outcome = "already closed"
					q.Close = false
				} else if !st.ManualClose {
					q.Close = false
					outcome = "acknowledged only: the trigger does not allow manual close"
					desc += " [trigger has manual close off, will acknowledge only]"
				} else {
					outcome = "closed"
				}
			}
			s := &step{
				Key: st.EventID, Desc: desc,
				Before: map[string]any{"eventid": st.EventID, "name": st.Name, "acknowledged": st.Acknowledged, "closed": st.Closed, "triggerid": st.TriggerID, "manual_close": st.ManualClose},
				Apply:  func() error { return resources.AcknowledgeEvent(c, q) },
			}
			row := map[string]any{"eventid": st.EventID, "action": string(q.Action), "outcome": outcome}
			if q.Message != "" {
				row["message"] = q.Message
			}
			if q.Severity >= 0 {
				row["severity"] = severityLabel(strconv.Itoa(q.Severity))
			}
			s.Row = row
			wantClosed := q.Action == resources.ActionAcknowledge && q.Close
			switch {
			case wantClosed:
				s.VerifyAfter = 15 * time.Second // closing is asynchronous
				s.Verify = func() error {
					now, err := resources.GetEventAckState(c, st.EventID)
					if err != nil {
						return err
					}
					if !now.Closed {
						return errors.New("problem not closed yet")
					}
					return nil
				}
			case q.Action == resources.ActionAcknowledge || q.Action == resources.ActionUnacknowledge:
				want := q.Action == resources.ActionAcknowledge
				s.Verify = func() error {
					now, err := resources.GetEventAckState(c, st.EventID)
					if err != nil {
						return err
					}
					if now.Acknowledged != want {
						return fmt.Errorf("acknowledged is %v, expected %v", now.Acknowledged, want)
					}
					return nil
				}
			}
			return s, nil
		},
	}
}

func init() {
	for _, c := range []*cobra.Command{enableHostCmd, disableHostCmd, enableTriggerCmd, disableTriggerCmd, acknowledgeCmd, maintenanceDeleteCmd, deleteHostCmd} {
		addBulkFlags(c)
		c.Use = c.Use + "..."
	}
}

// bulkFlagSchemas adds the shared bulk flags to the manifest entries of the
// commands that accept several targets.
func init() {
	extra := []FlagSchema{
		{Name: "id-file", Type: "string", Description: "Read more targets from this file, one per line (# comments allowed).", Default: ""},
		{Name: "expect", Type: "int", Description: "Refuse to run unless exactly N distinct targets resolve; checked before anything changes.", Default: -1},
		{Name: "state-dir", Type: "string", Description: "Where the before-state file is written (default ~/.local/state/zbxctl).", Default: ""},
		{Name: "no-state", Type: "bool", Description: "Do not write a before-state file.", Default: false},
		{Name: "no-verify", Type: "bool", Description: "Skip re-reading each target after the change.", Default: false},
	}
	bulk := map[string]bool{"enable host": true, "disable host": true, "enable trigger": true, "disable trigger": true, "acknowledge": true, "maintenance delete": true, "maintenance extend": true, "maintenance shorten": true, "delete host": true, "delete lost items": true, "delete lost triggers": true}
	for i := range capabilities {
		if bulk[capabilities[i].Command] {
			capabilities[i].Flags = append(capabilities[i].Flags, extra...)
			capabilities[i].Description += " Accepts several targets; with --apply it saves a before-state file and verifies each change."
		}
	}
}
