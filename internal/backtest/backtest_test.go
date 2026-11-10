package backtest

import (
	"testing"
	"time"
)

func series(vals ...float64) []Sample {
	s := make([]Sample, len(vals))
	for i, v := range vals {
		s[i] = Sample{T: int64(i) * 60, V: v} // one sample a minute
	}
	return s
}

func TestParse(t *testing.T) {
	good := map[string]Condition{
		"last>90":       {Func: "last", Op: ">", Threshold: 90},
		"avg(5m) >= 80": {Func: "avg", Window: 5 * time.Minute, Op: ">=", Threshold: 80},
		"max(1h)<0.5":   {Func: "max", Window: time.Hour, Op: "<", Threshold: 0.5},
		"nodata(10m)":   {Func: "nodata", Window: 10 * time.Minute},
		"min(2d)!=-1":   {Func: "min", Window: 48 * time.Hour, Op: "!=", Threshold: -1},
	}
	for in, want := range good {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("%q: got %+v err=%v want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "avg>5", "cpu>5", "avg(5x)>1", "last>"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestEvaluateLastThreshold(t *testing.T) {
	c, _ := Parse("last>90")
	ps := Evaluate(series(10, 95, 99, 20, 91, 92), c, 0, 0)
	if len(ps) != 2 || ps[0].Start != 60 || ps[0].End != 180 || ps[0].Peak != 99 || !ps[1].Open || ps[1].Start != 240 {
		t.Fatalf("%+v", ps)
	}
}

func TestEvaluateAvgWindowSmoothsSpikes(t *testing.T) {
	c, _ := Parse("avg(3m)>50")
	// a single spike of 100 among zeros averages 33 over 3 samples: no fire;
	// two high samples in a row average above 50: fire.
	if ps := Evaluate(series(0, 0, 100, 0, 0), c, 0, 0); len(ps) != 0 {
		t.Fatalf("spike alone must not fire: %+v", ps)
	}
	if ps := Evaluate(series(0, 0, 100, 100, 0, 0), c, 0, 0); len(ps) != 1 {
		t.Fatalf("sustained high must fire once: %+v", ps)
	}
}

func TestEvaluateNodata(t *testing.T) {
	c, _ := Parse("nodata(5m)")
	s := []Sample{{T: 0}, {T: 60}, {T: 120}, {T: 1000}, {T: 1060}}
	ps := Evaluate(s, c, 0, 1100)
	if len(ps) != 1 || ps[0].Start != 120+300 || ps[0].End != 1000 || ps[0].Open {
		t.Fatalf("%+v", ps)
	}
	ps = Evaluate(s, c, 0, 2000)
	if len(ps) != 2 || !ps[1].Open || ps[1].Start != 1060+300 {
		t.Fatalf("a trailing silence must be reported as open: %+v", ps)
	}
}

func TestTrendSamplesUseMinAvgMax(t *testing.T) {
	hour := int64(3600)
	s := []Sample{
		{T: 0, V: 50, Min: 10, Max: 90, Num: 60, Trend: true},
		{T: hour, V: 50, Min: 40, Max: 60, Num: 60, Trend: true},
	}
	maxC, _ := Parse("max(2h)>80")
	if ps := Evaluate(s, maxC, 0, 0); len(ps) != 1 {
		t.Fatalf("hourly max 90 must satisfy max>80: %+v", ps)
	}
	minC, _ := Parse("min(2h)<20")
	if ps := Evaluate(s, minC, 0, 0); len(ps) != 1 || !ps[0].Open {
		t.Fatalf("hourly min 10 must satisfy min<20: %+v", ps)
	}
}

func TestJudgeTruePositivesFalseAlarmsAndMisses(t *testing.T) {
	fires := []Period{{Start: 1000, End: 1100}, {Start: 5000, End: 5100}, {Start: 9000, End: 9100}}
	real := []Period{{Start: 1600, End: 2400}, {Start: 20000, End: 21000}} // incident at 1600 and one nobody caught
	vs, missed := Judge(fires, real, 15*time.Minute)
	if !vs[0].True || vs[0].LeadMinutes != 10 || vs[0].Timing != "early" {
		t.Errorf("first fire is 10 minutes ahead of the incident: %+v", vs[0])
	}
	if vs[1].True || vs[2].True {
		t.Errorf("later fires are false alarms: %+v", vs)
	}
	if len(missed) != 1 || missed[0] != 1 {
		t.Errorf("missed: %v", missed)
	}
}

func TestJudgeFireAtOrAfterStartIsNotAFalseAlarm(t *testing.T) {
	real := []Period{{Start: 1000, End: 1600}}
	for name, tc := range map[string]struct {
		fire   Period
		timing string
	}{
		"same second":     {Period{Start: 1000, End: 1060}, "at start"},
		"same minute":     {Period{Start: 1030, End: 1090}, "at start"},
		"later but on":    {Period{Start: 1300, End: 1400}, "late"},
		"a zero-length":   {Period{Start: 1000, End: 1000}, "at start"},
		"just after over": {Period{Start: 1650, End: 1700}, "late"}, // within the grace
	} {
		vs, missed := Judge([]Period{tc.fire}, real, 5*time.Minute)
		if !vs[0].True || vs[0].Timing != tc.timing || len(missed) != 0 {
			t.Errorf("%s: %+v missed=%v", name, vs[0], missed)
		}
	}
	// a fire that began long before and is still running when the problem starts overlaps it
	vs, _ := Judge([]Period{{Start: 100, End: 1100}}, real, time.Minute)
	if !vs[0].True {
		t.Errorf("an ongoing fire that spans the problem start is a catch: %+v", vs[0])
	}
	if vs, _ := Judge([]Period{{Start: 5000, End: 5100}}, real, 5*time.Minute); vs[0].True {
		t.Error("a fire long after the problem is a false alarm")
	}
}
