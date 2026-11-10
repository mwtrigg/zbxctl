package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// hostChanges is what an update host command asks for, resolved to IDs once.
type hostChanges struct {
	in    resources.UpdateHostInput // everything but the host
	notes []string                  // one line per change, for the dry run
}

func resolveHostChanges(c *api.Client) (*hostChanges, error) {
	h := &hostChanges{in: resources.UpdateHostInput{
		VisibleName: flagUpdateHostVisibleName, Description: flagUpdateHostDescription,
	}}
	if flagUpdateHostVisibleName != "" {
		h.notes = append(h.notes, fmt.Sprintf("set the visible name to %q", flagUpdateHostVisibleName))
	}
	if flagUpdateHostDescription != "" {
		h.notes = append(h.notes, "replace the description")
	}
	group := func(names []string, verb string, into *[]string) error {
		for _, n := range names {
			id, err := resources.ResolveGroupID(c, n)
			if err != nil {
				return fmt.Errorf("group %q: %w", n, err)
			}
			*into = append(*into, id)
			h.notes = append(h.notes, fmt.Sprintf("%s group %q", verb, n))
		}
		return nil
	}
	tmpl := func(names []string, verb string, into *[]string) error {
		for _, n := range names {
			id, err := resources.ResolveTemplateID(c, n)
			if err != nil {
				return fmt.Errorf("template %q: %w", n, err)
			}
			*into = append(*into, id)
			h.notes = append(h.notes, fmt.Sprintf("%s template %q", verb, n))
		}
		return nil
	}
	for _, f := range []func() error{
		func() error { return group(flagUpdateHostAddGroups, "add", &h.in.AddGroupIDs) },
		func() error { return group(flagUpdateHostRemoveGroups, "remove", &h.in.RemoveGroupIDs) },
		func() error { return tmpl(flagUpdateHostAddTemplates, "link", &h.in.AddTemplateIDs) },
		func() error {
			return tmpl(flagUpdateHostRemoveTemplates, "unlink (items, triggers and graphs stay)", &h.in.RemoveTemplateIDs)
		},
		func() error {
			return tmpl(flagUpdateHostClearTemplates, "UNLINK AND CLEAR (deletes the items, triggers, graphs and history it gave the host; cannot be undone)", &h.in.ClearTemplateIDs)
		},
	} {
		if err := f(); err != nil {
			return nil, err
		}
	}
	switch {
	case flagUpdateHostProxy == "none":
		h.in.MonitoredBy = "server"
		h.notes = append(h.notes, "monitor from the server")
	case flagUpdateHostProxy != "":
		id, err := resources.ResolveProxyID(c, flagUpdateHostProxy)
		if err != nil {
			return nil, err
		}
		h.in.MonitoredBy, h.in.ProxyID = "proxy", id
		h.notes = append(h.notes, fmt.Sprintf("monitor through proxy %q", flagUpdateHostProxy))
	case flagUpdateHostProxyGroup != "":
		id, err := resources.ResolveProxyGroupID(c, flagUpdateHostProxyGroup)
		if err != nil {
			return nil, err
		}
		h.in.MonitoredBy, h.in.ProxyGroupID = "group", id
		h.notes = append(h.notes, fmt.Sprintf("monitor through proxy group %q", flagUpdateHostProxyGroup))
	}
	return h, nil
}

func runUpdateHost(cmd *cobra.Command, args []string) error {
	if flagUpdateHostVisibleName == "" && flagUpdateHostDescription == "" &&
		len(flagUpdateHostAddGroups) == 0 && len(flagUpdateHostRemoveGroups) == 0 &&
		len(flagUpdateHostAddTemplates) == 0 && len(flagUpdateHostRemoveTemplates) == 0 && len(flagUpdateHostClearTemplates) == 0 &&
		flagUpdateHostProxy == "" && flagUpdateHostProxyGroup == "" {
		return cliFail(output.Format(flagOutput), &usageError{"no update flags provided; nothing to do"})
	}
	if flagUpdateHostProxy != "" && flagUpdateHostProxyGroup != "" {
		return cliFail(output.Format(flagOutput), &usageError{"--proxy and --proxy-group are exclusive"})
	}
	var changes *hostChanges
	return runBulk(cmd, args, bulkSpec{
		Name: "update-host", Tier: "write", Cols: hostCols,
		Prepare: func(c *api.Client, target string) (*step, error) {
			if changes == nil {
				var err error
				if changes, err = resolveHostChanges(c); err != nil {
					return nil, err
				}
			}
			h, err := resources.GetHost(c, target)
			if err != nil {
				return nil, err
			}
			in := changes.in
			in.HostID = h.HostID
			before := hostBefore(h)
			before["description"] = h.Description
			before["monitored_by"], before["proxyid"], before["proxy_groupid"] = h.MonitoredBy, h.ProxyID, h.ProxyGroupID
			// hostBefore keeps names; the IDs are what a restore needs.
			if g, t, err := resources.HostLinkIDs(c, h.HostID); err == nil {
				before["group_ids"], before["template_ids"] = g, t
			}
			return &step{
				Key: h.HostID, Desc: fmt.Sprintf("update host %q (%s): %s", h.Name, h.Host, strings.Join(changes.notes, "; ")), Before: before,
				Apply: func() error { _, err := resources.UpdateHost(c, in); return err },
				Verify: func() error {
					g, t, err := resources.HostLinkIDs(c, h.HostID)
					if err != nil {
						return err
					}
					for _, id := range in.AddGroupIDs {
						if !slices.Contains(g, id) {
							return fmt.Errorf("group %s is not on the host", id)
						}
					}
					for _, id := range in.RemoveGroupIDs {
						if slices.Contains(g, id) {
							return fmt.Errorf("group %s is still on the host", id)
						}
					}
					for _, id := range in.AddTemplateIDs {
						if !slices.Contains(t, id) {
							return fmt.Errorf("template %s is not linked", id)
						}
					}
					for _, id := range append(slices.Clone(in.RemoveTemplateIDs), in.ClearTemplateIDs...) {
						if slices.Contains(t, id) {
							return fmt.Errorf("template %s is still linked", id)
						}
					}
					return nil
				},
				Row: map[string]any{"hostid": h.HostID, "host": h.Host, "name": h.Name, "status": hostStatusLabel(h.Status)},
			}, nil
		},
	})
}
