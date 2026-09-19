package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"ps2mc/ps2mc"
)

func backupCmd() *cobra.Command {
	var dir, msg string
	c := &cobra.Command{
		Use:   "backup <image>",
		Short: "Snapshot a card image",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			b, err := ps2mc.Snapshot(args[0], dir, msg)
			if err != nil {
				return err
			}
			fmt.Printf("%s\n  %d bytes  sha256 %s\n", b.Path, b.Size, b.SHA256[:16])
			return nil
		},
	}
	c.Flags().StringVarP(&dir, "dir", "d", "", "snapshot directory")
	c.Flags().StringVarP(&msg, "message", "m", "", "comment recorded in the manifest")
	return c
}

func backupsCmd() *cobra.Command {
	var dir string
	c := &cobra.Command{
		Use:   "backups",
		Short: "List snapshots, newest first",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			list, err := ps2mc.Backups(dir)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("no snapshots")
				return nil
			}
			for _, b := range list {
				fmt.Printf("%s  %9d  %s  %s\n", b.Taken.Format("2006-01-02 15:04:05"),
					b.Size, b.SHA256[:12], filepath.Base(b.Path))
				if b.Comment != "" {
					fmt.Printf("    %s\n", b.Comment)
				}
			}
			return nil
		},
	}
	c.Flags().StringVarP(&dir, "dir", "d", "", "snapshot directory")
	return c
}

func restoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <snapshot> <image>",
		Short: "Restore a verified snapshot over an image",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			list, err := ps2mc.Backups(filepath.Dir(args[0]))
			if err != nil {
				return err
			}
			want := filepath.Base(args[0])
			for _, b := range list {
				if filepath.Base(b.Path) != want {
					continue
				}
				if err := b.Restore(args[1]); err != nil {
					return err
				}
				fmt.Printf("restored %s -> %s\n", b.Path, args[1])
				return nil
			}
			return fmt.Errorf("no snapshot manifest found for %s", args[0])
		},
	}
}
