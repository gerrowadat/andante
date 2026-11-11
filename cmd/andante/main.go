// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Command andante is the andante CLI. It talks to a server's API, through
// pkg/client, and nothing else.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/oapi-codegen/nullable"
	"github.com/spf13/cobra"
)

const (
	defaultURL     = "http://127.0.0.1:8080"
	apiPath        = "/api/v1"
	versionTimeout = 2 * time.Second

	outputTable = "table"
	outputJSON  = "json"

	operationKey = "operationId"
)

func main() {
	if err := newRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		os.Exit(1)
	}
}

// app is what the commands share.
type app struct {
	url, token, output string
	stdout, stderr     io.Writer
	client             *client.ClientWithResponses
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	a := &app{stdout: stdout, stderr: stderr}
	root := &cobra.Command{
		Use:               "andante",
		Short:             "Manage an andante server.",
		Version:           buildinfo.String(),
		SilenceUsage:      true,
		PersistentPreRunE: func(*cobra.Command, []string) error { return a.connect() },
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")

	flags := root.PersistentFlags()
	flags.StringVar(&a.url, "url", envOr("ANDANTE_URL", defaultURL), "API address (ANDANTE_URL)")
	flags.StringVar(&a.token, "token", os.Getenv("ANDANTE_TOKEN"), "API token (ANDANTE_TOKEN, which keeps it out of process listings)")
	flags.StringVarP(&a.output, "output", "o", outputTable, "output: table, or json for the API's response unchanged")

	root.AddCommand(a.versionCmd(), a.whoamiCmd(), a.sourceCmd(), a.readCmd(), a.tokenCmd(), a.exportCmd(), a.importCmd(), a.apiTokenCmd(), a.inviteCmd(), a.userCmd(), a.sessionCmd(), a.auditCmd(), a.apiCmd())
	return root
}

func (a *app) connect() error {
	if a.output != outputTable && a.output != outputJSON {
		return fmt.Errorf("--output must be %s or %s, got %q", outputTable, outputJSON, a.output)
	}
	var err error
	a.client, err = client.NewClientWithResponses(strings.TrimRight(a.url, "/")+apiPath,
		client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			if a.token != "" {
				r.Header.Set("Authorization", "Bearer "+a.token)
			}
			return nil
		}))
	if err != nil {
		return fmt.Errorf("--url: %w", err)
	}
	return nil
}

// operation records which API operation a command calls, which is how a test
// shows that every operation has a command.
func operation(cmd *cobra.Command, operationID string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[operationKey] = operationID
	return cmd
}

// problemError is a failed API call, shown the way a person would want to read it.
type problemError struct {
	problem client.Problem
}

func (e *problemError) Error() string {
	var b strings.Builder
	b.WriteString(e.problem.Title)
	if e.problem.Detail != nil && *e.problem.Detail != "" {
		b.WriteString(": " + *e.problem.Detail)
	}
	if e.problem.Errors != nil {
		for _, field := range *e.problem.Errors {
			b.WriteString("\n  ")
			if field.Pointer != nil {
				b.WriteString(*field.Pointer + ": ")
			}
			b.WriteString(field.Detail)
		}
	}
	return b.String()
}

// check turns an error response into something readable.
func check(status int, body []byte) error {
	if status < http.StatusBadRequest {
		return nil
	}
	var p client.Problem
	if err := json.Unmarshal(body, &p); err != nil || p.Title == "" {
		return fmt.Errorf("the server answered %d", status)
	}
	return &problemError{problem: p}
}

// show prints a response: the API's bytes for -o json, and the table otherwise.
func (a *app) show(status int, body []byte, render func(w *table)) error {
	if err := check(status, body); err != nil {
		return err
	}
	if a.output == outputJSON {
		_, err := a.stdout.Write(body)
		return err
	}
	w := &table{Writer: tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)}
	render(w)
	if w.err != nil {
		return w.err
	}
	return w.Flush()
}

// table lines output up, and keeps the first write error for show to return.
type table struct {
	*tabwriter.Writer
	err error
}

func (t *table) printf(format string, args ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintf(t.Writer, format, args...)
	}
}

func (t *table) println(args ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintln(t.Writer, args...)
	}
}

// done reports a call with no body to show.
func (a *app) done(status int, body []byte, message string) error {
	return a.show(status, body, func(w *table) { w.println(message) })
}

// more says how to get the next page. It goes to stderr, so that the table on
// stdout stays just the table.
func (a *app) more(next nullable.Nullable[string]) {
	if next.IsNull() || !next.IsSpecified() || a.output == outputJSON {
		return
	}
	if u, err := url.Parse(next.MustGet()); err == nil {
		_, _ = fmt.Fprintf(a.stderr, "more: --cursor %s\n", u.Query().Get("cursor"))
	}
}

func text(n nullable.Nullable[string]) string {
	if n.IsNull() || !n.IsSpecified() {
		return "-"
	}
	return n.MustGet()
}

func stamp(n nullable.Nullable[time.Time]) string {
	if n.IsNull() || !n.IsSpecified() {
		return "-"
	}
	return n.MustGet().Format(time.RFC3339)
}

func parseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%q is not an ID", raw)
	}
	return id, nil
}

// page holds the flags every list has.
type page struct {
	limit  int32
	cursor string
}

func (p *page) add(cmd *cobra.Command) {
	cmd.Flags().Int32Var(&p.limit, "limit", 0, "most items to return (1 to 100)")
	cmd.Flags().StringVar(&p.cursor, "cursor", "", "continue from an earlier page")
}

func (p *page) limitParam() *client.Limit {
	if p.limit == 0 {
		return nil
	}
	return &p.limit
}

func (p *page) cursorParam() *client.Cursor {
	if p.cursor == "" {
		return nil
	}
	return &p.cursor
}

func (a *app) whoamiCmd() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "whoami",
		Short: "Show who this token belongs to.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.GetMeWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.printf("Name:\t%s\nRole:\t%s\n", resp.JSON200.Name, resp.JSON200.Role)
			})
		},
	}, "getMe")
}

func (a *app) versionCmd() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "version",
		Short: "Print the client's version, and the server's when reachable.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), versionTimeout)
			defer cancel()
			resp, err := a.client.GetVersionWithResponse(ctx)
			if a.output == outputJSON {
				if err != nil {
					return fmt.Errorf("reach the server: %w", err)
				}
				return a.show(resp.StatusCode(), resp.Body, nil)
			}
			if _, err := fmt.Fprintln(a.stdout, "client:", buildinfo.String()); err != nil {
				return err
			}
			if err != nil || resp.JSON200 == nil {
				_, err := fmt.Fprintln(a.stdout, "server: not reachable")
				return err
			}
			v := resp.JSON200
			_, err = fmt.Fprintf(a.stdout, "server: %s (commit %s, built %s)\n", v.Version, v.Commit, v.Date)
			return err
		},
	}, "getVersion")
}

func (a *app) apiCmd() *cobra.Command {
	api := &cobra.Command{Use: "api", Short: "The API itself."}
	api.AddCommand(operation(&cobra.Command{
		Use:   "spec",
		Short: "Print the server's OpenAPI document.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.GetSpecWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, err = a.stdout.Write(resp.Body)
			return err
		},
	}, "getSpec"))
	return api
}

func (a *app) apiTokenCmd() *cobra.Command {
	tokens := &cobra.Command{Use: "api-token", Short: "Manage API tokens."}

	var label string
	add := operation(&cobra.Command{
		Use:   "add",
		Short: "Create an API token. It is shown only once.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.CreateAPITokenWithResponse(cmd.Context(), client.APITokenCreate{Label: label})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.printf("ID:\t%d\nToken:\t%s\n", resp.JSON201.ID, resp.JSON201.Token)
			})
		},
	}, "createAPIToken")
	add.Flags().StringVar(&label, "label", "", "what the token is for")
	_ = add.MarkFlagRequired("label")

	var p page
	list := operation(&cobra.Command{
		Use:   "list",
		Short: "List API tokens.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.ListAPITokensWithResponse(cmd.Context(), &client.ListAPITokensParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("ID\tLABEL\tLAST USED\tREVOKED")
				for _, t := range resp.JSON200.Items {
					w.printf("%d\t%s\t%s\t%s\n", t.ID, t.Label, stamp(t.LastUsedAt), stamp(t.RevokedAt))
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listAPITokens")
	p.add(list)

	remove := operation(&cobra.Command{
		Use:   "rm <id>",
		Short: "Revoke an API token.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.RevokeAPITokenWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "revoked")
		},
	}, "revokeAPIToken")

	tokens.AddCommand(add, list, remove)
	return tokens
}

var errNotFound = errors.New("not found")
