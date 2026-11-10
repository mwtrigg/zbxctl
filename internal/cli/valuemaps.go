package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	vmHost, vmTemplate, vmName, vmSearch               string
	vmMapEq, vmMapGE, vmMapLE, vmMapRange, vmMapRegexp []string
	vmDefault                                          string
	vmLimit                                            int
	vmDelHost, vmDelTemplate                           string

	reName, reTest string
	reExpr         []string
	reCase         bool
)

// ownerID resolves --host / --template to the ID that owns a value map. empty
// when neither was given.
func vmOwner(c *api.Client, host, template string) (id, label string, err error) {
	switch {
	case host != "" && template != "":
		return "", "", &usageError{"give --host or --template, not both"}
	case host != "":
		h, err := resources.GetHost(c, host)
		if err != nil {
			return "", "", err
		}
		return h.HostID, "host " + h.Host, nil
	case template != "":
		tid, err := resources.ResolveTemplateID(c, template)
		if err != nil {
			return "", "", err
		}
		return tid, "template " + template, nil
	}
	return "", "", nil
}

func mappingsFromFlags() ([]resources.Mapping, error) {
	var out []resources.Mapping
	add := func(kind string, specs []string) error {
		for _, s := range specs {
			m, err := resources.ParseMapping(kind, s)
			if err != nil {
				return &usageError{err.Error()}
			}
			out = append(out, m)
		}
		return nil
	}
	for _, p := range []struct {
		k string
		v []string
	}{{"equals", vmMapEq}, {"ge", vmMapGE}, {"le", vmMapLE}, {"range", vmMapRange}, {"regexp", vmMapRegexp}} {
		if err := add(p.k, p.v); err != nil {
			return nil, err
		}
	}
	if vmDefault != "" {
		m, _ := resources.ParseMapping("default", vmDefault)
		out = append(out, m)
	}
	return out, nil
}

func addMappingFlags(c *cobra.Command) {
	f := c.Flags()
	f.StringArrayVar(&vmMapEq, "map", nil, "map a value to text: VALUE=TEXT, e.g. 0=Down (repeatable)")
	f.StringArrayVar(&vmMapGE, "map-ge", nil, "values greater than or equal: VALUE=TEXT (repeatable)")
	f.StringArrayVar(&vmMapLE, "map-le", nil, "values less than or equal: VALUE=TEXT (repeatable)")
	f.StringArrayVar(&vmMapRange, "map-range", nil, "a range of values: FROM-TO=TEXT, e.g. 1-5=Low (repeatable)")
	f.StringArrayVar(&vmMapRegexp, "map-regexp", nil, "values matching a regular expression: PATTERN=TEXT (repeatable)")
	f.StringVar(&vmDefault, "default", "", "text shown when no mapping matches")
}

func deleteValueMapSpec() bulkSpec {
	return bulkSpec{
		Name: "delete-valuemap", Tier: "write",
		Cols: []output.Column{{Key: "valuemapid", Header: "ID"}, {Key: "owner", Header: "OWNER"}, {Key: "name", Header: "NAME"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, ref string) (*step, error) {
			vm, err := findValueMap(c, ref, vmDelHost, vmDelTemplate)
			if err != nil {
				return nil, err
			}
			return &step{
				Key: vm.ID, Desc: fmt.Sprintf("delete value map %q (%s) with %d mapping(s)", vm.Name, vm.Owner, len(vm.Mappings)),
				Before: map[string]any{"valuemapid": vm.ID, "owner": vm.Owner, "owner_id": vm.HostID, "name": vm.Name, "mappings": vm.MappingLabels(), "mappings_raw": vm.Mappings},
				Apply:  func() error { return resources.DeleteValueMap(c, vm.ID) },
				Proves: true,
				Verify: func() error {
					left, err := resources.GetValueMaps(c, resources.ValueMapFilter{IDs: []string{vm.ID}})
					if err != nil {
						return err
					}
					if len(left) > 0 {
						return errors.New("value map still exists")
					}
					return nil
				},
				Row: map[string]any{"valuemapid": vm.ID, "owner": vm.Owner, "name": vm.Name, "status": "deleted"},
			}, nil
		},
	}
}

// findValueMap resolves a value map by ID, or by name within a host/template.
func findValueMap(c *api.Client, ref, host, template string) (*resources.ValueMap, error) {
	f := resources.ValueMapFilter{}
	if isNumericStr(ref) {
		f.IDs = []string{ref}
	} else {
		ownerID, _, err := vmOwner(c, host, template)
		if err != nil {
			return nil, err
		}
		if ownerID == "" {
			return nil, &usageError{"a value map name needs --host or --template (names are only unique within one); or pass the numeric ID"}
		}
		f.HostIDs, f.Name = []string{ownerID}, ref
	}
	vms, err := resources.GetValueMaps(c, f)
	if err != nil {
		return nil, err
	}
	if len(vms) == 0 {
		return nil, &resources.NotFoundError{Resource: "value map", Query: ref}
	}
	return &vms[0], nil
}

func init() {
	// ── get valuemaps ──
	getVM := &cobra.Command{
		Use: "valuemaps", Short: "List value maps (they belong to a host or template; filter with --host or --template)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := newClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			id, _, err := vmOwner(c, vmHost, vmTemplate)
			if err != nil {
				return cliFail(outFmt, err)
			}
			f := resources.ValueMapFilter{Search: vmSearch, Limit: effectiveLimit(vmLimit, ctx)}
			if id != "" {
				f.HostIDs = []string{id}
			}
			vms, err := resources.GetValueMaps(c, f)
			if err != nil {
				return cliFail(outFmt, err)
			}
			rows := make([]map[string]any, len(vms))
			for i, v := range vms {
				rows[i] = map[string]any{"valuemapid": v.ID, "owner": v.Owner, "name": v.Name, "mappings": v.MappingLabels()}
			}
			return output.Print(outFmt, []output.Column{{Key: "valuemapid", Header: "ID"}, {Key: "owner", Header: "OWNER"}, {Key: "name", Header: "NAME"}, {Key: "mappings", Header: "MAPPINGS"}}, rows)
		},
	}
	getVM.Flags().StringVar(&vmHost, "host", "", "only value maps of this host")
	getVM.Flags().StringVar(&vmTemplate, "template", "", "only value maps of this template")
	getVM.Flags().StringVar(&vmSearch, "search", "", "substring of the name")
	getVM.Flags().IntVar(&vmLimit, "limit", 0, "max results")
	getCmd.AddCommand(getVM)

	// ── create valuemap ──
	createVM := &cobra.Command{
		Use: "valuemap", Short: "Create a value map on a host or template", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			id, label, err := vmOwner(c, vmHost, vmTemplate)
			if err != nil {
				return cliFail(outFmt, err)
			}
			if id == "" {
				return cliPrintErr(outFmt, "give --host or --template: a value map belongs to one of them", output.ExitUsage)
			}
			ms, err := mappingsFromFlags()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if len(ms) == 0 {
				return cliPrintErr(outFmt, "give at least one mapping: --map 0=Down", output.ExitUsage)
			}
			labels := make([]string, len(ms))
			for i, m := range ms {
				labels[i] = m.Label()
			}
			w, proceed, err := beginWrite(cmd, ctx, "create-valuemap", label+" "+vmName, fmt.Sprintf("create value map %q on %s with %s", vmName, label, strings.Join(labels, ", ")),
				map[string]any{"creates": "value map", "owner": label, "name": vmName, "mappings": labels})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			vid, err := resources.CreateValueMap(c, id, vmName, ms)
			if err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "valuemapid", Header: "ID"}, {Key: "owner", Header: "OWNER"}, {Key: "name", Header: "NAME"}, {Key: "mappings", Header: "MAPPINGS"}},
				[]map[string]any{{"valuemapid": vid, "owner": label, "name": vmName, "mappings": labels}})
		},
	}
	createVM.Flags().StringVar(&vmName, "name", "", "value map name (required)")
	createVM.Flags().StringVar(&vmHost, "host", "", "the host that owns it")
	createVM.Flags().StringVar(&vmTemplate, "template", "", "the template that owns it")
	addMappingFlags(createVM)
	_ = createVM.MarkFlagRequired("name")
	createCmd.AddCommand(createVM)

	// ── update valuemap ──
	updateVM := &cobra.Command{
		Use: "valuemap <id|name>", Short: "Rename a value map and/or replace all of its mappings", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			vm, err := findValueMap(c, args[0], vmHost, vmTemplate)
			if err != nil {
				return cliFail(outFmt, err)
			}
			ms, err := mappingsFromFlags()
			if err != nil {
				return cliFail(outFmt, err)
			}
			var name *string
			var what []string
			if cmd.Flags().Changed("name") {
				name = &vmName
				what = append(what, fmt.Sprintf("rename to %q", vmName))
			}
			if len(ms) > 0 {
				labels := make([]string, len(ms))
				for i, m := range ms {
					labels[i] = m.Label()
				}
				what = append(what, fmt.Sprintf("replace the mappings %s with %s", strings.Join(vm.MappingLabels(), ", "), strings.Join(labels, ", ")))
			} else {
				ms = nil
			}
			if len(what) == 0 {
				return cliPrintErr(outFmt, "nothing to change: give --name and/or mappings (they replace all existing ones)", output.ExitUsage)
			}
			w, proceed, err := beginWrite(cmd, ctx, "update-valuemap", vm.ID, fmt.Sprintf("update value map %q (%s): %s", vm.Name, vm.Owner, strings.Join(what, "; ")),
				map[string]any{"valuemapid": vm.ID, "owner": vm.Owner, "name": vm.Name, "mappings": vm.MappingLabels()})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			if err := resources.UpdateValueMap(c, vm.ID, name, ms); err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "valuemapid", Header: "ID"}, {Key: "changes", Header: "CHANGES"}},
				[]map[string]any{{"valuemapid": vm.ID, "owner": vm.Owner, "changes": what}})
		},
	}
	updateVM.Flags().StringVar(&vmName, "name", "", "new name")
	updateVM.Flags().StringVar(&vmHost, "host", "", "the host that owns it, when given by name")
	updateVM.Flags().StringVar(&vmTemplate, "template", "", "the template that owns it, when given by name")
	addMappingFlags(updateVM)
	updateCmd.AddCommand(updateVM)

	// ── delete valuemap ──
	delVM := &cobra.Command{
		Use: "valuemap <id|name>...", Short: "Delete value maps (names need --host or --template)", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runBulk(cmd, args, deleteValueMapSpec()) },
	}
	delVM.Flags().StringVar(&vmDelHost, "host", "", "the host that owns them, when given by name")
	delVM.Flags().StringVar(&vmDelTemplate, "template", "", "the template that owns them, when given by name")
	addBulkFlags(delVM)
	deleteCmd.AddCommand(delVM)
}

// ───────────────────────── global regular expressions ─────────────────────────

func regexpExprs(caseSensitive bool) ([]resources.Expression, error) {
	var out []resources.Expression
	for _, s := range reExpr {
		e, err := resources.ParseExpression(s, caseSensitive)
		if err != nil {
			return nil, &usageError{err.Error()}
		}
		out = append(out, e)
	}
	return out, nil
}

func regexpRow(r resources.Regexp) map[string]any {
	return map[string]any{"regexpid": r.ID, "name": r.Name, "test_string": r.TestString, "expressions": r.ExpressionLabels()}
}

var regexpCols = []output.Column{{Key: "regexpid", Header: "ID"}, {Key: "name", Header: "NAME"}, {Key: "expressions", Header: "EXPRESSIONS"}}

func deleteRegexpSpec() bulkSpec {
	return bulkSpec{
		Name: "delete-regexp", Tier: "write",
		Cols: []output.Column{{Key: "regexpid", Header: "ID"}, {Key: "name", Header: "NAME"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, ref string) (*step, error) {
			r, err := resources.FindRegexp(c, ref)
			if err != nil {
				return nil, err
			}
			return &step{
				Key: r.ID, Desc: fmt.Sprintf("delete regular expression %q (%d expression(s))", r.Name, len(r.Expressions)),
				Before: map[string]any{"regexpid": r.ID, "name": r.Name, "test_string": r.TestString, "expressions": r.ExpressionLabels(), "expressions_raw": r.Expressions},
				Apply:  func() error { return resources.DeleteRegexp(c, r.ID) },
				Proves: true,
				Verify: func() error {
					_, err := resources.FindRegexp(c, r.ID)
					var nfe *resources.NotFoundError
					if errors.As(err, &nfe) {
						return nil
					}
					if err != nil {
						return err
					}
					return errors.New("regular expression still exists")
				},
				Row: map[string]any{"regexpid": r.ID, "name": r.Name, "status": "deleted"},
			}, nil
		},
	}
}

func init() {
	getRe := &cobra.Command{
		Use: "regexps", Short: "List global regular expressions (used by discovery filters and log items)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, _, err := newClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			rs, err := resources.GetRegexps(c)
			if err != nil {
				return cliFail(outFmt, err)
			}
			rows := make([]map[string]any, len(rs))
			for i, r := range rs {
				rows[i] = regexpRow(r)
			}
			return output.Print(outFmt, regexpCols, rows)
		},
	}
	getCmd.AddCommand(getRe)

	createRe := &cobra.Command{
		Use: "regexp", Short: "Create a global regular expression", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			exprs, err := regexpExprs(reCase)
			if err != nil {
				return cliFail(outFmt, err)
			}
			if len(exprs) == 0 {
				return cliPrintErr(outFmt, "give at least one --expr TYPE:EXPRESSION, e.g. --expr 'matches:^(ext4|xfs)$'", output.ExitUsage)
			}
			labels := make([]string, len(exprs))
			for i, e := range exprs {
				labels[i] = e.Label()
			}
			w, proceed, err := beginWrite(cmd, ctx, "create-regexp", reName, fmt.Sprintf("create regular expression %q with %s", reName, strings.Join(labels, ", ")),
				map[string]any{"creates": "regular expression", "name": reName, "expressions": labels})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			id, err := resources.CreateRegexp(c, reName, reTest, exprs)
			if err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, regexpCols, []map[string]any{{"regexpid": id, "name": reName, "expressions": labels}})
		},
	}
	createRe.Flags().StringVar(&reName, "name", "", "name (required)")
	createRe.Flags().StringArrayVar(&reExpr, "expr", nil, "TYPE:EXPRESSION with TYPE contains, any, not-contains, matches or not-matches (repeatable)")
	createRe.Flags().BoolVar(&reCase, "case-sensitive", false, "match case (default: ignore case)")
	createRe.Flags().StringVar(&reTest, "test-string", "", "a sample string stored with it for testing")
	_ = createRe.MarkFlagRequired("name")
	createCmd.AddCommand(createRe)

	updateRe := &cobra.Command{
		Use: "regexp <id|name>", Short: "Rename a global regular expression and/or replace all of its expressions", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			r, err := resources.FindRegexp(c, args[0])
			if err != nil {
				return cliFail(outFmt, err)
			}
			exprs, err := regexpExprs(reCase)
			if err != nil {
				return cliFail(outFmt, err)
			}
			var name *string
			var what []string
			if cmd.Flags().Changed("name") {
				name = &reName
				what = append(what, fmt.Sprintf("rename to %q", reName))
			}
			if len(exprs) > 0 {
				labels := make([]string, len(exprs))
				for i, e := range exprs {
					labels[i] = e.Label()
				}
				what = append(what, fmt.Sprintf("replace the expressions %s with %s", strings.Join(r.ExpressionLabels(), ", "), strings.Join(labels, ", ")))
			} else {
				exprs = nil
			}
			if len(what) == 0 {
				return cliPrintErr(outFmt, "nothing to change: give --name and/or --expr (it replaces all existing expressions)", output.ExitUsage)
			}
			w, proceed, err := beginWrite(cmd, ctx, "update-regexp", r.ID, fmt.Sprintf("update regular expression %q: %s", r.Name, strings.Join(what, "; ")),
				map[string]any{"regexpid": r.ID, "name": r.Name, "expressions": r.ExpressionLabels()})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			if err := resources.UpdateRegexp(c, r.ID, name, exprs); err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "regexpid", Header: "ID"}, {Key: "changes", Header: "CHANGES"}}, []map[string]any{{"regexpid": r.ID, "changes": what}})
		},
	}
	updateRe.Flags().StringVar(&reName, "name", "", "new name")
	updateRe.Flags().StringArrayVar(&reExpr, "expr", nil, "TYPE:EXPRESSION (repeatable); replaces all existing expressions")
	updateRe.Flags().BoolVar(&reCase, "case-sensitive", false, "match case (default: ignore case)")
	updateCmd.AddCommand(updateRe)

	delRe := &cobra.Command{
		Use: "regexp <id|name>...", Short: "Delete global regular expressions", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runBulk(cmd, args, deleteRegexpSpec()) },
	}
	addBulkFlags(delRe)
	deleteCmd.AddCommand(delRe)
}
