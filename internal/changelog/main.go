// Command changelog rotates the Unreleased section of CHANGELOG.txt into a
// version heading and writes that section's body to NOTES.md, so a release's
// notes and the changelog cannot drift apart.
//
// Usage:
//
//	go run ./internal/changelog 0.2.0
//
// It exits with status 2 when there is nothing to rotate, either because there
// is no Unreleased section or because it is empty. The release workflow runs
// the rotation twice — once against the tag to build the notes, once against
// main to commit the result — and treats status 2 from the second run as work
// already done.
package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	changelogPath = "CHANGELOG.txt"
	notesPath     = "NOTES.md"
)

// errNothingToRotate reports a changelog that holds no unreleased changes.
// Callers distinguish it from a genuine failure by exit status.
var errNothingToRotate = errors.New("nothing to rotate")

var (
	unreleasedHeading = regexp.MustCompile(`(?m)^Unreleased\n-+\n`)
	releaseHeading    = regexp.MustCompile(`(?m)^[0-9]+\.[0-9]+\.[0-9]+( |$)`)
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: changelog <version>")
		os.Exit(1)
	}

	text, err := os.ReadFile(changelogPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	notes, rotated, err := rotate(string(text), os.Args[1], time.Now())
	if errors.Is(err, errNothingToRotate) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := os.WriteFile(notesPath, []byte(notes), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(changelogPath, []byte(rotated), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// rotate returns the body of the Unreleased section and the changelog with
// that section renamed to version, dated, and replaced by a fresh empty
// Unreleased section. The section runs to the next release heading, so an
// entry mentioning a version mid-line does not end it early.
func rotate(text, version string, date time.Time) (notes, updated string, err error) {
	loc := unreleasedHeading.FindStringIndex(text)
	if loc == nil {
		return "", "", fmt.Errorf("%w: CHANGELOG.txt has no Unreleased section", errNothingToRotate)
	}

	rest := text[loc[1]:]
	end := len(rest)
	if next := releaseHeading.FindStringIndex(rest); next != nil {
		end = next[0]
	}

	body := strings.Trim(rest[:end], "\n")
	if body == "" {
		return "", "", fmt.Errorf("%w: the Unreleased section is empty", errNothingToRotate)
	}

	heading := fmt.Sprintf("%s — %s", version, date.Format(time.DateOnly))
	var b strings.Builder
	b.WriteString(text[:loc[0]])
	b.WriteString("Unreleased\n----------\n\n\n")
	b.WriteString(heading + "\n")
	b.WriteString(strings.Repeat("-", utf8.RuneCountInString(heading)) + "\n")
	b.WriteString(body + "\n")
	if tail := rest[end:]; strings.TrimSpace(tail) != "" {
		b.WriteString("\n\n" + tail)
	}
	return body + "\n", b.String(), nil
}
