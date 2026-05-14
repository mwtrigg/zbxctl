package cli

import (
	"context"

	"github.com/spf13/cobra"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
	"codeberg.org/mwtrigg/zbxctl/internal/config"
	"codeberg.org/mwtrigg/zbxctl/internal/output"
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
		output.PrintError(outFmt, err.Error(), output.ExitError)
		return nil // already printed
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
	}

	row := map[string]any{
		"url":         ctx.URL,
		"context":     ctxName,
		"auth_via":    authVia,
		"connected":   connected,
		"api_version": apiVersion,
	}

	return output.Print(outFmt, cols, []map[string]any{row})
}
