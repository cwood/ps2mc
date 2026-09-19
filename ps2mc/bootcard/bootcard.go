// Package bootcard recognises what boot software a PlayStation 2 memory card
// carries.
//
// Cards are usually labelled only by whatever the owner typed into an external
// file, and those labels drift. Identifying a card from its contents is more
// reliable, and hashing its payloads shows when several cards are really the
// same install.
package bootcard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"ps2mc/ps2mc"
)

// Kind is the boot software a card appears to carry.
type Kind string

const (
	FMCB    Kind = "FMCB"
	PS2BBL  Kind = "PS2BBL"
	Unknown Kind = "unknown"
)

// Layout describes where the boot files sit.
type Layout string

const (
	// Standard keeps configuration in SYS-CONF, as a normal FMCB install does.
	Standard Layout = "standard"
	// Flat keeps everything in BOOT, as some installers produce.
	Flat Layout = "flat"
)

// Profile is what a card looks like.
type Profile struct {
	Kind    Kind
	Layout  Layout
	Markers []string // identifying files found, with their directory
	Boot    string   // short hash of BOOT/BOOT.ELF
	Config  string   // short hash of the FMCB config tool
	Apps    []string // notable executables
	Used    int64
	Free    int64
}

// marker files that identify a card's boot software.
var markers = map[string]Kind{
	"FREEMCB.CNF":  FMCB,
	"FMCB_CFG.ELF": FMCB,
	"PS2BBL.INI":   PS2BBL,
	"PS2BBL.icn":   PS2BBL,
}

// Identify inspects a card and reports what it carries.
func Identify(c *ps2mc.Card) (Profile, error) {
	p := Profile{Kind: Unknown, Layout: Standard}

	free, err := c.Free()
	if err != nil {
		return p, err
	}
	p.Free, p.Used = free, c.Capacity()-free

	// Configuration lives in SYS-CONF on a normal install and in BOOT on a
	// flat one, so both are searched.
	for _, dir := range []string{"SYS-CONF", "BOOT"} {
		entries, err := c.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			kind, ok := markers[e.Name]
			if !ok {
				continue
			}
			p.Markers = append(p.Markers, path.Join(dir, e.Name))
			if p.Kind == Unknown {
				p.Kind = kind
			}
			if dir == "BOOT" && kind == FMCB {
				p.Layout = Flat
			}
		}
	}
	sort.Strings(p.Markers)

	p.Boot = digest(c, "BOOT/BOOT.ELF")
	for _, candidate := range []string{"SYS-CONF/FMCB_CFG.ELF", "BOOT/FMCB_CFG.ELF"} {
		if h := digest(c, candidate); h != "" {
			p.Config = h
			break
		}
	}

	// Report the larger executables; they are what distinguishes one setup
	// from another in practice.
	for _, dir := range []string{"APPS", "BOOT"} {
		entries, err := c.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Length > 100_000 && strings.HasSuffix(strings.ToUpper(e.Name), ".ELF") {
				p.Apps = append(p.Apps, e.Name)
			}
		}
	}
	sort.Strings(p.Apps)
	p.Apps = dedupe(p.Apps)
	return p, nil
}

// digest returns a short content hash, or "" when the file is absent.
func digest(c *ps2mc.Card, p string) string {
	data, err := c.ReadFile(p)
	if err != nil || len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:8]
}

func dedupe(in []string) []string {
	out := in[:0]
	var last string
	for _, s := range in {
		if s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

// String renders a one-line summary.
func (p Profile) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %-8s", p.Kind, p.Layout)
	if p.Boot != "" {
		fmt.Fprintf(&b, " boot:%s", p.Boot)
	} else {
		fmt.Fprintf(&b, " boot:%-8s", "-")
	}
	if p.Config != "" {
		fmt.Fprintf(&b, " cfg:%s", p.Config)
	} else {
		fmt.Fprintf(&b, " cfg:%-8s", "-")
	}
	fmt.Fprintf(&b, " used:%5.1fMiB", float64(p.Used)/(1<<20))
	return b.String()
}
