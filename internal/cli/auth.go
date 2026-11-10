package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/config"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication commands",
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status and connectivity",
	RunE:  runAuthStatus,
}

var authCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Compare the permission tier you declared with what the Zabbix token's role lets it do (exit 8 if the tier is stronger)",
	Long: `zbxctl's permission tiers (readonly, operator, write) are checked by zbxctl itself, so they
guard against mistakes. What Zabbix will actually refuse is decided by the role (and its rules)
and the user group rights of the user the token belongs to. This command reads the role and its
rules (no change is made) and exits 8, with the reasons, when the declared tier is stronger than
what the server enforces: for a readonly tier, a role that can change the configuration (Admin,
Super admin), run scripts, act on problems (acknowledge, close, suppress, change severity,
comment), or call API methods other than reads. It does not read user group rights.

For an agent, give it a token of a user whose role turns off script execution and the problem
actions and whose API access is an allow list of the read methods (*.get).`,
	RunE: runAuthCheck,
}

func init() {
	authCmd.AddCommand(authStatusCmd, authCheckCmd)
}

// roleTypeLabel names the Zabbix role types.
func roleTypeLabel(t string) string {
	switch t {
	case "1":
		return "user"
	case "2":
		return "admin"
	case "3":
		return "super admin"
	}
	return "unknown"
}

// roleInfo is what the role's type and rules say a token can do.
type roleInfo struct {
	Type           string
	RulesKnown     bool
	Scripts        bool // can execute scripts
	ProblemActions bool // can acknowledge, close, suppress, change severity or comment on problems
	APIReadsOnly   bool // API access is off, or an allow list of read methods only
}

func ruleStr(v any) string { return strings.TrimSpace(fmt.Sprint(v)) }

// parseRoleRules reads the "rules" object of a Zabbix role (7.x layout).
func parseRoleRules(rules map[string]any, r *roleInfo) {
	r.RulesKnown = true
	defaultAllow := ruleStr(rules["actions.default_access"]) != "0"
	allowed := func(name string) bool {
		if list, ok := rules["actions"].([]any); ok {
			for _, e := range list {
				if m, ok := e.(map[string]any); ok && ruleStr(m["name"]) == name {
					return ruleStr(m["status"]) == "1"
				}
			}
		}
		return defaultAllow
	}
	r.Scripts = allowed("execute_scripts")
	for _, n := range []string{"acknowledge_problems", "close_problems", "change_severity", "add_problem_comments", "suppress_problems"} {
		if allowed(n) {
			r.ProblemActions = true
		}
	}
	switch {
	case ruleStr(rules["api.access"]) == "0":
		r.APIReadsOnly = true // no API access at all
	case ruleStr(rules["api.mode"]) == "1": // allow list
		r.APIReadsOnly = true
		methods, _ := rules["api"].([]any)
		for _, m := range methods {
			ms := ruleStr(m)
			if !strings.HasSuffix(ms, ".get") && ms != "apiinfo.version" && ms != "user.checkAuthentication" {
				r.APIReadsOnly = false
			}
		}
		if len(methods) == 0 {
			r.APIReadsOnly = true
		}
	}
}

// tierIssues lists why the declared tier is stronger than what the role enforces.
func tierIssues(tier string, r roleInfo) []string {
	if tier == "" {
		tier = "readonly"
	}
	var issues []string
	canConfigure := r.Type == "2" || r.Type == "3"
	if (tier == "readonly" || tier == "operator") && canConfigure {
		issues = append(issues, "the role ("+roleTypeLabel(r.Type)+") can change the configuration through any other client")
	}
	if tier == "readonly" {
		if !r.RulesKnown {
			issues = append(issues, "the role's rules could not be read, so script execution, problem actions and the API allow list are unknown")
		} else {
			if r.Scripts {
				issues = append(issues, "the role can execute scripts")
			}
			if r.ProblemActions {
				issues = append(issues, "the role can acknowledge, close, suppress or comment on problems")
			}
			if !r.APIReadsOnly {
				issues = append(issues, "the role's API access is not limited to read methods (*.get)")
			}
		}
	}
	return issues
}

func runAuthCheck(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	tier := ctx.Permissions
	if tier == "" {
		tier = "readonly"
	}
	row := map[string]any{"declared_tier": tier}
	uid, uname, err := c.CurrentUser()
	if err != nil {
		return cliFail(outFmt, err)
	}
	row["user"] = uname
	raw, err := c.Call("user.get", map[string]any{"userids": []string{uid}, "output": []string{"userid", "username"}, "selectRole": []string{"roleid", "name", "type"}})
	var users []struct {
		Role struct {
			ID   string `json:"roleid"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"role"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &users)
	}
	var issues []string
	if err != nil || len(users) == 0 {
		issues = append(issues, "could not read this token's role, so the tier cannot be compared with what Zabbix enforces")
		if err != nil {
			row["error"] = err.Error()
		}
	} else {
		role := roleInfo{Type: users[0].Role.Type}
		row["role"], row["role_type"] = users[0].Role.Name, roleTypeLabel(role.Type)
		row["can_change_configuration"] = role.Type == "2" || role.Type == "3"
		if rraw, rerr := c.Call("role.get", map[string]any{"roleids": []string{users[0].Role.ID}, "output": []string{"roleid"}, "selectRules": "extend"}); rerr == nil {
			var roles []struct {
				Rules map[string]any `json:"rules"`
			}
			if json.Unmarshal(rraw, &roles) == nil && len(roles) == 1 && roles[0].Rules != nil {
				parseRoleRules(roles[0].Rules, &role)
			}
		}
		if role.RulesKnown {
			row["scripts_allowed"], row["problem_actions_allowed"], row["api_reads_only"] = role.Scripts, role.ProblemActions, role.APIReadsOnly
		}
		issues = tierIssues(tier, role)
		if len(issues) == 0 && tier == "write" && role.Type == "1" {
			row["note"] = "this token's role (user) cannot change the configuration, so most writes will be refused by Zabbix even though the tier allows them."
		}
	}
	row["ok"] = len(issues) == 0
	row["issues"] = issues
	if len(issues) > 0 {
		row["note"] = "the tier " + tier + " is stronger than what Zabbix enforces for this token: " + strings.Join(issues, "; ") + ". The tier is enforced only by zbxctl: anything that skips zbxctl can do these things."
	} else if row["note"] == nil {
		row["note"] = ""
	}
	cols := []output.Column{{Key: "user", Header: "USER"}, {Key: "role", Header: "ROLE"}, {Key: "declared_tier", Header: "TIER"}, {Key: "ok", Header: "OK"}, {Key: "note", Header: "NOTE"}}
	if err := output.Print(outFmt, cols, []map[string]any{row}); err != nil {
		return err
	}
	if len(issues) > 0 {
		return &cliErr{code: output.ExitPermission} // so a script or CI step can gate on it
	}
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	outFmt := output.Format(flagOutput)

	cfg, ctx, err := config.LoadConfig(flagContext)
	if err != nil {
		return cliFail(outFmt, err)
	}

	// Apply --insecure flag.
	if flagInsecure {
		f := false
		ctx.TLSVerify = &f
	}

	authVia := "token"
	if ctx.APIToken == "" {
		authVia = "password"
	}

	// Resolve active context name for display.
	ctxName := cfg.CurrentContext
	if flagContext != "" {
		ctxName = flagContext
	}

	connected := false
	apiVersion := ""

	client, err := api.NewClient(context.Background(), ctx)
	if err == nil {
		if v, verr := client.APIVersion(); verr == nil {
			connected = true
			apiVersion = v
		}
	}

	cols := []output.Column{
		{Key: "url", Header: "URL"},
		{Key: "context", Header: "CONTEXT"},
		{Key: "auth_via", Header: "AUTH_VIA"},
		{Key: "connected", Header: "CONNECTED"},
		{Key: "api_version", Header: "API_VERSION"},
		{Key: "supported", Header: "SUPPORTED"},
	}

	row := map[string]any{
		"url":            ctx.URL,
		"context":        ctxName,
		"auth_via":       authVia,
		"connected":      connected,
		"api_version":    apiVersion,
		"pinned_version": ctx.APIVersion,
		"tested_with":    api.Tested.String(),
		"minimum":        api.MinSupported.String(),
		"mode":           map[bool]string{true: "apply", false: "dry-run"}[ctx.ApplyByDefault()],
	}
	if v, err := api.ParseVersion(apiVersion); err == nil {
		newer, cerr := v.Check()
		row["supported"] = cerr == nil
		row["newer_than_tested"] = newer
	}

	return output.Print(outFmt, cols, []map[string]any{row})
}
