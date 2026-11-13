// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

const (
	// maxWalk bounds walking a rule that cannot be counted in whole periods. Ten
	// years of an hourly rule is under 90,000.
	maxWalk = 1 << 20

	daysPerWeek = 7
	day         = 24 * time.Hour
)

// ErrTooManyOccurrences means a rule would take too long to walk.
var ErrTooManyOccurrences = errors.New("too many occurrences to count")

// rule is a compiled RRULE, anchored at its segment's start.
type rule struct {
	option rrule.ROption // In floating time: see floating.
	loc    *time.Location
	start  time.Time // Occurrences at or before this do not count.

	// For a rule counted in whole periods. periodDays is 0 for one that is not.
	periodDays int
	perPeriod  int
	base       time.Time // Local noon, one period before start.
	baseCount  int       // Occurrences in (base, start].
}

// parse reads the rule. It fills in what RFC 5545 would otherwise take from
// DTSTART, so that moving the start does not change the rule. UNTIL comes back
// in floating time.
func parse(p RRulePacing, start time.Time) (rrule.ROption, *time.Location, error) {
	loc, err := time.LoadLocation(p.TimeZone)
	if err != nil {
		return rrule.ROption{}, nil, fmt.Errorf("%w: %w", ErrInvalidPacing, err)
	}
	if strings.Contains(strings.ToUpper(p.RRule), "DTSTART") {
		return rrule.ROption{}, nil, fmt.Errorf("%w: an RRULE here has no DTSTART; a read starts when it starts", ErrInvalidPacing)
	}
	option, err := rrule.StrToROptionInLocation(p.RRule, loc)
	if err != nil {
		return rrule.ROption{}, nil, fmt.Errorf("%w: %w", ErrInvalidPacing, err)
	}
	if option == nil {
		return rrule.ROption{}, nil, fmt.Errorf("%w: empty RRULE", ErrInvalidPacing)
	}
	local := start.In(loc)
	if option.Freq == rrule.DAILY || option.Freq == rrule.WEEKLY {
		if len(option.Byhour) == 0 {
			option.Byhour = []int{local.Hour()}
		}
		if len(option.Byminute) == 0 {
			option.Byminute = []int{local.Minute()}
		}
		if len(option.Bysecond) == 0 {
			option.Bysecond = []int{local.Second()}
		}
	}
	if option.Freq == rrule.WEEKLY && len(option.Byweekday) == 0 && len(option.Bymonthday) == 0 {
		option.Byweekday = []rrule.Weekday{weekdays[local.Weekday()]}
	}
	if !option.Until.IsZero() {
		option.Until = floating(option.Until, loc)
	}
	return *option, loc, nil
}

// floating returns t's wall clock in loc, as the same wall clock in UTC.
//
// rrule-go finds each occurrence's date from local midnight, which does not
// exist on the day a zone such as America/Havana moves its clocks forward at
// midnight, and so it puts that day's occurrence on the day before. UTC never
// changes its clocks, so the rule runs on wall clocks there and each occurrence
// goes back to loc after (see local). An RRULE describes wall clocks anyway.
func floating(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// local is the inverse of floating. A wall clock that loc skips or repeats
// resolves as time.Date resolves it, which is what rrule-go did itself.
func local(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

var weekdays = map[time.Weekday]rrule.Weekday{
	time.Monday: rrule.MO, time.Tuesday: rrule.TU, time.Wednesday: rrule.WE, time.Thursday: rrule.TH,
	time.Friday: rrule.FR, time.Saturday: rrule.SA, time.Sunday: rrule.SU,
}

// repeats reports whether a rule has the same number of occurrences in every
// period of 7 × INTERVAL days.
func repeats(o rrule.ROption) bool {
	if o.Freq != rrule.DAILY && o.Freq != rrule.WEEKLY {
		return false
	}
	if o.Count != 0 || !o.Until.IsZero() {
		return false
	}
	if len(o.Bymonth)+len(o.Bymonthday)+len(o.Byyearday)+len(o.Byweekno)+len(o.Byeaster)+len(o.Bysetpos) > 0 {
		return false
	}
	for _, weekday := range o.Byweekday {
		if weekday.N() != 0 {
			return false
		}
	}
	return true
}

func compile(p RRulePacing, start time.Time) (*rule, error) {
	option, loc, err := parse(p, start)
	if err != nil {
		return nil, err
	}
	r := &rule{option: option, loc: loc, start: start.In(loc)}
	if !repeats(option) {
		return r, nil
	}

	interval := max(option.Interval, 1)
	r.periodDays = daysPerWeek * interval
	local := start.In(loc)
	noon := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
	r.base = noon.AddDate(0, 0, -r.periodDays)

	if r.perPeriod, err = r.walk(r.base, r.base.AddDate(0, 0, r.periodDays)); err != nil {
		return nil, err
	}
	if r.baseCount, err = r.walk(r.base, start); err != nil {
		return nil, err
	}
	return r, nil
}

// iterate returns the occurrences after from, for a rule anchored at from.
func (r *rule) iterate(from time.Time) (rrule.Next, error) {
	option := r.option
	option.Dtstart = floating(from, r.loc)
	parsed, err := rrule.NewRRule(option)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPacing, err)
	}
	next := parsed.Iterator()
	return func() (time.Time, bool) {
		at, ok := next()
		if !ok {
			return time.Time{}, false
		}
		return local(at, r.loc), true
	}, nil
}

// walk counts the occurrences in (from, to].
func (r *rule) walk(from, to time.Time) (int, error) {
	next, err := r.iterate(from)
	if err != nil {
		return 0, err
	}
	count := 0
	for walked := 0; walked < maxWalk; walked++ {
		occurrence, ok := next()
		if !ok || occurrence.After(to) {
			return count, nil
		}
		if occurrence.After(from) {
			count++
		}
	}
	return 0, ErrTooManyOccurrences
}

// periodsTo returns how many whole periods from base end before t.
func (r *rule) periodsTo(t time.Time) int {
	// One less than the division, so that a clock change cannot carry it past t.
	return max(int(t.Sub(r.base)/(time.Duration(r.periodDays)*day))-1, 0)
}

// sinceBase counts the occurrences in (base, t].
func (r *rule) sinceBase(t time.Time) (int, error) {
	periods := r.periodsTo(t)
	rest, err := r.walk(r.base.AddDate(0, 0, periods*r.periodDays), t)
	if err != nil {
		return 0, err
	}
	return periods*r.perPeriod + rest, nil
}

// count returns the occurrences in (start, t].
func (r *rule) count(t time.Time) (int, error) {
	if !t.After(r.start) {
		return 0, nil
	}
	if r.periodDays == 0 {
		return r.walk(r.start, t)
	}
	total, err := r.sinceBase(t)
	if err != nil {
		return 0, err
	}
	return total - r.baseCount, nil
}

// nth returns the nth occurrence after start, counting from 1, and whether there
// is one.
func (r *rule) nth(n int) (time.Time, bool, error) {
	from, skipped := r.start, 0
	if r.periodDays != 0 {
		if r.perPeriod == 0 {
			return time.Time{}, false, nil
		}
		n += r.baseCount
		periods := (n - 1) / r.perPeriod
		from, skipped = r.base.AddDate(0, 0, periods*r.periodDays), periods*r.perPeriod
	}
	next, err := r.iterate(from)
	if err != nil {
		return time.Time{}, false, err
	}
	seen := skipped
	for walked := 0; walked < maxWalk; walked++ {
		occurrence, ok := next()
		if !ok {
			return time.Time{}, false, nil
		}
		if !occurrence.After(from) {
			continue
		}
		seen++
		if seen == n {
			return occurrence, true, nil
		}
	}
	return time.Time{}, false, ErrTooManyOccurrences
}

// ValidatePacing checks pacing that came from outside.
func ValidatePacing(p Pacing) error {
	switch p := p.(type) {
	case RRulePacing:
		if p.PerRelease < 1 {
			return fmt.Errorf("%w: per_release must be at least 1", ErrInvalidPacing)
		}
		// Compiled, not just parsed: some rules parse and then cannot be built,
		// and one of those would break its read on every later request.
		compiled, err := compile(p, time.Unix(0, 0))
		if err != nil {
			return err
		}
		if compiled.option.Freq == rrule.MINUTELY || compiled.option.Freq == rrule.SECONDLY {
			return fmt.Errorf("%w: a rule can be at most hourly", ErrInvalidPacing)
		}
		return nil
	case ReplayPacing:
		if p.Speed <= 0 {
			return fmt.Errorf("%w: speed must be above zero", ErrInvalidPacing)
		}
		if p.MinGap < 0 || p.MaxGap < 0 || p.DefaultGap < 0 {
			return fmt.Errorf("%w: gaps cannot be negative", ErrInvalidPacing)
		}
		if p.MaxGap > 0 && p.MinGap > p.MaxGap {
			return fmt.Errorf("%w: min_gap is above max_gap", ErrInvalidPacing)
		}
		return nil
	case Paused:
		return fmt.Errorf("%w: pause a read with a pause adjustment", ErrInvalidPacing)
	}
	return fmt.Errorf("%w: %T", ErrInvalidPacing, p)
}
