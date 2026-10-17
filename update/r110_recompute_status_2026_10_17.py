#!/usr/bin/env python3
"""Round-110 ledger status reconciliation (reproducible).

The Round-109 symbol-coverage TSV assigned verdicts from symbol presence alone,
so commits that earlier rounds landed (or closed as not-applicable with
evidence) still read implementation_pending. This script re-derives each row's
verdict from update/r90_commit_ledger_2026_10_16.tsv (the evidence ledger) and
keeps two hand-verified rows for commits whose Go symbols intentionally differ.

Mapping: landed / AlreadyImplemented / complete -> complete;
not_applicable -> not_applicable; blocked_precondition -> blocked_precondition;
partial -> partial. Rows without a ledger entry keep their audited verdict.

Run from the repository root:  python update/r110_recompute_status_2026_10_17.py
"""
import collections, io, os, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LEDGER = os.path.join(ROOT, "update", "r90_commit_ledger_2026_10_16.tsv")
TSV = os.path.join(ROOT, "update", "r109_symbol_coverage_2026_10_17.tsv")

VERDICT = {
    "landed": "complete",
    "AlreadyImplemented": "complete",
    "complete": "complete",
    "not_applicable": "not_applicable",
    "blocked_precondition": "blocked_precondition",
    "partial": "partial",
}
# Rows verified by hand: (verdict, matched, coverage, unmatched).
MANUAL = {
    "47773": ("complete", "1", "1.00", ""),
    "47677": (
        "complete",
        "2",
        "0.25",
        "ListMcpResourceTemplatesHandler,ListMcpResourcesHandler,ReadMcpResourceHandler,"
        "create_list_mcp_resource_templates_tool,create_list_mcp_resources_tool,"
        "create_read_mcp_resource_tool",
    ),
}

def main():
    ledger = {}
    for line in io.open(LEDGER, encoding="utf-8", newline="").read().split("\r\n")[1:]:
        if not line or line.count("\t") < 5:
            continue
        fields = line.split("\t")
        ledger[fields[1]] = fields[4]
    lines = io.open(TSV, encoding="utf-8", newline="").read().split("\r\n")
    header, rest = lines[0], [line for line in lines[1:] if line]
    rows = [line.split("\t") for line in rest]
    before = collections.Counter(row[10] for row in rows)
    changed = 0
    for row in rows:
        pr = row[1]
        manual = MANUAL.get(pr)
        if manual:
            verdict, matched, coverage, unmatched = manual
            if row[10] != verdict or row[6] != matched or row[7] != coverage:
                row[10], row[6], row[7], row[11] = verdict, matched, coverage, unmatched
                changed += 1
            continue
        verdict = VERDICT.get(ledger.get(pr, ""))
        if verdict and row[10] != verdict:
            row[10] = verdict
            changed += 1
    after = collections.Counter(row[10] for row in rows)
    print("before:", dict(sorted(before.items())))
    print("after: ", dict(sorted(after.items())))
    print("changed:", changed)
    io.open(TSV, "w", encoding="utf-8", newline="").write(
        "\r\n".join([header] + ["\t".join(row) for row in rows]) + "\r\n"
    )

if __name__ == "__main__":
    main()
