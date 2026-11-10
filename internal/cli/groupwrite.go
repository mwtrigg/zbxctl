package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	groupName    string
	groupNewName string
	groupAllow   bool
)

func deleteGroupSpec() bulkSpec {
	return bulkSpec{
		Name: "delete-group", Tier: "write",
		Cols: []output.Column{{Key: "groupid", Header: "GROUP_ID"}, {Key: "name", Header: "NAME"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, target string) (*step, error) {
			g, err := resources.GetGroupInfo(c, target)
			if err != nil {
				return nil, err
			}
			desc := fmt.Sprintf("delete host group %q (id %s)", g.Name, g.GroupID)
			if g.HostCount > 0 {
				if !groupAllow {
					return nil, &usageError{fmt.Sprintf("host group %q still has %d host(s); move them first, or pass --allow-hosts (Zabbix still refuses if a host would be left without a group)", g.Name, g.HostCount)}
				}
				desc += fmt.Sprintf(" with %d host(s) in it", g.HostCount)
			}
			return &step{
				Key: g.GroupID, Desc: desc,
				Before: map[string]any{"groupid": g.GroupID, "name": g.Name, "host_count": g.HostCount},
				Apply:  func() error { return resources.DeleteGroup(c, g.GroupID) },
				Verify: func() error {
					_, err := resources.GetGroupInfo(c, g.GroupID)
					var nfe *resources.NotFoundError
					if errors.As(err, &nfe) {
						return nil
					}
					if err != nil {
						return err
					}
					return errors.New("host group still exists")
				},
				Row: map[string]any{"groupid": g.GroupID, "name": g.Name, "status": "deleted"},
			}, nil
		},
	}
}

func init() {
	createGroup := &cobra.Command{
		Use: "group", Short: "Create a host group", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			if _, err := resources.GetGroupInfo(c, groupName); err == nil {
				return cliFail(outFmt, &usageError{fmt.Sprintf("host group %q already exists", groupName)})
			}
			w, proceed, err := beginWrite(cmd, ctx, "create-group", groupName, fmt.Sprintf("create host group %q", groupName),
				map[string]any{"creates": "host group", "name": groupName, "undo": "delete group " + groupName})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			id, err := resources.CreateGroup(c, groupName)
			if err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "groupid", Header: "GROUP_ID"}, {Key: "name", Header: "NAME"}}, []map[string]any{{"groupid": id, "name": groupName}})
		},
	}
	createGroup.Flags().StringVar(&groupName, "name", "", "group name, e.g. Linux servers/Web (required)")
	_ = createGroup.MarkFlagRequired("name")
	createCmd.AddCommand(createGroup)

	updateGroup := &cobra.Command{
		Use: "group <id|name>", Short: "Rename a host group", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, outFmt, ctx, err := writeClient()
			if err != nil {
				return cliFail(outFmt, err)
			}
			if err := ctx.CheckPermission("write"); err != nil {
				return cliFail(outFmt, err)
			}
			g, err := resources.GetGroupInfo(c, args[0])
			if err != nil {
				return cliFail(outFmt, err)
			}
			w, proceed, err := beginWrite(cmd, ctx, "update-group", g.GroupID, fmt.Sprintf("rename host group %q to %q", g.Name, groupNewName),
				map[string]any{"groupid": g.GroupID, "name": g.Name})
			if err != nil {
				return cliFail(outFmt, err)
			}
			if !proceed {
				return nil
			}
			if err := resources.RenameGroup(c, g.GroupID, groupNewName); err != nil {
				return cliFail(outFmt, w.failed(err))
			}
			w.ok()
			return output.Print(outFmt, []output.Column{{Key: "groupid", Header: "GROUP_ID"}, {Key: "name", Header: "NAME"}, {Key: "previous_name", Header: "PREVIOUS"}},
				[]map[string]any{{"groupid": g.GroupID, "name": groupNewName, "previous_name": g.Name}})
		},
	}
	updateGroup.Flags().StringVar(&groupNewName, "name", "", "the new name (required)")
	_ = updateGroup.MarkFlagRequired("name")
	updateCmd.AddCommand(updateGroup)

	del := &cobra.Command{
		Use: "group <id|name>...", Short: "Delete host groups (refuses ones that still have hosts unless --allow-hosts)", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runBulk(cmd, args, deleteGroupSpec()) },
	}
	del.Flags().BoolVar(&groupAllow, "allow-hosts", false, "delete even if hosts are still in the group")
	addBulkFlags(del)
	deleteCmd.AddCommand(del)
}
