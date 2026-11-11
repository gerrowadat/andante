// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var dayNames = map[string]string{
	"MO": "Mondays", "TU": "Tuesdays", "WE": "Wednesdays", "TH": "Thursdays",
	"FR": "Fridays", "SA": "Saturdays", "SU": "Sundays",
}

// Describe says how a read is paced, in words, for someone who doesn't read RRULEs.
// A rule it doesn't recognise comes back as the rule itself, which is still true.
func Describe(p Pacing) string {
	switch p := p.(type) {
	case RRulePacing:
		return describeRule(p)
	case ReplayPacing:
		return describeReplay(p)
	}
	return "Paused"
}

func describeRule(p RRulePacing) string {
	parts := map[string]string{}
	for part := range strings.SplitSeq(strings.ToUpper(p.RRule), ";") {
		key, value, _ := strings.Cut(part, "=")
		parts[key] = value
	}
	interval := parts["INTERVAL"]
	known := (interval == "" || interval == "1") && parts["COUNT"] == "" && parts["UNTIL"] == "" &&
		len(parts) <= 5
	var when string
	switch {
	case !known:
	case parts["FREQ"] == "DAILY" && parts["BYDAY"] == "":
		when = "Every day"
	case parts["FREQ"] == "WEEKLY" && parts["BYDAY"] == "MO,TU,WE,TH,FR":
		when = "Every weekday"
	case parts["FREQ"] == "WEEKLY" && parts["BYDAY"] != "":
		var days []string
		for day := range strings.SplitSeq(parts["BYDAY"], ",") {
			name, ok := dayNames[day]
			if !ok {
				return rawRule(p)
			}
			days = append(days, name)
		}
		when = listOf(days)
	}
	hour, minute := parts["BYHOUR"], parts["BYMINUTE"]
	if when == "" || hour == "" || strings.Contains(hour, ",") || strings.Contains(minute, ",") {
		return rawRule(p)
	}
	h, errH := strconv.Atoi(hour)
	m, errM := strconv.Atoi(minute)
	if errH != nil || (minute != "" && errM != nil) {
		return rawRule(p)
	}
	text := fmt.Sprintf("%s at %02d:%02d (%s)", when, h, m, p.TimeZone)
	return text + perRelease(p.PerRelease)
}

func rawRule(p RRulePacing) string {
	return "On the schedule " + p.RRule + " (" + p.TimeZone + ")" + perRelease(p.PerRelease)
}

func perRelease(n int) string {
	if n > 1 {
		return ", " + strconv.Itoa(n) + " at a time"
	}
	return ""
}

func listOf(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func describeReplay(p ReplayPacing) string {
	text := "The original timing"
	if p.Speed != 1 {
		text = "The original timing, " + strconv.FormatFloat(p.Speed, 'g', -1, 64) + " times as fast"
	}
	var limits []string
	if p.MinGap > 0 {
		limits = append(limits, "at least "+span(p.MinGap))
	}
	if p.MaxGap > 0 {
		limits = append(limits, "at most "+span(p.MaxGap))
	}
	if len(limits) > 0 {
		text += ", with " + strings.Join(limits, " and ") + " between entries"
	}
	return text
}

// span says a duration the way a person would, in the largest whole unit.
func span(d time.Duration) string {
	unit := func(n int64, one string) string {
		if n == 1 {
			return "1 " + one
		}
		return strconv.FormatInt(n, 10) + " " + one + "s"
	}
	switch {
	case d%(24*time.Hour) == 0:
		return unit(int64(d/(24*time.Hour)), "day")
	case d%time.Hour == 0:
		return unit(int64(d/time.Hour), "hour")
	case d%time.Minute == 0:
		return unit(int64(d/time.Minute), "minute")
	}
	return d.String()
}
