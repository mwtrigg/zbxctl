package resources

import (
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// AcknowledgeParams holds parameters for acknowledging an event.
type AcknowledgeParams struct {
	EventID string
	Message string // optional
	Close   bool   // if true, also close the problem (sets action bit for close)
}

// AcknowledgeEvent acknowledges a Zabbix event.
//
// Zabbix event.acknowledge action bitmask:
//   1  — close problem
//   2  — acknowledge
//   4  — add message
//   8  — change severity
//   16 — unacknowledge
//   32 — suppress
//   64 — unsuppress
//   128 — change cause
func AcknowledgeEvent(c *api.Client, p AcknowledgeParams) error {
	// Build action bitmask: always acknowledge (2).
	action := 2
	if p.Close {
		action |= 1 // close problem
	}
	if p.Message != "" {
		action |= 4 // add message
	}

	params := map[string]any{
		"eventids": []string{p.EventID},
		"action":   action,
	}
	if p.Message != "" {
		params["message"] = p.Message
	}

	raw, err := c.Call("event.acknowledge", params)
	if err != nil {
		return fmt.Errorf("event.acknowledge: %w", err)
	}
	// Response is {"eventids": [<int>, ...]} — just verify it's not an error.
	_ = raw
	return nil
}
