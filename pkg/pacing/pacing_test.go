// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package pacing_test

import (
	"testing"

	"github.com/gerrowadat/andante/pkg/pacing"
)

func TestRules(t *testing.T) {
	tests := []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{"daily", func() (string, error) { return pacing.Daily("07:00") }, "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0"},
		{"one digit hour", func() (string, error) { return pacing.Daily("7:05") }, "FREQ=DAILY;BYHOUR=7;BYMINUTE=5;BYSECOND=0"},
		{"weekdays", func() (string, error) { return pacing.Days(pacing.Weekdays, "18:30") }, "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=18;BYMINUTE=30;BYSECOND=0"},
		{"lower case days with spaces", func() (string, error) { return pacing.Days("mo, we, fr", "08:00") }, "FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=8;BYMINUTE=0;BYSECOND=0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.got()
			if err != nil || got != tt.want {
				t.Errorf("= %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestBadShorthand(t *testing.T) {
	for _, at := range []string{"24:00", "7", "07:60", "noon", ""} {
		if _, err := pacing.Daily(at); err == nil {
			t.Errorf("Daily(%q) succeeded", at)
		}
	}
	for _, days := range []string{"", "MON", "MO;WE", "XX"} {
		if _, err := pacing.Days(days, "07:00"); err == nil {
			t.Errorf("Days(%q) succeeded", days)
		}
	}
}
