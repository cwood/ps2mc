# ps2mc

[![ci](https://github.com/cwood/ps2mc/actions/workflows/ci.yml/badge.svg)](https://github.com/cwood/ps2mc/actions/workflows/ci.yml)
[![licence: GPL-3.0-or-later](https://img.shields.io/badge/licence-GPL--3.0--or--later-blue)](LICENSE)
[![go reference](https://pkg.go.dev/badge/github.com/cwood/ps2mc.svg)](https://pkg.go.dev/github.com/cwood/ps2mc)

Read and write PlayStation 2 memory card images.

`ps2mc` understands both page layouts found in the wild, can mount a card as a
filesystem, identifies what boot software a card carries, and snapshots a card
before changing it.

## Why

PS2 memory card images come in two layouts that cannot be told apart by file
size:

- **ECC** — each 512-byte page is followed by a 16-byte spare holding its
  Hamming codes. Physical cards and emulators use this.
- **Raw** — pages stored back to back, no spare. Written by adapters such as
  the SD2PSX.

A size heuristic looks workable and is not. Images exist whose byte length
divides cleanly by 528 while their pages are really 512 bytes apart, so a
size-based guess reads plausible-looking garbage rather than failing. `ps2mc`
probes each stride and keeps the one that yields a coherent root directory.

## Install

Download an archive for your platform from the
[releases page](https://github.com/cwood/ps2mc/releases), verify it, and put
the binary on your `PATH`:

```sh
version=v0.1.0
os=linux                 # or darwin
arch=amd64               # or arm64
base="https://github.com/cwood/ps2mc/releases/download/${version}"

curl -LO "${base}/ps2mc-${version}-${os}-${arch}.tar.gz"
curl -LO "${base}/SHA256SUMS"
sha256sum --ignore-missing -c SHA256SUMS

tar -xzf "ps2mc-${version}-${os}-${arch}.tar.gz"
sudo install "ps2mc-${version}-${os}-${arch}/ps2mc" /usr/local/bin/ps2mc
```

Windows builds ship as a `.zip` holding `ps2mc.exe`. On macOS use `shasum -a
256` in place of `sha256sum`.

The binaries are static and need nothing installed. The mount command is the
exception: it needs FUSE, which Linux has in the kernel and macOS gets from
macFUSE. On Windows `mount` is a stub that says so; every other command
works there.

With a Go toolchain you can install it straight from source instead:

```sh
go install github.com/cwood/ps2mc/cmd/ps2mc@latest
```

To work on it, see [Development](#development).

## Usage

### Look at a card

```sh
ps2mc info      card.mcd            # layout, capacity, free space
ps2mc ls        card.mcd APPS       # list a directory
ps2mc cat       card.mcd APPS/CONF  # write a file to stdout
ps2mc extract   card.mcd APPS/OPNPS2LD.ELF -o ./out
```

### Change a card

Commands that write take a snapshot first and abort if it fails.

```sh
ps2mc install card.mcd OPNPS2LD.ELF -d APPS
ps2mc rm      card.mcd APPS/OLD.ELF
ps2mc rm      card.mcd OLDDIR -d          # -d removes an empty directory
```

Add `--no-backup` to skip the snapshot, `--backup-dir` to choose where it goes.

### Mount it

```sh
ps2mc mount card.mcd ~/mnt/card      # --ro for read-only
cp OPNPS2LD.ELF ~/mnt/card/APPS/
fusermount -u ~/mnt/card
```

The mountpoint is created if missing and removed again on unmount. A non-empty
directory is refused, since mounting over it would hide its contents.

Files are buffered in memory and written back when the last handle closes. The
format records a file's length in its directory entry and stores contents as a
cluster chain, so there is no meaningful partial write; a card holds at most
64 MiB, so buffering costs nothing.

The mountpoint must be somewhere `fusermount` can traverse. Restrictive
temporary directories are refused by FUSE itself, not by this tool.

### Identify a card

```sh
ps2mc identify /path/to/BootCard-*.mcd
```

Reports whether each card carries FMCB or PS2BBL based on its contents, and
hashes the boot payloads so cards that are really the same install are grouped:

```
BootCard-1.mcd   FMCB   standard  boot:29709bde cfg:7881ad9a  used: 5.1MiB
                   markers: SYS-CONF/FMCB_CFG.ELF, SYS-CONF/FREEMCB.CNF
BootCard-3.mcd   FMCB   standard  boot:29709bde cfg:7881ad9a  used: 4.0MiB

identical payloads: BootCard-1.mcd, BootCard-3.mcd
```

This is more reliable than the label a device keeps in a side file, which
drifts. On the card set this was developed against, two channels had their
labels swapped and four "different versions" were two builds duplicated.

### Snapshots

```sh
ps2mc backup  card.mcd -m "before OPL update"
ps2mc backups                                  # newest first
ps2mc restore BootCard-1.mcd-20260919T011004Z card.mcd
```

Snapshots go to `~/.local/share/ps2mc/backups` by default, each with a SHA-256
manifest. `restore` verifies the checksum before writing.

### Convert between layouts

```sh
ps2mc convert card.mcd out.ps2 --to ecc
```

## Constraints of the format

- Filenames are capped at **32 characters**. Longer names are rejected, and
  through a mount this surfaces as `ENAMETOOLONG`.
- A card is at most **64 MiB**; the common size is 8 MiB.
- Directories grow but never shrink, so creating a file in a full directory
  permanently costs one cluster.

## Development

Requires Go 1.27. The library API is documented on
[pkg.go.dev](https://pkg.go.dev/github.com/cwood/ps2mc); `ps2mc/` is the
filesystem, `ps2mc/image/` the on-disk layout and ECC, `ps2mc/bootcard/` boot
software identification and `ps2mc/fusefs/` the FUSE mount.

```sh
git clone https://github.com/cwood/ps2mc && cd ps2mc

make build    # builds ./bin/ps2mc
make test     # go test ./... with fixture paths wired up
make lint     # golangci-lint when present, else vet + gofmt
make all      # fmt, vet, lint, test, build
```

`make vet`, `make fmt` and `make clean` are there too; `make help` lists the
lot with the fixture paths it resolved.

Tests need two fixtures and skip cleanly without them:

- `testdata/fixtures/` — `test-ecc.ps2` and `test-raw.ps2`, generated with
  [mymc+](https://github.com/thestr4ng3r/mymcplus):
  ```sh
  python -m mymcplus testdata/fixtures/test-ecc.ps2 format -c 8192 -f
  python -m mymcplus testdata/fixtures/test-raw.ps2 format -c 8192 -f -e
  ```
  They are 8 MiB binaries and are not tracked in the repository.
- `testdata/ecc_vectors.txt` — reference ECC output, tracked.

Override either with `PS2MC_FIXTURES` and `ECC_VECTORS`.

Every pull request is expected to add a line to the `Unreleased` section of
`CHANGELOG.txt`; CI checks for it, and the `no-changelog` label skips the check
for changes that do not warrant a release note. Tagging `vX.Y.Z` turns that
section into the release notes and the version heading.

The ECC implementation is verified against mymc+ on 32 vectors including the
all-zero and all-`0xFF` cases. A card written with wrong ECC is silently
corrupt on real hardware, so this is checked rather than assumed.

## Licence

GPL-3.0-or-later. See [LICENSE](LICENSE).

The ECC implementation is derived from
[mymc+](https://github.com/thestr4ng3r/mymcplus) by Florian Märkl, itself based
on mymc by Ross Ridge, both GPL-3.0. That derivation is why this project is
copyleft rather than permissively licensed.
