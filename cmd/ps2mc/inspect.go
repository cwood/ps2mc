package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ps2mc/ps2mc"
	"ps2mc/ps2mc/bootcard"
)

func infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <image>",
		Short: "Report layout, capacity and free space",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := ps2mc.Open(args[0], false)
			if err != nil {
				return err
			}
			defer c.Close()

			free, err := c.Free()
			if err != nil {
				return err
			}
			fmt.Printf("layout:    %s\n", c.Format())
			fmt.Printf("cluster:   %d bytes\n", c.ClusterSize())
			fmt.Printf("capacity:  %d bytes (%.1f MiB)\n", c.Capacity(), float64(c.Capacity())/(1<<20))
			fmt.Printf("free:      %d bytes (%.1f MiB)\n", free, float64(free)/(1<<20))
			fmt.Printf("used:      %d bytes\n", c.Capacity()-free)
			return nil
		},
	}
}

func lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <image> [path]",
		Short: "List a directory",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			dir := "/"
			if len(args) > 1 {
				dir = args[1]
			}
			c, err := ps2mc.Open(args[0], false)
			if err != nil {
				return err
			}
			defer c.Close()

			entries, err := c.ReadDir(dir)
			if err != nil {
				return err
			}
			for _, e := range entries {
				fmt.Println(e)
			}
			return nil
		},
	}
}

func catCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cat <image> <path>",
		Short: "Write a file on the card to stdout",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := ps2mc.Open(args[0], false)
			if err != nil {
				return err
			}
			defer c.Close()

			data, err := c.ReadFile(args[1])
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(data)
			return err
		},
	}
}

func extractCmd() *cobra.Command {
	var out string
	c := &cobra.Command{
		Use:   "extract <image> <path>...",
		Short: "Copy files off the card",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			card, err := ps2mc.Open(args[0], false)
			if err != nil {
				return err
			}
			defer card.Close()

			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			for _, p := range args[1:] {
				data, err := card.ReadFile(p)
				if err != nil {
					return err
				}
				dst := filepath.Join(out, filepath.Base(p))
				if err := os.WriteFile(dst, data, 0o644); err != nil {
					return err
				}
				fmt.Printf("%s -> %s (%d bytes)\n", p, dst, len(data))
			}
			return nil
		},
	}
	c.Flags().StringVarP(&out, "out", "o", ".", "output directory")
	return c
}

func identifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "identify <image>...",
		Short: "Recognise boot software and flag duplicate cards",
		Long: "Identify inspects each card's contents rather than trusting an external\n" +
			"label, and hashes the boot payloads so cards that are really the same\n" +
			"install are reported together.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			seen := map[string][]string{}
			for _, img := range args {
				name := filepath.Base(img)
				card, err := ps2mc.Open(img, false)
				if err != nil {
					fmt.Printf("%-18s  ERROR: %v\n", name, err)
					continue
				}
				p, err := bootcard.Identify(card)
				card.Close()
				if err != nil {
					fmt.Printf("%-18s  ERROR: %v\n", name, err)
					continue
				}
				fmt.Printf("%-18s  %s\n", name, p)
				if len(p.Markers) > 0 {
					fmt.Printf("%-18s    markers: %s\n", "", strings.Join(p.Markers, ", "))
				}
				if len(p.Apps) > 0 {
					fmt.Printf("%-18s    apps:    %s\n", "", strings.Join(p.Apps, ", "))
				}
				if p.Boot != "" || p.Config != "" {
					key := p.Boot + "/" + p.Config
					seen[key] = append(seen[key], name)
				}
			}
			for _, names := range seen {
				if len(names) > 1 {
					fmt.Printf("\nidentical payloads: %s\n", strings.Join(names, ", "))
				}
			}
			return nil
		},
	}
}
