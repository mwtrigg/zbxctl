package resources

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// TriggerDiagnosis explains why a trigger's problem is (still) open.
type TriggerDiagnosis struct {
	TriggerID    string
	Description  string
	Severity     string
	Enabled      bool
	InProblem    bool
	State        string // trigger state: "0" normal, "1" unknown
	Error        string
	ManualClose  bool
	Hosts        []string
	OpenProblems []DiagProblem
	Items        []DiagItem
	Reasons      []string
	Recoverable  bool // false when a problem on this trigger can never recover by itself
}

// DiagProblem is an open problem on the trigger.
type DiagProblem struct {
	EventID      string
	Since        string // Unix
	Acknowledged bool
	Suppressed   bool
	Until        string // suppress_until Unix; "" or "0" = indefinitely
	Maintenance  string // maintenance window name when suppressed by one
}

// DiagItem is an item the trigger depends on.
type DiagItem struct {
	ItemID  string
	Key     string
	Enabled bool
	State   string // "0" normal, "1" not supported
	Error   string
}

// DiagnoseTrigger gathers everything that decides whether a trigger's problem
// can recover, and turns it into plain-language reasons.
func DiagnoseTrigger(c *api.Client, triggerID string) (*TriggerDiagnosis, error) {
	raw, err := c.Call("trigger.get", map[string]any{
		"triggerids":        []string{triggerID},
		"output":            []string{"triggerid", "description", "priority", "status", "value", "state", "error", "manual_close"},
		"selectHosts":       []string{"host", "status", "maintenance_status", "maintenanceid"},
		"selectItems":       []string{"itemid", "key_", "status", "state", "error"},
		"expandDescription": true,
	})
	if err != nil {
		return nil, fmt.Errorf("trigger.get: %w", err)
	}
	var ts []struct {
		TriggerID   string `json:"triggerid"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
		Status      string `json:"status"`
		Value       string `json:"value"`
		State       string `json:"state"`
		Error       string `json:"error"`
		ManualClose string `json:"manual_close"`
		Hosts       []struct {
			Host   string `json:"host"`
			Status string `json:"status"`
			Maint  string `json:"maintenance_status"`
		} `json:"hosts"`
		Items []struct {
			ItemID string `json:"itemid"`
			Key    string `json:"key_"`
			Status string `json:"status"`
			State  string `json:"state"`
			Error  string `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil, fmt.Errorf("parsing trigger: %w", err)
	}
	if len(ts) == 0 {
		return nil, &NotFoundError{Resource: "trigger", Query: triggerID}
	}
	t := ts[0]
	d := &TriggerDiagnosis{
		TriggerID: t.TriggerID, Description: t.Description, Severity: t.Priority, Enabled: t.Status == "0",
		InProblem: t.Value == "1", State: t.State, Error: t.Error, ManualClose: t.ManualClose == "1", Recoverable: true,
	}
	hostsOff, hostsInMaint := 0, 0
	for _, h := range t.Hosts {
		d.Hosts = append(d.Hosts, h.Host)
		if h.Status == "1" {
			hostsOff++
		}
		if h.Maint == "1" {
			hostsInMaint++
		}
	}
	itemsOff, itemsBad := 0, 0
	for _, it := range t.Items {
		d.Items = append(d.Items, DiagItem{ItemID: it.ItemID, Key: it.Key, Enabled: it.Status == "0", State: it.State, Error: it.Error})
		if it.Status == "1" {
			itemsOff++
		} else if it.State == "1" {
			itemsBad++
		}
	}

	// Open problems on this trigger, with suppression details.
	raw, err = c.Call("problem.get", map[string]any{
		"objectids":             []string{triggerID},
		"output":                []string{"eventid", "clock", "acknowledged", "suppressed"},
		"selectSuppressionData": []string{"maintenanceid", "suppress_until"},
	})
	if err != nil {
		return nil, fmt.Errorf("problem.get: %w", err)
	}
	var ps []struct {
		EventID      string `json:"eventid"`
		Clock        string `json:"clock"`
		Acknowledged string `json:"acknowledged"`
		Suppressed   string `json:"suppressed"`
		Suppression  []struct {
			MaintenanceID string `json:"maintenanceid"`
			Until         string `json:"suppress_until"`
		} `json:"suppression_data"`
	}
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("parsing problems: %w", err)
	}
	maintNames := map[string]string{}
	for _, p := range ps {
		dp := DiagProblem{EventID: p.EventID, Since: p.Clock, Acknowledged: p.Acknowledged == "1", Suppressed: p.Suppressed == "1"}
		for _, s := range p.Suppression {
			if s.Until > dp.Until {
				dp.Until = s.Until
			}
			if s.MaintenanceID != "" && s.MaintenanceID != "0" {
				if _, ok := maintNames[s.MaintenanceID]; !ok {
					if m, err := ResolveMaintenance(c, s.MaintenanceID); err == nil {
						maintNames[s.MaintenanceID] = m.Name
					} else {
						maintNames[s.MaintenanceID] = "maintenance " + s.MaintenanceID
					}
				}
				dp.Maintenance = maintNames[s.MaintenanceID]
			}
		}
		d.OpenProblems = append(d.OpenProblems, dp)
	}

	add := func(reason string, neverRecovers bool) {
		d.Reasons = append(d.Reasons, reason)
		if neverRecovers {
			d.Recoverable = false
		}
	}
	if !d.Enabled {
		add("the trigger is disabled: it is no longer evaluated, so its problem can never recover; only a manual close can end it", true)
	}
	if len(t.Hosts) > 0 && hostsOff == len(t.Hosts) {
		add("every host of the trigger is disabled: nothing is evaluated, so the problem can never recover", true)
	}
	if len(t.Items) > 0 && itemsOff == len(t.Items) {
		add("every item the trigger uses is disabled: no new data arrives, so the problem can never recover", true)
	} else if itemsOff > 0 {
		add(fmt.Sprintf("%d of %d items the trigger uses are disabled", itemsOff, len(t.Items)), false)
	}
	if itemsBad > 0 {
		var errs []string
		for _, it := range d.Items {
			if it.State == "1" && it.Error != "" {
				errs = append(errs, it.Key+": "+it.Error)
			}
		}
		add(fmt.Sprintf("%d item(s) are not supported, so the trigger cannot be evaluated (%s)", itemsBad, strings.Join(errs, "; ")), false)
	}
	if d.State == "1" {
		add("the trigger is in an unknown state: "+d.Error, false)
	}
	if hostsInMaint > 0 {
		add("a host is in maintenance", false)
	}
	for _, p := range d.OpenProblems {
		if p.Suppressed {
			who := "suppressed"
			if p.Maintenance != "" {
				who = "suppressed by maintenance " + p.Maintenance
			}
			if p.Until != "" && p.Until != "0" {
				who += " until " + p.Until + " (Unix)"
			} else {
				who += " indefinitely"
			}
			add(fmt.Sprintf("event %s is %s", p.EventID, who), false)
		}
	}
	if len(d.OpenProblems) > 0 && !d.ManualClose {
		add("manual close is off for this trigger: the problem can only be closed by recovering, or by enabling manual close", false)
	}
	if len(d.OpenProblems) == 0 && d.InProblem {
		add("the trigger is in PROBLEM state but has no open problem event", false)
	}
	if len(d.OpenProblems) == 0 && !d.InProblem {
		d.Reasons = append(d.Reasons, "no open problem: the trigger is OK")
	}
	return d, nil
}
