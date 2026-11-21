package cli

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/config"
)

var configCmd = &cobra.Command{Use: "config", Short: "The config file: where it is, and a starting point for it"}

var contextName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// configTemplate is the file config init writes: one context, the rest as
// comments to switch on.
const configTemplate = `# zbxctl configuration. Keep this file private (mode 0600): it holds an API token.
# Flags override ZBXCTL_* environment variables, which override this file.

current_context: %[1]s

contexts:
  %[1]s:
    # The address of the Zabbix frontend (the API is found under it).
    url: %[2]s
    # An API token (Zabbix: Users > API tokens). The token's user role and its
    # host group permissions are the real limit of what zbxctl can do.
    api_token: %[3]s
    # What this context may do. readonly: look, and show changes as dry runs.
    # operator: also day-to-day operations (acknowledge and suppress problems,
    # maintenance, enable and disable hosts and triggers, allowlisted scripts).
    # write: everything. Without this line a context is readonly.
    permissions: %[4]s

    # mode: dry-run            # dry-run (default): a change needs --apply. apply: it needs none.
    # allowed_scripts:         # the only scripts "run script" may run, each "name@scope"
    # default_limit: 100       # rows a list returns unless --limit says otherwise
    # output: json             # default output format: json | jsonl | table | csv
    # ca_file: /path/to/ca.pem # a private certificate authority
    # state_dir: ~/.local/state/zbxctl     # before-state files of applied changes
    # state_keep: 200          # how many of them to keep

  # A second server is another block:
  # lab:
  #   url: https://zabbix.lab.example.com
  #   api_token: "<token>"
  #   permissions: write
`

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Write a starting config file with one context",
	Long: `Writes ~/.config/zbxctl/config.yaml (YAML, readable only by you) with one context
and the other settings as comments. It never overwrites an existing file.

  zbxctl config init --url https://zabbix.example.com
  zbxctl config init --context prod --url https://zabbix.example.com --token-stdin < token.txt

The API token is not taken as a flag, so that it does not end up in the shell
history: pipe it in with --token-stdin, or leave it out and put it into the file
yourself (or keep it out of the file altogether and set ZBXCTL_API_TOKEN).

Then: zbxctl auth status`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		name, _ := cmd.Flags().GetString("context")
		rawURL, _ := cmd.Flags().GetString("url")
		tier, _ := cmd.Flags().GetString("permissions")
		fromStdin, _ := cmd.Flags().GetBool("token-stdin")
		if !contextName.MatchString(name) {
			return &usageError{"--context is a short name of letters, digits, - _ and ."}
		}
		if tier == "" || config.ValidateTier(tier) != nil {
			return &usageError{"--permissions is readonly, operator or write"}
		}
		if rawURL == "" {
			rawURL = "https://zabbix.example.com"
		} else if u, err := url.Parse(rawURL); err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return &usageError{"--url is the Zabbix frontend's address, like https://zabbix.example.com (https; no user, query or fragment)"}
		}
		token := `"<paste the API token here>"`
		if fromStdin {
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			t := strings.TrimSpace(line)
			if t == "" || strings.ContainsAny(t, " \t\"'\\") {
				return &usageError{"--token-stdin: no token on standard input (or it holds spaces or quotes)"}
			}
			token = `"` + t + `"`
		}
		path, err := config.Path()
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return &usageError{path + " already exists: edit it, or move it away first (zbxctl context list shows what it holds)"}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return &usageError{err.Error()}
		}
		// O_EXCL: never write over a file that appeared in the meantime.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return &usageError{err.Error()}
		}
		if _, err := fmt.Fprintf(f, configTemplate, name, strings.TrimRight(rawURL, "/"), token, tier); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "wrote "+path+" (context "+name+", permissions "+tier+")")
		if !fromStdin {
			fmt.Fprintln(os.Stderr, "next: put the API token into it (or set ZBXCTL_API_TOKEN), then run: zbxctl auth status")
		} else {
			fmt.Fprintln(os.Stderr, "next: zbxctl auth status")
		}
		return nil
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print where the config file is looked for",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		path, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	},
}

func init() {
	configInitCmd.Flags().String("context", "default", "name of the context to write")
	configInitCmd.Flags().String("url", "", "the Zabbix frontend's address, e.g. https://zabbix.example.com")
	configInitCmd.Flags().String("permissions", "readonly", "readonly, operator or write")
	configInitCmd.Flags().Bool("token-stdin", false, "read the API token from standard input")
	configCmd.AddCommand(configInitCmd, configPathCmd)
	rootCmd.AddCommand(configCmd)
}
