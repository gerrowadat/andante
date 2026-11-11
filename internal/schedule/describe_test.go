// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule_test

import (
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/schedule"
)

func TestDescribe(t *testing.T) {
	tests := []struct {
		pacing schedule.Pacing
		want   string
	}{
		{schedule.RRulePacing{RRule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0", TimeZone: "Europe/Dublin", PerRelease: 1},
			"Every day at 07:00 (Europe/Dublin)"},
		{schedule.RRulePacing{RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=7;BYMINUTE=0;BYSECOND=0", TimeZone: "UTC", PerRelease: 3},
			"Every weekday at 07:00 (UTC), 3 at a time"},
		{schedule.RRulePacing{RRule: "FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=18;BYMINUTE=30;BYSECOND=0", TimeZone: "UTC", PerRelease: 1},
			"Mondays, Wednesdays and Fridays at 18:30 (UTC)"},
		{schedule.RRulePacing{RRule: "FREQ=WEEKLY;BYDAY=SA;BYHOUR=9", TimeZone: "UTC", PerRelease: 1},
			"Saturdays at 09:00 (UTC)"},
		{schedule.RRulePacing{RRule: "FREQ=DAILY;INTERVAL=2;BYHOUR=7", TimeZone: "UTC", PerRelease: 1},
			"On the schedule FREQ=DAILY;INTERVAL=2;BYHOUR=7 (UTC)"},
		{schedule.RRulePacing{RRule: "FREQ=DAILY", TimeZone: "UTC", PerRelease: 1},
			"On the schedule FREQ=DAILY (UTC)"},
		{schedule.ReplayPacing{Speed: 1}, "The original timing"},
		{schedule.ReplayPacing{Speed: 7, MaxGap: 72 * time.Hour}, "The original timing, 7 times as fast, with at most 3 days between entries"},
		{schedule.ReplayPacing{Speed: 2.5, MinGap: 12 * time.Hour, MaxGap: 24 * time.Hour},
			"The original timing, 2.5 times as fast, with at least 12 hours and at most 1 day between entries"},
		{schedule.Paused{}, "Paused"},
	}
	for _, tt := range tests {
		if got := schedule.Describe(tt.pacing); got != tt.want {
			t.Errorf("Describe(%+v) = %q, want %q", tt.pacing, got, tt.want)
		}
	}
}
