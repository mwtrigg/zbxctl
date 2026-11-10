package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	smHost, smMacro, smValue, smValueFile, smDesc string
	smGlobal, smSecret, smVault, smValueStdin     bool
	dmHost                                        string
	dmGlobal                                      bool
)

var macroType = func() string {
	switch {
	case smSecret:
		return "1"
	case smVault:
		return "2"
	}
	return "0"
}

func macroValue(cmd *cobra.Command) (string, bool, error) {
	n := 0
	for _, on := range []bool{cmd.Flags().Changed("value"), smValueFile != "", smValueStdin} {
		if on {
			n++
		}
	}
	if n > 1 {
		return "", false, &usageError{"use only one of --value, --value-file and --value-stdin"}
	}
	switch {
	case smValueFile != "":
		b, err := os.ReadFile(smValueFile)
		if err != nil {
			return "", false, fmt.Errorf("reading value file: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), true, nil
	case smValueStdin:
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", false, &usageError{"no value on stdin"}
		}
		return strings.TrimRight(line, "\r\n"), true, nil
	case cmd.Flags().Changed("value"):
		if smSecret {
			return "", false, &usageError{"a secret value must come from --value-file or --value-stdin, not the command line"}
		}
		return smValue, true, nil
	}
	return "", false, nil
}

func init() {
	set := &cobra.Command{
		Use: "macro", Short: "Create or update a user macro on a host (or a global macro); secret values never go on the command line", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			if smGlobal == (smHost != "") {
				return cliFail(outFmt, &usageError{"give exactly one of --host and --global"})
			}
			if !strings.HasPrefix(smMacro, "{$") || !strings.HasSuffix(smMacro, "}") {
				return cliFail(outFmt, &usageError{fmt.Sprintf("invalid macro %q: use the form {$NAME} or {$NAME:context}", smMacro)})
			}
			if smSecret && smVault {
				return cliFail(outFmt, &usageError{"--secret and --vault are exclusive"})
			}
			val, haveVal, err := macroValue(cmd)
			if err != nil {
				return cliFail(outFmt, err)
			}
			var hostID, scope string
			if smGlobal {
				scope = "global macros"
			} else {
				h, err := resources.GetHost(c, smHost)
				if err != nil {
					return cliFail(outFmt, err)
				}
				hostID, scope = h.HostID, "host "+h.Host
			}
			existing, err := resources.FindMacro(c, hostID, smGlobal, smMacro)
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !haveVal {
				if existing == nil {
					return cliFail(outFmt, &usageError{"a new macro needs a value: --value, --value-file or --value-stdin"})
				}
				if existing.Type == "1" {
					return cliFail(outFmt, &usageError{"an existing secret macro cannot keep its old value (the API never returns it): give a new --value-file or --value-stdin"})
				}
				val = existing.Value // changing only the description or type
			}
			typ := macroType()
			if !smSecret && !smVault && existing != nil && !cmd.Flags().Changed("secret") {
				typ = existing.Type // keep the type unless one was asked for
			}
			if typ == "1" && cmd.Flags().Changed("value") {
				return cliFail(outFmt, &usageError{"this macro is secret: give the value with --value-file or --value-stdin, not on the command line"})
			}
			shown := "'" + val + "'"
			if typ == "1" {
				shown = "(secret)"
			}
			verb := "create"
			if existing != nil {
				verb = "update"
			}
			// What a person needs to put the old value back. A secret's value is never
			// returned by the API, so it cannot be saved.
			before := map[string]any{"scope": scope, "macro": smMacro, "existed": existing != nil}
			if existing != nil {
				before["type"] = map[string]string{"0": "text", "1": "secret", "2": "vault"}[existing.Type]
				before["description"] = existing.Description
				if existing.Type == "1" {
					before["value"] = "(secret, not recoverable)"
				} else {
					before["value"] = existing.Value
				}
			}
			w, proceed, err := beginWrite(cmd, ctx, "set-macro", scope+" "+smMacro, fmt.Sprintf("%s macro %s on %s to %s", verb, smMacro, scope, shown), before)
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			in := resources.MacroInput{HostID: hostID, Global: smGlobal, Macro: smMacro, Value: val, Type: typ}
			if cmd.Flags().Changed("description") {
				in.Description = &smDesc
			}
			action, err := resources.SetMacro(c, in)
			if err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "scope", Header: "SCOPE"}, {Key: "macro", Header: "MACRO"}, {Key: "action", Header: "ACTION"}},
				[]map[string]any{{"scope": scope, "macro": smMacro, "action": action, "type": map[string]string{"0": "text", "1": "secret", "2": "vault"}[typ]}})
		},
	}
	f := set.Flags()
	f.StringVar(&smHost, "host", "", "host name or ID")
	f.BoolVar(&smGlobal, "global", false, "a global macro instead of a host macro")
	f.StringVar(&smMacro, "macro", "", "macro name, e.g. {$SNMP_COMMUNITY} (required)")
	f.StringVar(&smValue, "value", "", "text macro value")
	f.StringVar(&smValueFile, "value-file", "", "read the value from this file (use for secrets)")
	f.BoolVar(&smValueStdin, "value-stdin", false, "read the value from stdin (use for secrets)")
	f.BoolVar(&smSecret, "secret", false, "make it a secret macro (write-only in Zabbix)")
	f.BoolVar(&smVault, "vault", false, "make it a vault macro (the value is a vault path)")
	f.StringVar(&smDesc, "description", "", "macro description")
	_ = set.MarkFlagRequired("macro")
	setCmd.AddCommand(set)

	del := &cobra.Command{
		Use: "macro <{$NAME}>...", Short: "Delete user macros from a host (or global macros)", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dmGlobal == (dmHost != "") {
				return cliFail(output.Format(flagOutput), &usageError{"give exactly one of --host and --global"})
			}
			return runBulk(cmd, args, deleteMacroSpec())
		},
	}
	del.Flags().StringVar(&dmHost, "host", "", "host name or ID")
	del.Flags().BoolVar(&dmGlobal, "global", false, "delete global macros")
	addBulkFlags(del)
	deleteCmd.AddCommand(del)
}

var setCmd = &cobra.Command{Use: "set", Short: "Create or update a setting"}

func init() { rootCmd.AddCommand(setCmd) }

func deleteMacroSpec() bulkSpec {
	return bulkSpec{
		Name: "delete-macro", Tier: "write",
		Cols: []output.Column{{Key: "scope", Header: "SCOPE"}, {Key: "macro", Header: "MACRO"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, name string) (*step, error) {
			var hostID, scope string
			if dmGlobal {
				scope = "global"
			} else {
				h, err := resources.GetHost(c, dmHost)
				if err != nil {
					return nil, err
				}
				hostID, scope = h.HostID, h.Host
			}
			m, err := resources.FindMacro(c, hostID, dmGlobal, name)
			if err != nil {
				return nil, err
			}
			if m == nil {
				return nil, &resources.NotFoundError{Resource: "macro", Query: name + " on " + scope}
			}
			val := m.Value
			if m.Type == "1" {
				val = "(secret, not recoverable)"
			}
			return &step{
				Key: m.ID, Desc: fmt.Sprintf("delete macro %s from %s", m.Macro, scope),
				Before: map[string]any{"scope": scope, "macro": m.Macro, "value": val, "type": m.TypeLabel(), "description": m.Description},
				Apply:  func() error { return resources.DeleteMacro(c, m.ID, dmGlobal) },
				Proves: true,
				Verify: func() error {
					left, err := resources.FindMacro(c, hostID, dmGlobal, name)
					if err != nil {
						return err
					}
					if left != nil {
						return errors.New("macro still exists")
					}
					return nil
				},
				Row: map[string]any{"scope": scope, "macro": m.Macro, "status": "deleted"},
			}, nil
		},
	}
}
