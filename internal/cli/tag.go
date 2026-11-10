package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	tagName       string
	tagValue      string
	untagName     string
	untagValue    string
	untagValueSet bool
)

var tagCmd = &cobra.Command{Use: "tag", Short: "Add a tag to a resource"}

var tagHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Add a tag to a host (existing tags are kept)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTagHost,
}

var untagCmd = &cobra.Command{Use: "untag", Short: "Remove a tag from a resource"}

var untagHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Remove a tag from a host",
	Args:  cobra.ExactArgs(1),
	RunE:  runUntagHost,
}

func init() {
	tagHostCmd.Flags().StringVar(&tagName, "tag", "", "tag name (required)")
	tagHostCmd.Flags().StringVar(&tagValue, "value", "", "tag value (default empty)")
	_ = tagHostCmd.MarkFlagRequired("tag")
	untagHostCmd.Flags().StringVar(&untagName, "tag", "", "tag name to remove (required)")
	untagHostCmd.Flags().StringVar(&untagValue, "value", "", "remove only the pair with this value (default: every tag with that name)")
	_ = untagHostCmd.MarkFlagRequired("tag")
	tagCmd.AddCommand(tagHostCmd)
	untagCmd.AddCommand(untagHostCmd)
	rootCmd.AddCommand(tagCmd, untagCmd)
}

func tagLabel(t resources.HostTag) string {
	if t.Value == "" {
		return t.Tag
	}
	return t.Tag + "=" + t.Value
}

func tagLabels(tags []resources.HostTag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = tagLabel(t)
	}
	return out
}

func runTagHost(cmd *cobra.Command, args []string) error {
	return changeHostTags(cmd, args[0], func(cur []resources.HostTag) ([]resources.HostTag, bool, string) {
		out, changed := resources.HostTagsAfterAdd(cur, tagName, tagValue)
		return out, changed, fmt.Sprintf("add tag %s to host", tagLabel(resources.HostTag{Tag: tagName, Value: tagValue}))
	})
}

func runUntagHost(cmd *cobra.Command, args []string) error {
	var val *string
	if cmd.Flags().Changed("value") {
		val = &untagValue
	}
	return changeHostTags(cmd, args[0], func(cur []resources.HostTag) ([]resources.HostTag, bool, string) {
		out, n := resources.HostTagsAfterRemove(cur, untagName, val)
		return out, n > 0, fmt.Sprintf("remove %d tag(s) named %s from host", n, untagName)
	})
}

// changeHostTags is the shared flow: resolve, compute, gate on --apply, write.
func changeHostTags(cmd *cobra.Command, idOrName string, change func([]resources.HostTag) ([]resources.HostTag, bool, string)) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliFail(outFmt, err)
	}
	h, err := resources.GetHost(c, idOrName)
	if err != nil {
		return cliFail(outFmt, err)
	}
	newTags, changed, what := change(h.Tags)
	cols := []output.Column{{Key: "host", Header: "HOST"}, {Key: "changed", Header: "CHANGED"}, {Key: "tags", Header: "TAGS"}}
	if !changed {
		return output.Print(outFmt, cols, []map[string]any{{"hostid": h.HostID, "host": h.Host, "changed": false, "tags": tagLabels(h.Tags)}})
	}
	if !confirm(fmt.Sprintf("%s %q?", what, h.Host)) {
		return nil
	}
	if err := resources.SetHostTags(c, h.HostID, newTags); err != nil {
		return cliFail(outFmt, err)
	}
	return output.Print(outFmt, cols, []map[string]any{{"hostid": h.HostID, "host": h.Host, "changed": true, "tags": tagLabels(newTags)}})
}
