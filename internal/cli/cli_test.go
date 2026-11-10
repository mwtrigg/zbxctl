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

func TestUserDeleteBlocks(t *testing.T) {
	if userDeleteBlock("admin", "1", "9") == nil {
		t.Error("Admin must be refused (case-insensitive)")
	}
	if userDeleteBlock("svc", "4", "4") == nil {
		t.Error("token owner must be refused")
	}
	if err := userDeleteBlock("bob", "7", "4"); err != nil {
		t.Errorf("ordinary user refused: %v", err)
	}
}

func TestNormalizeProblemName(t *testing.T) {
	p := resources.Problem{Name: "/boot is low on web-01 (below 700M)", Hosts: []string{"web-01"}}
	if got := normalizeProblemName(p); got != "/boot is low on {HOST} (below 700M)" {
		t.Fatalf("got %q", got)
	}
}

func hrec(item string, clock int64) resources.HistoryRecord {
	return resources.HistoryRecord{ItemID: item, Host: "h", Key: "k", ClockUnix: clock}
}

func TestDownsampleKeepsLastPerBucketPerItem(t *testing.T) {
	recs := []resources.HistoryRecord{hrec("1", 0), hrec("1", 10), hrec("2", 11), hrec("1", 70), hrec("1", 75)}
	got := downsampleHistory(recs, time.Minute)
	if len(got) != 3 || got[0].ClockUnix != 10 || got[1].ItemID != "2" || got[2].ClockUnix != 75 {
		t.Fatalf("got %+v", got)
	}
}

func TestFindHistoryGapsIncludingWindowEdges(t *testing.T) {
	recs := []resources.HistoryRecord{hrec("1", 100), hrec("1", 160), hrec("1", 1000), hrec("1", 1060)}
	gaps := findHistoryGaps(recs, 5*time.Minute, 0, 2000)
	if len(gaps) != 2 || gaps[0].from != 160 || gaps[0].to != 1000 || gaps[1].edge != "end" || gaps[1].from != 1060 {
		t.Fatalf("gaps: %+v", gaps)
	}
	if g := findHistoryGaps(recs, 5*time.Minute, 0, 1200); len(g) != 1 {
		t.Fatalf("end gap should not count when it is shorter than the limit: %+v", g)
	}
	if g := findHistoryGaps(recs, time.Hour, 0, 0); len(g) != 0 {
		t.Fatalf("no gaps over an hour: %+v", g)
	}
}
