package cli

import (
	"context"
	"encoding/json"

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
	Short: "Compare the permission tier you declared with what the Zabbix token's role lets it do",
	Long: `zbxctl's permission tiers (readonly, operator, write) are checked by zbxctl itself, so they
guard against mistakes. What Zabbix will actually refuse is decided by the role and the user group
rights of the user the token belongs to. This command reads that role (no change is made) and warns
when the declared tier is stronger than what the server enforces: for example a context marked
readonly whose token belongs to an Admin or Super admin user, which could change the configuration
through any other client. Give agent contexts a token of a user with the User role and read-only
group rights.`,
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

// tierVerdict compares the declared tier with what a role type can do in Zabbix
// (admin and super admin roles can change configuration; the user role cannot).
func tierVerdict(tier, roleType string) (ok bool, note string) {
	if tier == "" {
		tier = "readonly"
	}
	canConfigure := roleType == "2" || roleType == "3"
	switch {
	case canConfigure && (tier == "readonly" || tier == "operator"):
		return false, "the tier " + tier + " is enforced only by zbxctl: this token's Zabbix role (" + roleTypeLabel(roleType) + ") can change the configuration through any other client. For a context an agent can use, give it a token of a user with the User role and read-only group rights."
	case !canConfigure && roleType != "" && tier == "write":
		return true, "this token's role (" + roleTypeLabel(roleType) + ") cannot change the configuration, so most writes will be refused by Zabbix even though the tier allows them."
	}
	return true, ""
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
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"role"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &users)
	}
	if err != nil || len(users) == 0 {
		row["ok"] = false
		row["note"] = "could not read this token's role, so the tier cannot be compared with what Zabbix enforces"
		if err != nil {
			row["error"] = err.Error()
		}
	} else {
		r := users[0].Role
		ok, note := tierVerdict(tier, r.Type)
		row["role"], row["role_type"] = r.Name, roleTypeLabel(r.Type)
		row["can_change_configuration"] = r.Type == "2" || r.Type == "3"
		row["ok"], row["note"] = ok, note
	}
	cols := []output.Column{{Key: "user", Header: "USER"}, {Key: "role", Header: "ROLE"}, {Key: "declared_tier", Header: "TIER"}, {Key: "ok", Header: "OK"}, {Key: "note", Header: "NOTE"}}
	return output.Print(outFmt, cols, []map[string]any{row})
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
