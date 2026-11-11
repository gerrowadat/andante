// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"errors"

	"github.com/gerrowadat/andante/pkg/client"
	"github.com/spf13/cobra"
)

func (a *app) userCmd() *cobra.Command {
	users := &cobra.Command{Use: "user", Short: "Manage users. Admins only."}

	var p page
	list := operation(&cobra.Command{
		Use:   "list",
		Short: "List users.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.ListUsersWithResponse(cmd.Context(), &client.ListUsersParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("ID\tNAME\tROLE\tDISABLED")
				for _, u := range resp.JSON200.Items {
					w.printf("%d\t%s\t%s\t%s\n", u.ID, u.Name, u.Role, stamp(u.DisabledAt))
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listUsers")
	p.add(list)

	var (
		role            string
		disable, enable bool
	)
	edit := operation(&cobra.Command{
		Use:   "edit <id>",
		Short: "Change a user's role, or disable or enable them. Disabling leaves their feeds running.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			var patch client.UserPatch
			if role != "" {
				r := client.UserPatchRole(role)
				patch.Role = &r
			}
			if disable && enable {
				return errors.New("choose one of --disable and --enable")
			}
			if disable || enable {
				patch.Disabled = &disable
			}
			resp, err := a.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(cmd.Context(), id, patch)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.printf("Name:\t%s\nRole:\t%s\nDisabled:\t%s\n", resp.JSON200.Name, resp.JSON200.Role, stamp(resp.JSON200.DisabledAt))
			})
		},
	}, "updateUser")
	edit.Flags().StringVar(&role, "role", "", "admin or user")
	edit.Flags().BoolVar(&disable, "disable", false, "stop them logging in or using the API")
	edit.Flags().BoolVar(&enable, "enable", false, "let them back in")

	var sure bool
	remove := operation(&cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a user with their reads and tokens. It can't be undone.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			if !sure {
				return errors.New("this deletes the user's reads and tokens for good; add --yes if you mean it")
			}
			resp, err := a.client.DeleteUserWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "deleted")
		},
	}, "deleteUser")
	remove.Flags().BoolVar(&sure, "yes", false, "really delete them")

	users.AddCommand(list, edit, remove)
	return users
}
