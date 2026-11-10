// Package backtest replays a simple threshold condition over collected samples
// to see when it would have fired. It is deliberately small: one item, one
// function, one comparison, for answering "would this alert have caught the
// incident, and how often would it have cried wolf?".
package backtest

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Sample is one data point. Raw history has only V; trend buckets also carry
// the hourly Min, Max and the number of samples behind Avg.
type Sample struct {
	T        int64 // Unix seconds (for trends, the start of the hour)
	V        float64
	Min, Max float64
	Num      float64
	Trend    bool
}

// Condition is "func(window) op threshold", e.g. avg(5m)>90, last>=1, nodata(10m).
type Condition struct {
	Func      string        // last, avg, min, max, nodata
	Window    time.Duration // zero for last
	Op        string        // > >= < <= = !=
	Threshold float64
}

var condRe = regexp.MustCompile(`^\s*(last|avg|min|max)\s*(?:\(\s*(\d+[smhdw])\s*\))?\s*(>=|<=|!=|=|>|<)\s*(-?[0-9]*\.?[0-9]+)\s*$`)
var nodataRe = regexp.MustCompile(`^\s*nodata\s*\(\s*(\d+[smhdw])\s*\)\s*$`)

func parseDur(s string) (time.Duration, error) {
	n, _ := strconv.Atoi(s[:len(s)-1])
	unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	return time.Duration(n) * unit, nil
}

// Parse reads a condition such as "avg(5m)>90", "last>=1" or "nodata(10m)".
func Parse(s string) (Condition, error) {
	if m := nodataRe.FindStringSubmatch(s); m != nil {
		w, _ := parseDur(m[1])
		return Condition{Func: "nodata", Window: w}, nil
	}
	m := condRe.FindStringSubmatch(s)
	if m == nil {
		return Condition{}, fmt.Errorf("cannot read condition %q: use last>N, avg(5m)>N, min(10m)>=N, max(1h)<N or nodata(10m)", s)
	}
	c := Condition{Func: m[1], Op: m[3]}
	c.Threshold, _ = strconv.ParseFloat(m[4], 64)
	if m[2] != "" {
		c.Window, _ = parseDur(m[2])
	} else if c.Func != "last" {
		return Condition{}, fmt.Errorf("%s needs a window, e.g. %s(5m)", c.Func, c.Func)
	}
	return c, nil
}

func (c Condition) compare(v float64) bool {
	switch c.Op {
	case ">":
		return v > c.Threshold
	case ">=":
		return v >= c.Threshold
	case "<":
		return v < c.Threshold
	case "<=":
		return v <= c.Threshold
	case "=":
		return v == c.Threshold
	case "!=":
		return v != c.Threshold
	}
	return false
}

// Period is a stretch of time during which the condition held.
type Period struct {
	Start, End int64   // End is when it stopped holding, or the window end
	Peak       float64 // most extreme value that satisfied it (max for > / >=, min for < / <=); for expressions, the highest value of the main item
	Low        float64 // expressions only: the lowest value of the main item while it held
	Open       bool    // still true at the end of the window
}

// value of the condition's function at sample i (samples sorted by T).
func (c Condition) value(s []Sample, i int) float64 {
	cur := s[i]
	if c.Func == "last" || c.Window == 0 {
		return cur.V
	}
	lo := cur.T - int64(c.Window.Seconds())
	var sum, n, min, max float64
	first := true
	for j := i; j >= 0 && s[j].T > lo; j-- {
		x := s[j]
		w, xmin, xmax := 1.0, x.V, x.V
		if x.Trend {
			w, xmin, xmax = x.Num, x.Min, x.Max
		}
		sum += x.V * w
		n += w
		if first || xmin < min {
			min = xmin
		}
		if first || xmax > max {
			max = xmax
		}
		first = false
	}
	switch c.Func {
	case "avg":
		if n == 0 {
			return cur.V
		}
		return sum / n
	case "min":
		return min
	}
	return max
}

// Evaluate returns when c would have held over samples between from and till
// (Unix; till 0 means up to the last sample). Samples need not be sorted.
func Evaluate(samples []Sample, c Condition, from, till int64) []Period {
	s := append([]Sample(nil), samples...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].T < s[j].T })
	if len(s) == 0 {
		return nil
	}
	end := till
	if end == 0 {
		end = s[len(s)-1].T
	}
	if c.Func == "nodata" {
		return nodataPeriods(s, c, from, end)
	}
	var out []Period
	var cur *Period
	for i := range s {
		if s[i].T < from || (till > 0 && s[i].T > till) {
			continue
		}
		v := c.value(s, i)
		if c.compare(v) {
			if cur == nil {
				cur = &Period{Start: s[i].T, Peak: v}
			}
			if (c.Op == ">" || c.Op == ">=") && v > cur.Peak || (c.Op == "<" || c.Op == "<=") && v < cur.Peak {
				cur.Peak = v
			}
		} else if cur != nil {
			cur.End = s[i].T
			out = append(out, *cur)
			cur = nil
		}
	}
	if cur != nil {
		cur.End, cur.Open = end, true
		out = append(out, *cur)
	}
	return out
}

// nodataPeriods: nodata(w) holds from lastSample+w until the next sample.
func nodataPeriods(s []Sample, c Condition, from, end int64) []Period {
	w := int64(c.Window.Seconds())
	var out []Period
	prev := from
	if len(s) > 0 && s[0].T >= from {
		// before the first sample inside the window we know nothing; start there
		prev = s[0].T
	}
	for _, x := range s {
		if x.T < from {
			prev = x.T
			continue
		}
		if x.T > end {
			break
		}
		if x.T-prev > w {
			out = append(out, Period{Start: prev + w, End: x.T})
		}
		prev = x.T
	}
	if end-prev > w {
		out = append(out, Period{Start: prev + w, End: end, Open: true})
	}
	return out
}

// Verdict of one fired period against the real problems of the same host.
type Verdict struct {
	Period      Period
	True        bool    // the fire caught a real problem: before it, at its start, or while it ran
	Timing      string  // "early", "at start" or "late" (only when True)
	LeadMinutes float64 // minutes between the fire and the real problem's start (negative = fired late)
	Problem     int     // index of the matched real problem, -1 when none
}

// atStart is how close to a problem's start a fire still counts as "at start".
const atStart = 60

// grace lets a fire that begins just after a problem ended still count: data
// and events are stamped a few seconds apart.
const grace = 120

// Judge matches fired periods against real problem periods. A fire is true when
// it overlaps the window from (problem start - lead) to (problem end + a short
// grace): it came early enough, at the start, or late but while the problem
// was still on. Problems no fire touched are reported as missed.
func Judge(fires, real []Period, lead time.Duration) (verdicts []Verdict, missed []int) {
	l := int64(lead.Seconds())
	caught := make([]bool, len(real))
	for _, f := range fires {
		fEnd := f.End
		if fEnd < f.Start {
			fEnd = f.Start
		}
		v := Verdict{Period: f, Problem: -1}
		for i, p := range real {
			if f.Start <= p.End+grace && fEnd >= p.Start-l {
				v.True, v.Problem = true, i
				d := p.Start - f.Start
				v.LeadMinutes = float64(d) / 60
				switch {
				case d > atStart:
					v.Timing = "early"
				case d < -atStart:
					v.Timing = "late"
				default:
					v.Timing = "at start"
					v.LeadMinutes = 0
				}
				caught[i] = true
				break
			}
		}
		verdicts = append(verdicts, v)
	}
	for i, ok := range caught {
		if !ok {
			missed = append(missed, i)
		}
	}
	return verdicts, missed
}
