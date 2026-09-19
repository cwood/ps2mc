package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"ps2mc/ps2mc"
	"ps2mc/ps2mc/image"
)

func convertCmd() *cobra.Command {
	var to string
	c := &cobra.Command{
		Use:   "convert <image> <out>",
		Short: "Rewrite a card in the other page layout",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			var format image.Format
			switch to {
			case "raw":
				format = image.Raw
			case "ecc":
				format = image.ECC
			default:
				return fmt.Errorf("unknown layout %q (want raw or ecc)", to)
			}
			// Open through the filesystem layer so the source stride is probed.
			card, err := ps2mc.Open(args[0], false)
			if err != nil {
				return err
			}
			defer card.Close()

			src := card.Image()
			if err := image.Convert(src, args[1], format); err != nil {
				return err
			}
			fmt.Printf("%s (%s) -> %s (%s)\n", args[0], src.Format(), args[1], format)
			return nil
		},
	}
	c.Flags().StringVar(&to, "to", "", "target layout: raw or ecc")
	c.MarkFlagRequired("to")
	return c
}

// readLocal reads a file from the host filesystem.
func readLocal(path string) ([]byte, error) { return os.ReadFile(path) }
