// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package pacing turns the shorthand people use for a schedule into the RFC 5545
// RRULEs the API takes. The CLI and the website share it, so that "every weekday at
// 07:00" means the same rule from either.
package pacing

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Weekdays is Monday to Friday, as BYDAY wants it.
const Weekdays = "MO,TU,WE,TH,FR"

var (
	clockTime = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):([0-5][0-9])$`)
	dayList   = regexp.MustCompile(`^(MO|TU|WE|TH|FR|SA|SU)(,(MO|TU|WE|TH|FR|SA|SU))*$`)
)

// At turns HH:MM into the part of a rule that says when in the day.
func At(raw string) (string, error) {
	match := clockTime.FindStringSubmatch(raw)
	if match == nil {
		return "", fmt.Errorf("%q is not a time, as HH:MM", raw)
	}
	hour, err := strconv.Atoi(match[1])
	if err != nil {
		return "", err
	}
	minute, err := strconv.Atoi(match[2])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("BYHOUR=%d;BYMINUTE=%d;BYSECOND=0", hour, minute), nil
}

// Daily is every day at a time.
func Daily(at string) (string, error) {
	when, err := At(at)
	if err != nil {
		return "", err
	}
	return "FREQ=DAILY;" + when, nil
}

// Days is on some days of the week at a time. days is a list such as MO,WE,FR, in
// either case.
func Days(days, at string) (string, error) {
	days = strings.ToUpper(strings.ReplaceAll(days, " ", ""))
	if !dayList.MatchString(days) {
		return "", errors.New("days must be a list of MO, TU, WE, TH, FR, SA and SU, such as MO,WE,FR")
	}
	when, err := At(at)
	if err != nil {
		return "", err
	}
	return "FREQ=WEEKLY;BYDAY=" + days + ";" + when, nil
}
