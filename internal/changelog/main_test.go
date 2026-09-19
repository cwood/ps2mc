package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const header = `Changelog
=========

All notable changes to ps2mc are recorded here. Versions follow semantic
versioning; dates are ISO 8601.


`

const unreleased = header + `Unreleased
----------

Added
  - A thing.
`

var released = time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)

// rotateOK rotates text and fails the test if the changelog was unusable.
func rotateOK(t *testing.T, text, version string) (notes, updated string) {
	t.Helper()
	notes, updated, err := rotate(text, version, released)
	if err != nil {
		t.Fatalf("rotate(%s): %v", version, err)
	}
	return notes, updated
}

func TestRotateFirstRelease(t *testing.T) {
	notes, updated := rotateOK(t, unreleased, "0.1.0")

	if want := "Added\n  - A thing.\n"; notes != want {
		t.Errorf("notes = %q, want %q", notes, want)
	}
	want := header + `Unreleased
----------


0.1.0 — 2026-09-19
------------------
Added
  - A thing.
`
	if updated != want {
		t.Errorf("rotated changelog =\n%s\nwant\n%s", updated, want)
	}
}

// The underline is drawn per rune, so the em dash must not leave it short.
func TestRotateUnderlineMatchesHeading(t *testing.T) {
	_, updated := rotateOK(t, unreleased, "0.1.0")

	lines := strings.Split(updated, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "0.1.0 ") {
			continue
		}
		if want := strings.Repeat("-", len([]rune(line))); lines[i+1] != want {
			t.Fatalf("underline = %q, want %q", lines[i+1], want)
		}
		return
	}
	t.Fatal("no version heading in the rotated changelog")
}

func TestRotateKeepsEarlierReleases(t *testing.T) {
	_, once := rotateOK(t, unreleased, "0.1.0")
	next := strings.Replace(once, "Unreleased\n----------\n", "Unreleased\n----------\n\nFixed\n  - A later fix.\n", 1)

	_, twice := rotateOK(t, next, "0.2.0")
	want := header + `Unreleased
----------


0.2.0 — 2026-09-19
------------------
Fixed
  - A later fix.


0.1.0 — 2026-09-19
------------------
Added
  - A thing.
`
	if twice != want {
		t.Errorf("rotated changelog =\n%s\nwant\n%s", twice, want)
	}
}

// A re-pushed tag rotates an already rotated changelog. It must say so rather
// than empty the section it just wrote.
func TestRotateIsIdempotent(t *testing.T) {
	_, once := rotateOK(t, unreleased, "0.1.0")

	if _, _, err := rotate(once, "0.1.0", released); !errors.Is(err, errNothingToRotate) {
		t.Fatalf("err = %v, want errNothingToRotate", err)
	}
}

func TestRotateRejectsUnusableChangelog(t *testing.T) {
	_, rotated := rotateOK(t, unreleased, "0.1.0")

	for name, text := range map[string]string{
		"empty section":   rotated,
		"no section":      header + "0.1.0 — 2026-09-19\n------------------\nAdded\n  - A thing.\n",
		"not a changelog": "nothing here",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := rotate(text, "0.2.0", released); !errors.Is(err, errNothingToRotate) {
				t.Fatalf("err = %v, want errNothingToRotate", err)
			}
		})
	}
}

// An entry that mentions a version must not read as a heading, which would
// truncate the notes at that line.
func TestRotateIgnoresVersionInsideEntry(t *testing.T) {
	notes, _ := rotateOK(t, header+"Unreleased\n----------\n\nFixed\n  - Broken since\n    0.1.0 and now fixed.\n", "0.2.0")

	if !strings.Contains(notes, "and now fixed.") {
		t.Errorf("notes truncated at a version mention:\n%s", notes)
	}
}
