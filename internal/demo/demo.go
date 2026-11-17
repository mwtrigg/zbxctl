// Package demo serves a small made-up Zabbix API on localhost, so zbxctl can be
// tried without a Zabbix server at hand (zbxctl demo).
//
// Everything it serves is invented (hosts web-01, db-01, ... under example.com). It keeps changes
// in memory, so disable, enable and acknowledge can be tried with --apply. It
// answers the calls these commands make, not the whole API: a method it does not
// know gets an empty list.
package demo

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Token is what the demo expects as the API token (it accepts anything).
const Token = "demo"

type object = map[string]any

type server struct {
	mu       sync.Mutex
	log      io.Writer
	hosts    []object
	triggers []object
	problems []object
	items    []object
	events   []object
}

// Start serves the demo on addr ("127.0.0.1:8044"). It returns the URL to point
// zbxctl at and a function that stops it. With a log writer, every API method
// that is called is written to it.
func Start(addr string, log io.Writer) (url string, stop func(), err error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	s := &server{log: log}
	s.seed()
	srv := &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	return "http://" + l.Addr().String(), func() { _ = srv.Close() }, nil
}

func ago(d time.Duration) string { return strconv.FormatInt(time.Now().Add(-d).Unix(), 10) }

func (s *server) seed() {
	host := func(id, name, group string, status string) {
		s.hosts = append(s.hosts, object{"hostid": id, "host": name, "name": name, "status": status, "description": "", "monitored_by": "0",
			"proxyid": "0", "proxy_groupid": "0", "maintenance_status": "0", "maintenanceid": "0", "flags": "0",
			"hostgroups": []object{{"groupid": "2", "name": group}}, "groups": []object{{"groupid": "2", "name": group}},
			"interfaces":      []object{{"interfaceid": "9" + id, "ip": "192.0.2." + id[len(id)-2:], "dns": name + ".example.com", "port": "10050", "main": "1", "available": "1", "type": "1", "useip": "0", "details": []any{}}},
			"parentTemplates": []object{{"templateid": "10001", "name": "Linux by Zabbix agent"}}, "macros": []any{}, "tags": []any{}, "inventory": []any{}})
	}
	host("10501", "web-01", "Web servers", "0")
	host("10502", "web-02", "Web servers", "0")
	host("10503", "web-03", "Web servers", "0")
	host("10511", "app-01", "Application servers", "0")
	host("10512", "app-02", "Application servers", "0")
	host("10521", "db-01", "Databases", "0")

	n := 0
	problem := func(trigger, hostID, name, severity string, age time.Duration, manualClose string, itemKey string, itemStatus string) {
		n++
		var h object
		for _, x := range s.hosts {
			if x["hostid"] == hostID {
				h = x
			}
		}
		itemID := "3" + trigger
		s.items = append(s.items, object{"itemid": itemID, "hostid": hostID, "key_": itemKey, "name": itemKey, "status": itemStatus, "state": "0", "error": "",
			"lastvalue": "93.4", "lastclock": ago(40 * time.Second), "value_type": "0", "units": "%", "hosts": []object{{"hostid": hostID, "host": h["host"]}}})
		s.triggers = append(s.triggers, object{"triggerid": trigger, "description": name, "priority": severity, "status": "0", "value": "1", "state": "0",
			"error": "", "manual_close": manualClose, "flags": "0", "lastchange": ago(age), "expression": "", "recovery_mode": "0", "comments": "",
			"hosts": []object{{"hostid": hostID, "host": h["host"], "name": h["name"], "status": "0", "maintenance_status": "0", "maintenanceid": "0"}},
			"items": []object{{"itemid": itemID, "key_": itemKey, "status": itemStatus, "state": "0", "error": ""}}, "tags": []any{}, "dependencies": []any{}})
		event := strconv.Itoa(88000 + n)
		p := object{"eventid": event, "objectid": trigger, "object": "0", "source": "0", "name": name, "severity": severity, "acknowledged": "0", "clock": ago(age),
			"r_eventid": "0", "r_clock": "0", "suppressed": "0", "suppression_data": []any{}, "acknowledges": []any{}, "tags": []any{}, "value": "1"}
		s.problems = append(s.problems, p)
		s.events = append(s.events, p)
	}
	problem("4711", "10502", "High CPU utilization", "3", 47*time.Minute, "0", "system.cpu.util", "0")
	problem("4712", "10503", "High CPU utilization", "3", 41*time.Minute, "0", "system.cpu.util", "0")
	problem("4721", "10521", "/var: disk space is low", "2", 6*time.Hour, "1", "vfs.fs.size[/var,pused]", "0")
	problem("4722", "10511", "/var: disk space is low", "2", 5*time.Hour, "1", "vfs.fs.size[/var,pused]", "0")
	problem("4723", "10512", "/var: disk space is low", "2", 3*time.Hour, "1", "vfs.fs.size[/var,pused]", "0")
	problem("4731", "10512", "Zabbix agent is not available", "3", 12*time.Minute, "0", "zabbix[host,agent,available]", "0")
	// An orphan: its item was disabled while the problem was open, so nothing can
	// ever close it. get problems --exclude-orphans leaves it out; diagnose trigger says why.
	problem("4741", "10501", "Certificate expires in 7 days", "2", 9*24*time.Hour, "0", "web.certificate.get[web-01.example.com]", "1")
}

// pick returns the objects whose field is one of the wanted values; an absent
// or empty wanted list keeps everything.
func pick(objs []object, field string, wanted any) []object {
	set := map[string]bool{}
	switch w := wanted.(type) {
	case []any:
		for _, v := range w {
			set[fmt.Sprint(v)] = true
		}
	case string:
		set[w] = true
	case float64:
		set[strconv.Itoa(int(w))] = true
	}
	if len(set) == 0 {
		return objs
	}
	var out []object
	for _, o := range objs {
		if set[fmt.Sprint(o[field])] {
			out = append(out, o)
		}
	}
	return out
}

// filtered applies the request's "filter" (exact matches) and "limit".
func filtered(objs []object, params object) []object {
	if f, ok := params["filter"].(map[string]any); ok {
		for field, wanted := range f {
			if _, isList := wanted.([]any); !isList {
				wanted = []any{wanted}
			}
			objs = pick(objs, field, wanted)
		}
	}
	if lim, ok := params["limit"].(float64); ok && int(lim) > 0 && len(objs) > int(lim) {
		objs = objs[:int(lim)]
	}
	if objs == nil {
		objs = []object{}
	}
	return objs
}

func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     any             `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	params := object{}
	_ = json.Unmarshal(req.Params, &params)
	s.mu.Lock()
	result := s.call(req.Method, params)
	s.mu.Unlock()
	if s.log != nil {
		fmt.Fprintf(s.log, "%s %s\n", req.Method, req.Params)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(object{"jsonrpc": "2.0", "result": result, "id": req.ID})
}

func (s *server) call(method string, p object) any {
	switch method {
	case "apiinfo.version":
		return "7.4.0"
	case "host.get":
		out := pick(s.hosts, "hostid", p["hostids"])
		return filtered(out, p)
	case "host.update":
		for _, h := range pick(s.hosts, "hostid", p["hostid"]) {
			for k, v := range p {
				if k != "hostid" {
					h[k] = fmt.Sprint(v)
				}
			}
		}
		return object{"hostids": []any{p["hostid"]}}
	case "trigger.get":
		out := pick(s.triggers, "triggerid", p["triggerids"])
		if ids, ok := p["hostids"]; ok {
			var kept []object
			for _, t := range out {
				if len(pick(t["hosts"].([]object), "hostid", ids)) > 0 {
					kept = append(kept, t)
				}
			}
			out = kept
		}
		return filtered(out, p)
	case "problem.get":
		out := pick(s.problems, "objectid", p["objectids"])
		out = pick(out, "eventid", p["eventids"])
		if ids, ok := p["hostids"]; ok {
			var kept []object
			for _, pr := range out {
				for _, t := range pick(s.triggers, "triggerid", pr["objectid"]) {
					if len(pick(t["hosts"].([]object), "hostid", ids)) > 0 {
						kept = append(kept, pr)
					}
				}
			}
			out = kept
		}
		// newest first, as zbxctl asks
		sorted := append([]object(nil), out...)
		for i, j := 0, len(sorted)-1; i < j; i, j = i+1, j-1 {
			sorted[i], sorted[j] = sorted[j], sorted[i]
		}
		return filtered(sorted, p)
	case "event.get":
		out := pick(s.events, "eventid", p["eventids"])
		out = pick(out, "objectid", p["objectids"])
		return filtered(out, p)
	case "event.acknowledge":
		for _, e := range pick(s.problems, "eventid", p["eventids"]) {
			e["acknowledged"] = "1"
		}
		return object{"eventids": p["eventids"]}
	case "item.get":
		out := pick(s.items, "itemid", p["itemids"])
		out = pick(out, "hostid", p["hostids"])
		return filtered(out, p)
	}
	return []any{} // a method the demo does not know: nothing found
}
