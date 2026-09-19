# Upstream sync statistics - 2026-09-19

Generated for the work session spanning 2026-09-19 (00:07 pull through the
`sync53` commit). Two data sets:

- **Upstream Rust** checkout `D:\qax\reagent\dev\git\codex` (origin
  `github.com/openai/codex`), measured across every `pull` of the session.
- **This repo (Go)** measured from the session baseline `de0421d8`
  (2026-09-14, the last commit before the session) to `HEAD` (`36f42a54`,
  `sync53`).

## 1. Scope and method

- Upstream baseline is `5b1d656018` (pulled 2026-09-14, the last pull before
  this session); upstream head is `78245b47af` (pulled 2026-09-19 13:03).
- Go baseline is `de0421d8` (2026-09-14 22:41), the last commit before the
  session; Go head is `36f42a54` (`sync53`).
- **There was no repository activity on 2026-09-18 in either checkout** (no
  upstream pull, no Go commit). All work is dated 2026-09-19.
- "Net diff" only counts the endpoints; "per-commit churn" additionally counts
  lines rewritten inside the range. Both are reported where they differ.
- Timestamps are local (+0800); upstream commit dates shown by `git log` are
  +0000 and therefore appear one day earlier for evening commits.

Reproduction:

```powershell
# upstream
cd D:\qax\reagent\dev\git\codex
git reflog --date=iso -12
git rev-list --count 5b1d656018..78245b47af
git diff --shortstat 5b1d656018 78245b47af

# this repo
cd D:\qax\reagent\dev\codex_go
git rev-list --count de0421d8..HEAD
git diff --shortstat de0421d8 HEAD
```

## 2. Upstream pull events

Nine fast-forward pulls, all on 2026-09-19:

| Local time | Resulting HEAD | Commits pulled |
|---|---|---|
| 00:07 | `7498521d28` | 239 |
| 06:41 | `dbf478850f` | 2 |
| 07:10 | `f5b941c910` | 11 |
| 07:28 | `36430b3688` | 7 |
| 07:37 | `dfb265764d` | 1 |
| 08:31 | `ab59b78287` | 20 |
| 08:33 | `22dea11020` | 1 |
| 09:24 | `a633ebc124` | 13 |
| 13:03 | `78245b47af` | 25 |

## 3. Upstream change volume

Range `5b1d656018..78245b47af` (net diff):

| Metric | Value |
|---|---|
| Commits | 319 |
| Files changed | 1,985 |
| Insertions / deletions | **+130,988 / -28,926** |
| `codex-rs/` only | 1,965 files, +129,945 / -28,840 |
| Non-Rust (`sdk`, `scripts`, `.github`, bazel locks) | ~20 files, +1,043 / -86 |

Per-pull segments (chronological, matching the table above):

| Segment | Commits | Files | Insertions | Deletions |
|---|---|---|---|---|
| `5b1d6560` -> `7498521d` | 239 | 1,614 | 101,925 | 19,341 |
| `7498521d` -> `dbf47885` | 2 | 43 | 1,123 | 62 |
| `dbf47885` -> `f5b941c9` | 11 | 97 | 11,237 | 7,447 |
| `f5b941c9` -> `36430b36` | 7 | 134 | 2,419 | 287 |
| `36430b36` -> `dfb26576` | 1 | 6 | 165 | 32 |
| `dfb26576` -> `ab59b782` | 20 | 150 | 3,025 | 695 |
| `ab59b782` -> `22dea110` | 1 | 3 | 64 | 4 |
| `22dea110` -> `a633ebc1` | 13 | 183 | 4,316 | 1,073 |
| `a633ebc1` -> `78245b47` | 25 | 214 | 7,935 | 1,206 |

The 00:07 pull alone (the batch the Go side triaged as **sync39**) is 239
commits and 101,925 insertions, i.e. 78% of the session's upstream volume.

### By file type

| Type | Files | Insertions | Deletions |
|---|---|---|---|
| `.rs` Rust source | 1,517 | 116,367 | 27,910 |
| `.snap` insta snapshots | 307 | 9,294 | 207 |
| `.json` | 34 | 2,660 | 302 |
| `.py` / `.yml` / `.md` / `.toml` / `.ts` / `.ps1` / `.lock` | ~105 | ~2,300 | ~500 |

### By crate (`codex-rs/`, top 25 by insertions)

| Crate | Files | Insertions | Deletions |
|---|---|---|---|
| `tui/src` | 607 | 36,265 | 4,127 |
| `core/src` | 259 | 12,583 | 5,992 |
| `core/tests` | 110 | 11,568 | 796 |
| `analytics` | 24 | 8,013 | 6,473 |
| `ext` | 107 | 4,986 | 2,821 |
| `windows-sandbox-rs` | 68 | 4,259 | 831 |
| `app-server/tests` | 53 | 3,976 | 493 |
| `login` | 32 | 3,595 | 687 |
| `exec-server` | 38 | 3,237 | 504 |
| `app-server-protocol` | 79 | 3,161 | 372 |
| `app-server-daemon` | 26 | 3,015 | 260 |
| `core-plugins` | 27 | 2,947 | 927 |
| `windows-sandbox-service` | 28 | 2,920 | 439 |
| `mermaid` | 19 | 2,267 | 0 |
| `cli` | 25 | 2,238 | 68 |
| `linux-sandbox` | 17 | 1,817 | 341 |
| `app-server/src` | 39 | 1,801 | 482 |
| `model-provider` | 17 | 1,745 | 85 |
| `backend-client` | 17 | 1,661 | 25 |
| `prompts` | 15 | 1,513 | 661 |
| `http-client` | 17 | 1,423 | 229 |
| `codex-mcp` | 20 | 1,338 | 250 |
| `network-proxy` | 27 | 1,326 | 175 |
| `config` | 27 | 1,200 | 151 |
| `protocol` | 27 | 1,113 | 317 |

New crates/directories introduced in the range: `mermaid`, `tcp-tunnel`,
`analytics`, `windows-sandbox-service`, `app-server-daemon`, `network-proxy`.

### Upstream commits authored on 2026-09-18 (UTC)

If "yesterday" is read as the upstream author date, the subset is 34 commits,
302 files, **+11,186 / -2,503**, ending exactly at `7498521d28` (the Go side's
sync39 baseline). Hottest crates: `login` (+3,148), `core/src` (+1,821),
`core/tests` (+1,751), `ext` (+604), `app-server` (+1,011). Representative
subjects:

- `a129392ebb` Add OAuth credential management for model provider gateways
- `8f73cdee45` Centralize OAuth login and refresh handling
- `7498521d28` Keep MCP policy evaluation consistent with turn environments
- `a1efb59c4a` Record active plugin inventory in turn analytics
- `93321c88d8` Preserve selected reasoning effort for synchronous Guardian reviews
- `0a5b999169` Connect app-server workspace discovery to model request routing

## 4. Go side (this repo)

Range `de0421d8..36f42a54` (net diff): **95 commits**, 317 files,
**+30,223 / -1,934**. Per-commit churn sums to +30,425 / -2,464 (the
difference is lines rewritten inside the range).

### By kind

| Kind | Files | Insertions | Deletions |
|---|---|---|---|
| Go production | 167 | 14,069 | 1,274 |
| Go tests | 132 | 12,344 | 611 |
| Docs (plan/notes) | 5 | 3,427 | 3 |
| Other (`go.mod`/`go.sum` and assets) | 10 | 372 | 39 |
| JSON | 3 | 11 | 7 |

Tests are 47% of the Go insertions - parity work lands with its regression
tests.

### By module (net diff)

| Module | Files | Insertions | Deletions |
|---|---|---|---|
| `appserver` | 49 | 5,560 | 392 |
| `tui` | 45 | 2,686 | 471 |
| `model` | 25 | 2,636 | 60 |
| `auth` | 14 | 2,428 | 25 |
| `tcptunnel` | 6 | 1,658 | 0 |
| `shell` | 4 | 1,489 | 23 |
| `config` | 19 | 1,056 | 196 |
| `rollout` | 9 | 1,048 | 15 |
| `state` | 8 | 929 | 67 |
| `mcp` | 15 | 874 | 63 |
| `tool` | 9 | 663 | 132 |
| `network` | 6 | 646 | 4 |
| `plugin` | 11 | 609 | 44 |
| `turn` | 14 | 559 | 66 |
| `app` | 8 | 461 | 32 |
| `doctor` | 4 | 427 | 0 |
| `safety` | 3 | 402 | 22 |
| `exec` | 4 | 346 | 46 |
| `sandbox` | 2 | 298 | 3 |
| `parity` | 13 | 290 | 57 |
| `agent` | 3 | 279 | 0 |
| `context` | 4 | 230 | 24 |
| `jsonschema` | 2 | 212 | 0 |
| `execserver` | 6 | 185 | 84 |
| `chatgptapi` | 2 | 174 | 0 |
| `metrics` | 2 | 144 | 0 |
| `telemetry`, `apps`, `codemode`, `cli`, `cmd`, `features`, `utils`, `protocol`, `reasoningoverride`, `otelinit`, `session`, `compact`, `codexapi` | 25 | ~1,000 | ~130 |
| `update` (plan doc, 1 file) | 1 | 3,270 | 0 |

## 5. Two-sided comparison

| | Upstream Rust | Go |
|---|---|---|
| Commits | 319 | 95 |
| Insertions | 130,988 | 30,223 |
| Files | 1,985 | 317 |

Roughly a 1:4.3 line ratio, consistent with the Go port only implementing the
Go-visible deltas: most of the Rust range is crate extraction and refactoring
(Guardian crate split, Windows sandbox service, analytics dashboards) that has
no Go counterpart.

## 6. Alignment status

All 319 upstream commits are triaged in `update/plan_2026_09_19.md` across
sync39-sync53. The upstream pin is current: the plan's static parity layer was
re-pinned to `78245b47` during sync49, and every pull in the range has a
disposition (landed / N/A / queued).

### Still open (Go-visible work not yet ported)

Guardian/instructions:

- #46580 Guardian reviews on the applied instruction snapshot - needs Guardian
  instruction inheritance first.
- #46577 shared instruction-provider updates reaching subagents - Go has no
  `ThreadInstructionsProvider` extension API.
- #46556 step settings and approval environments consistency - Go's
  `get_unreadable_roots_with_context` counterpart does not exist.

Windows sandbox / platform:

- #46575 Windows package identity for sandboxed descendants - needs the
  extended-startup-info attribute set (job list + desktop-app policy) and a
  `GetPackageFullName` lookup; not verifiable on an unpackaged host.
- #46571 remaining Seatbelt piece: daemon-socket protection - Go has no
  shared-daemon-socket directory. (Scratch exclusions and protected-path rename
  guards landed.)
- #46568 daemon-recovery half of captured environment state
  (`session/daemon_recovery.rs`); the permission half landed.

Plugin / MCP / executor:

- #46572 turn-start cloud plugin discovery in the MCP extension - needs a cloud
  `PluginCatalog` listing provider.
- #46567 remainder: Rust's `plugin` -> `core-plugins` module move (structural).
- #46564 plugin/app extension renames (no Go extension-registry surface).
- #46555 executor registrations across connection refresh/recovery - Go has no
  lazy refresh client.

Daemon lane:

- `--no-daemon`, the `/daemon` menu, and the `daemon_auto_start` behaviour
  (the registry entry exists; no consumer yet).

TUI lane:

- #46566 recovery commands while the current thread is unavailable - Go's TUI
  has no unavailable-thread restricted-input mode.

Workspace routing (the current lane, sync52/sync53):

- Wiring the discovered routing into the `exec` CLI provider path (in progress
  in the working tree).
- `notify_workspace_routing_to_connection` account-update notification.

## 7. Uncommitted working tree at report time

sync53 continuation (workspace routing for the `exec` provider path):

| File | Insertions | Deletions |
|---|---|---|
| `appserver/workspace_routing.go` | 22 | 142 |
| `appserver/workspace_routing_test.go` | 5 | 5 |
| `exec/exec.go` | 54 | 0 |
| `model/workspace_routing.go` | 138 | 0 |
| `model/workspace_routing_test.go` | 61 | 0 |
| `exec/workspace_routing_test.go` (new, untracked) | 79 | - |

## 8. Observations

- The session's upstream intake is dominated by one pull: the 00:07
  fast-forward to `7498521d288b` (239 commits, 101,925 insertions).
- Rust-side hotspots are `tui/src` (+36k) and `analytics` (+8.0k with 6.5k
  deletions, i.e. a rewrite), while the Go side's hotspots are `appserver` and
  `auth`/`tui`/`model`.
- The Go range is test-heavy (47% of insertions), matching the repo's
  differential-parity workflow.
- No upstream commits remain untriaged at the current pin; the open items are
  documented architecture gaps rather than missing analysis.
