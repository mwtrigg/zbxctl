package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	itHost, itName, itKey, itType, itValueType, itDelay, itUnits, itHistory, itTrends, itDesc, itParams, itMaster string
	itStatus                                                                                                      string
	tgHost, tgDesc, tgExpr, tgSeverity, tgComments                                                                string
	tgManual                                                                                                      bool
	delItemHost                                                                                                   string
)

func keys(m map[string]string) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, "|")
}

func statusCode(s string) (string, bool) {
	switch s {
	case "enabled":
		return "0", true
	case "disabled":
		return "1", true
	}
	return "", false
}

func init() {
	// ── create item ──
	createItem := &cobra.Command{
		Use: "item", Short: "Create an item on a host", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			typ, ok := resources.ItemTypes[itType]
			if !ok {
				return cliPrintErr(outFmt, fmt.Sprintf("invalid --type %q: use %s", itType, keys(resources.ItemTypes)), output.ExitUsage)
			}
			vt, ok := resources.ValueTypes[itValueType]
			if !ok {
				return cliPrintErr(outFmt, fmt.Sprintf("invalid --value-type %q: use %s", itValueType, keys(resources.ValueTypes)), output.ExitUsage)
			}
			if itType != "trapper" && itType != "dependent" && itDelay == "" {
				return cliPrintErr(outFmt, "--delay is required for this item type (e.g. 1m)", output.ExitUsage)
			}
			if itType == "calculated" && itParams == "" {
				return cliPrintErr(outFmt, "a calculated item needs --formula", output.ExitUsage)
			}
			h, err := resources.GetHost(c, itHost)
			if err != nil {
				return cliFail(outFmt, err)
			}
			p := map[string]any{"hostid": h.HostID, "name": itName, "key_": itKey, "type": typ, "value_type": vt}
			if itDelay != "" {
				p["delay"] = itDelay
			}
			if itUnits != "" {
				p["units"] = itUnits
			}
			if itHistory != "" {
				p["history"] = itHistory
			}
			if itTrends != "" {
				p["trends"] = itTrends
			}
			if itDesc != "" {
				p["description"] = itDesc
			}
			if itParams != "" {
				p["params"] = itParams
			}
			if itType == "dependent" {
				if itMaster == "" {
					return cliPrintErr(outFmt, "a dependent item needs --master-item (its key or ID)", output.ExitUsage)
				}
				m, err := resources.GetItemFull(c, itMaster, h.HostID)
				if err != nil {
					return cliFail(outFmt, err)
				}
				p["master_itemid"] = m.ID
			}
			if st, ok := statusCode(itStatus); ok {
				p["status"] = st
			}
			if itType == "agent" || itType == "simple" {
				if p["interfaceid"], err = resources.MainAgentInterfaceID(c, h.HostID); err != nil {
					return cliFail(outFmt, err)
				}
			}
			w, proceed, err := beginWrite(cmd, ctx, "create-item", h.Host+" "+itKey, fmt.Sprintf("create %s item %q (%s) on %s", itType, itName, itKey, h.Host),
				map[string]any{"creates": "item", "host": h.Host, "name": itName, "key": itKey, "type": itType, "undo": "delete item " + itKey + " --host " + h.Host})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			id, err := resources.CreateItem(c, p)
			if err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "itemid", Header: "ITEM_ID"}, {Key: "host", Header: "HOST"}, {Key: "name", Header: "NAME"}, {Key: "key_", Header: "KEY"}},
				[]map[string]any{{"itemid": id, "host": h.Host, "name": itName, "key_": itKey}})
		},
	}
	f := createItem.Flags()
	f.StringVar(&itHost, "host", "", "host name or ID (required)")
	f.StringVar(&itName, "name", "", "item name (required)")
	f.StringVar(&itKey, "key", "", "item key (required)")
	f.StringVar(&itType, "type", "agent", "item type: "+keys(resources.ItemTypes))
	f.StringVar(&itValueType, "value-type", "float", "value type: "+keys(resources.ValueTypes))
	f.StringVar(&itDelay, "delay", "", "update interval, e.g. 30s, 1m (not for trapper or dependent)")
	f.StringVar(&itUnits, "units", "", "units shown with values")
	f.StringVar(&itHistory, "history", "", "how long to keep history, e.g. 7d (default from Zabbix)")
	f.StringVar(&itTrends, "trends", "", "how long to keep trends, e.g. 365d")
	f.StringVar(&itDesc, "description", "", "description")
	f.StringVar(&itParams, "formula", "", "formula of a calculated item, e.g. avg(//system.cpu.util,5m)")
	f.StringVar(&itMaster, "master-item", "", "master item (key on --host, or ID) of a dependent item")
	f.StringVar(&itStatus, "status", "", "enabled | disabled (default enabled)")
	for _, r := range []string{"host", "name", "key"} {
		_ = createItem.MarkFlagRequired(r)
	}
	itParams = ""
	createCmd.AddCommand(createItem)

	// ── update item ──
	updateItem := &cobra.Command{
		Use: "item <id|key>", Short: "Change an item (give --host when using a key)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			hostID := ""
			if itHost != "" {
				h, err := resources.GetHost(c, itHost)
				if err != nil {
					return cliFail(outFmt, err)
				}
				hostID = h.HostID
			}
			it, err := resources.GetItemFull(c, args[0], hostID)
			if err != nil {
				return cliFail(outFmt, err)
			}
			fields := map[string]any{}
			var changes []string
			set := func(flag, field, old, val string) {
				if cmd.Flags().Changed(flag) {
					fields[field] = val
					changes = append(changes, fmt.Sprintf("%s %q -> %q", field, old, val))
				}
			}
			set("name", "name", it.Name, itName)
			set("delay", "delay", it.Delay, itDelay)
			set("units", "units", it.Units, itUnits)
			set("history", "history", it.History, itHistory)
			set("trends", "trends", it.Trends, itTrends)
			set("description", "description", it.Description, itDesc)
			set("formula", "params", it.Params, itParams)
			if cmd.Flags().Changed("status") {
				st, ok := statusCode(itStatus)
				if !ok {
					return cliPrintErr(outFmt, "invalid --status: use enabled or disabled", output.ExitUsage)
				}
				fields["status"] = st
				changes = append(changes, fmt.Sprintf("status -> %s", itStatus))
			}
			if len(fields) == 0 {
				return cliPrintErr(outFmt, "nothing to change: give at least one of --name, --delay, --units, --history, --trends, --description, --formula, --status", output.ExitUsage)
			}
			w, proceed, err := beginWrite(cmd, ctx, "update-item", it.ID, fmt.Sprintf("update item %q (%s) on %s: %s", it.Name, it.Key, it.Host, strings.Join(changes, ", ")), fieldsOf(it))
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			if err := resources.UpdateItem(c, it.ID, fields); err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "itemid", Header: "ITEM_ID"}, {Key: "host", Header: "HOST"}, {Key: "key_", Header: "KEY"}, {Key: "changes", Header: "CHANGES"}},
				[]map[string]any{{"itemid": it.ID, "host": it.Host, "key_": it.Key, "changes": changes}})
		},
	}
	g := updateItem.Flags()
	g.StringVar(&itHost, "host", "", "host name or ID, needed when the item is given by key")
	g.StringVar(&itName, "name", "", "new name")
	g.StringVar(&itDelay, "delay", "", "new update interval")
	g.StringVar(&itUnits, "units", "", "new units")
	g.StringVar(&itHistory, "history", "", "new history retention")
	g.StringVar(&itTrends, "trends", "", "new trends retention")
	g.StringVar(&itDesc, "description", "", "new description")
	g.StringVar(&itParams, "formula", "", "new formula of a calculated item")
	g.StringVar(&itStatus, "status", "", "enabled | disabled")
	updateCmd.AddCommand(updateItem)

	// ── delete item ──
	delItem := &cobra.Command{
		Use: "item <id|key>...", Short: "Delete items (discovered items go through 'delete lost items')", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runBulk(cmd, args, deleteItemSpec()) },
	}
	delItem.Flags().StringVar(&delItemHost, "host", "", "host name or ID, needed when items are given by key")
	addBulkFlags(delItem)
	deleteCmd.AddCommand(delItem)

	// ── create / update / delete trigger ──
	createTrigger := &cobra.Command{
		Use: "trigger", Short: "Create a trigger from an expression", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			sev, err := parseSeverity(tgSeverity)
			if err != nil {
				return cliFail(outFmt, &usageError{err.Error()})
			}
			p := map[string]any{"description": tgDesc, "expression": tgExpr, "priority": sev}
			if tgComments != "" {
				p["comments"] = tgComments
			}
			if tgManual {
				p["manual_close"] = 1
			}
			w, proceed, err := beginWrite(cmd, ctx, "create-trigger", tgDesc, fmt.Sprintf("create trigger %q with expression %s", tgDesc, tgExpr),
				map[string]any{"creates": "trigger", "description": tgDesc, "expression": tgExpr, "severity": severityLabel(fmt.Sprint(sev))})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			id, err := resources.CreateTrigger(c, p)
			if err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "triggerid", Header: "TRIGGER_ID"}, {Key: "description", Header: "DESCRIPTION"}},
				[]map[string]any{{"triggerid": id, "description": tgDesc, "expression": tgExpr, "severity": severityLabel(sev)}})
		},
	}
	t := createTrigger.Flags()
	t.StringVar(&tgDesc, "description", "", "trigger name, may use macros like {HOST.NAME} (required)")
	t.StringVar(&tgExpr, "expression", "", "trigger expression, e.g. last(/web-01/system.cpu.util)>90 (required)")
	t.StringVar(&tgSeverity, "severity", "average", "severity: 0-5 or not_classified|information|warning|average|high|disaster")
	t.StringVar(&tgComments, "comments", "", "comments")
	t.BoolVar(&tgManual, "manual-close", false, "allow closing problems of this trigger by hand")
	_ = createTrigger.MarkFlagRequired("description")
	_ = createTrigger.MarkFlagRequired("expression")
	createCmd.AddCommand(createTrigger)

	updateTrigger := &cobra.Command{
		Use: "trigger <id>", Short: "Change a trigger", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			tr, err := resources.GetTriggerFull(c, args[0])
			if err != nil {
				return cliFail(outFmt, err)
			}
			fields := map[string]any{}
			var changes []string
			if cmd.Flags().Changed("description") {
				fields["description"] = tgDesc
				changes = append(changes, fmt.Sprintf("description %q -> %q", tr.Description, tgDesc))
			}
			if cmd.Flags().Changed("expression") {
				fields["expression"] = tgExpr
				changes = append(changes, fmt.Sprintf("expression %q -> %q", tr.Expression, tgExpr))
			}
			if cmd.Flags().Changed("severity") {
				sev, err := parseSeverity(tgSeverity)
				if err != nil {
					return cliFail(outFmt, &usageError{err.Error()})
				}
				fields["priority"] = sev
				changes = append(changes, fmt.Sprintf("severity %s -> %s", severityLabel(tr.Priority), severityLabel(fmt.Sprint(sev))))
			}
			if cmd.Flags().Changed("comments") {
				fields["comments"] = tgComments
				changes = append(changes, "comments changed")
			}
			if cmd.Flags().Changed("manual-close") {
				v := 0
				if tgManual {
					v = 1
				}
				fields["manual_close"] = v
				changes = append(changes, fmt.Sprintf("manual_close -> %v", tgManual))
			}
			if len(fields) == 0 {
				return cliPrintErr(outFmt, "nothing to change: give at least one of --description, --expression, --severity, --comments, --manual-close", output.ExitUsage)
			}
			w, proceed, err := beginWrite(cmd, ctx, "update-trigger", tr.ID, fmt.Sprintf("update trigger %s on %s: %s", tr.ID, strings.Join(tr.Hosts, ","), strings.Join(changes, "; ")), fieldsOf(tr))
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			if err := resources.UpdateTrigger(c, tr.ID, fields); err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "triggerid", Header: "TRIGGER_ID"}, {Key: "changes", Header: "CHANGES"}},
				[]map[string]any{{"triggerid": tr.ID, "hosts": tr.Hosts, "changes": changes}})
		},
	}
	u := updateTrigger.Flags()
	u.StringVar(&tgDesc, "description", "", "new name")
	u.StringVar(&tgExpr, "expression", "", "new expression")
	u.StringVar(&tgSeverity, "severity", "", "new severity")
	u.StringVar(&tgComments, "comments", "", "new comments")
	u.BoolVar(&tgManual, "manual-close", false, "allow (or with =false, forbid) manual close")
	updateCmd.AddCommand(updateTrigger)

	delTrigger := &cobra.Command{
		Use: "trigger <id>...", Short: "Delete triggers (discovered triggers go through 'delete lost triggers')", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runBulk(cmd, args, deleteTriggerSpec()) },
	}
	addBulkFlags(delTrigger)
	deleteCmd.AddCommand(delTrigger)
}

func deleteItemSpec() bulkSpec {
	return bulkSpec{
		Name: "delete-item", Tier: "write",
		Cols: []output.Column{{Key: "itemid", Header: "ITEM_ID"}, {Key: "host", Header: "HOST"}, {Key: "key_", Header: "KEY"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, target string) (*step, error) {
			hostID := ""
			if delItemHost != "" {
				h, err := resources.GetHost(c, delItemHost)
				if err != nil {
					return nil, err
				}
				hostID = h.HostID
			}
			it, err := resources.GetItemFull(c, target, hostID)
			if err != nil {
				return nil, err
			}
			if it.Flags == "4" {
				return nil, &usageError{fmt.Sprintf("item %s is a discovered item; use 'delete lost items' (only lost ones can be deleted)", it.ID)}
			}
			if it.TemplateID != "" && it.TemplateID != "0" {
				return nil, &usageError{fmt.Sprintf("item %q on %s comes from a template; delete it on the template", it.Key, it.Host)}
			}
			return &step{
				Key: it.ID, Desc: fmt.Sprintf("delete item %q (%s) on %s", it.Name, it.Key, it.Host),
				Before: map[string]any{"itemid": it.ID, "host": it.Host, "name": it.Name, "key": it.Key, "type": it.Type, "value_type": it.ValueType, "delay": it.Delay, "units": it.Units, "history": it.History, "trends": it.Trends, "description": it.Description, "params": it.Params},
				Apply:  func() error { return resources.DeleteItem(c, it.ID) },
				Verify: func() error {
					_, err := resources.GetItemFull(c, it.ID, "")
					var nfe *resources.NotFoundError
					if errors.As(err, &nfe) {
						return nil
					}
					if err != nil {
						return err
					}
					return errors.New("item still exists")
				},
				Row: map[string]any{"itemid": it.ID, "host": it.Host, "key_": it.Key, "status": "deleted"},
			}, nil
		},
	}
}

func deleteTriggerSpec() bulkSpec {
	return bulkSpec{
		Name: "delete-trigger", Tier: "write",
		Cols: []output.Column{{Key: "triggerid", Header: "TRIGGER_ID"}, {Key: "hosts", Header: "HOSTS"}, {Key: "description", Header: "DESCRIPTION"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, target string) (*step, error) {
			t, err := resources.GetTriggerFull(c, target)
			if err != nil {
				return nil, err
			}
			if t.Flags == "4" {
				return nil, &usageError{fmt.Sprintf("trigger %s is a discovered trigger; use 'delete lost triggers' (only lost ones can be deleted)", t.ID)}
			}
			if t.TemplateID != "" && t.TemplateID != "0" {
				return nil, &usageError{fmt.Sprintf("trigger %s comes from a template; delete it on the template", t.ID)}
			}
			return &step{
				Key: t.ID, Desc: fmt.Sprintf("delete trigger %s %q (%s)", t.ID, t.Description, strings.Join(t.Hosts, ",")),
				Before: map[string]any{"triggerid": t.ID, "description": t.Description, "expression": t.Expression, "severity": severityLabel(t.Priority), "status": enabledLabel(t.Status), "comments": t.Comments, "manual_close": t.ManualClose == "1", "hosts": t.Hosts},
				Apply:  func() error { return resources.DeleteTrigger(c, t.ID) },
				Verify: func() error {
					_, err := resources.GetTriggerFull(c, t.ID)
					var nfe *resources.NotFoundError
					if errors.As(err, &nfe) {
						return nil
					}
					if err != nil {
						return err
					}
					return errors.New("trigger still exists")
				},
				Row: map[string]any{"triggerid": t.ID, "hosts": t.Hosts, "description": t.Description, "status": "deleted"},
			}, nil
		},
	}
}
