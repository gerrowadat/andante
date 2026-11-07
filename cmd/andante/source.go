// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"context"
	"encoding/json"
	"errors"
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

// configFlags choose a strategy and set it up. The URL argument means the strategy's
// own URL: a start page, a feed, or a template with {n}.
type configFlags struct {
	strategy, format, pageParam string
	start                       int64
	misses, maxPages            int32
	selectors, fields           []string
	fullText, fillGaps          bool
}

func (c *configFlags) add(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&c.strategy, "strategy", string(client.Crawl), "crawl, feed, feed-paged or numeric")
	f.StringArrayVar(&c.selectors, "selector", nil, "crawl: override extraction, as field=css")
	f.BoolVar(&c.fullText, "full-text", false, "store articles' main text (off by default; see docs/sources.md)")
	f.BoolVar(&c.fillGaps, "fill-gaps", false, "feed: fetch each item's page for what the feed lacks")
	f.Int32Var(&c.maxPages, "max-pages", 0, "feed-paged: most pages to walk")
	f.StringVar(&c.pageParam, "page-param", "", "feed-paged: the page parameter (default paged)")
	f.Int64Var(&c.start, "start-id", 0, "numeric: the first ID")
	f.Int32Var(&c.misses, "max-misses", 0, "numeric: misses in a row that mean the live end")
	f.StringVar(&c.format, "format", "", "numeric: html or json")
	f.StringArrayVar(&c.fields, "field", nil, "numeric, json: where a field is, as field=key, such as title=safe_title")
}

func jsonFieldsOf(flags []string) (*client.JSONFields, error) {
	if len(flags) == 0 {
		return nil, nil //nolint:nilnil // None is a normal answer.
	}
	var f client.JSONFields
	for _, flag := range flags {
		field, key, ok := strings.Cut(flag, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--field %q: want field=key", flag)
		}
		switch field {
		case "title":
			f.Title = &key
		case "image":
			f.Image = &key
		case "alt_text":
			f.AltText = &key
		case "date":
			f.Date = &key
		case "year":
			f.Year = &key
		case "month":
			f.Month = &key
		case "day":
			f.Day = &key
		default:
			return nil, fmt.Errorf("--field %q: the fields are title, image, alt_text, date, year, month and day", flag)
		}
	}
	return &f, nil
}

func (c *configFlags) build(target string) (client.SourceConfig, error) {
	var config client.SourceConfig
	switch c.strategy {
	case string(client.Crawl):
		parsed, err := selectors(c.selectors)
		if err != nil {
			return config, err
		}
		crawl := client.CrawlConfig{Strategy: client.Crawl, StartURL: target, Selectors: parsed}
		if c.fullText {
			crawl.FullText = &c.fullText
		}
		return config, config.FromCrawlConfig(crawl)
	case string(client.Feed):
		feed := client.FeedConfig{Strategy: client.Feed, FeedURL: target}
		if c.fillGaps {
			feed.FillGaps = &c.fillGaps
		}
		if c.fullText {
			feed.FullText = &c.fullText
		}
		return config, config.FromFeedConfig(feed)
	case string(client.FeedPaged):
		feed := client.FeedPagedConfig{Strategy: client.FeedPaged, FeedURL: target}
		if c.fillGaps {
			feed.FillGaps = &c.fillGaps
		}
		if c.fullText {
			feed.FullText = &c.fullText
		}
		if c.maxPages != 0 {
			feed.MaxPages = &c.maxPages
		}
		if c.pageParam != "" {
			feed.PageParam = &c.pageParam
		}
		return config, config.FromFeedPagedConfig(feed)
	case string(client.Numeric):
		numeric := client.NumericConfig{Strategy: client.Numeric, URLTemplate: target, Start: c.start}
		if c.misses != 0 {
			numeric.MaxConsecutiveMisses = &c.misses
		}
		if c.format != "" {
			format := client.NumericConfigFormat(c.format)
			numeric.Format = &format
		}
		fields, err := jsonFieldsOf(c.fields)
		if err != nil {
			return config, err
		}
		numeric.Fields = fields
		return config, config.FromNumericConfig(numeric)
	}
	return config, fmt.Errorf("--strategy must be crawl, feed, feed-paged or numeric, got %q", c.strategy)
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
	source.AddCommand(a.sourceDiscover(), a.sourceProbe(), a.sourceAdd(), a.sourceList(), a.sourceGet(), a.sourceEdit(),
		a.sourceRemove(), a.sourceCrawl(), a.sourceEntries())
	return source
}

func (a *app) sourceDiscover() *cobra.Command {
	var limit int32
	cmd := operation(&cobra.Command{
		Use:   "discover <url>",
		Short: "Work out how to read a site, from its address. Saves nothing.",
		Long: "Work out how to read a site, from its address. Saves nothing.\n\n" +
			"It keeps to the crawl interval, so it can take a minute or two.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request := client.DiscoveryRequest{URL: args[0]}
			if limit != 0 {
				request.Limit = &limit
			}
			resp, err := a.client.CreateDiscoveryWithResponse(cmd.Context(), request)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				if len(resp.JSON200.Candidates) == 0 {
					w.println("Nothing found. Try source probe with the URL of the first page.")
					return
				}
				for _, c := range resp.JSON200.Candidates {
					printCandidate(w, c)
				}
			})
		},
	}, "createDiscovery")
	cmd.Flags().Int32Var(&limit, "limit", 0, "most entries to try for each candidate (at most 10)")
	return cmd
}

func printCandidate(w *table, c client.Candidate) {
	strategy, _ := c.Config.Discriminator()
	raw, _ := c.Config.MarshalJSON()
	var config struct {
		FeedURL  string `json:"feed_url"`
		StartURL string `json:"start_url"`
	}
	_ = json.Unmarshal(raw, &config)
	w.printf("Title:\t%s\nStrategy:\t%s\nKind:\t%s\nURL:\t%s%s\nHome:\t%s\n", c.Title, strategy, c.DefaultKind, config.FeedURL, config.StartURL, c.HomeURL)
	if id, err := c.ExistingSourceID.Get(); err == nil {
		w.printf("Already here:\tsource %d\n", id)
	} else {
		w.printf("Dated:\t%.0f%%\n", c.DatedShare*100)
	}
	for _, id := range c.SimilarSourceIds {
		w.printf("Looks like:\tsource %d\n", id)
	}
	for _, warning := range c.Warnings {
		w.println("Warning:", warning)
	}
	w.println()
}

func (a *app) sourceProbe() *cobra.Command {
	var (
		flags configFlags
		kind  string
		limit int32
	)
	cmd := operation(&cobra.Command{
		Use:   "probe <url>",
		Short: "Try a source and show what would be indexed. Saves nothing.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := flags.build(args[0])
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
	flags.add(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "entry kind: page, episode or article")
	cmd.Flags().Int32Var(&limit, "limit", 0, "most entries to try (at most 25)")
	return cmd
}

func (a *app) sourceAdd() *cobra.Command {
	var (
		slug, title, homeURL, kind, imageURL string
		flags                                configFlags
	)
	cmd := operation(&cobra.Command{
		Use:   "add <url>",
		Short: "Add a source. Probe it first.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := flags.build(args[0])
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
	flags.add(cmd)
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
	f.StringVar(&startURL, "start-url", "", "the strategy's URL: start page, feed or template")
	f.StringArrayVar(&flags, "selector", nil, "crawl: replace the selectors, as field=css")
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
	config = resp.JSON200.Config
	strategy, err := config.Discriminator()
	if err != nil {
		return config, err
	}
	if len(flags) > 0 && strategy != string(client.Crawl) {
		return config, errors.New("--selector is for the crawl strategy")
	}
	switch strategy {
	case string(client.Crawl):
		current, err := config.AsCrawlConfig()
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
		return config, config.FromCrawlConfig(current)
	case string(client.Feed):
		current, err := config.AsFeedConfig()
		if err != nil {
			return config, err
		}
		current.FeedURL = startURL
		return config, config.FromFeedConfig(current)
	case string(client.FeedPaged):
		current, err := config.AsFeedPagedConfig()
		if err != nil {
			return config, err
		}
		current.FeedURL = startURL
		return config, config.FromFeedPagedConfig(current)
	}
	current, err := config.AsNumericConfig()
	if err != nil {
		return config, err
	}
	current.URLTemplate = startURL
	return config, config.FromNumericConfig(current)
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
