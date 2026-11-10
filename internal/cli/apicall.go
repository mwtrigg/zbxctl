package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	apiParams      string
	apiParamsFile  string
	apiParamsStdin bool
)

// Methods that are never sent through "api call": they hand out secrets or
// have a safer dedicated command.
// Keys are lower case: the method name is lower-cased before it is looked up, so
// token.Generate cannot slip past a check written for token.generate.
var apiDenied = map[string]string{
	"user.login":               "authentication is handled by zbxctl itself",
	"user.logout":              "authentication is handled by zbxctl itself",
	"user.checkauthentication": "use 'zbxctl auth status'",
	"token.generate":           "it would print a new API token",
	"script.execute":           "use 'zbxctl run script', which has an allowlist and a confirmation token",
}

// Object families whose write methods are as good as admin: they change who can
// log in, what runs where, and what is allowed. api call refuses them unless the
// context sets api_call_admin; dedicated commands cover the legitimate cases.
var apiAdminFamilies = map[string]bool{
	"user": true, "token": true, "script": true, "action": true, "role": true, "usergroup": true,
	"authentication": true, "userdirectory": true, "mediatype": true,
}

// isReadMethod reports methods that only read.
func isReadMethod(m string) bool {
	return strings.HasSuffix(m, ".get") || m == "apiinfo.version" || m == "configuration.export" || m == "configuration.importcompare"
}

var apiMethodRe = regexp.MustCompile(`^[a-z]+\.[a-zA-Z]+$|^apiinfo\.version$`)
var secretKeyRe = regexp.MustCompile(`(?i)pass|secret|token|auth|api_?key|private|psk|community`)

// maskSecrets hides values of keys that look like secrets, for the dry run and
// the audit file.
func maskSecrets(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if secretKeyRe.MatchString(k) {
				out[k] = "******"
			} else {
				out[k] = maskSecrets(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = maskSecrets(x)
		}
		return out
	}
	return v
}

var apiCmd = &cobra.Command{Use: "api", Short: "Escape hatch for API methods zbxctl has no command for"}

var apiCallCmd = &cobra.Command{
	Use:   "call <method>",
	Short: "Call any Zabbix API method (reads freely; anything else needs --apply)",
	Long: `Calls a Zabbix API method with JSON params and prints the raw result. Use it only when
no dedicated command exists; the dedicated commands give flat results, dry runs and checks.

Read methods (*.get, apiinfo.version, configuration.export, configuration.importcompare) run
directly. Every other method is a dry run unless --apply, and then needs the write permission
tier; an audit record (secrets masked) is saved like for other writes. user.login, user.logout,
user.checkAuthentication, token.generate and script.execute are refused.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outFmt := output.Format(flagOutput)
		method := args[0]
		lower := strings.ToLower(method)
		if why, no := apiDenied[lower]; no {
			return cliPrintErr(outFmt, fmt.Sprintf("%s is not available through api call: %s", method, why), output.ExitUsage)
		}
		if !apiMethodRe.MatchString(method) {
			return cliPrintErr(outFmt, fmt.Sprintf("invalid method %q: use object.action, e.g. host.get", method), output.ExitUsage)
		}
		n := 0
		for _, on := range []bool{apiParams != "", apiParamsFile != "", apiParamsStdin} {
			if on {
				n++
			}
		}
		if n > 1 {
			return cliPrintErr(outFmt, "use only one of --params, --params-file and --params-stdin", output.ExitUsage)
		}
		var rawParams []byte
		switch {
		case apiParamsFile != "":
			b, err := os.ReadFile(apiParamsFile)
			if err != nil {
				return cliFail(outFmt, fmt.Errorf("reading params file: %w", err))
			}
			rawParams = b
		case apiParamsStdin:
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return cliFail(outFmt, err)
			}
			rawParams = b
		case apiParams != "":
			rawParams = []byte(apiParams)
		default:
			rawParams = []byte("{}")
		}
		var params any
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return cliPrintErr(outFmt, "the params are not valid JSON: "+err.Error(), output.ExitUsage)
		}
		switch params.(type) {
		case map[string]any, []any:
		default:
			return cliPrintErr(outFmt, "the params must be a JSON object or array", output.ExitUsage)
		}

		c, outFmt, ctx, err := newClient()
		if err != nil {
			return cliFail(outFmt, err)
		}
		applyStateSettings(cmd, ctx)
		var write *writeRecord // the audit record of a write method
		read := isReadMethod(lower)
		if !read {
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			if family, _, _ := strings.Cut(lower, "."); apiAdminFamilies[family] && !ctx.APICallAdmin {
				return cliPrintErr(outFmt, fmt.Sprintf("%s changes %s objects, which api call refuses: it would be full admin access. Use the dedicated command, or set api_call_admin: true on this context if you mean it", method, family), output.ExitUsage)
			}
			if !flagApply {
				return output.Print(outFmt, []output.Column{{Key: "method", Header: "METHOD"}, {Key: "would", Header: "WOULD"}, {Key: "applied", Header: "APPLIED"}},
					[]map[string]any{{"method": method, "would": "call " + method, "params": maskSecrets(params), "applied": false, "dry_run": true}})
			}
			if !bulkNoState {
				r, err := newStateRecorder("api-call-"+strings.ReplaceAll(method, ".", "-"), []*step{{Key: method, Desc: "call " + method, Before: map[string]any{"params": maskSecrets(params)}}})
				if err != nil {
					return cliFail(outFmt, fmt.Errorf("cannot save the audit record (use --no-state to skip): %w", err))
				}
				fmt.Fprintf(os.Stderr, "audit record saved: %s\n", r.path)
				write = &writeRecord{rec: r}
				write.note(stateSent, nil)
			} else {
				write = &writeRecord{}
			}
		}
		var result any
		if method == "apiinfo.version" { // Zabbix wants this one without the auth header
			v, err := c.APIVersion()
			if err != nil {
				return cliFail(outFmt, err)
			}
			result = v
		} else {
			raw, err := c.Call(method, params)
			if err != nil {
				if write != nil {
					err = write.failed(err)
				}
				return cliFail(outFmt, err)
			}
			if write != nil {
				write.ok()
			}
			_ = json.Unmarshal(raw, &result)
			if !flagShowSecrets {
				result = maskSecrets(result) // item passwords, PSKs, communities: --show-secrets to see them
			}
		}
		doc := map[string]any{"data": result, "meta": map[string]any{"method": method, "command": "api call"}}
		if outFmt == output.FormatJSONL {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	},
}

func init() {
	f := apiCallCmd.Flags()
	f.StringVar(&apiParams, "params", "", "params as a JSON object or array")
	f.StringVar(&apiParamsFile, "params-file", "", "read the params JSON from this file")
	f.BoolVar(&apiParamsStdin, "params-stdin", false, "read the params JSON from stdin")
	f.BoolVar(&bulkNoState, "no-state", false, "do not save an audit record for write methods")
	f.IntVar(&bulkKeep, "state-keep", -1, "keep at most this many before-state files (default 200; 0 keeps all)")
	f.StringVar(&bulkStateDir, "state-dir", "", "where the audit record is written (default ~/.local/state/zbxctl)")
	apiCmd.AddCommand(apiCallCmd)
	rootCmd.AddCommand(apiCmd)
}
