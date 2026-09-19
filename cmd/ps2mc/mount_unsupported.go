//go:build !linux && !darwin

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// mountCmd is a placeholder on platforms without FUSE support. Keeping the
// command present means help output and scripts behave consistently, and the
// failure explains itself rather than looking like a missing feature.
func mountCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mount <image> <dir>",
		Short: "Mount the card as a filesystem (unavailable on this platform)",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("mount requires FUSE, which is unavailable on %s; "+
				"use install, extract and ls instead", runtime.GOOS)
		},
	}
}
