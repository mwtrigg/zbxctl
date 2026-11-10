package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mwtrigg/zbxctl/internal/config"
)

func flaky(t *testing.T, failures int32) (*Client, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":[],"id":1}`))
	}))
	t.Cleanup(srv.Close)
	retryBackoff = time.Millisecond
	c, err := NewClient(context.Background(), &config.Context{URL: srv.URL, APIToken: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return c, &calls
}

func TestReadsAreRetriedOnGatewayErrors(t *testing.T) {
	c, calls := flaky(t, 2)
	if _, err := c.Call("host.get", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if *calls != 3 {
		t.Fatalf("calls=%d, want 3", *calls)
	}
}

func TestWritesAreNeverRetried(t *testing.T) {
	c, calls := flaky(t, 5)
	if _, err := c.Call("host.update", map[string]any{}); err == nil {
		t.Fatal("expected an error")
	}
	if *calls != 1 {
		t.Fatalf("a write was sent %d times", *calls)
	}
}

func TestEndpointAcceptsBaseOrFullURL(t *testing.T) {
	for _, u := range []string{"http://z/zabbix", "http://z/zabbix/", "http://z/zabbix/api_jsonrpc.php"} {
		c, _ := NewClient(context.Background(), &config.Context{URL: u, APIToken: "x"})
		if c.endpoint != "http://z/zabbix/api_jsonrpc.php" {
			t.Errorf("%s -> %s", u, c.endpoint)
		}
	}
}
