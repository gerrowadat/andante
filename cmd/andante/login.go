// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"time"

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
		uses         int32
		days         int
	)
	add := operation(&cobra.Command{
		Use:   "add [name]",
		Short: "Make a link for people to set a password. Without a name, whoever uses it picks their own.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role := client.InviteCreateRoleUser
			if admin {
				role = client.InviteCreateRoleAdmin
			}
			body := client.InviteCreate{Role: &role, Reset: &reset, MaxUses: &uses}
			if len(args) == 1 {
				body.Name = &args[0]
			}
			if days > 0 {
				expires := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
				body.ExpiresAt = &expires
			}
			resp, err := a.client.CreateInviteWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				i := resp.JSON201
				w.printf("ID:\t%d\nName:\t%s\nRole:\t%s\nUses:\t%d\nExpires:\t%s\nLink:\t%s\n", i.ID, i.Name, i.Role, i.MaxUses, i.ExpiresAt, i.URL)
			})
		},
	}, "createInvite")
	add.Flags().BoolVar(&admin, "admin", false, "make them an admin (admins only)")
	add.Flags().BoolVar(&reset, "reset", false, "this is for an existing user, to set a new password (admins only)")
	add.Flags().Int32Var(&uses, "uses", 1, "how many people can use it")
	add.Flags().IntVar(&days, "days", 0, "how many days it works for, up to 30 (default 7)")

	var p page
	list := operation(&cobra.Command{
		Use:   "list",
		Short: "List invites with uses left: yours, or everyone's for an admin.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.ListInvitesWithResponse(cmd.Context(), &client.ListInvitesParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("ID\tNAME\tROLE\tUSED\tEXPIRES\tMADE BY")
				for _, i := range resp.JSON200.Items {
					name, by := i.Name, "the server"
					if name == "" {
						name = "-"
					}
					if i.Reset {
						name += " (reset)"
					}
					if i.CreatedBy != nil {
						by = *i.CreatedBy
					}
					w.printf("%d\t%s\t%s\t%d of %d\t%s\t%s\n", i.ID, name, i.Role, i.Uses, i.MaxUses, i.ExpiresAt, by)
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listInvites")
	p.add(list)

	remove := operation(&cobra.Command{
		Use:   "rm <id>",
		Short: "Withdraw an invite, so that its link stops working.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.DeleteInviteWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "withdrawn")
		},
	}, "deleteInvite")

	invites.AddCommand(add, list, remove)
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
		w.printf("Require two-factor:\t%t\nWho can invite:\t%s\nInvite limit:\t%d\nPassword min length:\t%d\nPassword classes:\t%d\n",
			s.RequireTwoFactor, s.WhoCanInvite, s.InviteLimit, s.PasswordMinLength, s.PasswordClasses)
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

	var (
		requireTwoFactor bool
		whoCanInvite     string
		inviteLimit      int32
		minLength        int32
		classes          int32
	)
	set := operation(&cobra.Command{
		Use:   "set",
		Short: "Change the site's settings. Admins only.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var patch client.SettingsPatch
			if cmd.Flags().Changed("require-two-factor") {
				patch.RequireTwoFactor = &requireTwoFactor
			}
			if cmd.Flags().Changed("who-can-invite") {
				who := client.WhoCanInvite(whoCanInvite)
				patch.WhoCanInvite = &who
			}
			if cmd.Flags().Changed("invite-limit") {
				patch.InviteLimit = &inviteLimit
			}
			if cmd.Flags().Changed("password-min-length") {
				patch.PasswordMinLength = &minLength
			}
			if cmd.Flags().Changed("password-classes") {
				patch.PasswordClasses = &classes
			}
			resp, err := a.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(cmd.Context(), patch)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { showSettings(w, resp.JSON200) })
		},
	}, "updateSettings")
	set.Flags().BoolVar(&requireTwoFactor, "require-two-factor", false, "make everyone who logs in to the website with a password set up two-factor")
	set.Flags().StringVar(&whoCanInvite, "who-can-invite", "", "who can invite people: admins or users")
	set.Flags().Int32Var(&inviteLimit, "invite-limit", 0, "how many unused invite places a user can have out at once")
	set.Flags().Int32Var(&minLength, "password-min-length", 0, "the fewest characters a new password can have, 8 to 128")
	set.Flags().Int32Var(&classes, "password-classes", 0,
		"how many of lower-case letters, capitals, digits and anything else a new password needs, 0 to 4")
	settings.AddCommand(set)
	return settings
}
