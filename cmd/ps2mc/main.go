// Command ps2mc inspects and edits PlayStation 2 memory card images.
//
// It handles both page layouts found in the wild — the 528-byte ECC stride
// used by physical cards and emulators, and the bare 512-byte stride written
// by adapters such as the SD2PSX — choosing between them by probing the image
// rather than guessing from its size.
//
// Commands that modify a card snapshot it first. Use --no-backup to skip that.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"ps2mc/ps2mc"
)

func main() {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ps2mc:", err)
		os.Exit(1)
	}
}

func root() *cobra.Command {
	c := &cobra.Command{
		Use:   "ps2mc",
		Short: "Inspect and edit PlayStation 2 memory card images",
		Long: "ps2mc reads and writes PlayStation 2 memory card images.\n\n" +
			"Both page layouts are supported: the 528-byte ECC stride used by physical\n" +
			"cards and emulators, and the bare 512-byte stride written by adapters such\n" +
			"as the SD2PSX. The layout is detected by probing the image, because file\n" +
			"size alone cannot distinguish them reliably.\n\n" +
			"Commands that modify a card take a snapshot first unless --no-backup.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.AddCommand(
		infoCmd(), lsCmd(), catCmd(), extractCmd(), identifyCmd(),
		installCmd(), rmCmd(),
		backupCmd(), backupsCmd(), restoreCmd(),
		mountCmd(), convertCmd(),
	)
	return c
}

// backupFlags are shared by every command that writes to a card.
type backupFlags struct {
	noBackup bool
	dir      string
}

func (b *backupFlags) register(c *cobra.Command) {
	c.Flags().BoolVar(&b.noBackup, "no-backup", false, "skip the snapshot taken before modifying")
	c.Flags().StringVar(&b.dir, "backup-dir", "", "directory to write the snapshot to")
}

// openForWrite opens a card, snapshotting it first unless suppressed. A failed
// snapshot aborts: modifying a boot card without a way back is not worth it.
func (b *backupFlags) openForWrite(path, comment string) (*ps2mc.Card, error) {
	if !b.noBackup {
		snap, err := ps2mc.Snapshot(path, b.dir, comment)
		if err != nil {
			return nil, fmt.Errorf("backup failed, refusing to modify: %w", err)
		}
		fmt.Fprintf(os.Stderr, "backed up to %s\n", snap.Path)
	}
	return ps2mc.Open(path, true)
}
