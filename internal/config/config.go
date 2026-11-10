package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

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
	AllowedScripts []string `yaml:"allowed_scripts"` // allowlist for run script; absent = blocked

	// BeforeState controls the before-state file that bulk writes save under
	// --apply. nil means the default, which is on. StateDir is where they go
	// (default ~/.local/state/zbxctl).
	// APIVersion pins the Zabbix version this context talks to, e.g. "7.4" or
	// "8.0". Empty means detect it from the server.
	APIVersion string `yaml:"api_version"`

	BeforeState *bool  `yaml:"before_state"`
	StateDir    string `yaml:"state_dir"`

	// StateKeep is how many before-state files to keep; older ones are deleted
	// as new ones are written. nil means DefaultStateKeep; 0 means keep everything.
	StateKeep *int `yaml:"state_keep"`
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

// PermissionError is returned when the context lacks the required permission tier.
type PermissionError struct {
	Required string
	Have     string
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission denied: command requires %q tier, context has %q", e.Required, e.Have)
}

// CheckPermission returns an error if the context's tier does not permit required.
// An empty Permissions field defaults to "write" (backwards-compatible).
func (ctx *Context) CheckPermission(required string) error {
	tier := ctx.Permissions
	if tier == "" {
		tier = "write"
	}
	if permLevel(tier) < permLevel(required) {
		have := tier
		if ctx.Permissions == "" {
			have = "write (default)"
		}
		return &PermissionError{Required: required, Have: have}
	}
	return nil
}

func permLevel(tier string) int {
	switch tier {
	case "readonly":
		return 0
	case "operator":
		return 1
	case "write":
		return 2
	default:
		return 2 // unknown tiers are permissive (human operator use case)
	}
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
		if err := yaml.Unmarshal(data, cfg); err != nil {
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
		}
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
		if err := yaml.Unmarshal(data, cfg); err != nil {
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
		if err := yaml.Unmarshal(data, cfg); err != nil {
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
