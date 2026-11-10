package cli

import (
	"context"

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

func init() {
	authCmd.AddCommand(authStatusCmd)
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
	}
	if v, err := api.ParseVersion(apiVersion); err == nil {
		newer, cerr := v.Check()
		row["supported"] = cerr == nil
		row["newer_than_tested"] = newer
	}

	return output.Print(outFmt, cols, []map[string]any{row})
}
