#!/usr/bin/env python3
"""Regenerate parity/baseline.json counters and blocker text from the ledger.

Reads parity/commits.json, parity/domains.json and parity/contracts/manifest.json
so the reported numbers cannot drift from the facts they summarise.
"""
import collections, io, json, os, re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load(rel):
    return json.loads(io.open(os.path.join(ROOT, rel), encoding="utf-8").read())


ledger = load("parity/commits.json")
domains = load("parity/domains.json")
contracts = load("parity/contracts/manifest.json")
baseline = load("parity/baseline.json")

commits = ledger["commits"]
counts = collections.Counter(c["status"] for c in commits)
partial = [c for c in commits if c["status"] == "partial"]
blocked = [c for c in commits if c["status"] == "blocked_precondition"]
open_domains = [d for d in domains["domains"] if d["status"] not in ("complete", "equivalent", "not_applicable")]
open_contracts = [c for c in contracts["contracts"] if c["status"] != "complete"]
subject_matched = [c for c in commits if any(e.startswith("digest resolved by subject:") for e in c["evidence"])]


def pr(commit):
    m = re.search(r"\(#(\d+)\)", commit["subject"])
    return "#" + m.group(1) if m else commit["sha"][:10]


def short(commit):
    return re.sub(r"\s*\(#\d+\)\s*$", "", commit["subject"]).strip()


blockers = [
    "Per-commit certification of the %d upstream commits in %s..%s is in progress; parity/commits.json records every commit with its "
    "audit status (%d complete, %d not applicable, %d audit_pending, %d regression_pending, %d partial, %d blocked_precondition)"
    % (len(commits), ledger["rustFromExclusive"], ledger["rustToInclusive"], counts["complete"], counts["not_applicable"],
       counts["audit_pending"] + counts["implementation_pending"], counts["regression_pending"], counts["partial"],
       counts["blocked_precondition"]),
    "%d commits are blocked on architecture: %s"
    % (len(blocked), ", ".join("%s (%s)" % (pr(c), short(c)) for c in blocked)),
    "%d commits are partial and keep the candidate open: %s; each entry's evidence records the residual it was judged against"
    % (len(partial), ", ".join(pr(c) for c in partial)),
]
if open_domains:
    blockers.append(
        "%d domains remain open in parity/domains.json: %s"
        % (len(open_domains), ", ".join("%s (%s)" % (d["id"], d["status"]) for d in open_domains))
    )
if open_contracts:
    blockers.append(
        "%d contracts remain open in parity/contracts/manifest.json: %s"
        % (len(open_contracts), ", ".join("%s (%s)" % (c["id"], c["status"]) for c in open_contracts))
    )
blockers.append(
    "app-server-schema stays partial on one generator gap: account/securitySetup/read (#60082) is absent "
    "from BuildProtocolSchema, so ClientRequest.json and ClientRequest.ts differ from the Rust fixtures at "
    "%s; the vendored precomputed pair is byte-identical to the anchor tree, so "
    "TestPrecomputedExportArtifactsMatchTargetRustCommit and TestPrecomputedAppServerExportsMatchRustTarget pass"
    % ledger["rustToInclusive"]
)
if subject_matched:
    blockers.append(
        "%d commits (%s) are identified by subject rather than by upstream sha: the recorded prefix has no upstream object, so the "
        "40-hex digest comes from the byte-identical subject in the local Rust mirror (a rebased history line, not an upstream object). "
        "They are auditable via `git cat-file -e <sha>` in the mirror but must not be read as GitHub-compare entries; "
        "see update/ledger_prefix_resolution_2026_11_08.json"
        % (len(subject_matched), ", ".join(pr(c) for c in subject_matched))
    )

baseline["frozenAt"] = "2026-11-10"
baseline["plan"] = "update/plan_2026_11_10_ledger.md"
baseline["target"]["rustCommit"] = ledger["rustToInclusive"]
baseline["certificationBlockers"] = blockers
baseline["certificationReady"] = False

text = json.dumps(baseline, indent=1, ensure_ascii=False) + "\n"
io.open(os.path.join(ROOT, "parity/baseline.json"), "wb").write(text.replace("\n", "\r\n").encode("utf-8"))
print("baseline regenerated:", len(blockers), "blockers")
for line in blockers:
    print(" -", line[:150])
