// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/spf13/cobra"
)

func (a *app) inviteCmd() *cobra.Command {
	invites := &cobra.Command{Use: "invite", Short: "Invite people."}
	var (
		admin, reset bool
	)
	add := operation(&cobra.Command{
		Use:   "add <name>",
		Short: "Make a single-use link for someone to set a password. Admins only.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role := client.InviteCreateRoleUser
			if admin {
				role = client.InviteCreateRoleAdmin
			}
			resp, err := a.client.CreateInviteWithResponse(cmd.Context(), client.InviteCreate{Name: args[0], Role: &role, Reset: &reset})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.printf("Name:\t%s\nRole:\t%s\nExpires:\t%s\nLink:\t%s\n", resp.JSON201.Name, resp.JSON201.Role, resp.JSON201.ExpiresAt, resp.JSON201.URL)
			})
		},
	}, "createInvite")
	add.Flags().BoolVar(&admin, "admin", false, "make them an admin")
	add.Flags().BoolVar(&reset, "reset", false, "this is for an existing user, to set a new password")
	invites.AddCommand(add)
	return invites
}

func (a *app) sessionCmd() *cobra.Command {
	sessions := &cobra.Command{Use: "session", Short: "Manage your logged-in sessions."}

	var p page
	list := operation(&cobra.Command{
		Use:   "list",
		Short: "List your sessions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.ListSessionsWithResponse(cmd.Context(), &client.ListSessionsParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("ID\tADDRESS\tSTARTED\tLAST SEEN\tBROWSER")
				for _, s := range resp.JSON200.Items {
					w.printf("%d\t%s\t%s\t%s\t%s\n", s.ID, s.IP, s.CreatedAt, s.LastSeenAt, s.UserAgent)
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listSessions")
	p.add(list)

	remove := operation(&cobra.Command{
		Use:   "rm <id>",
		Short: "End a session.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.RevokeSessionWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "ended")
		},
	}, "revokeSession")

	sessions.AddCommand(list, remove)
	return sessions
}

func (a *app) auditCmd() *cobra.Command {
	var p page
	audit := operation(&cobra.Command{
		Use:   "audit",
		Short: "Read the audit log. Admins only.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.ListAuditWithResponse(cmd.Context(), &client.ListAuditParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("AT\tWHO\tACTION\tTARGET\tADDRESS")
				for _, e := range resp.JSON200.Items {
					w.printf("%s\t%s\t%s\t%s\t%s\n", e.At, e.Who, e.Action, e.Target, e.IP)
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listAudit")
	p.add(audit)
	return audit
}
