// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/gerrowadat/andante/pkg/client"
	"github.com/oapi-codegen/nullable"
	"github.com/spf13/cobra"
)

func nextCursor(next nullable.Nullable[string]) (string, bool) {
	if next.IsNull() || !next.IsSpecified() {
		return "", false
	}
	u, err := url.Parse(next.MustGet())
	if err != nil {
		return "", false
	}
	cursor := u.Query().Get("cursor")
	return cursor, cursor != ""
}

// startSeq turns --start into a seq: nil for the first entry, a seq, or the entry
// at a URL.
func (a *app) startSeq(ctx context.Context, source int64, start string) (*int32, error) {
	if start == "" || start == "first" {
		return nil, nil //nolint:nilnil // The server's default is the answer.
	}
	if seq, err := strconv.ParseInt(start, 10, 32); err == nil {
		value := int32(seq)
		return &value, nil
	}
	params := &client.ListEntriesParams{}
	for {
		resp, err := a.client.ListEntriesWithResponse(ctx, source, params)
		if err != nil {
			return nil, err
		}
		if err := check(resp.StatusCode(), resp.Body); err != nil {
			return nil, err
		}
		for _, e := range resp.JSON200.Items {
			if e.URL.IsSpecified() && !e.URL.IsNull() && e.URL.MustGet() == start {
				return &e.Seq, nil
			}
		}
		cursor, more := nextCursor(resp.JSON200.Next)
		if !more {
			return nil, fmt.Errorf("no indexed entry at %s", start)
		}
		params.Cursor = &cursor
	}
}

func printRead(w *table, r *client.Read) {
	end := "-"
	if r.IndexEnd.IsSpecified() && !r.IndexEnd.IsNull() {
		end = strconv.Itoa(int(r.IndexEnd.MustGet()))
	}
	w.printf("ID:\t%d\nSource:\t%d\nName:\t%s\nState:\t%s\nPosition:\t%d\nDue:\t%d\nIndex end:\t%s\nNext release:\t%s\n",
		r.ID, r.SourceID, r.Name, r.State, r.Position, r.Due, end, stamp(r.NextReleaseAt))
}

func (a *app) readCmd() *cobra.Command {
	read := &cobra.Command{Use: "read", Short: "Manage reads."}
	read.AddCommand(a.readCreate(), a.readList(), a.readGet(), a.readEdit(), a.readRemove(),
		a.readPause(), a.readResume(), a.readSkip(), a.readJump(), a.readPace(), a.readHistory())
	return read
}

func (a *app) readCreate() *cobra.Command {
	var (
		pacing          pacingFlags
		name, start     string
		catchUp         string
		buffer, initial int32
	)
	cmd := operation(&cobra.Command{
		Use:   "create <source>",
		Short: "Create a read of a source, by ID or slug, with one pacing.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			built, err := pacing.pacing()
			if err != nil {
				return err
			}
			source, err := a.resolveSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			seq, err := a.startSeq(cmd.Context(), source, start)
			if err != nil {
				return err
			}
			create := client.ReadCreate{SourceID: source, Name: name, Pacing: built, StartSeq: seq}
			if cmd.Flags().Changed("initial") {
				create.Initial = &initial
			}
			if catchUp != "" {
				value := client.CatchUp(catchUp)
				create.CatchUp = &value
			}
			if cmd.Flags().Changed("buffer") {
				create.Buffer = &buffer
			}
			resp, err := a.client.CreateReadWithResponse(cmd.Context(), create)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { printRead(w, resp.JSON201) })
		},
	}, "createRead")
	pacing.add(cmd)
	f := cmd.Flags()
	f.StringVar(&name, "name", "main", "name of the read")
	f.StringVar(&start, "start", "first", "where to start: first, a seq, or an entry's URL")
	f.Int32Var(&initial, "initial", 1, "entries there immediately")
	f.StringVar(&catchUp, "catch-up", "", "at the end of the index: passthrough or buffer")
	f.Int32Var(&buffer, "buffer", 0, "entries to hold back, with --catch-up buffer")
	return cmd
}

func (a *app) readList() *cobra.Command {
	var p page
	cmd := operation(&cobra.Command{
		Use:   "list",
		Short: "List reads.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.ListReadsWithResponse(cmd.Context(), &client.ListReadsParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("ID\tSOURCE\tNAME\tSTATE\tPOSITION\tNEXT RELEASE")
				for _, r := range resp.JSON200.Items {
					w.printf("%d\t%d\t%s\t%s\t%d\t%s\n", r.ID, r.SourceID, r.Name, r.State, r.Position, stamp(r.NextReleaseAt))
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listReads")
	p.add(cmd)
	return cmd
}

func (a *app) readGet() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "get <id>",
		Short: "Show a read, with its position and next release.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.GetReadWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { printRead(w, resp.JSON200) })
		},
	}, "getRead")
}

func (a *app) readEdit() *cobra.Command {
	var name, config string
	cmd := operation(&cobra.Command{
		Use:   "edit <id>",
		Short: "Change a read's name or config. Pacing changes with read pace.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			var patch client.ReadPatch
			if cmd.Flags().Changed("name") {
				patch.Name = &name
			}
			if config != "" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(config), &parsed); err != nil {
					return fmt.Errorf("--config: %w", err)
				}
				patch.Config = &parsed
			}
			resp, err := a.client.UpdateReadWithApplicationMergePatchPlusJSONBodyWithResponse(cmd.Context(), id, patch)
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) { printRead(w, resp.JSON200) })
		},
	}, "updateRead")
	cmd.Flags().StringVar(&name, "name", "", "name of the read")
	cmd.Flags().StringVar(&config, "config", "", `config as JSON, such as {"window": 50}`)
	return cmd
}

func (a *app) readRemove() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a read, its history and its feed tokens.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.DeleteReadWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.done(resp.StatusCode(), resp.Body, "deleted")
		},
	}, "deleteRead")
}

func (a *app) adjust(cmd *cobra.Command, raw string, adjustment client.Adjustment) error {
	id, err := parseID(raw)
	if err != nil {
		return err
	}
	resp, err := a.client.CreateAdjustmentWithResponse(cmd.Context(), id, adjustment)
	if err != nil {
		return err
	}
	return a.show(resp.StatusCode(), resp.Body, func(w *table) { printRead(w, resp.JSON200) })
}

func (a *app) readPause() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "pause <id>",
		Short: "Stop releases.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var adjustment client.Adjustment
			if err := adjustment.FromPauseAdjustment(client.PauseAdjustment{Type: client.PauseAdjustmentTypePause}); err != nil {
				return err
			}
			return a.adjust(cmd, args[0], adjustment)
		},
	}, "createAdjustment")
}

func (a *app) readResume() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "resume <id>",
		Short: "Restart releases.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var adjustment client.Adjustment
			if err := adjustment.FromResumeAdjustment(client.ResumeAdjustment{Type: client.ResumeAdjustmentTypeResume}); err != nil {
				return err
			}
			return a.adjust(cmd, args[0], adjustment)
		},
	}, "createAdjustment")
}

func (a *app) readSkip() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "skip <id> [n]",
		Short: "Release the next n entries now.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			count := int32(1)
			if len(args) == 2 {
				parsed, err := strconv.ParseInt(args[1], 10, 32)
				if err != nil {
					return fmt.Errorf("%q is not a count", args[1])
				}
				count = int32(parsed)
			}
			var adjustment client.Adjustment
			if err := adjustment.FromSkipAdjustment(client.SkipAdjustment{Type: client.SkipAdjustmentTypeSkip, Count: &count}); err != nil {
				return err
			}
			return a.adjust(cmd, args[0], adjustment)
		},
	}, "createAdjustment")
}

func (a *app) readJump() *cobra.Command {
	return operation(&cobra.Command{
		Use:   "jump <id> <seq>",
		Short: "Release everything up to seq now. A read only moves forward.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			seq, err := strconv.ParseInt(args[1], 10, 32)
			if err != nil {
				return fmt.Errorf("%q is not a seq", args[1])
			}
			var adjustment client.Adjustment
			if err := adjustment.FromJumpAdjustment(client.JumpAdjustment{Type: client.JumpAdjustmentTypeJump, Seq: int32(seq)}); err != nil {
				return err
			}
			return a.adjust(cmd, args[0], adjustment)
		},
	}, "createAdjustment")
}

func (a *app) readPace() *cobra.Command {
	var pacing pacingFlags
	cmd := operation(&cobra.Command{
		Use:   "pace <id>",
		Short: "Change a read's pacing.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			built, err := pacing.pacing()
			if err != nil {
				return err
			}
			var adjustment client.Adjustment
			if err := adjustment.FromPacingAdjustment(client.PacingAdjustment{Type: client.PacingAdjustmentTypePacing, Pacing: built}); err != nil {
				return err
			}
			return a.adjust(cmd, args[0], adjustment)
		},
	}, "createAdjustment")
	pacing.add(cmd)
	return cmd
}

func (a *app) readHistory() *cobra.Command {
	var p page
	cmd := operation(&cobra.Command{
		Use:   "history <id>",
		Short: "Show a read's history.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.ListSegmentsWithResponse(cmd.Context(), id, &client.ListSegmentsParams{Cursor: p.cursorParam(), Limit: p.limitParam()})
			if err != nil {
				return err
			}
			return a.show(resp.StatusCode(), resp.Body, func(w *table) {
				w.println("REASON\tFROM SEQ\tAT\tPACING")
				for _, s := range resp.JSON200.Items {
					w.printf("%s\t%d\t%s\t%s\n", s.Reason, s.FromSeq, s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), describe(s.Pacing))
				}
				a.more(resp.JSON200.Next)
			})
		},
	}, "listSegments")
	p.add(cmd)
	return cmd
}
