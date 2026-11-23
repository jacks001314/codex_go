#!/usr/bin/env python3
"""Append a Rust upstream commit window to the parity ledger.

Usage:
  python update/register_rust_window.py --repo D:\\qax\\reagent\\dev\\git\\codex \
      --from <sha> --to <sha> --window 2026-11-23

Every appended commit starts as audit_pending with a registration note; the
per-commit audit replaces the status and evidence afterwards. The ledger tail
must match --from so a window cannot be registered twice.
"""
import argparse
import json
import subprocess
from pathlib import Path

KINDS = {"test", "runtime", "protocol", "persistence", "security", "release"}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--from", dest="from_sha", required=True)
    parser.add_argument("--to", dest="to_sha", required=True)
    parser.add_argument("--window", required=True)
    parser.add_argument("--ledger", default=str(Path(__file__).resolve().parent.parent / "parity" / "commits.json"))
    args = parser.parse_args()

    ledger_path = Path(args.ledger)
    ledger = json.loads(ledger_path.read_text(encoding="utf-8"))
    if ledger["rustToInclusive"] != args.from_sha:
        raise SystemExit("ledger head %s does not match --from %s" % (ledger["rustToInclusive"], args.from_sha))

    log = subprocess.run(
        ["git", "-C", args.repo, "log", "--reverse", "--format=%H%x00%s", args.from_sha + ".." + args.to_sha],
        check=True, capture_output=True, text=True, encoding="utf-8").stdout
    rows = [line.split("\x00", 1) for line in log.splitlines() if line]
    if not rows:
        raise SystemExit("empty commit range")

    seen = {entry["sha"] for entry in ledger["commits"]}
    added = 0
    for sha, subject in rows:
        if sha in seen:
            raise SystemExit("duplicate commit %s" % sha)
        ledger["commits"].append({
            "sha": sha,
            "subject": subject,
            "kind": "runtime",
            "domains": ["codex_go"],
            "status": "audit_pending",
            "evidence": ["Registered from the %s upstream window; awaiting per-commit audit." % args.window],
        })
        seen.add(sha)
        added += 1
    ledger["rustToInclusive"] = args.to_sha
    ledger_path.write_text(json.dumps(ledger, indent=1, sort_keys=True, ensure_ascii=False) + "\n", encoding="utf-8", newline="\n")
    print("appended %d commits; ledger now %d, head %s" % (added, len(ledger["commits"]), args.to_sha))


if __name__ == "__main__":
    main()
