package resources

import (
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// AckAction is what event.acknowledge should do to the problem.
type AckAction string

const (
	ActionAcknowledge   AckAction = "acknowledge"
	ActionUnacknowledge AckAction = "unacknowledge"
	ActionSuppress      AckAction = "suppress"
	ActionUnsuppress    AckAction = "unsuppress"
)

// AcknowledgeParams holds parameters for event.acknowledge.
type AcknowledgeParams struct {
	EventID       string
	Action        AckAction // default acknowledge
	Message       string    // optional, added to the problem's history
	Close         bool      // acknowledge only: also close the problem
	Severity      int       // new severity 0-5; -1 = leave unchanged
	SuppressUntil int64     // suppress only: Unix time; 0 = indefinitely
}

// Action bits of event.acknowledge: 1 close, 2 acknowledge, 4 message,
// 8 change severity, 16 unacknowledge, 32 suppress, 64 unsuppress.
func (p AcknowledgeParams) actionMask() int {
	mask := 0
	switch p.Action {
	case ActionUnacknowledge:
		mask = 16
	case ActionSuppress:
		mask = 32
	case ActionUnsuppress:
		mask = 64
	default:
		mask = 2
		if p.Close {
			mask |= 1
		}
	}
	if p.Message != "" {
		mask |= 4
	}
	if p.Severity >= 0 {
		mask |= 8
	}
	return mask
}

// AcknowledgeEvent applies an acknowledge-family action to an event.
func AcknowledgeEvent(c *api.Client, p AcknowledgeParams) error {
	params := map[string]any{
		"eventids": []string{p.EventID},
		"action":   p.actionMask(),
	}
	if p.Message != "" {
		params["message"] = p.Message
	}
	if p.Severity >= 0 {
		params["severity"] = p.Severity
	}
	if p.Action == ActionSuppress {
		params["suppress_until"] = p.SuppressUntil
	}
	if _, err := c.Call("event.acknowledge", params); err != nil {
		return fmt.Errorf("event.acknowledge: %w", err)
	}
	return nil
}
