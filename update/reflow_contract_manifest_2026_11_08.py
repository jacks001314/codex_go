#!/usr/bin/env python3
"""Re-emit parity/contracts/manifest.json in its canonical on-disk format.

Canonical format (verified byte-identical against the pre-rewrite file):
  * one-space indent
  * CRLF line endings
  * no trailing newline
  * non-ASCII kept literal (ensure_ascii=False)

Usage:
  python3 update/reflow_contract_manifest_2026_11_08.py --check parity/contracts/manifest.json
  python3 update/reflow_contract_manifest_2026_11_08.py --write parity/contracts/manifest.json

--check exits 1 when the file is not in canonical form and prints the byte/line
delta; --write rewrites it in place (keeping a .bak next to it) and re-reads to
confirm the JSON is unchanged.
"""
import argparse
import hashlib
import json
import os
import sys


def canonical_bytes(document):
    text = json.dumps(document, indent=1, ensure_ascii=False)
    return text.replace("\n", "\r\n").encode("utf-8")


def main():
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--check", action="store_true")
    mode.add_argument("--write", action="store_true")
    parser.add_argument("path")
    args = parser.parse_args()

    raw = open(args.path, "rb").read()
    document = json.loads(raw)
    wanted = canonical_bytes(document)

    if raw == wanted:
        print("canonical: %d bytes, sha256 %s" % (len(raw), hashlib.sha256(raw).hexdigest()))
        return 0

    old_lines = raw.decode("utf-8").split("\r\n")
    new_lines = wanted.decode("utf-8").split("\r\n")
    print("NOT canonical")
    print("  current : %6d bytes, %4d lines, %s, sha256 %s"
          % (len(raw), len(old_lines), "CRLF" if b"\r\n" in raw else "LF", hashlib.sha256(raw).hexdigest()))
    print("  canonical: %6d bytes, %4d lines, CRLF, sha256 %s"
          % (len(wanted), len(new_lines), hashlib.sha256(wanted).hexdigest()))
    print("  delta   : %+d bytes, %+d lines" % (len(wanted) - len(raw), len(new_lines) - len(old_lines)))
    if not args.write:
        return 1

    backup = args.path + ".bak"
    with open(backup, "wb") as handle:
        handle.write(raw)
    with open(args.path, "wb") as handle:
        handle.write(wanted)
    if json.loads(open(args.path, "rb").read()) != document:
        sys.exit("ERROR: reflow changed the JSON semantics; restored nothing, backup at %s" % backup)
    print("rewrote %s (backup %s)" % (args.path, os.path.basename(backup)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
