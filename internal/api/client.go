package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/mwtrigg/zbxctl/internal/config"
)

// RPCError represents a Zabbix JSON-RPC error response.
type RPCError struct {
	Code    int
	Message string
	Data    string
}

func (e *RPCError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("rpc error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Sentinel errors the CLI maps to exit codes.
var (
	ErrConnect = errors.New("cannot reach Zabbix")
	ErrAuth    = errors.New("authentication failed")
)

// IsAuth reports whether the RPC error means the token or login was refused.
func (e *RPCError) IsAuth() bool {
	m := strings.ToLower(e.Message + " " + e.Data)
	return strings.Contains(m, "not authorised") || strings.Contains(m, "not authorized") ||
		strings.Contains(m, "session terminated") || strings.Contains(m, "incorrect user name or password") ||
		strings.Contains(m, "api token")
}

// Client is a Zabbix JSON-RPC 2.0 client.
type Client struct {
	httpClient  *http.Client
	endpoint    string
	bearerToken string // pre-auth header token (api_token or session token)

	// Filters are exact-match KEY=VALUE pairs (--filter) merged into the API
	// "filter" object of list calls made through CallList.
	Filters map[string][]string
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
	ID      int    `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcErrorBody   `json:"error"`
	ID      int             `json:"id"`
}

type rpcErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

// NewClient creates and authenticates a Client from the given context config.
func NewClient(ctx context.Context, cfg *config.Context) (*Client, error) {
	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Transport: transport}
	endpoint := cfg.URL + "/api_jsonrpc.php"

	c := &Client{
		httpClient: httpClient,
		endpoint:   endpoint,
	}

	if cfg.APIToken != "" {
		c.bearerToken = cfg.APIToken
		return c, nil
	}

	// Authenticate with username+password.
	if cfg.Username == "" {
		return nil, fmt.Errorf("no api_token and no username configured")
	}

	params := map[string]string{
		"username": cfg.Username,
		"password": cfg.Password,
	}
	raw, err := c.callRaw(ctx, "user.login", params)
	if err != nil {
		return nil, fmt.Errorf("user.login: %w", err)
	}

	var token string
	if err := json.Unmarshal(raw, &token); err != nil {
		return nil, fmt.Errorf("user.login response: %w", err)
	}
	c.bearerToken = token
	return c, nil
}

// Call invokes a Zabbix API method with the given params.
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	return c.callRaw(context.Background(), method, params)
}

// CallList is Call for list queries: it merges c.Filters into params["filter"].
// Zabbix ignores filter keys placed at the top level of params, so they must
// live inside the filter object.
func (c *Client) CallList(method string, params map[string]any) (json.RawMessage, error) {
	if len(c.Filters) > 0 {
		f, _ := params["filter"].(map[string]any)
		if f == nil {
			f = map[string]any{}
		}
		for k, v := range c.Filters {
			f[k] = v
		}
		params["filter"] = f
	}
	return c.Call(method, params)
}

// CurrentUser returns the user the credentials belong to. Zabbix requires
// user.checkAuthentication to be called without an Authorization header.
func (c *Client) CurrentUser() (userID, username string, err error) {
	tmp := &Client{httpClient: c.httpClient, endpoint: c.endpoint}
	raw, err := tmp.callRaw(context.Background(), "user.checkAuthentication", map[string]any{"token": c.bearerToken})
	if err != nil {
		return "", "", err
	}
	var u struct {
		UserID   string `json:"userid"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return "", "", fmt.Errorf("parsing current user: %w", err)
	}
	return u.UserID, u.Username, nil
}

// APIVersion calls apiinfo.version (no authentication required).
func (c *Client) APIVersion() (string, error) {
	// Use a client without auth header for this call.
	tmp := &Client{httpClient: c.httpClient, endpoint: c.endpoint}
	raw, err := tmp.callRaw(context.Background(), "apiinfo.version", map[string]any{})
	if err != nil {
		return "", err
	}
	var ver string
	if err := json.Unmarshal(raw, &ver); err != nil {
		return "", fmt.Errorf("parsing api version: %w", err)
	}
	return ver, nil
}

func (c *Client) callRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json-rpc")
	if c.bearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnect, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: HTTP %d", ErrAuth, resp.StatusCode)
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, &RPCError{
			Code:    rpcResp.Error.Code,
			Message: rpcResp.Error.Message,
			Data:    rpcResp.Error.Data,
		}
	}

	return rpcResp.Result, nil
}

func buildTransport(cfg *config.Context) (http.RoundTripper, error) {
	tlsCfg := &tls.Config{}

	if cfg.TLSVerify != nil && !*cfg.TLSVerify {
		fmt.Fprintln(os.Stderr, "warning: TLS verification disabled")
		tlsCfg.InsecureSkipVerify = true //nolint:gosec
	}

	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certificates found in ca_file %s", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}

	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
	}

	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("parsing proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	return transport, nil
}
