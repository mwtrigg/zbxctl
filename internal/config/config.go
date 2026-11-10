package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level config file structure.
type Config struct {
	CurrentContext string             `yaml:"current_context"`
	Contexts       map[string]Context `yaml:"contexts"`
}

// Context holds per-context connection settings.
type Context struct {
	URL            string   `yaml:"url"`
	APIToken       string   `yaml:"api_token"`
	Username       string   `yaml:"username"`
	Password       string   `yaml:"password"`
	TLSVerify      *bool    `yaml:"tls_verify"` // nil = default true
	CAFile         string   `yaml:"ca_file"`
	Proxy          string   `yaml:"proxy"`
	DefaultLimit   int      `yaml:"default_limit"`   // default 100
	Permissions    string   `yaml:"permissions"`     // readonly | operator | write; default write
	AllowedScripts []string `yaml:"allowed_scripts"` // allowlist for run script, each entry "name@sha256:<hex of the command>"; absent = blocked
	APICallAdmin   bool     `yaml:"api_call_admin"`  // allow api call to write user, token, script, action, role, usergroup, authentication, userdirectory and mediatype objects

	// BeforeState controls the before-state file that bulk writes save under
	// --apply. nil means the default, which is on. StateDir is where they go
	// (default ~/.local/state/zbxctl).
	// APIVersion pins the Zabbix version this context talks to, e.g. "7.4" or
	// "8.0". Empty means detect it from the server.
	APIVersion string `yaml:"api_version"`

	BeforeState *bool  `yaml:"before_state"`
	StateDir    string `yaml:"state_dir"`

	// Mode is what a command that changes things does when neither --apply nor
	// --dry-run is given: "dry-run" (the default) only shows what it would do,
	// "apply" does it. The MCP server ignores this and always defaults to dry-run.
	Mode string `yaml:"mode"`

	// StateKeep is how many before-state files to keep; older ones are deleted
	// as new ones are written. nil means DefaultStateKeep; 0 means keep everything.
	StateKeep *int `yaml:"state_keep"`
}

// ApplyByDefault reports whether this context applies changes unless --dry-run is given.
func (c *Context) ApplyByDefault() bool { return strings.EqualFold(strings.TrimSpace(c.Mode), "apply") }

// ValidateMode rejects anything but "", "dry-run" and "apply".
func (c *Context) ValidateMode() error {
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case "", "dry-run", "dryrun", "apply":
		return nil
	}
	return fmt.Errorf("mode %q is not valid: use dry-run or apply", c.Mode)
}

// DefaultStateKeep is how many before-state files are kept unless configured.
const DefaultStateKeep = 200

// KeepStates is the number of before-state files to keep (0 = all of them).
func (c *Context) KeepStates() int {
	if c.StateKeep == nil || *c.StateKeep < 0 {
		return DefaultStateKeep
	}
	return *c.StateKeep
}

// SaveBeforeState reports whether before-state files should be written.
func (c *Context) SaveBeforeState() bool { return c.BeforeState == nil || *c.BeforeState }

// ErrNotConfigured marks a missing URL or credentials: the user has to configure
// something, which is a usage problem, not an API failure.
var ErrNotConfigured = errors.New("not configured")

// PermissionError is returned when the context lacks the required permission tier.
type PermissionError struct {
	Required string
	Have     string
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission denied: command requires %q tier, context has %q", e.Required, e.Have)
}

// CheckPermission returns an error if the context's tier does not permit required.
// An empty Permissions field means "readonly": a connection that was never given a
// tier (credentials from the environment and no config, say) must not be able to
// change anything. Set permissions: write in the config, or ZBXCTL_PERMISSIONS=write.
// A tier that is not one of the three is refused, never granted: a typo must not
// give more access than was meant.
func (ctx *Context) CheckPermission(required string) error {
	tier := normalizeTier(ctx.Permissions)
	if tier == "" {
		tier = "readonly"
	}
	have, req := permLevel(tier), permLevel(required)
	if have < 0 || have < req {
		shown := ctx.Permissions
		if shown == "" {
			shown = "readonly (the default: set permissions: write in the config or ZBXCTL_PERMISSIONS=write)"
		}
		return &PermissionError{Required: required, Have: shown}
	}
	return nil
}

func normalizeTier(t string) string { return strings.ToLower(strings.TrimSpace(t)) }

// ValidateTier reports whether t is empty (the default) or a known tier.
func ValidateTier(t string) error {
	n := normalizeTier(t)
	if n == "" || permLevel(n) >= 0 {
		return nil
	}
	return fmt.Errorf("permissions %q is not a known tier (use readonly, operator or write)", t)
}

// permLevel returns the rank of a tier, or -1 for one that is not known.
func permLevel(tier string) int {
	switch tier {
	case "readonly":
		return 0
	case "operator":
		return 1
	case "write":
		return 2
	default:
		return -1
	}
}

// parseConfig decodes the config file strictly: an unknown key (a misspelt
// setting) is an error rather than a setting that silently does nothing.
func parseConfig(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && err.Error() != "EOF" {
		return err
	}
	return nil
}

// defaultConfigPath returns ~/.config/zbxctl/config.yaml.
func defaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "zbxctl", "config.yaml"), nil
}

// LoadConfig loads the config file, merges env vars, and resolves the active
// context. contextOverride, if non-empty, selects the context instead of
// current_context.
//
// Priority for each field: contextOverride / ZBXCTL_CONTEXT > config file
// current_context > "default". Per-field env vars then override config values.
// Returns an error if the resolved URL is still empty.
func LoadConfig(contextOverride string) (*Config, *Context, error) {
	cfg := &Config{
		Contexts: make(map[string]Context),
	}

	path, err := defaultConfigPath()
	if err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("reading config: %w", err)
	}
	if err == nil {
		warnIfReadable(path, data)
		if err := parseConfig(data, cfg); err != nil {
			return nil, nil, fmt.Errorf("parsing config: %w", err)
		}
	}

	// Resolve active context name.
	ctxName := cfg.CurrentContext
	if v := os.Getenv("ZBXCTL_CONTEXT"); v != "" {
		ctxName = v
	}
	if contextOverride != "" {
		ctxName = contextOverride
	}

	// Start from an existing context entry or a zero-value one.
	var ctx Context
	if ctxName != "" {
		if c, ok := cfg.Contexts[ctxName]; ok {
			ctx = c
		} else {
			// Named (by flag, environment or current_context) but not in the file: it
			// must not fall back to an empty context that the environment then fills in.
			return nil, nil, fmt.Errorf("context %q is not defined in the config file", ctxName)
		}
	}
	if err := ValidateTier(ctx.Permissions); err != nil {
		return nil, nil, fmt.Errorf("context %q: %w", ctxName, err)
	}

	// Per-field env var overrides.
	if v := os.Getenv("ZBXCTL_PERMISSIONS"); v != "" {
		if err := ValidateTier(v); err != nil {
			return nil, nil, fmt.Errorf("ZBXCTL_PERMISSIONS: %w", err)
		}
		// The environment can lower a tier but never raise one: an operator who once
		// exported ZBXCTL_PERMISSIONS=write must not raise a context written readonly.
		cur := normalizeTier(ctx.Permissions)
		switch {
		case cur == "" || permLevel(normalizeTier(v)) < permLevel(cur):
			ctx.Permissions = v
		case permLevel(normalizeTier(v)) > permLevel(cur):
			fmt.Fprintf(os.Stderr, "warning: ZBXCTL_PERMISSIONS=%s is ignored: context %q is %s, and the environment can lower a tier but never raise it\n", v, ctxName, cur)
		}
	}
	if v := os.Getenv("ZBXCTL_URL"); v != "" {
		ctx.URL = v
	}
	if v := os.Getenv("ZBXCTL_API_TOKEN"); v != "" {
		ctx.APIToken = v
	}
	if v := os.Getenv("ZBXCTL_USERNAME"); v != "" {
		ctx.Username = v
	}
	if v := os.Getenv("ZBXCTL_PASSWORD"); v != "" {
		ctx.Password = v
	}
	if v := os.Getenv("ZBXCTL_INSECURE"); v != "" {
		b, _ := strconv.ParseBool(v)
		if b {
			f := false
			ctx.TLSVerify = &f
		}
	}
	if v := os.Getenv("ZBXCTL_CA_FILE"); v != "" {
		ctx.CAFile = v
	}
	if v := os.Getenv("ZBXCTL_API_VERSION"); v != "" {
		ctx.APIVersion = v
	}
	if v := os.Getenv("ZBXCTL_BEFORE_STATE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			ctx.BeforeState = &b
		}
	}
	if v := os.Getenv("ZBXCTL_MODE"); v != "" {
		ctx.Mode = v
	}
	if v := os.Getenv("ZBXCTL_STATE_KEEP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			ctx.StateKeep = &n
		}
	}
	if v := os.Getenv("ZBXCTL_STATE_DIR"); v != "" {
		ctx.StateDir = v
	}

	if ctx.DefaultLimit == 0 {
		ctx.DefaultLimit = 100
	}

	if ctx.URL == "" {
		return nil, nil, fmt.Errorf("%w: no url configured for context %q (set ZBXCTL_URL or config url field)", ErrNotConfigured, ctxName)
	}

	return cfg, &ctx, nil
}

// warnIfReadable warns when a config file that holds a token or a password can be
// read by other users on the machine.
func warnIfReadable(path string, data []byte) {
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm()&0o077 == 0 {
		return
	}
	if !bytes.Contains(data, []byte("api_token")) && !bytes.Contains(data, []byte("password")) {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %s holds credentials but is readable by other users (mode %04o); run: chmod 600 %s\n", path, fi.Mode().Perm(), path)
}

// ActiveContextName returns the name of the context a command would use (flag,
// ZBXCTL_CONTEXT, then current_context), "" if none is named, and an error if the
// name is not in the config file. The MCP server resolves it once at start and pins it.
func ActiveContextName(contextOverride string) (string, error) {
	cfg, err := LoadConfigFile()
	if err != nil {
		return "", err
	}
	name := cfg.CurrentContext
	if v := os.Getenv("ZBXCTL_CONTEXT"); v != "" {
		name = v
	}
	if contextOverride != "" {
		name = contextOverride
	}
	if name != "" {
		if _, ok := cfg.Contexts[name]; !ok {
			return "", fmt.Errorf("context %q is not defined in the config file", name)
		}
	}
	return name, nil
}

// LoadConfigFile reads the raw config file without requiring a valid context or URL.
// Useful for listing contexts before a URL is needed.
func LoadConfigFile() (*Config, error) {
	path, err := defaultConfigPath()
	if err != nil {
		return nil, err
	}

	cfg := &Config{Contexts: make(map[string]Context)}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if err == nil {
		if err := parseConfig(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}
	return cfg, nil
}

// WriteCurrentContext persists current_context to the config file. It edits only
// that one value in the YAML document, so comments, key order and every other
// setting stay exactly as they were.
func WriteCurrentContext(name string) error {
	path, err := defaultConfigPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("context %q not found in config: there is no config file", name)
		}
		return fmt.Errorf("reading config: %w", err)
	}
	cfg := &Config{Contexts: make(map[string]Context)}
	if err := parseConfig(data, cfg); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if _, ok := cfg.Contexts[name]; !ok {
		return fmt.Errorf("context %q not found in config", name)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("the config file is not a YAML mapping")
	}
	root := doc.Content[0]
	set := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "current_context" {
			root.Content[i+1].Kind, root.Content[i+1].Tag, root.Content[i+1].Value, root.Content[i+1].Style = yaml.ScalarNode, "!!str", name, 0
			set = true
		}
	}
	if !set {
		root.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "current_context"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		}, root.Content...)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
