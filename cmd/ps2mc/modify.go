package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func installCmd() *cobra.Command {
	var dir string
	var bf backupFlags
	c := &cobra.Command{
		Use:   "install <image> <file>...",
		Short: "Copy files onto the card",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			img, files := args[0], args[1:]
			card, err := bf.openForWrite(img, "before install "+strings.Join(files, " "))
			if err != nil {
				return err
			}
			defer card.Close()

			for _, f := range files {
				data, err := readLocal(f)
				if err != nil {
					return err
				}
				dst := filepath.Join(dir, filepath.Base(f))
				if err := card.WriteFile(dst, data); err != nil {
					return fmt.Errorf("install %s: %w", f, err)
				}
				fmt.Printf("%s -> %s (%d bytes)\n", f, dst, len(data))
			}
			return card.Sync()
		},
	}
	c.Flags().StringVarP(&dir, "dir", "d", "/", "destination directory on the card")
	bf.register(c)
	return c
}

func rmCmd() *cobra.Command {
	var recursive bool
	var bf backupFlags
	c := &cobra.Command{
		Use:     "rm <image> <path>...",
		Short:   "Delete files, or empty directories with -d",
		Aliases: []string{"remove"},
		Args:    cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			card, err := bf.openForWrite(args[0], "before rm "+strings.Join(args[1:], " "))
			if err != nil {
				return err
			}
			defer card.Close()

			for _, p := range args[1:] {
				remove := card.Remove
				if recursive {
					remove = card.RemoveDir
				}
				if err := remove(p); err != nil {
					return err
				}
				fmt.Printf("removed %s\n", p)
			}
			return card.Sync()
		},
	}
	c.Flags().BoolVarP(&recursive, "dir", "d", false, "remove an empty directory instead of a file")
	bf.register(c)
	return c
}
