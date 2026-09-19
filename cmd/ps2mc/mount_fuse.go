//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"ps2mc/ps2mc"
	"ps2mc/ps2mc/fusefs"
)

func mountCmd() *cobra.Command {
	var readOnly bool
	var bf backupFlags
	c := &cobra.Command{
		Use:   "mount <image> <dir>",
		Short: "Mount the card as a filesystem",
		Long: "Mount exposes the card over FUSE so it can be browsed and edited with\n" +
			"ordinary tools. Files are buffered in memory and written back when the\n" +
			"last handle closes.\n\n" +
			"The mountpoint is created if it does not exist, and removed again on\n" +
			"unmount if this command created it.\n\n" +
			"Linux uses kernel FUSE directly. macOS additionally requires macFUSE to\n" +
			"be installed. The mountpoint must be somewhere fusermount can reach;\n" +
			"paths under restrictive temporary directories are refused by FUSE.",
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			img, dir := args[0], args[1]

			// Create the mountpoint if needed, and clean it up afterwards so
			// repeated mounts do not litter empty directories.
			created, err := ensureMountpoint(dir)
			if err != nil {
				return err
			}
			if created {
				defer os.Remove(dir)
			}

			var card *ps2mc.Card
			if readOnly {
				card, err = ps2mc.Open(img, false)
			} else {
				card, err = bf.openForWrite(img, "before mount")
			}
			if err != nil {
				return err
			}
			defer card.Close()

			server, err := fusefs.Mount(card, dir, readOnly)
			if err != nil {
				return fmt.Errorf("mount %s: %w", dir, err)
			}
			mode := "read-write"
			if readOnly {
				mode = "read-only"
			}
			fmt.Printf("%s mounted %s at %s\n", img, mode, dir)
			fmt.Printf("unmount with: fusermount -u %s\n", dir)

			// Unmount on interrupt so the card is always left synced.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sig
				server.Unmount()
			}()
			server.Wait()
			return card.Sync()
		},
	}
	c.Flags().BoolVar(&readOnly, "ro", false, "mount read-only")
	bf.register(c)
	return c
}

// ensureMountpoint makes sure dir exists and is a usable mountpoint, reporting
// whether it had to be created. A non-empty directory is refused: mounting over
// existing files hides them until unmount, which is rarely what anyone wants.
func ensureMountpoint(dir string) (bool, error) {
	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return false, fmt.Errorf("%s is not a directory", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false, err
		}
		if len(entries) > 0 {
			return false, fmt.Errorf("%s is not empty; mounting would hide its contents", dir)
		}
		return false, nil
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, fmt.Errorf("create mountpoint %s: %w", dir, err)
		}
		return true, nil
	default:
		return false, err
	}
}
