// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Command andante-server is the andante server. For now it only reports its
// version.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/alecthomas/kong"
	"github.com/gerrowadat/andante/internal/buildinfo"

	// Embedded because the distroless image has no zoneinfo, and schedules
	// are evaluated in IANA time zones.
	_ "time/tzdata"
)

type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit."`

	VersionCmd struct{} `cmd:"" name:"version" help:"Print the version."`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Exit))
}

func run(args []string, stdout, stderr io.Writer, exit func(int)) int {
	var c cli
	parser, err := kong.New(&c,
		kong.Name("andante-server"),
		kong.Description("Replays a serial's archive as a paced feed."),
		kong.Vars{"version": buildinfo.String()},
		kong.Writers(stdout, stderr),
		kong.Exit(exit),
	)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := parser.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	_, _ = fmt.Fprintln(stdout, buildinfo.String())
	return 0
}
