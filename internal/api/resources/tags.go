package resources

import (
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// HostTagsAfterAdd returns the tag list with tag=value added. changed is false
// when the exact pair is already present.
func HostTagsAfterAdd(cur []HostTag, tag, value string) (out []HostTag, changed bool) {
	out = append([]HostTag{}, cur...)
	for _, t := range cur {
		if t.Tag == tag && t.Value == value {
			return out, false
		}
	}
	return append(out, HostTag{Tag: tag, Value: value}), true
}

// HostTagsAfterRemove returns the tag list without the named tag. If value is
// non-nil only that exact tag=value pair is removed; otherwise every tag with
// that name is. removed counts the tags dropped.
func HostTagsAfterRemove(cur []HostTag, tag string, value *string) (out []HostTag, removed int) {
	out = []HostTag{}
	for _, t := range cur {
		if t.Tag == tag && (value == nil || t.Value == *value) {
			removed++
			continue
		}
		out = append(out, t)
	}
	return out, removed
}

// SetHostTags replaces a host's tag list.
func SetHostTags(c *api.Client, hostID string, tags []HostTag) error {
	if tags == nil {
		tags = []HostTag{}
	}
	if _, err := c.Call("host.update", map[string]any{"hostid": hostID, "tags": tags}); err != nil {
		return fmt.Errorf("host.update: %w", err)
	}
	return nil
}
