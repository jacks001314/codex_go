#!/usr/bin/env python3
"""Recompute every number reported from parity/commits.json and gate the ledger shape.

Anchor-agnostic since the 2026-11-10 round: the range comes from the ledger itself so a
re-anchor cannot leave a stale pin behind in this verifier.
"""
import collections, json, os, re, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ledger = json.loads(open(os.path.join(ROOT, "parity/commits.json"), "rb").read())
commits = ledger["commits"]
sha40 = re.compile(r"^[0-9a-f]{40}$")
sha10 = re.compile(r"^[0-9a-f]{10}$")

counts = collections.Counter(c["status"] for c in commits)
kinds = collections.Counter(c["kind"] for c in commits)
domains = collections.Counter(tuple(c["domains"]) for c in commits)
shas = [c["sha"] for c in commits]
dup = [s for s, n in collections.Counter(shas).items() if n > 1]
prefix_only = [c["sha"] for c in commits if sha10.match(c["sha"])]
open_items = [c for c in commits if c["status"] in ("audit_pending", "implementation_pending", "regression_pending")]

report = {
    "rustFromExclusive": ledger["rustFromExclusive"],
    "rustToInclusive": ledger["rustToInclusive"],
    "total": len(commits),
    "uniqueSha": len(set(shas)),
    "duplicateSha": dup,
    "statusCounts": dict(sorted(counts.items())),
    "kindCounts": dict(sorted(kinds.items())),
    "domainSets": {"+".join(k): v for k, v in domains.items()},
    "sha40": sum(1 for s in shas if sha40.match(s)),
    "prefixOnly10": prefix_only,
    "openStatusCount": len(open_items),
    "firstSha": shas[0],
    "lastSha": shas[-1],
    "fieldsConsistent": all(sorted(c) == ["domains", "evidence", "kind", "sha", "status", "subject"] for c in commits),
    "closedWithEvidence": all(c["evidence"] for c in commits if c["status"] in ("complete", "not_applicable")),
}
print(json.dumps(report, indent=1))

# A 10-hex prefix is accepted only when the entry's evidence records why the digest
# could not be resolved; the list stays in the report so it cannot go unnoticed.
undocumented = [
    c["sha"] for c in commits
    if sha10.match(c["sha"]) and "10-hex prefix" not in " ".join(c["evidence"])
]

ok = (
    report["total"] == report["uniqueSha"]
    and not dup
    and not undocumented
    and report["openStatusCount"] == 0
    and report["fieldsConsistent"]
    and report["closedWithEvidence"]
    and sha40.match(ledger["rustFromExclusive"] or "")
    and sha40.match(ledger["rustToInclusive"] or "")
    and len(ledger["commits"]) > 0
    and report["lastSha"] == ledger["rustToInclusive"]
)
print("\nprefix-only without evidence:", undocumented)
print("GATE:", "PASS" if ok else "FAIL")
sys.exit(0 if ok else 1)
