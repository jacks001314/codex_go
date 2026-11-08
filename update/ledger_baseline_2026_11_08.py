#!/usr/bin/env python3
"""Regenerate parity/baseline.json counters and blocker text from parity/commits.json."""
import collections, json, os, re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ledger = json.loads(open(os.path.join(ROOT, "parity/commits.json"), "rb").read())
domains = json.loads(open(os.path.join(ROOT, "parity/domains.json"), "rb").read())
old = json.loads(open(os.path.join(ROOT, "parity/baseline.json"), "rb").read())

commits = ledger["commits"]
n = len(commits)
counts = collections.Counter(c["status"] for c in commits)
partial = [c for c in commits if c["status"] == "partial"]
blocked = [c for c in commits if c["status"] == "blocked_precondition"]
prefix_only = [c for c in commits if len(c["sha"]) != 40]
subject_matched = [c for c in commits if any(e.startswith("digest resolved by subject:") for e in c["evidence"])]


def pr(c):
    m = re.search(r"\(#(\d+)\)", c["subject"])
    return "#" + m.group(1) if m else c["sha"][:10]


def short(c):
    return re.sub(r"\s*\(#\d+\)\s*$", "", c["subject"]).strip()


blockers = [
    "Per-commit certification of the %d upstream commits in %s..%s is in progress; parity/commits.json records every commit with its "
    "audit status (%d complete, %d not applicable, %d audit_pending, %d regression_pending, %d partial, %d blocked_precondition)"
    % (n, ledger["rustFromExclusive"], ledger["rustToInclusive"], counts["complete"], counts["not_applicable"],
       counts["audit_pending"] + counts["implementation_pending"], counts["regression_pending"], counts["partial"],
       counts["blocked_precondition"]),
    "%d commits are blocked on architecture: %s"
    % (len(blocked), ", ".join("%s (%s)" % (pr(c), short(c)) for c in blocked)),
    "%d commits are partial and keep the candidate open: %s; each entry's evidence records the residual it was judged against"
    % (len(partial), ", ".join(pr(c) for c in partial)),
]
open_domains = [d for d in domains["domains"] if d["status"] not in ("complete", "equivalent", "not_applicable")]
if open_domains:
    blockers.append(
        "%d domains remain open in parity/domains.json: %s"
        % (len(open_domains), ", ".join("%s (%s)" % (d["id"], d["status"]) for d in open_domains))
    )
blockers.append(
    "The vendored precomputed app-server exports lag pin %s: decompressed key counts are stable typescript 776 vs 778 and "
    "json_schema 326 vs 328, experimental 941 vs 943 and 474 vs 476; Rust's v2/GatewayOAuthLoginParams and "
    "v2/GatewayOAuthReadParams are missing and 4 (stable) / 7 (experimental) shared entries differ in content, so "
    "app-server-schema is partial and TestPrecomputedExportArtifactsMatchTargetRustCommit fails on sha256 "
    "6e0fb0e51dd149b0add1013835b8caeab941467a6bbf9b62d4a6f3b3b500f072 / "
    "6b336adc058aaacc53614253887415de3c79dae2a946611004be95538f483b8a versus the frozen pins "
    "5a93be76c2f389899b3a201bb470d69e48ac01ddc5233a431613bbff5a634a7f / "
    "72216e6360b4aadb896c46fe3608f43c2f1f1e46ff894da0f82539703faa5667"
    % ledger["rustToInclusive"]
)
if prefix_only:
    blockers.append(
        "%d commits (%s) keep a 10-hex prefix because no object resolves it (the upstream commits API answers 422 for the prefix) and "
        "fabricating a digest is not allowed; each entry's evidence records why. Resolve them by subject before certification"
        % (len(prefix_only), ", ".join(c["sha"] for c in prefix_only))
    )
if subject_matched:
    blockers.append(
        "%d commits (%s) are identified by subject rather than by upstream sha: the recorded prefix has no upstream object, so the "
        "40-hex digest comes from the byte-identical subject in the local Rust mirror (a rebased history line, not an upstream object). "
        "They are auditable via `git cat-file -e <sha>` in the mirror but must not be read as GitHub-compare entries; "
        "see update/ledger_prefix_resolution_2026_11_08.json"
        % (len(subject_matched), ", ".join(pr(c) for c in subject_matched))
    )

old["frozenAt"] = "2026-11-08"
old["plan"] = "update/plan_2026_11_08_ledger.md"
# verified.rustCommit tracks the last *certified* parity.json head (parity.json
# rustUpstreamHead). Advancing it without a renewed SDK/parity run would overclaim
# certification, and TestAlignmentBaselineIsFrozenAndTraceable enforces the link.
old["verified"]["rustCommit"] = "67f0994b532b8740f14b5b738af7b7b2a915b1d6"
old["target"]["rustCommit"] = ledger["rustToInclusive"]
old["certificationBlockers"] = blockers
old["certificationReady"] = False

text = json.dumps(old, indent=1, ensure_ascii=False).replace("\n", "\r\n") + "\r\n"
open(os.path.join(ROOT, "parity/baseline.json"), "wb").write(text.encode("utf-8"))
print(text)
