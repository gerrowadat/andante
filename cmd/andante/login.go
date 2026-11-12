// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"bufio"
	"errors"
	"os"
	"strings"

	"github.com/gerrowadat/andante/pkg/client"
	"github.com/spf13/cobra"
)

func (a *app) bootstrapCmd() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "bootstrap <name>",
		Short: "Make the first admin with the bootstrap token, and print their API token.",
		Long: "Make the first admin with the bootstrap token, and print their API token and a link to set a password. " +
			"The token comes from ANDANTE_BOOTSTRAP_TOKEN, or the first line of standard input, so that it stays out of process listings. " +
			"It only works while the server has no admin.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			token := os.Getenv("ANDANTE_BOOTSTRAP_TOKEN")
			if token == "" {
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				token = strings.TrimSpace(line)
				if token == "" {
					return errors.Join(errors.New("give the bootstrap token in ANDANTE_BOOTSTRAP_TOKEN or on standard input"), err)
				}
			}
			resp, err := a.client.BootstrapWithResponse(cmd.Context(), client.BootstrapRequest{Token: token, Name: args[0]})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				result := resp.JSON201
				w.printf("Name:\t%s\nRole:\t%s\nAPI token:\t%s\n", result.User.Name, result.User.Role, result.Token)
				if result.PasswordURL != nil {
					w.printf("Password link:\t%s\n", *result.PasswordURL)
				}
			})
		},
	}, "bootstrap")
}

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

func (a *app) settingsCmd() *cobra.Command {
	showSettings := func(w *table, s *client.Settings) {
		w.printf("Require two-factor:\t%t\n", s.RequireTwoFactor)
	}
	settings := operation(&cobra.Command{
		Use:   "settings",
		Short: "Show the site's settings. Admins only.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.GetSettingsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { showSettings(w, resp.JSON200) })
		},
	}, "getSettings")

	var requireTwoFactor bool
	set := operation(&cobra.Command{
		Use:   "set",
		Short: "Change the site's settings. Admins only.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var patch client.SettingsPatch
			if cmd.Flags().Changed("require-two-factor") {
				patch.RequireTwoFactor = &requireTwoFactor
			}
			resp, err := a.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(cmd.Context(), patch)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { showSettings(w, resp.JSON200) })
		},
	}, "updateSettings")
	set.Flags().BoolVar(&requireTwoFactor, "require-two-factor", false, "make everyone who logs in to the website with a password set up two-factor")
	settings.AddCommand(set)
	return settings
}
