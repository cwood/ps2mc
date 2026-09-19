#!/usr/bin/env python3
"""Fetch OPL cover art for PS2 ISOs on /mnt/ps2drive.

Resolves each ISO's boot/startup id by parsing ISO9660 (PVD -> root dir ->
SYSTEM.CNF -> BOOT2 line), then downloads the matching Named_Boxart from the
libretro thumbnail server, downscales it to 256px wide and writes
ART/<STARTUP_ID>_COV.png. Existing covers are never overwritten.
"""

import io
import os
import re
import sys
import time
import difflib
import urllib.parse
import urllib.error
import urllib.request

from PIL import Image

DRIVE = "/mnt/ps2drive"
ART = os.path.join(DRIVE, "ART")
ISO_DIRS = [os.path.join(DRIVE, "DVD"), os.path.join(DRIVE, "CD")]
BASE = "https://thumbnails.libretro.com/Sony%20-%20PlayStation%202/Named_Boxarts/"
SECTOR = 2048
TARGET_WIDTH = 256
BOOT2_RE = re.compile(rb"BOOT2\s*=\s*cdrom0?:\\?([A-Z]+_[0-9]{3}\.[0-9]{2})", re.I)
# RetroArch substitutes these with '_' when naming thumbnail files.
ILLEGAL = set('&*/:`<>?\\|"')
LANG_TAG_RE = re.compile(r"\s*\((?:[A-Z][a-z](?:,[A-Z][a-z])*)\)")


def _records(buf):
    """Yield (name, lba, size) for each directory record in a raw dir extent."""
    off = 0
    while off < len(buf):
        rlen = buf[off]
        if rlen == 0:
            off = (off // SECTOR + 1) * SECTOR
            continue
        rec = buf[off:off + rlen]
        if len(rec) < 33:
            break
        lba = int.from_bytes(rec[2:6], "little")
        size = int.from_bytes(rec[10:14], "little")
        namelen = rec[32]
        name = rec[33:33 + namelen].split(b";")[0].decode("ascii", "replace")
        yield name, lba, size
        off += rlen


def startup_id(path):
    with open(path, "rb") as f:
        f.seek(16 * SECTOR)
        pvd = f.read(SECTOR)
        if len(pvd) < SECTOR or pvd[1:6] != b"CD001":
            return None
        root = pvd[156:190]
        lba = int.from_bytes(root[2:6], "little")
        size = int.from_bytes(root[10:14], "little")
        f.seek(lba * SECTOR)
        rootdir = f.read(size)
        for name, clba, csize in _records(rootdir):
            if name.upper() == "SYSTEM.CNF":
                f.seek(clba * SECTOR)
                m = BOOT2_RE.search(f.read(csize))
                return m.group(1).decode("ascii").upper() if m else None
    return None


def sanitized(name):
    return "".join("_" if c in ILLEGAL else c for c in name)


def candidates(name):
    out = []
    for n in (sanitized(name), name, sanitized(LANG_TAG_RE.sub("", name)),
              LANG_TAG_RE.sub("", name)):
        if n not in out:
            out.append(n)
    return out


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "ps2-art/1.0"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


_index = None


def index():
    """Lazily fetch and cache the boxart directory listing for fuzzy matching."""
    global _index
    if _index is None:
        html = fetch(BASE).decode("utf-8", "replace")
        _index = sorted({urllib.parse.unquote(m)[:-4]
                         for m in re.findall(r'href="([^"]+\.png)"', html)})
    return _index


PAREN_RE = re.compile(r"\s*\([^)]*\)")
REGION_RE = re.compile(r"\((USA|Europe|Japan|Asia|Korea|China|Australia|World)[^)]*\)")


def _key(name):
    """Order-insensitive comparison key: title words minus all parentheticals."""
    base = PAREN_RE.sub("", name).lower()
    return " ".join(sorted(re.findall(r"[a-z0-9]+", base)))


def _discs(name):
    return set(re.findall(r"\(Disc \d\)", name))


def best_match(name):
    """Fuzzy-pick an index entry, preferring the same region and disc number."""
    regions = set(REGION_RE.findall(name)) or {"USA"}
    key = _key(name)
    discs = _discs(name)
    best, best_score = None, 0.0
    for entry in index():
        # A disc-numbered release must match the same disc, or art gets swapped.
        if _discs(entry) != discs:
            continue
        score = difflib.SequenceMatcher(None, key, _key(entry)).ratio()
        if score < 0.82:
            continue
        if set(REGION_RE.findall(entry)) & regions:
            score += 0.15
        if score > best_score:
            best, best_score = entry, score
    return best


def download(name):
    """Return (png_bytes, matched_name) or (None, None)."""
    for cand in candidates(name):
        url = BASE + urllib.parse.quote(cand) + ".png"
        try:
            return fetch(url), cand
        except urllib.error.HTTPError as e:
            if e.code != 404:
                raise
        time.sleep(0.2)
    match = best_match(name)
    if match:
        try:
            return fetch(BASE + urllib.parse.quote(match) + ".png"), match
        except urllib.error.HTTPError:
            pass
    return None, None


def save(data, dest):
    img = Image.open(io.BytesIO(data)).convert("RGBA")
    h = max(1, round(img.height * TARGET_WIDTH / img.width))
    img.resize((TARGET_WIDTH, h), Image.LANCZOS).save(dest, "PNG", optimize=True)


def isos():
    for d in ISO_DIRS:
        for fn in sorted(os.listdir(d)):
            if fn.lower().endswith(".iso"):
                yield os.path.join(d, fn)


def main():
    os.makedirs(ART, exist_ok=True)
    fetched = skipped = failed = 0
    unresolved, noart, seen = [], [], {}

    for path in isos():
        name = os.path.basename(path)[:-4]
        try:
            sid = startup_id(path)
        except OSError as e:
            sid = None
            print(f"[read-error] {name}: {e}")
        if not sid:
            unresolved.append(name)
            failed += 1
            print(f"[no-id]   {name}")
            continue
        seen.setdefault(sid, []).append(name)
        dest = os.path.join(ART, f"{sid}_COV.png")
        if os.path.exists(dest):
            skipped += 1
            print(f"[skip]    {sid}  {name}")
            continue
        data, matched = download(name)
        if data is None:
            noart.append((name, sid))
            failed += 1
            print(f"[no-art]  {sid}  {name}")
            continue
        save(data, dest)
        fetched += 1
        print(f"[ok]      {sid}  {name}" + ("" if matched == name else f"  <- {matched}"))
        time.sleep(0.3)

    print(f"\nfetched={fetched} skipped={skipped} failed={failed}")
    print(f"unique ids={len(seen)}")
    for sid, names in sorted(seen.items()):
        if len(names) > 1:
            print(f"shared id {sid}: " + " | ".join(names))
    if unresolved:
        print("unresolved ids:")
        for n in unresolved:
            print("  " + n)
    if noart:
        print("no art found:")
        for n, s in noart:
            print(f"  {n} ({s})")

    have = {f[:-8] for f in os.listdir(ART) if f.endswith("_COV.png")}
    missing = sorted(set(seen) - have)
    orphans = sorted(have - set(seen))
    print(f"missing covers: {missing or 'none'}")
    print(f"orphan covers: {orphans or 'none'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
