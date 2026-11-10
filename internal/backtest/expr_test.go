package backtest

import (
	"testing"
	"time"
)

func TestParseExprErrorsAreHelpful(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "ends too early",
		"avg > 5":                 "needs a window",
		"cpu > 5":                 "unknown function",
		"last > ":                 "ends too early",
		"(last > 5":               "missing )",
		"last > 5 )":              "unexpected",
		"nodata(5m) AND last > 1": "nodata cannot be combined",
		`last("x`:                 "unterminated",
	} {
		_, err := ParseExpr(in)
		if err == nil || !contains(err.Error(), want) {
			t.Errorf("%q: got %v, want it to mention %q", in, err, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestExprKeysAndWindow(t *testing.T) {
	e, err := ParseExpr(`(max(15m) - last) > 10% of last("total") AND avg("free", 1h) < 50 OR last("total") = 0`)
	if err != nil {
		t.Fatal(err)
	}
	if k := e.Keys(); len(k) != 2 || k[0] != "free" || k[1] != "total" {
		t.Errorf("keys: %v", k)
	}
	if e.Window() != time.Hour {
		t.Errorf("window: %v", e.Window())
	}
}

// the "memory dropped" rule from real use: available memory fell by more than
// 10% of total within 15 minutes, while less than half is available.
func TestEvaluateMemoryDropRuleAcrossTwoItems(t *testing.T) {
	e, err := ParseExpr(`(max(15m) - last) > 10% of last("total") AND last("pavail") < 50`)
	if err != nil {
		t.Fatal(err)
	}
	// minute samples of available memory in GB (total 100): steady 60, then a drop to 40, then recovery
	var avail, total, pavail []Sample
	vals := []float64{60, 60, 60, 60, 60, 40, 40, 40, 55, 60, 60}
	for i, v := range vals {
		ts := int64(i) * 60
		avail = append(avail, Sample{T: ts, V: v})
		total = append(total, Sample{T: ts, V: 100})
		pavail = append(pavail, Sample{T: ts, V: v}) // percent available equals GB because total is 100
	}
	series := map[string][]Sample{"": avail, "total": total, "pavail": pavail}
	ps := EvaluateExpr(e, series, 0, 0)
	if len(ps) != 1 || ps[0].Start != 5*60 || ps[0].End != 8*60 || ps[0].Low != 40 || ps[0].Peak != 40 {
		t.Fatalf("one drop, minutes 5-8: %+v", ps)
	}
	// a drop that happens while plenty is still free (pavail >= 50) must not fire
	for i := range pavail {
		pavail[i].V = 90
	}
	if ps := EvaluateExpr(e, series, 0, 0); len(ps) != 0 {
		t.Fatalf("pavail >= 50 must veto the rule: %+v", ps)
	}
}

func TestEvaluateExprMissingInputIsNotAFire(t *testing.T) {
	e, _ := ParseExpr(`last > 5 AND last("other") > 1`)
	series := map[string][]Sample{"": {{T: 0, V: 10}, {T: 60, V: 10}}} // "other" has no data
	if ps := EvaluateExpr(e, series, 0, 0); len(ps) != 0 {
		t.Fatalf("unknown inputs must not fire: %+v", ps)
	}
	// "other" starts later: carries forward only from its first sample
	series["other"] = []Sample{{T: 60, V: 5}}
	if ps := EvaluateExpr(e, series, 0, 0); len(ps) != 1 || ps[0].Start != 60 {
		t.Fatalf("fires once the other item has a value: %+v", ps)
	}
}

func TestExprArithmeticPrecedenceAndOr(t *testing.T) {
	cases := map[string]bool{
		"1 + 2 * 3 = 7":        true,
		"(1 + 2) * 3 = 9":      true,
		"10 / 4 = 2.5":         true,
		"-2 + 5 = 3":           true,
		"50% of 40 = 20":       true,
		"1 > 2 OR 3 > 2":       true,
		"1 > 2 AND 3 > 2":      false,
		"1 > 2 OR 3 > 2 AND 0": false, // AND binds tighter than OR
		"5 != 5":               false,
		"10 / 0 > 1":           false, // dividing by zero is unknown, not true
	}
	for src, want := range cases {
		e, err := ParseExpr(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		got := len(EvaluateExpr(e, map[string][]Sample{"": {{T: 0, V: 0}}}, 0, 0)) == 1
		if got != want {
			t.Errorf("%s = %v, want %v", src, got, want)
		}
	}
}
