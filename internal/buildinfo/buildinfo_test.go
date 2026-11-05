// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package buildinfo

import "testing"

func TestStringDefaults(t *testing.T) {
	want := "dev (commit unknown, built unknown)"
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestStringUsesStampedValues(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() { version, commit, date = oldVersion, oldCommit, oldDate })

	version, commit, date = "v1.2.3", "abc123", "2026-11-05T00:00:00Z"

	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q", got)
	}
	if got := Commit(); got != "abc123" {
		t.Errorf("Commit() = %q", got)
	}
	want := "v1.2.3 (commit abc123, built 2026-11-05T00:00:00Z)"
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
