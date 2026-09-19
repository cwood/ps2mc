# ps2mc

A Go library and CLI for reading and writing PlayStation 2 memory card images.

## Layout

| Package | Responsibility |
| --- | --- |
| `ps2mc/` | Filesystem: superblock, FAT, directories, files, snapshots |
| `ps2mc/image/` | On-disk byte layout and ECC. Knows nothing about the filesystem |
| `ps2mc/bootcard/` | Recognises FMCB / PS2BBL from card contents |
| `ps2mc/fusefs/` | FUSE mount |
| `cmd/ps2mc/` | Cobra CLI, one file per command group |

The split between `ps2mc` and `ps2mc/image` matters: the filesystem asks for
page N, and `image` decides whether that lives at `N*512` or `N*528` and
whether ECC needs recomputing.

## Format facts worth not rediscovering

These were each learned by getting them wrong first.

**Layout cannot be inferred from file size.** Two layouts exist: ECC (512-byte
page plus 16-byte spare) and raw (bare pages, written by adapters like the
SD2PSX). Images exist whose byte length divides cleanly by 528 while their
pages are really 512 apart — `mymcplus --no-ecc` produces one. A size
heuristic reads plausible-looking garbage rather than failing. `Open` probes
each stride and keeps the one whose root directory contains a valid "." entry.

**ECC must be byte-exact.** A card written with wrong ECC is silently corrupt
on real hardware. `image.Spare` is verified against mymc+ output on 32 vectors
in `testdata/ecc_vectors.txt`, including all-zero and all-`0xFF`.

**Never re-encode a directory entry to change its length.** `Entry` does not
model every field the format carries — the attribute word and 28 reserved
bytes are absent — so `encodeEntry` zeroes them. For the root it is worse: the
root's entry *is* its own "." slot, so rewriting it replaces "." with a bogus
name and leaves the card unreadable. Use `setLength`, which patches the four
length bytes in place. There is a regression test.

**Respect a directory's length when reading it.** Slots past `Length` hold
uninitialised bytes that decode into nonsense entries with absurd sizes. Only
real cards have such slots; freshly formatted fixtures do not, so this is
invisible in tests unless you look for it.

**Filenames cap at 32 characters.** Longer names are rejected with
`ErrNameTooLong`, and through a mount that must surface as `ENAMETOOLONG`
rather than silently truncating.

**Directories grow but never shrink.** Creating a file in a full directory
permanently costs one cluster. Tests should assert steady-state invariants
(repeated write/remove cycles are free-space neutral) rather than absolute
byte counts, or they encode that one-off cost as a bug.

**A directory's "." slot is not self-referential.** Its `Cluster` and
`ParentEntry` point back at the entry describing that directory in its parent.

## Testing

```sh
make test     # wires up fixture paths
make lint vet fmt build
```

Two fixtures, both optional — tests skip cleanly without them:

- `testdata/fixtures/{test-ecc,test-raw}.ps2` — 8 MiB binaries, untracked.
  Regenerate with mymc+; the README has the commands. CI generates them.
- `testdata/ecc_vectors.txt` — tracked.

Override with `PS2MC_FIXTURES` and `ECC_VECTORS`.

**Cross-validate against mymc+ rather than only against ourselves.** The
strongest checks in this project are: does an independent implementation read
what we wrote, does its fsck report `No errors found`, and does a file we
wrote extract byte-identically? Several real bugs were caught that way and
would have passed a self-consistent test suite.

```sh
pip install mymcplus
python -m mymcplus card.ps2 check      # fsck
python -m mymcplus card.ps2 ls APPS
```

mymc+ cannot read the raw layout, so convert first:
`ps2mc convert card.mcd out.ps2 --to ecc`.

## Gotchas

- **FUSE mountpoints must be somewhere `fusermount` can traverse.** Paths under
  restrictive temp directories are refused by FUSE itself. Mount tests create
  their mountpoint under `$HOME` for this reason; `t.TempDir()` would make them
  skip everywhere.
- **`golangci-lint` may be built against an older Go** than the toolchain and
  refuse to run. `make lint` surfaces that rather than hiding it, which means
  `make all` fails at that step. Rebuild it with `go install`.
- FUSE is Linux and macOS only. `cmd/ps2mc/mount_unsupported.go` stubs the
  command on Windows so the rest of the tool still builds there.

## Conventions

GPL-3.0-or-later, because the ECC implementation derives from mymc+. Keep new
code compatible; no per-file licence headers are used.

`CHANGELOG.txt` is plain text and the release workflow extracts a tag's
section as the release notes, so keep the version heading format intact.
