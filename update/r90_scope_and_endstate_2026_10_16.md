# R90 scope arithmetic and the proposed end state (2026-10-16)

Owner: `muiltcode-lead`. This file exists because the team goal's completion criterion
("完成和上游 rust 版本 codex cli 的最新功能同步") is not checkable as written, and because the
remaining work is now measurable. It records the arithmetic, the throughput actually observed, a
checkable end state, and the honest conclusion that follows.

## 1. The upstream target has moved

- Parity pin / authoritative LF checkout: `C:\rw\codex-rs` at **`4eb4a65407`** (#54590).
- Upstream `origin/main` is now **`4491caf1ad`** - **4 commits ahead** of the pin:

| sha | PR | size (files) | subject | Go relevance |
| --- | --- | --- | --- | --- |
| `4491caf1ad` | #54639 | 2 | Stream rollout records during thread history materialization | **direct follow-up to the just-landed #53947** (`rollout/**`, `state/thread_history_projection.go`) |
| `34420d4264` | #54637 | 2 | Preserve disconnect reasons in Noise handshake errors | exec-server surface |
| `b5115cdd66` | #54623 | 9 | Resolve stable releases dynamically for exec-server compatibility tests | mostly CI (`.github/**`), likely N-A for Go |
| `2bf2bdf82c` | #54619 | 31 | Bind plugin hook approval to plugin contents | real feature, mostly `codex-rs/hooks` |

These four are appended to `update/r90_lane_batches_2026_10_16.tsv` as batch `b15`
(2 -> B, 2 -> B, 9 -> V adjudication, 31 -> A), so the accounted universe is now **342 rows**.
Explicit caveat: **the local `C:\rw\codex-rs` checkout is at the pin, not at the new tip** - the four
commits above were read from `origin/main` metadata only, and `parity/` still has to be run against the
LF pin. Moving the pin is a separate, batched operation (see section 4).

## 2. Where the work actually stands

| metric | value |
| --- | --- |
| upstream PRs in the accounted universe | **342** |
| ledgered (has a verdict + evidence) | **78** in-batch (+1 out-of-batch) = 79 rows |
| **undecided** | **264** |
| of the undecided: `impl` kind | **264 / 264 (100%)** |
| of the undecided: summed upstream changed-file count | **2931** |

Ledgered verdicts so far: 38 `not_applicable`, 16 `complete`, 15 `landed`, 6 `partial`,
2 `needs_go_test`, 1 `AlreadyImplemented`, 1 `blocked_precondition`.

**The 264 remaining items are all real implementation work - there is no test-only or build-only
remainder left to discharge cheaply.** At the observed batch rate (lane batches in this round landed 4
PRs and adjudicated ~18 test-only items over several turns; the current round has 6 lanes each carrying
2-4 items per batch), the honest projection is on the order of tens of turns for the implementation
backlog alone. **The remaining turn budget cannot close 264 implementation items.**

## 3. Proposed checkable end state (E)

Because "synchronised" is not observable, the team should be driven to a state a verifier can check
mechanically:

- **E1 - accounted.** Every one of the 342 universe PRs carries a ledger verdict with positive evidence:
  `landed` (with the commit + a leader-re-run gate), `not_applicable` (with >= 5 upstream identifiers at
  zero Go hits plus a structural argument), `partial` (with an explicit list of what is and is not
  ported), or `backlog` (with the reason it is not portable now, e.g. requires the Linux node or is a
  brand-new subsystem).
- **E2 - green except named reds.** `go build ./...`, `go vet` (3-item whitelist), and `go test ./...`
  produce only the frozen R5-bis red list, re-verified with `-count=3` to separate genuine reds from the
  three known load flakes.
- **E3 - pinned and reproducible.** `parity/` is green against a *named* upstream commit with the pin
  recorded in the ledger, and moving the pin is a documented, repeatable procedure.
- **E4 - no silent gaps.** Anything not ported appears in an explicit backlog list with a reason; nothing
  is quietly dropped, and no member's self-report stands as the only evidence.

E1 is the load-bearing one: it converts an unbounded "implement everything" into a finite, auditable
question, and it is the only form of "synchronised" that a verifier can actually check.

## 4. Pin policy (proposed, needs the user's nod)

The upstream tip moves continuously; re-pinning to it every turn is a treadmill and re-anchors fixtures
each time (the `95ec468619` -> `4eb4a65407` move was a full-surface job). Proposed policy:

1. The pin stays at a named commit until the accumulated increment is worth a re-anchor.
2. New upstream commits are **immediately appended to the accounted universe** (done for #54619/#54623/
   #54637/#54639 above), so drift is always visible and never silently ignored.
3. A re-pin is scheduled when the increment touches a surface the pin guards (notably `features/`,
   `app-server-protocol/schema`, or the TUI snapshot corpus). None of the current four do, so
   **`4eb4a65407` stays as the pin for now** and the four commits are implemented/adjudicated against it.

## 5. What this turn did about it

- Appended the four new commits to the batch universe with lane assignments (section 1).
- #54639 is queued to lane B because it is a follow-up to code lane B wrote and re-verified this round
  (`rollout/**`, `state/thread_history_projection.go`) - same files, same author lane, no cross-lane risk.
- #54637 is queued to lane B (exec-server owner); #54623 is queued to lane V as a cheap adjudication
  (mostly CI); #54619 is queued to lane A (`hooks/**`), behind its current context-provenance recon.

## 6. The decision the user owes us

Three rounds of questions are unanswered. To close the objective I need exactly one answer:

- **(a)** accept end state E in section 3 as the completion criterion (then E1-E4 is the finish line and
  the remaining work is accountable triage plus implementation to whatever depth the budget allows), or
- **(b)** name the specific feature set that must be ported, or
- **(c)** extend the round/turn budget so the 264 implementation items can actually be worked.

Without one of these, the objective as literally written cannot be reported complete, and it should not
be reported complete on the strength of "we landed 5 more syncs this round".