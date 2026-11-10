package backtest

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Expressions let a backtest compare an item with other items and with shares
// of them, e.g. a "memory dropped" rule:
//
//	(max(15m) - last) > 10% of last("vm.memory.size[total]") AND last("vm.memory.size[pavailable]") < 50
//
// Grammar (lowest precedence first):
//
//	expr   = and { "OR" and }
//	and    = cmp { "AND" cmp }
//	cmp    = sum [ (">" | ">=" | "<" | "<=" | "=" | "!=") sum ]
//	sum    = term { ("+" | "-") term }
//	term   = unary { ("*" | "/") unary }
//	unary  = "-" unary | atom
//	atom   = number | number "%" [ "of" unary ] | call | "(" expr ")"
//	call   = func [ "(" [ "\"key\"" "," ] [ window ] ")" ]      func = last | avg | min | max
//
// A call without a key looks at the item being backtested; with a key it looks
// at that item on the same host, carrying its latest value forward. "10% of X"
// means 0.10 * X.

type node interface{}

type (
	numNode  struct{ v float64 }
	callNode struct {
		fn     string
		key    string // "" = the item being backtested
		window time.Duration
	}
	negNode struct{ x node }
	binNode struct {
		op   string
		l, r node
	}
)

// Expr is a parsed expression.
type Expr struct {
	src  string
	root node
}

type token struct {
	kind string // num, pct, dur, ident, str, op, end
	text string
	pos  int
}

var (
	numRe = regexp.MustCompile(`^[0-9]*\.?[0-9]+`)
	durRe = regexp.MustCompile(`^[0-9]+[smhdw]\b`)
	idRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*`)
)

func lex(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '"':
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				return nil, fmt.Errorf("unterminated quote at position %d", i)
			}
			out = append(out, token{"str", s[i+1 : i+1+j], i})
			i += j + 2
		case c >= '0' && c <= '9' || c == '.':
			if m := durRe.FindString(s[i:]); m != "" {
				out = append(out, token{"dur", m, i})
				i += len(m)
				continue
			}
			m := numRe.FindString(s[i:])
			if m == "" {
				return nil, fmt.Errorf("unexpected %q at position %d", string(c), i)
			}
			i += len(m)
			if i < len(s) && s[i] == '%' {
				out = append(out, token{"pct", m, i})
				i++
			} else {
				out = append(out, token{"num", m, i})
			}
		case c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_':
			m := idRe.FindString(s[i:])
			out = append(out, token{"ident", m, i})
			i += len(m)
		case strings.ContainsRune("+-*/(),", rune(c)):
			out = append(out, token{"op", string(c), i})
			i++
		case c == '>' || c == '<' || c == '!' || c == '=':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{"op", s[i : i+2], i})
				i += 2
			} else if c == '!' {
				return nil, fmt.Errorf("unexpected \"!\" at position %d (use != )", i)
			} else {
				out = append(out, token{"op", string(c), i})
				i++
			}
		default:
			return nil, fmt.Errorf("unexpected %q at position %d", string(c), i)
		}
	}
	out = append(out, token{"end", "", len(s)})
	return out, nil
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }
func (p *parser) isOp(op string) bool {
	t := p.peek()
	return t.kind == "op" && t.text == op
}
func (p *parser) isWord(w string) bool {
	t := p.peek()
	return t.kind == "ident" && strings.EqualFold(t.text, w)
}

// ParseExpr parses an expression.
func ParseExpr(s string) (*Expr, error) {
	toks, err := lex(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.or()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != "end" {
		return nil, fmt.Errorf("unexpected %q at position %d", t.text, t.pos)
	}
	return &Expr{src: s, root: n}, nil
}

func (p *parser) or() (node, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.isWord("OR") {
		p.next()
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = binNode{"OR", l, r}
	}
	return l, nil
}

func (p *parser) and() (node, error) {
	l, err := p.cmp()
	if err != nil {
		return nil, err
	}
	for p.isWord("AND") {
		p.next()
		r, err := p.cmp()
		if err != nil {
			return nil, err
		}
		l = binNode{"AND", l, r}
	}
	return l, nil
}

func (p *parser) cmp() (node, error) {
	l, err := p.sum()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{">=", "<=", "!=", ">", "<", "="} {
		if p.isOp(op) {
			p.next()
			r, err := p.sum()
			if err != nil {
				return nil, err
			}
			return binNode{op, l, r}, nil
		}
	}
	return l, nil
}

func (p *parser) sum() (node, error) {
	l, err := p.term()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.next().text
		r, err := p.term()
		if err != nil {
			return nil, err
		}
		l = binNode{op, l, r}
	}
	return l, nil
}

func (p *parser) term() (node, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") {
		op := p.next().text
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		l = binNode{op, l, r}
	}
	return l, nil
}

func (p *parser) unary() (node, error) {
	if p.isOp("-") {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return negNode{x}, nil
	}
	return p.atom()
}

func (p *parser) atom() (node, error) {
	t := p.next()
	switch t.kind {
	case "num":
		v, _ := strconv.ParseFloat(t.text, 64)
		return numNode{v}, nil
	case "pct":
		v, _ := strconv.ParseFloat(t.text, 64)
		share := numNode{v / 100}
		if p.isWord("of") {
			p.next()
			x, err := p.unary()
			if err != nil {
				return nil, err
			}
			return binNode{"*", share, x}, nil
		}
		return share, nil
	case "op":
		if t.text == "(" {
			n, err := p.or()
			if err != nil {
				return nil, err
			}
			if !p.isOp(")") {
				return nil, fmt.Errorf("missing ) after position %d", t.pos)
			}
			p.next()
			return n, nil
		}
	case "ident":
		fn := strings.ToLower(t.text)
		switch fn {
		case "last", "avg", "min", "max":
		case "nodata":
			return nil, fmt.Errorf("nodata cannot be combined in an expression; use it alone, e.g. --when 'nodata(10m)'")
		default:
			return nil, fmt.Errorf("unknown function %q at position %d: use last, avg, min or max", t.text, t.pos)
		}
		c := callNode{fn: fn}
		if p.isOp("(") {
			p.next()
			if p.peek().kind == "str" {
				c.key = p.next().text
				if p.isOp(",") {
					p.next()
				}
			}
			if p.peek().kind == "dur" {
				c.window = parseDurText(p.next().text)
			}
			if !p.isOp(")") {
				return nil, fmt.Errorf("expected ) in %s(...) at position %d", fn, p.peek().pos)
			}
			p.next()
		}
		if c.fn != "last" && c.window == 0 {
			return nil, fmt.Errorf("%s needs a window, e.g. %s(5m)", fn, fn)
		}
		return c, nil
	}
	if t.kind == "end" {
		return nil, fmt.Errorf("the expression ends too early")
	}
	return nil, fmt.Errorf("unexpected %q at position %d", t.text, t.pos)
}

func parseDurText(s string) time.Duration { d, _ := parseDur(s); return d }

// Keys lists the other items the expression refers to (not the main item).
func (e *Expr) Keys() []string {
	seen := map[string]bool{}
	var out []string
	var walk func(n node)
	walk = func(n node) {
		switch t := n.(type) {
		case callNode:
			if t.key != "" && !seen[t.key] {
				seen[t.key] = true
				out = append(out, t.key)
			}
		case negNode:
			walk(t.x)
		case binNode:
			walk(t.l)
			walk(t.r)
		}
	}
	walk(e.root)
	sort.Strings(out)
	return out
}

// Window is the longest look-back the expression needs, so callers can fetch
// enough data before the start of the replay window.
func (e *Expr) Window() time.Duration {
	var w time.Duration
	var walk func(n node)
	walk = func(n node) {
		switch t := n.(type) {
		case callNode:
			if t.window > w {
				w = t.window
			}
		case negNode:
			walk(t.x)
		case binNode:
			walk(t.l)
			walk(t.r)
		}
	}
	walk(e.root)
	return w
}

// evalCtx is the data an expression is evaluated against at one point in time.
type evalCtx struct {
	series map[string][]Sample // "" is the item being backtested
	mainI  int
	t      int64
}

func (c evalCtx) value(n callNode) (float64, bool) {
	s := c.series[n.key]
	if len(s) == 0 {
		return 0, false
	}
	j := c.mainI
	if n.key != "" { // latest sample at or before t
		j = sort.Search(len(s), func(i int) bool { return s[i].T > c.t }) - 1
		if j < 0 {
			return 0, false
		}
	}
	return Condition{Func: n.fn, Window: n.window}.value(s, j), true
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (c evalCtx) eval(n node) (float64, bool) {
	switch t := n.(type) {
	case numNode:
		return t.v, true
	case callNode:
		return c.value(t)
	case negNode:
		v, ok := c.eval(t.x)
		return -v, ok
	case binNode:
		l, lok := c.eval(t.l)
		r, rok := c.eval(t.r)
		if !lok || !rok {
			return 0, false // a missing input makes the whole comparison unknown
		}
		switch t.op {
		case "+":
			return l + r, true
		case "-":
			return l - r, true
		case "*":
			return l * r, true
		case "/":
			if r == 0 {
				return 0, false
			}
			return l / r, true
		case ">":
			return b2f(l > r), true
		case ">=":
			return b2f(l >= r), true
		case "<":
			return b2f(l < r), true
		case "<=":
			return b2f(l <= r), true
		case "=":
			return b2f(math.Abs(l-r) < 1e-9), true
		case "!=":
			return b2f(math.Abs(l-r) >= 1e-9), true
		case "AND":
			return b2f(l != 0 && r != 0), true
		case "OR":
			return b2f(l != 0 || r != 0), true
		}
	}
	return 0, false
}

// EvaluateExpr replays e over series (key "" is the item being backtested),
// evaluating at every sample of that item between from and till (till 0 = to the
// last sample). Peak and Low are the highest and lowest value of the main item
// while the expression held.
func EvaluateExpr(e *Expr, series map[string][]Sample, from, till int64) []Period {
	main := append([]Sample(nil), series[""]...)
	sort.SliceStable(main, func(i, j int) bool { return main[i].T < main[j].T })
	sorted := map[string][]Sample{"": main}
	for k, v := range series {
		if k == "" {
			continue
		}
		s := append([]Sample(nil), v...)
		sort.SliceStable(s, func(i, j int) bool { return s[i].T < s[j].T })
		sorted[k] = s
	}
	if len(main) == 0 {
		return nil
	}
	end := till
	if end == 0 {
		end = main[len(main)-1].T
	}
	var out []Period
	var cur *Period
	for i, s := range main {
		if s.T < from || (till > 0 && s.T > till) {
			continue
		}
		v, ok := evalCtx{series: sorted, mainI: i, t: s.T}.eval(e.root)
		if ok && v != 0 {
			if cur == nil {
				cur = &Period{Start: s.T, Peak: s.V, Low: s.V}
			}
			if s.V > cur.Peak {
				cur.Peak = s.V
			}
			if s.V < cur.Low {
				cur.Low = s.V
			}
		} else if cur != nil {
			cur.End = s.T
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
