# round 90 - commit-ledger audit and repair (2026-10-17, lead)

Scope: `update/r90_commit_ledger_2026_10_16.tsv` (the decision ledger the team goal is
measured against) cross-checked against `update/raw_commits_round90_2026_10_16.tsv`
(the managed commit set of this round). Read-only analysis plus one mechanical repair of
the sha column; no product code touched.

## 1. Managed set - corrected count

`raw_commits_round90_2026_10_16.tsv` **has no header row**: its first line is already a
commit (`4eb4a65407 2026-10-16 12:37:14 54590 NO 2 codex-rs/core(2) Capture sender
context for codex_app.create_thread deliveries (#54590)`). Any accounting that skips
line 1 as a header loses one item - the turn-14 report inherited exactly that error when
it quoted 341 managed commits.

Measured facts:

- raw lines = 338, all with 7 tab-separated columns, 338 unique shas, 338 unique PRs
- therefore **managed commits = 338** (the pin tip `4eb4a65407` / #54590 is one of them)

## 2. Findings on the ledger (before repair)

| id | finding | evidence |
| --- | --- | --- |
| F1 | the sha column is not joinable to the managed set: 44 of 97 rows carried a sha that is not the managed sha for their PR | 54069 `938ddfb66c` vs managed `0fd85b0752`; 54169 `ed2607e7` vs `7943713993`; ... 44 rows in total |
| F2 | 7 sha strings were **reused across several rows**, which silently destroys per-commit accounting | `d8ab87bc` x2, `ed2607e7` x2, `c289c53d` x5, `d6827da4` x3, `915dbfa2` x4, `820adc8f` x2, `83a61c6e` x2 |
| F3 | mixed conventions: the managed file uses 10-char shas, the ledger mixed 8-char Go commit shas, 10-char upstream shas and occasional 40-char shas | sha length histogram before repair = {40, 10, 8} |
| F4 | 5 ledger rows decide items **outside** the managed set - legitimate, but they must not be counted as progress inside it | PRs 27198, 54619, 54639, 54637, 54623 |
| F5 | the status vocabulary has no in-flight state, so dispatched-but-unfinished batches are representable nowhere in the ledger; they live in the batch plan and the blackboard | statuses: complete 15, landed 27, not_applicable 44, partial 7, blocked_precondition 3, AlreadyImplemented 1 |

Because of F1/F2 a sha join under-counted progress (it reported 285 undecided instead of
246). The PR column was always clean: 97 rows, 97 unique PRs, no duplicates.

## 3. Repair applied

Mechanical, PR-keyed normalisation of the sha column only:

1. every row whose PR is in the managed set gets the managed 10-char sha for that PR (fixes F1/F2/F3);
2. every row outside the managed set is truncated to the same 10-char convention (F4 stays keyed by PR);
3. no status, kind, subject or evidence text was modified.

## 4. Post-repair invariants (recomputed from the repaired file)

```
ledger rows total          : 97
managed commits (raw rows) : 338
sha strings used           : {10: 97}          # single convention
duplicate sha              : []                # was 7 reused strings
duplicate pr               : []
sha == managed sha for all in-set rows : True
in-set decided             : 92
set-external decided       : 5   (27198, 54619, 54639, 54637, 54623)
REMAINING managed          : 246
statuses : not_applicable 44, landed 27, complete 15, partial 7, blocked_precondition 3, AlreadyImplemented 1
kinds    : impl 62, test 19, build 16
```

**Corrected progress statement (supersedes the numbers in the turn-14 report):** of the
338 managed commits, **92 are decided inside the set** and 5 further decisions are
set-external, so **246 managed commits remain undecided**.

## 5. Consequences for the goal

- The binding constraint is lane throughput, not tooling. Measured rate this round is
  about 12 decisions per turn; 246 remaining at that rate needs ~20 more turns while the
  run has ~15 left. Closing the literal criterion therefore needs either a scope decision
  (which feature set must be ported) or more parallel decision throughput; this has been
  reported as blocked-on-user for several rounds.
- The repair makes the number trustworthy: any later "N decided / M remaining" claim can
  be recomputed by joining the ledger PR column to `raw_commits_round90_2026_10_16.tsv`
  and re-checking the invariant block in section 4.

## 6. Residual risks

- The 5 set-external rows stay by design (older rounds plus the four post-pin commits) and
  must not be counted as managed progress.
- The ledger still cannot express "in flight"; until that is fixed, per-turn progress must
  be read together with the batch plan and the blackboard record `tasks/round90-state`.