package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/config"
)

// newFake returns a client wired to a fake Zabbix that records each request's
// method and params and answers with the canned result for that method.
func newFake(t *testing.T, results map[string]string) (*api.Client, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, map[string]any{"method": req.Method, "params": req.Params, "auth": r.Header.Get("Authorization")})
		res, ok := results[req.Method]
		if !ok {
			res = "[]"
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":` + res + `,"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c, err := api.NewClient(context.Background(), &config.Context{URL: srv.URL, APIToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return c, &calls
}

// Zabbix 7.x removed user.type; asking for it makes user.get fail.
func TestGetUsersDoesNotRequestRemovedTypeField(t *testing.T) {
	c, calls := newFake(t, map[string]string{"user.get": `[{"userid":"1","username":"Admin","name":"Z","surname":"A","roleid":"3","role":{"name":"Super admin role"}}]`})
	users, err := GetUsers(c, UserFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if users[0].Role.Name != "Super admin role" {
		t.Fatalf("role = %q", users[0].Role.Name)
	}
	out := (*calls)[0]["params"].(map[string]any)["output"].([]any)
	if slices.Contains(out, any("type")) {
		t.Fatalf("output still requests type: %v", out)
	}
}
