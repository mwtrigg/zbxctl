package config

import (
	"bytes"
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
}

// SaveBeforeState reports whether before-state files should be written.
func (c *Context) SaveBeforeState() bool { return c.BeforeState == nil || *c.BeforeState }

// PermissionError is returned when the context lacks the required permission tier.
type PermissionError struct {
	Required string
	Have     string
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission denied: command requires %q tier, context has %q", e.Required, e.Have)
}

// CheckPermission returns an error if the context's tier does not permit required.
// An empty Permissions field defaults to "write" (backwards-compatible). A tier
// that is not one of the three is refused, never granted: a typo must not give
// more access than was meant.
func (ctx *Context) CheckPermission(required string) error {
	tier := normalizeTier(ctx.Permissions)
	if tier == "" {
		tier = "write"
	}
	have, req := permLevel(tier), permLevel(required)
	if have < 0 || have < req {
		shown := ctx.Permissions
		if shown == "" {
			shown = "write (default)"
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
		} else if contextOverride != "" || os.Getenv("ZBXCTL_CONTEXT") != "" {
			// Named on purpose (flag or environment) but not in the file: a
			// misspelt name must not fall back to an empty context that the
			// environment then fills in with the default (write) tier.
			return nil, nil, fmt.Errorf("context %q is not defined in the config file", ctxName)
		}
	}
	if err := ValidateTier(ctx.Permissions); err != nil {
		return nil, nil, fmt.Errorf("context %q: %w", ctxName, err)
	}

	// Per-field env var overrides.
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
	if v := os.Getenv("ZBXCTL_STATE_DIR"); v != "" {
		ctx.StateDir = v
	}

	if ctx.DefaultLimit == 0 {
		ctx.DefaultLimit = 100
	}

	if ctx.URL == "" {
		return nil, nil, fmt.Errorf("no url configured for context %q (set ZBXCTL_URL or config url field)", ctxName)
	}

	return cfg, &ctx, nil
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

// WriteCurrentContext persists current_context to the config file.
func WriteCurrentContext(name string) error {
	path, err := defaultConfigPath()
	if err != nil {
		return err
	}

	cfg := &Config{Contexts: make(map[string]Context)}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config: %w", err)
	}
	if err == nil {
		if err := parseConfig(data, cfg); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}
	}

	if _, ok := cfg.Contexts[name]; !ok {
		return fmt.Errorf("context %q not found in config", name)
	}

	cfg.CurrentContext = name

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	return os.WriteFile(path, out, 0o600)
}
