// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gerrowadat/andante/pkg/client"
	"github.com/gerrowadat/andante/pkg/pacing"
	"github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"
)

// pacingFlags are the flags that choose a pacing. The shorthand becomes an RRULE
// here, because the API only takes RRULEs.
type pacingFlags struct {
	daily, weekdays, days, at, rrule, timezone string
	count                                      int32
	replay                                     bool
	speed                                      float64
	minGap, maxGap, defaultGap                 string
}

func (p *pacingFlags) add(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&p.daily, "daily", "", "every day at HH:MM")
	f.StringVar(&p.weekdays, "weekdays", "", "every weekday at HH:MM")
	f.StringVar(&p.days, "days", "", "on these days, such as MO,WE,FR, with --at")
	f.StringVar(&p.at, "at", "", "the time for --days, as HH:MM")
	f.Int32Var(&p.count, "count", 0, "entries per occurrence (default 1)")
	f.StringVar(&p.rrule, "rrule", "", "an RRULE, passed through")
	f.StringVar(&p.timezone, "tz", "UTC", "IANA time zone")
	f.BoolVar(&p.replay, "replay", false, "replay the original run's timing")
	f.Float64Var(&p.speed, "speed", 1, "replay speed: 7 turns weekly into daily")
	f.StringVar(&p.minGap, "min-gap", "", "shortest gap, as an ISO 8601 duration")
	f.StringVar(&p.maxGap, "max-gap", "", "longest gap, as an ISO 8601 duration")
	f.StringVar(&p.defaultGap, "default-gap", "", "gap for undated entries, as an ISO 8601 duration")
}

func (p *pacingFlags) rule() (string, error) {
	chosen := 0
	for _, set := range []bool{p.daily != "", p.weekdays != "", p.days != "", p.rrule != "", p.replay} {
		if set {
			chosen++
		}
	}
	if chosen != 1 {
		return "", errors.New("choose one pacing: --daily, --weekdays, --days, --rrule or --replay")
	}
	switch {
	case p.daily != "":
		return pacing.Daily(p.daily)
	case p.weekdays != "":
		return pacing.Days(pacing.Weekdays, p.weekdays)
	case p.days != "":
		if p.at == "" {
			return "", errors.New("--days needs --at")
		}
		return pacing.Days(p.days, p.at)
	}
	return p.rrule, nil
}

func gap(raw, name string) (*types.Duration, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // No gap is a normal answer.
	}
	d, err := types.ParseDuration(raw)
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", name, err)
	}
	return &d, nil
}

func (p *pacingFlags) pacing() (client.Pacing, error) {
	var out client.Pacing
	rule, err := p.rule()
	if err != nil {
		return out, err
	}
	if p.replay {
		replay := client.ReplayPacing{Mode: client.Replay, Speed: p.speed}
		if replay.MinGap, err = gap(p.minGap, "min-gap"); err != nil {
			return out, err
		}
		if replay.MaxGap, err = gap(p.maxGap, "max-gap"); err != nil {
			return out, err
		}
		if replay.DefaultGap, err = gap(p.defaultGap, "default-gap"); err != nil {
			return out, err
		}
		return out, out.FromReplayPacing(replay)
	}
	if p.minGap != "" || p.maxGap != "" || p.defaultGap != "" || p.speed != 1 {
		return out, errors.New("--speed and the gap flags are for --replay")
	}
	scheduled := client.RRulePacing{Mode: client.Rrule, Rrule: rule, Timezone: p.timezone}
	if p.count != 0 {
		scheduled.PerRelease = &p.count
	}
	return out, out.FromRRulePacing(scheduled)
}

// describe sums a pacing up for a table.
func describe(p client.Pacing) string {
	mode, err := p.Discriminator()
	if err != nil {
		return "-"
	}
	switch mode {
	case string(client.Rrule):
		rule, err := p.AsRRulePacing()
		if err != nil {
			return mode
		}
		return rule.Rrule + " (" + rule.Timezone + ")"
	case string(client.Replay):
		replay, err := p.AsReplayPacing()
		if err != nil {
			return mode
		}
		return "replay at " + strconv.FormatFloat(replay.Speed, 'g', -1, 64) + "x"
	}
	return mode
}
