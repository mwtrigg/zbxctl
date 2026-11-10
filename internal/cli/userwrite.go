package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	cuUsername, cuName, cuSurname, cuRole string
	cuGroups                              []string
	cuPasswordFile                        string
	cuPasswordStdin                       bool

	cgName     string
	cgGUI      string
	cgDisabled bool
)

var createUserCmd = &cobra.Command{
	Use:   "user",
	Short: "Create a user (password from --password-file or --password-stdin, never a flag value)",
	Args:  cobra.NoArgs,
	RunE:  runCreateUser,
}

var deleteUserCmd = &cobra.Command{
	Use:   "user <id|username>",
	Short: "Delete a user (refuses Admin and the user that owns the current token)",
	Args:  cobra.ExactArgs(1),
	RunE:  runDeleteUser,
}

var createUserGroupCmd = &cobra.Command{
	Use:   "usergroup",
	Short: "Create a user group",
	Args:  cobra.NoArgs,
	RunE:  runCreateUserGroup,
}

var deleteUserGroupCmd = &cobra.Command{
	Use:   "usergroup <id|name>",
	Short: "Delete a user group",
	Args:  cobra.ExactArgs(1),
	RunE:  runDeleteUserGroup,
}

func init() {
	f := createUserCmd.Flags()
	f.StringVar(&cuUsername, "username", "", "login name (required)")
	f.StringVar(&cuPasswordFile, "password-file", "", "file holding the initial password")
	f.BoolVar(&cuPasswordStdin, "password-stdin", false, "read the initial password from stdin")
	f.StringVar(&cuRole, "role", "", "role name or ID (required)")
	f.StringArrayVar(&cuGroups, "group", nil, "user group name or ID (required, repeatable)")
	f.StringVar(&cuName, "name", "", "first name")
	f.StringVar(&cuSurname, "surname", "", "surname")
	_ = createUserCmd.MarkFlagRequired("username")
	_ = createUserCmd.MarkFlagRequired("role")
	_ = createUserCmd.MarkFlagRequired("group")

	g := createUserGroupCmd.Flags()
	g.StringVar(&cgName, "name", "", "group name (required)")
	g.StringVar(&cgGUI, "gui-access", "default", "frontend access: default|internal|ldap|disabled")
	g.BoolVar(&cgDisabled, "disabled", false, "create the group disabled")
	_ = createUserGroupCmd.MarkFlagRequired("name")

	createCmd.AddCommand(createUserCmd, createUserGroupCmd)
	deleteCmd.AddCommand(deleteUserCmd, deleteUserGroupCmd)
}

func readPassword() (string, error) {
	var r io.Reader
	switch {
	case cuPasswordFile != "" && cuPasswordStdin:
		return "", &usageError{"use only one of --password-file and --password-stdin"}
	case cuPasswordFile != "":
		b, err := os.ReadFile(cuPasswordFile)
		if err != nil {
			return "", fmt.Errorf("reading password file: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	case cuPasswordStdin:
		r = os.Stdin
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil && line == "" {
			return "", &usageError{"no password on stdin"}
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	return "", &usageError{"provide the password with --password-file or --password-stdin"}
}

func runCreateUser(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliFail(outFmt, err)
	}
	pw, err := readPassword()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if pw == "" {
		return cliPrintErr(outFmt, "the password is empty", output.ExitUsage)
	}
	role, err := resources.GetRole(c, cuRole)
	if err != nil {
		return cliFail(outFmt, err)
	}
	var gids, gnames []string
	for _, g := range cuGroups {
		ug, err := resources.GetUserGroup(c, g)
		if err != nil {
			return cliFail(outFmt, err)
		}
		gids = append(gids, ug.UserGroupID)
		gnames = append(gnames, ug.Name)
	}
	w, proceed, err := beginWrite(cmd, ctx, "create-user", cuUsername, fmt.Sprintf("create user %q (role %s, groups %s)", cuUsername, role.Name, strings.Join(gnames, ",")),
		map[string]any{"creates": "user", "username": cuUsername, "role": role.Name, "groups": gnames, "undo": "delete user " + cuUsername})
	if err != nil {
		return cliFail(outFmt, err)
	}
	if !proceed {
		return nil
	}
	id, err := resources.CreateUser(c, resources.CreateUserInput{
		Username: cuUsername, Password: pw, Name: cuName, Surname: cuSurname, RoleID: role.RoleID, GroupIDs: gids,
	})
	if err != nil {
		return cliFail(outFmt, w.failed(err))
	}
	w.ok()
	return output.Print(outFmt, []output.Column{{Key: "userid", Header: "USER_ID"}, {Key: "username", Header: "USERNAME"}, {Key: "role", Header: "ROLE"}, {Key: "groups", Header: "GROUPS"}},
		[]map[string]any{{"userid": id, "username": cuUsername, "role": role.Name, "groups": gnames}})
}

// userDeleteBlock returns an error when deleting this user must be refused.
func userDeleteBlock(username, userID, ownerID string) error {
	if strings.EqualFold(username, "Admin") {
		return &usageError{"refusing to delete the Admin user"}
	}
	if userID == ownerID {
		return &usageError{fmt.Sprintf("refusing to delete %q: it owns the credentials zbxctl is using", username)}
	}
	return nil
}

func runDeleteUser(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliFail(outFmt, err)
	}
	u, err := resources.GetUser(c, args[0])
	if err != nil {
		return cliFail(outFmt, err)
	}
	ownerID, _, err := c.CurrentUser()
	if err != nil {
		return cliFail(outFmt, fmt.Errorf("cannot tell which user owns the current credentials, so refusing to delete users: %w", err))
	}
	if err := userDeleteBlock(u.Username, u.UserID, ownerID); err != nil {
		return cliFail(outFmt, err)
	}
	w, proceed, err := beginWrite(cmd, ctx, "delete-user", u.UserID, fmt.Sprintf("delete user %q (id %s)", u.Username, u.UserID), fieldsOf(u))
	if err != nil {
		return cliFail(outFmt, err)
	}
	if !proceed {
		return nil
	}
	if err := resources.DeleteUser(c, u.UserID); err != nil {
		return cliFail(outFmt, w.failed(err))
	}
	w.ok()
	return output.Print(outFmt, []output.Column{{Key: "userid", Header: "USER_ID"}, {Key: "username", Header: "USERNAME"}, {Key: "status", Header: "STATUS"}},
		[]map[string]any{{"userid": u.UserID, "username": u.Username, "status": "deleted"}})
}

var guiAccessCodes = map[string]string{"default": "0", "internal": "1", "ldap": "2", "disabled": "3"}

func runCreateUserGroup(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliFail(outFmt, err)
	}
	code, ok := guiAccessCodes[cgGUI]
	if !ok {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --gui-access %q: use default|internal|ldap|disabled", cgGUI), output.ExitUsage)
	}
	w, proceed, err := beginWrite(cmd, ctx, "create-usergroup", cgName, fmt.Sprintf("create user group %q", cgName),
		map[string]any{"creates": "user group", "name": cgName, "gui_access": cgGUI, "disabled": cgDisabled, "undo": "delete usergroup " + cgName})
	if err != nil {
		return cliFail(outFmt, err)
	}
	if !proceed {
		return nil
	}
	id, err := resources.CreateUserGroup(c, cgName, code, cgDisabled)
	if err != nil {
		return cliFail(outFmt, w.failed(err))
	}
	w.ok()
	return output.Print(outFmt, []output.Column{{Key: "usrgrpid", Header: "GROUP_ID"}, {Key: "name", Header: "NAME"}},
		[]map[string]any{{"usrgrpid": id, "name": cgName, "gui_access": cgGUI, "disabled": cgDisabled}})
}

func runDeleteUserGroup(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliFail(outFmt, err)
	}
	g, err := resources.GetUserGroup(c, args[0])
	if err != nil {
		return cliFail(outFmt, err)
	}
	w, proceed, err := beginWrite(cmd, ctx, "delete-usergroup", g.UserGroupID, fmt.Sprintf("delete user group %q (id %s)", g.Name, g.UserGroupID), fieldsOf(g))
	if err != nil {
		return cliFail(outFmt, err)
	}
	if !proceed {
		return nil
	}
	if err := resources.DeleteUserGroup(c, g.UserGroupID); err != nil {
		return cliFail(outFmt, w.failed(err))
	}
	w.ok()
	return output.Print(outFmt, []output.Column{{Key: "usrgrpid", Header: "GROUP_ID"}, {Key: "name", Header: "NAME"}, {Key: "status", Header: "STATUS"}},
		[]map[string]any{{"usrgrpid": g.UserGroupID, "name": g.Name, "status": "deleted"}})
}
