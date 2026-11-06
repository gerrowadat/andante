// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gerrowadat/andante/pkg/client"
	"github.com/oapi-codegen/nullable"
	"github.com/spf13/cobra"
)

// resolveSource accepts an ID or a slug.
func (a *app) resolveSource(ctx context.Context, ref string) (int64, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return id, nil
	}
	params := &client.ListSourcesParams{}
	for {
		resp, err := a.client.ListSourcesWithResponse(ctx, params)
		if err != nil {
			return 0, err
		}
		if err := check(resp.StatusCode(), resp.Body); err != nil {
			return 0, err
		}
		for _, s := range resp.JSON200.Items {
			if s.Slug == ref {
				return s.ID, nil
			}
		}
		cursor, more := nextCursor(resp.JSON200.Next)
		if !more {
			return 0, fmt.Errorf("source %q: %w", ref, errNotFound)
		}
		params.Cursor = &cursor
	}
}

// selectors parses repeated --selector field=css flags.
func selectors(flags []string) (*client.Selectors, error) {
	if len(flags) == 0 {
		return nil, nil //nolint:nilnil // None is a normal answer.
	}
	var s client.Selectors
	for _, flag := range flags {
		field, css, ok := strings.Cut(flag, "=")
		if !ok || css == "" {
			return nil, fmt.Errorf("--selector %q: want field=css", flag)
		}
		switch field {
		case "title":
			s.Title = &css
		case "image":
			s.Image = &css
		case "alt_text":
			s.AltText = &css
		case "date":
			s.Date = &css
		case "next":
			s.Next = &css
		default:
			return nil, fmt.Errorf("--selector %q: the fields are title, image, alt_text, date and next", flag)
		}
	}
	return &s, nil
}

func crawlConfig(startURL string, flags []string) (client.SourceConfig, error) {
	var config client.SourceConfig
	parsed, err := selectors(flags)
	if err != nil {
		return config, err
	}
	err = config.FromCrawlConfig(client.CrawlConfig{Strategy: client.Crawl, StartURL: startURL, Selectors: parsed})
	return config, err
}

func kindOf(raw string) (client.Kind, error) {
	kind := client.Kind(raw)
	if !kind.Valid() {
		return "", fmt.Errorf("--kind must be page, episode or article, got %q", raw)
	}
	return kind, nil
}

func (a *app) sourceCmd() *cobra.Command {
	source := &cobra.Command{Use: "source", Short: "Manage sources."}
	source.AddCommand(a.sourceProbe(), a.sourceAdd(), a.sourceList(), a.sourceGet(), a.sourceEdit(),
		a.sourceRemove(), a.sourceCrawl(), a.sourceEntries())
	return source
}

func (a *app) sourceProbe() *cobra.Command {
	var (
		flags []string
		kind  string
		limit int32
	)
	cmd := operation(&cobra.Command{
		Use:   "probe <url>",
		Short: "Try a start URL and show what would be indexed. Saves nothing.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := crawlConfig(args[0], flags)
			if err != nil {
				return err
			}
			request := client.ProbeRequest{Config: config}
			if kind != "" {
				parsed, err := kindOf(kind)
				if err != nil {
					return err
				}
				request.DefaultKind = &parsed
			}
			if limit != 0 {
				request.Limit = &limit
			}
			resp, err := a.client.CreateProbeWithResponse(cmd.Context(), request)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("SEQ\tTITLE\tDATE\tURL")
				for _, e := range resp.JSON200.Entries {
					w.printf("%d\t%s\t%s\t%s\n", e.Seq, text(e.Title), stamp(e.OriginalDate), text(e.URL))
				}
				w.printf("\nDated:\t%.0f%%\n", resp.JSON200.DatedShare*100)
				for _, warning := range resp.JSON200.Warnings {
					w.println("Warning:", warning)
				}
			})
		},
	}, "createProbe")
	cmd.Flags().StringArrayVar(&flags, "selector", nil, "override extraction, as field=css")
	cmd.Flags().StringVar(&kind, "kind", "", "entry kind: page, episode or article")
	cmd.Flags().Int32Var(&limit, "limit", 0, "most entries to try (at most 25)")
	return cmd
}

func (a *app) sourceAdd() *cobra.Command {
	var (
		slug, title, homeURL, kind, imageURL string
		flags                                []string
	)
	cmd := operation(&cobra.Command{
		Use:   "add <start-url>",
		Short: "Add a source. Probe it first.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := crawlConfig(args[0], flags)
			if err != nil {
				return err
			}
			parsed, err := kindOf(kind)
			if err != nil {
				return err
			}
			create := client.SourceCreate{Slug: slug, Title: title, HomeURL: homeURL, DefaultKind: parsed, Config: config}
			if create.Title == "" {
				create.Title = slug
			}
			if create.HomeURL == "" {
				create.HomeURL = args[0]
			}
			if imageURL != "" {
				create.ImageURL = &imageURL
			}
			resp, err := a.client.CreateSourceWithResponse(cmd.Context(), create)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { printSource(w, resp.JSON201) })
		},
	}, "createSource")
	cmd.Flags().StringVar(&slug, "slug", "", "short name, such as my-comic")
	cmd.Flags().StringVar(&title, "title", "", "title, by default the slug")
	cmd.Flags().StringVar(&homeURL, "home-url", "", "home page, by default the start URL")
	cmd.Flags().StringVar(&kind, "kind", string(client.Page), "entry kind: page, episode or article")
	cmd.Flags().StringVar(&imageURL, "image-url", "", "artwork")
	cmd.Flags().StringArrayVar(&flags, "selector", nil, "override extraction, as field=css")
	_ = cmd.MarkFlagRequired("slug")
	return cmd
}

func printSource(w *table, s *client.Source) {
	w.printf("ID:\t%d\nSlug:\t%s\nTitle:\t%s\nHome:\t%s\nKind:\t%s\nState:\t%s\n", s.ID, s.Slug, s.Title, s.HomeURL, s.DefaultKind, s.State)
	w.printf("Due:\t%s\nLast crawled:\t%s\nLast error:\t%s\n", stamp(s.DueAt), stamp(s.LastCrawledAt), text(s.LastError))
}

func (a *app) sourceList() *cobra.Command {
	var p page
	cmd := operation(&cobra.Command{
		Use:   "list",
		Short: "List sources.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.ListSourcesWithResponse(cmd.Context(), &client.ListSourcesParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("ID\tSLUG\tSTATE\tKIND\tTITLE")
				for _, s := range resp.JSON200.Items {
					w.printf("%d\t%s\t%s\t%s\t%s\n", s.ID, s.Slug, s.State, s.DefaultKind, s.Title)
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listSources")
	p.add(cmd)
	return cmd
}

func (a *app) sourceGet() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "get <id|slug>",
		Short: "Show a source.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.resolveSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.GetSourceWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { printSource(w, resp.JSON200) })
		},
	}, "getSource")
}

func (a *app) sourceEdit() *cobra.Command {
	var (
		title, homeURL, kind, imageURL, startURL string
		clearImage, pause, resume                bool
		flags                                    []string
	)
	cmd := operation(&cobra.Command{
		Use:   "edit <id|slug>",
		Short: "Change a source.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.resolveSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var patch client.SourcePatch
			changed := cmd.Flags().Changed
			if changed("title") {
				patch.Title = &title
			}
			if changed("home-url") {
				patch.HomeURL = &homeURL
			}
			if changed("kind") {
				parsed, err := kindOf(kind)
				if err != nil {
					return err
				}
				patch.DefaultKind = &parsed
			}
			if changed("image-url") {
				patch.ImageURL = nullable.NewNullableWithValue(imageURL)
			}
			if clearImage {
				patch.ImageURL = nullable.NewNullNullable[string]()
			}
			if pause {
				state := client.SourcePatchStatePaused
				patch.State = &state
			}
			if resume {
				state := client.SourcePatchStateIndexing
				patch.State = &state
			}
			if changed("start-url") || changed("selector") {
				config, err := a.editedConfig(cmd.Context(), id, startURL, flags)
				if err != nil {
					return err
				}
				patch.Config = &config
			}
			resp, err := a.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(cmd.Context(), id, patch)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { printSource(w, resp.JSON200) })
		},
	}, "updateSource")
	f := cmd.Flags()
	f.StringVar(&title, "title", "", "title")
	f.StringVar(&homeURL, "home-url", "", "home page")
	f.StringVar(&kind, "kind", "", "entry kind: page, episode or article")
	f.StringVar(&imageURL, "image-url", "", "artwork")
	f.BoolVar(&clearImage, "clear-image", false, "remove the artwork")
	f.BoolVar(&pause, "pause", false, "stop crawling")
	f.BoolVar(&resume, "resume", false, "crawl again, which also restarts a source in error")
	f.StringVar(&startURL, "start-url", "", "where the crawl starts")
	f.StringArrayVar(&flags, "selector", nil, "replace the selectors, as field=css")
	cmd.MarkFlagsMutuallyExclusive("pause", "resume")
	cmd.MarkFlagsMutuallyExclusive("image-url", "clear-image")
	return cmd
}

// editedConfig changes a crawl config without losing the parts not being changed.
func (a *app) editedConfig(ctx context.Context, id int64, startURL string, flags []string) (client.SourceConfig, error) {
	var config client.SourceConfig
	resp, err := a.client.GetSourceWithResponse(ctx, id)
	if err != nil {
		return config, err
	}
	if err := check(resp.StatusCode(), resp.Body); err != nil {
		return config, err
	}
	current, err := resp.JSON200.Config.AsCrawlConfig()
	if err != nil {
		return config, err
	}
	if startURL != "" {
		current.StartURL = startURL
	}
	if len(flags) > 0 {
		if current.Selectors, err = selectors(flags); err != nil {
			return config, err
		}
	}
	err = config.FromCrawlConfig(current)
	return config, err
}

func (a *app) sourceRemove() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "rm <id|slug>",
		Short: "Delete a source and its entries.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.resolveSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.DeleteSourceWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "deleted")
		},
	}, "deleteSource")
}

func (a *app) sourceCrawl() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "crawl <id|slug>",
		Short: "Ask for a crawl now. It happens in the background.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.resolveSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.RequestCrawlWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "crawl requested")
		},
	}, "requestCrawl")
}

func (a *app) sourceEntries() *cobra.Command {
	var p page
	cmd := operation(&cobra.Command{
		Use:   "entries <id|slug>",
		Short: "List a source's indexed entries.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.resolveSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.ListEntriesWithResponse(cmd.Context(), id, &client.ListEntriesParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("SEQ\tKIND\tTITLE\tURL")
				for _, e := range resp.JSON200.Items {
					w.printf("%d\t%s\t%s\t%s\n", e.Seq, e.Kind, text(e.Title), text(e.URL))
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listEntries")
	p.add(cmd)
	return cmd
}
