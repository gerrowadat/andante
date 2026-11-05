// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Command andante is the andante CLI. For now it only reports its version.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:          "andante",
		Short:        "Manage an andante server.",
		Version:      buildinfo.String(),
		SilenceUsage: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.String())
			return err
		},
	})
	return root
}
