#!/usr/bin/env python3
"""Re-align parity/commits.json with the round records (67f0994b..55897425e8).

Inputs:  parity/commits.json (existing ledger)
         update/plan_2026_11_0{2,3,4,5,6}*.md "Commit ledger" tables
Output:  parity/commits.json (rewritten, same JSON formatting)
"""
import json, re, subprocess, sys, urllib.request, datetime, collections, os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GIT = ["git", "-C", os.environ.get("CODEX_RUST_ROOT", "/home/jacks/jacks_dev/codex/codex-rs")]
FROM_EXCL = "bd19459358f534ed1cae464ec13d56600aeb45f2"
TO_INCL_PREFIX = "748ab55ca7"
ROUND_FILES = [
    "update/plan_2026_11_02.md", "update/plan_2026_11_03.md", "update/plan_2026_11_04.md",
    "update/plan_2026_11_04_round1309.md", "update/plan_2026_11_04_round1311.md",
    "update/plan_2026_11_04_round1312.md", "update/plan_2026_11_05.md",
    "update/plan_2026_11_06.md", "update/plan_2026_11_06_round1316.md",
    "update/plan_2026_11_06_round1317.md",
]
SHA = re.compile(r"^[0-9a-f]{7,40}$")


def table_rows(path):
    """Rows of every markdown table that has a Commit *and* a Disposition column."""
    def cells(line):
        return [c.strip() for c in line.strip().strip("|").split("|")]

    out, lines = [], open(os.path.join(ROOT, path), encoding="utf-8", errors="replace").read().split("\n")
    i = 0
    while i < len(lines) - 1:
        if lines[i].strip().startswith("|") and re.match(r"^\|[\s\-:|]+\|$", lines[i + 1].strip()):
            hdr = [h.lower() for h in cells(lines[i])]
            if any("commit" in h for h in hdr) and any(("disposition" in h) or ("status" in h) for h in hdr):
                ci = next(k for k, h in enumerate(hdr) if "commit" in h)
                si = next((k for k, h in enumerate(hdr) if "subject" in h), None)
                di = next(k for k, h in enumerate(hdr) if "disposition" in h or "status" in h)
                j = i + 2
                while j < len(lines) and lines[j].strip().startswith("|"):
                    c = cells(lines[j])
                    need = max(ci, di) if si is None else max(ci, si, di)
                    if len(c) > need and SHA.match(c[ci].strip("`")):
                        out.append((c[ci].strip("`"), c[si] if si is not None else "", c[di], path))
                    j += 1
                i = j
                continue
        i += 1
    return out


RESOLUTION_FILE = "update/ledger_sha_resolution_2026_11_08.json"


def recorded_resolution():
    """prefix -> full sha recorded by the audited resolution run (see RESOLUTION_FILE)."""
    path = os.path.join(ROOT, RESOLUTION_FILE)
    if not os.path.exists(path):
        return {}
    data = json.load(open(path, encoding="utf-8"))
    return {p: v for p, v in data["entries"].items() if v.get("sha")}


def git_commit(prefix):
    r = subprocess.run(GIT + ["log", "-1", "--format=%H|%ct|%s", prefix], capture_output=True, text=True)
    if r.returncode != 0 or "|" not in r.stdout:
        return None
    full, ct, subj = r.stdout.strip().split("|", 2)
    return {"sha": full, "ct": int(ct), "subject": subj}


def github_pr(pr):
    with urllib.request.urlopen("https://api.github.com/repos/openai/codex/pulls/%d" % pr, timeout=25) as r:
        d = json.load(r)
    merged = d.get("merged_at")
    ct = int(datetime.datetime.strptime(merged, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.timezone.utc).timestamp()) if merged else None
    return {"sha": d.get("merge_commit_sha"), "ct": ct, "title": d.get("title") or ""}


def status_for(disposition):
    """Round-log disposition -> ledger status. Anything unrecognised is audit_pending."""
    d = disposition.strip()
    low = d.lower()
    if re.match(r"^(ported|aligned)\b", low):
        return "complete"
    if re.match(r"^(n/?a\b|not[ _]applicable\b)", low):
        return "not_applicable"
    if re.match(r"^partial\b", low):
        return "partial"
    if re.match(r"^blocked\b", low):
        return "blocked_precondition"
    if low.startswith("no go counterpart"):
        return "not_applicable"
    if low.startswith("refactor only"):
        return "not_applicable"
    if low.startswith("tracked:"):
        return "partial"
    if "not applicable" in low:
        return "not_applicable"
    return "audit_pending"


def kind_for(subject):
    """Subject-keyword kind classification (deterministic; see plan_2026_11_08_ledger.md)."""
    t = subject.lower()
    if re.search(r"\bbazel\b|\bcargo\b|lockfile|\bnpm\b|installer|\bpackaging\b|release (artifacts|builds|configs|notes|pipeline)|\bci\b|github action|toolchain|rusty_v8|dependency audit", t):
        return "release"
    if re.search(r"\btests?\b|\bfixtures?\b|test-only", t):
        return "test"
    if re.search(r"auth|credential|token|permission|sandbox|security|oauth|tls|certificate|secret|policy", t):
        return "security"
    if re.search(r"sqlite|migrat|persist|rollout|database|storage|world state", t):
        return "persistence"
    if re.search(r"protocol|schema|json|\brpc\b|app-server|appserver|\bsdk\b|wire|typescript|\bapi\b", t):
        return "protocol"
    return "runtime"


def main():
    ledger_path = os.path.join(ROOT, "parity/commits.json")
    ledger = json.loads(open(ledger_path, "rb").read())
    existing = {c["sha"] for c in ledger["commits"]}

    rows, seen = [], set()
    for path in ROUND_FILES:
        for sha, subj, disp, src in table_rows(path):
            if sha in seen or sha in existing:
                continue
            seen.add(sha)
            rows.append({"prefix": sha, "subject": subj, "disposition": disp, "file": src})

    known = recorded_resolution()
    resolved, unresolved = {}, []
    for row in rows:
        rec = known.get(row["prefix"])
        if rec:
            resolved[row["prefix"]] = {"sha": rec["sha"], "ct": rec.get("commitTime"), "subject": ""}
    for row in rows:
        if row["prefix"] in resolved:
            continue
        hit = git_commit(row["prefix"])
        if hit:
            resolved[row["prefix"]] = hit
    for row in rows:
        if row["prefix"] in resolved:
            continue
        m = re.search(r"\(#(\d+)\)\s*$", row["subject"])
        if not m:
            unresolved.append(row["prefix"])
            continue
        try:
            d = github_pr(int(m.group(1)))
        except Exception:
            unresolved.append(row["prefix"])
            continue
        if d["sha"] and d["sha"].startswith(row["prefix"]):
            resolved[row["prefix"]] = {"sha": d["sha"], "ct": d["ct"], "subject": d["title"] or row["subject"]}
        else:
            unresolved.append(row["prefix"])

    # chronological order == git rev-list --reverse for this window; unresolved rows
    # keep their recorded relative position between the surrounding resolved commits.
    n = len(rows)
    keys = [None] * n
    for i, row in enumerate(rows):
        if row["prefix"] in resolved:
            keys[i] = float(resolved[row["prefix"]]["ct"] or 0)
    i = 0
    while i < n:
        if keys[i] is not None:
            i += 1
            continue
        j = i
        while j < n and keys[j] is None:
            j += 1
        prev = keys[i - 1] if i > 0 else (keys[j] - 1000 if j < n else 0.0)
        nxt = keys[j] if j < n else prev + 1000
        for k in range(j - i):
            keys[i + k] = prev + (nxt - prev) * (k + 1) / (j - i + 1)
        i = j
    order = sorted(range(n), key=lambda k: (keys[k], k))

    new_entries, prefix_only = [], []
    for idx in order:
        row = rows[idx]
        hit = resolved.get(row["prefix"])
        sha = hit["sha"] if hit else row["prefix"]
        if not hit:
            prefix_only.append((sha, row["file"]))
        evidence = ["%s: %s" % (row["file"], re.sub(r"\s+", " ", row["disposition"]).strip())]
        if not hit:
            evidence.append(
                "Sha recorded as the 10-hex prefix `%s` in %s. The commit object is absent from the local Rust mirror and from upstream "
                "openai/codex (the same subject is upstream as a rebased commit), so the full 40-hex digest could not be resolved; "
                "backfill it from the authoritative mirror before certification." % (row["prefix"], row["file"])
            )
        subject = row["subject"] or (hit or {}).get("subject") or row["prefix"]
        new_entries.append({
            "sha": sha,
            "subject": subject,
            "kind": kind_for(subject),
            "domains": ["codex_go"],
            "status": status_for(row["disposition"]),
            "evidence": evidence,
        })

    # #57267 re-audit: blocked_precondition -> partial (P0/P1 semantics landed; residuals per cloud_thread_plan.md §7).
    for c in ledger["commits"]:
        if c["sha"].startswith("654b2ec1f5"):
            c["status"] = "partial"
            c["evidence"] = [
                "Partial: the ThreadParamsMode/ThreadSettingsOwner model and the server-ownership points that depend on it are ported. "
                "app/cloud_thread_mode.go mirrors app_server_session.rs:361-402, :692 and :1195-1197 (mode -> settings owner, remote workspace, "
                "fork bail text); app/remote_tui.go builds a server-owned thread/start (remoteThreadStartParamsForMode, :6080) and turn/start "
                "(remoteTurnStartParamsForMode, :6237) that omit cwd/roots/permissions/model/effort/summary/service tier, skips the dynamic tool MCP "
                "host (remoteTUITaskToolsHostForSession, :423 and startThread, :3898), tolerates configRequirements/read -32601 only in cloud mode "
                "(:1257), rejects forks (:1810, :6136) and gates recap/thread-title generation on ownership (:611-612). Evidence: "
                "app/cloud_thread_mode.go, app/remote_tui.go; app/cloud_thread_mode_like_rust_test.go (TestCloudThreadParamsModeLikeRust, "
                "TestCloudThreadStartParamsServerOwnedLikeRust, TestCloudThreadStartSkipsClientToolTransportLikeRust, TestCloudThreadForkRejectedLikeRust), "
                "app/cloud_turn_params_like_rust_test.go (TestCloudTurnStartOmitsClientSettingsLikeRust), app/cloud_turn_permissions_like_rust_test.go "
                "(TestCloudTurnStartOmitsPermissionsLikeRust), app/cloud_requirements_tolerance_like_rust_test.go "
                "(TestCloudConfigRequirementsMethodNotFoundToleratedLikeRust), app/cloud_local_generation_like_rust_test.go "
                "(TestCloudSkipsLocalRecapGenerationLikeRust), app/cloud_task_tools_like_rust_test.go (TestCloudSkipsLocalTaskToolsHostLikeRust).",
                "Residuals (cloud_thread_plan.md §7, still open): the hosted transport now exists (app/remote_tui_cloud.go, RemoteEndpointCloud, "
                "TestResolveInteractiveRemoteAddressSelectsCloudLikeRust) but the per-turn client still takes its mode from "
                "threadParamsModeForLaunch, which can only return Embedded or Remote, so a Cloud endpoint keeps client-owned settings in the "
                "production path (threadParamsModeForEndpoint has no caller); server-side model/permission writes, the server-selected "
                "permission-discovery text, workspace/agents-overview cwd handling and an end-to-end cloud conversation test are not implemented. "
                "Nothing here is architecture-blocked; the remaining work is a wiring lane, so the entry is partial rather than blocked_precondition.",
            ]
            break

    ledger["rustFromExclusive"] = FROM_EXCL
    ledger["rustToInclusive"] = resolved[TO_INCL_PREFIX]["sha"] if TO_INCL_PREFIX in resolved else TO_INCL_PREFIX
    ledger["commits"].extend(new_entries)

    text = json.dumps(ledger, indent=1, ensure_ascii=False).replace("\n", "\r\n") + "\r\n"
    open(ledger_path, "wb").write(text.encode("utf-8"))

    counts = collections.Counter(c["status"] for c in ledger["commits"])
    print(json.dumps({
        "appended": len(new_entries),
        "total": len(ledger["commits"]),
        "rustToInclusive": ledger["rustToInclusive"],
        "statusCounts": dict(sorted(counts.items())),
        "prefixOnly": [p for p, _ in prefix_only],
        "unresolvedNoRecord": unresolved,
        "kindCounts": dict(sorted(collections.Counter(c["kind"] for c in new_entries).items())),
    }, indent=1))


if __name__ == "__main__":
    main()
