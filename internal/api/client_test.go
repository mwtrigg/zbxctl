package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestParseVersionAndChecks(t *testing.T) {
	for in, want := range map[string][3]int{"7.4": {7, 4, 0}, "7.4.16": {7, 4, 16}, "8.0.0alpha1": {8, 0, 0}, "7.0.5rc2": {7, 0, 5}} {
		v, err := ParseVersion(in)
		if err != nil || [3]int{v.Major, v.Minor, v.Patch} != want {
			t.Errorf("%s: %+v %v", in, v, err)
		}
	}
	if _, err := ParseVersion("banana"); err == nil {
		t.Error("garbage must not parse")
	}
	cases := []struct {
		v     string
		newer bool
		err   bool
	}{{"7.4.16", false, false}, {"7.4", false, false}, {"7.6.0", true, false}, {"8.0.1", true, false}, {"7.0.9", false, true}, {"6.4.1", false, true}}
	for _, c := range cases {
		v, _ := ParseVersion(c.v)
		newer, err := v.Check()
		if newer != c.newer || (err != nil) != c.err {
			t.Errorf("%s: newer=%v err=%v", c.v, newer, err)
		}
	}
	v, _ := ParseVersion("8.0")
	if !v.AtLeast(7, 4) || !v.AtLeast(8, 0) || v.AtLeast(8, 1) {
		t.Error("AtLeast")
	}
}

func TestPlainHTTPWarnsThatTheTokenTravelsInClearText(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	_, _ = NewClient(context.Background(), &config.Context{URL: "http://zbx.example.com", APIToken: "t"})
	_, _ = NewClient(context.Background(), &config.Context{URL: "https://zbx.example.com", APIToken: "t"})
	_ = w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	if strings.Count(string(b), "plain http://") != 1 {
		t.Fatalf("exactly the http:// URL must warn: %q", b)
	}
}
