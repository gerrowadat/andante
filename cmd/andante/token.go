// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"fmt"
	"os"

	"github.com/gerrowadat/andante/pkg/client"
	"github.com/spf13/cobra"
)

const opmlType = "text/x-opml"

func optional(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func (a *app) tokenCmd() *cobra.Command {
	tokens := &cobra.Command{Use: "token", Short: "Manage feed tokens."}

	var (
		label string
		owner bool
	)
	add := operation(&cobra.Command{
		Use:   "add <read>",
		Short: "Issue a feed token. Its URLs are shown only once.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			role := client.Viewer
			if owner {
				role = client.Owner
			}
			resp, err := a.client.CreateFeedTokenWithResponse(cmd.Context(), id, client.FeedTokenCreate{Role: role, Label: label})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				t := resp.JSON201
				w.printf("ID:\t%d\nRole:\t%s\nAtom:\t%s\nRSS:\t%s\nJSON:\t%s\nSuggested:\t%s\n", t.ID, t.Role, t.Urls.Atom, t.Urls.Rss, t.Urls.JSON, t.Suggested)
			})
		},
	}, "createFeedToken")
	add.Flags().StringVar(&label, "label", "", "who the token is for")
	add.Flags().BoolVar(&owner, "owner", false, "include the next-one-now control")
	_ = add.MarkFlagRequired("label")

	var p page
	list := operation(&cobra.Command{
		Use:   "list <read>",
		Short: "List a read's feed tokens.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.ListFeedTokensWithResponse(cmd.Context(), id, &client.ListFeedTokensParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("ID\tROLE\tLABEL\tLAST SEEN\tREVOKED")
				for _, t := range resp.JSON200.Items {
					w.printf("%d\t%s\t%s\t%s\t%s\n", t.ID, t.Role, t.Label, stamp(t.LastSeenAt), stamp(t.RevokedAt))
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listFeedTokens")
	p.add(list)

	remove := operation(&cobra.Command{
		Use:   "rm <id>",
		Short: "Revoke a feed token.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.RevokeFeedTokenWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "revoked")
		},
	}, "revokeFeedToken")

	tokens.AddCommand(add, list, remove)
	return tokens
}

func (a *app) exportCmd() *cobra.Command {
	export := &cobra.Command{Use: "export", Short: "Export data."}
	var issue, label string
	opml := operation(&cobra.Command{
		Use:   "opml",
		Short: "Write an OPML file of every read, issuing a new token for each.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			role := client.Role(issue)
			if !role.Valid() {
				return fmt.Errorf("--issue must be owner or viewer, got %q", issue)
			}
			resp, err := a.client.ExportOPMLWithResponse(cmd.Context(), client.OPMLExportRequest{Issue: role, Label: label})
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, err = a.stdout.Write(resp.Body)
			return err
		},
	}, "exportOPML")
	opml.Flags().StringVar(&issue, "issue", "", "role of the new tokens: owner or viewer")
	opml.Flags().StringVar(&label, "label", "", "who the new tokens are for")
	_ = opml.MarkFlagRequired("issue")
	_ = opml.MarkFlagRequired("label")
	export.AddCommand(opml)
	return export
}

func (a *app) importCmd() *cobra.Command {
	imports := &cobra.Command{Use: "import", Short: "Import data."}
	imports.AddCommand(operation(&cobra.Command{
		Use:   "opml <file>",
		Short: "Create a paused source for each feed in an OPML file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer func() { _ = file.Close() }()
			resp, err := a.client.ImportOPMLWithBodyWithResponse(cmd.Context(), opmlType, file)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("STATUS\tTITLE\tFEED")
				for _, r := range resp.JSON200.Results {
					w.printf("%s\t%s\t%s\n", r.Status, optional(r.Title), optional(r.FeedURL))
				}
			})
		},
	}, "importOPML"))
	return imports
}
