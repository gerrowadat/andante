// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package buildinfo

import "fmt"

// Set at build time with -ldflags "-X", see the Makefile.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// String returns the version, commit and build date on one line.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
}
