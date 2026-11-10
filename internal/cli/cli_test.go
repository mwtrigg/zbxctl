package cli

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

func TestClassifyMapsErrorsToExitCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", &resources.NotFoundError{Resource: "host", Query: "x"}, output.ExitNotFound},
		{"wrapped not found", fmt.Errorf("host %q: %w", "x", &resources.NotFoundError{Resource: "host", Query: "x"}), output.ExitNotFound},
		{"usage", &usageError{"bad flag"}, output.ExitUsage},
		{"connect", fmt.Errorf("%w: dial tcp", api.ErrConnect), output.ExitConnect},
		{"auth http", fmt.Errorf("%w: HTTP 401", api.ErrAuth), output.ExitAuthError},
		{"auth rpc", &api.RPCError{Code: -32602, Message: "Invalid params.", Data: "Not authorised."}, output.ExitAuthError},
		{"other rpc", &api.RPCError{Code: -32500, Message: "Application error."}, output.ExitError},
		{"plain", errors.New("boom"), output.ExitError},
	}
	for _, c := range cases {
		if got := classify(c.err); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestErrorKinds(t *testing.T) {
	for code, want := range map[int]string{2: "not_found", 3: "auth", 4: "connection", 5: "usage", 1: "error"} {
		if got := output.KindFor(code); got != want {
			t.Errorf("code %d: %q want %q", code, got, want)
		}
	}
}

func TestParseTimestampRelativeAndErrors(t *testing.T) {
	now := time.Now().Unix()
	for in, secs := range map[string]int64{"30m": 1800, "24h": 86400, "7d": 7 * 86400, "2w": 14 * 86400} {
		got, err := parseTimestamp(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if d := now - got - secs; d < -2 || d > 2 {
			t.Errorf("%s: off by %ds", in, d)
		}
	}
	for _, in := range []string{"1700000000", "2026-01-02", "2026-01-02T03:04:05Z"} {
		if _, err := parseTimestamp(in); err != nil {
			t.Errorf("%s: %v", in, err)
		}
	}
	_, err := parseTimestamp("yesterday")
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("want usageError, got %v", err)
	}
}

func TestFmtUnixIsRFC3339UTC(t *testing.T) {
	if got := fmtUnix("1700000000"); got != "2023-11-14T22:13:20Z" {
		t.Fatalf("got %q", got)
	}
}

func TestCobraAndFieldErrorsAreUsage(t *testing.T) {
	for _, msg := range []string{"unknown flag: --nope", "accepts 1 arg(s), received 0", `unknown command "x" for "zbxctl"`} {
		if classify(errors.New(msg)) != output.ExitUsage {
			t.Errorf("%q not classified as usage", msg)
		}
	}
	if classify(&output.FieldError{Msg: "x"}) != output.ExitUsage {
		t.Error("FieldError not usage")
	}
}
