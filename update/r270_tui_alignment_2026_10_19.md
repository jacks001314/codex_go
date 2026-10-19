# Round 270: TUI alignment closeout and ledger audit

Baseline: Rust mirror under `D:\qax\reagent\dev\git\codex`; Go worktree kept
dirty without intermediate commits or pushes, as requested.

## Implemented in this pass

- `#51230` stable session lookup pagination.
  - `CreatedAt` ordering plus `timestamp|threadID` cursors and state-DB-only
    lookup.
  - List errors propagate; resume/fork discovery walks every page and retains
    already loaded rows when a later page fails.
- `#43298` managed-worktree creation moved out of the Bubble Tea `Update` loop.
  - `startManagedWorktree` returns a command; the thread switch happens after
    `managedWorktreeStartMsg` returns to a fresh update cycle.
- `#46838` command-center key-chord hints.
  - Pending chord hints replace the normal footer/notice and render as one
    clipped line, matching Rust's `hint_line`.
- `#43934` voice lifecycle metrics.
  - `codex.voice.session.start`, `.connected`, `.failure`, `.ended`, and
    `.duration`.
  - `connected` is recorded only on the starting -> active transition.
  - Failure metrics carry `cause` and `phase`; duration carries
    `outcome=success|failure`. Duration is consumed once per session.
- `#46839` command-center status filter tabs.
  - `All`, `Needs you`, `Working`, `Ready`, `Inactive` counted tabs.
  - `Tab`/`Shift+Tab` cycle tabs unless the key is bound to a list/agents
    action; metadata editing owns the key.
  - Status filtering combines with search, hide, and pinning.
  - The row being renamed stays visible until the rename ends.
- `#46837` command-center row/paging subset.
  - `systemError` renders as `Error`; unloaded rows render `Inactive`.
  - PageUp/PageDown move by the rendered viewport and clamp at both ends.
  - Column/relative-time layout remains unported, so the ledger verdict is
    `partial`.
- `#46116` image activity summary subset.
  - Viewed images render as one compact `• Viewed image <filename>` line.
  - Raw and expanded transcript lines retain the original path.
- `#47911` composer prompt suggestions foundation.
  - `tui.prompt_suggestions` defaults to false and is accepted by the strict
    config loader.
  - Strict `{"suggestion":"..."}` parsing rejects unknown fields, trailing
    JSON, non-strings, control/paragraph separators, and text over 240 runes.
  - Suggestions stay outside the draft, render dimly only in an empty, focused,
    idle composer, and yield to popups, attachments, paste bursts, running
    turns, read-only/review states, and interactive footer surfaces.
  - Tab inserts the suggestion without submitting; Escape cancels; duplicate
    Tab input is consumed; explicit editor/Vim/composer Tab remaps take
    precedence; stale results are ignored.
- `#49715` account security setup reminders.
  - The local ChatGPT reader validates saved account/user identity before and
    after the `/wham/security-setup` read, skips FedRAMP accounts, and applies a
    three-second bound.
  - Notice copy and actions reject control characters, oversized fields, and
    non-`https://chatgpt.com` destinations.
  - The TUI renders the reminder as a dismissible inline banner, gives usage
    banners priority, opens its validated action through the app URL opener,
    and keeps dismissal across refreshes for the same identity.

## Ledger result

The TUI `audit_pending` set is now empty. Current r109 distribution:

| verdict | count |
| --- | ---: |
| complete | 1300 |
| not_applicable | 1287 |
| audit_pending | 91 |
| partial | 16 |
| blocked_precondition | 4 |
| implementation_pending | 1 |

All 91 remaining `audit_pending` rows are outside TUI: Guardian (37),
appserver (16), sandbox (13), core (6), execserver (5), mcp (5), realtime (3),
config (2), persistence (2), model (1), plugins (1).

Confirmed TUI gaps retained as `implementation_pending`:

- `#40679` Amazon Bedrock onboarding flow.

`#49106` and `#42428` were rechecked against Go's architecture:

- `#49106`: Go already pages `thread/loaded/list` up to 1000 sessions and seeds
  recent sessions before rendering, so there is no Rust-style 10-row cap,
  per-source cursor, or deferred `Show more` discovery state to reproduce.
- `#42428`: the Rust shared command-center composer was removed by later
  upstream restructuring; Go's command center intentionally manages tasks and
  starts/resumes sessions without composing turns.

Confirmed TUI partials:

- `#46837` row columns, relative update times, and group-heading paging layout.
- `#46731` dynamic tool activity and persisted ordering.
- `#47377` opt-in realtime V3 reasoning status.
- `#46882` command-center help/group rendering.
- `#50504` remaining specialized modal-stack ownership.
- `#40492` automatic post-turn title generation.
- `#43279` linked-worktree session discovery edges.
- `#43921` streaming reasoning summary surfaces.
- `#49112` / `#53753` Linux PRIMARY helper ownership integration coverage.

Architecture-specific TUI rows marked `not_applicable` include the account
analytics dashboard lane (`#45740`-`#45769`, `#46863`, `#46866`), Rust's
app-owned transcript selection/layout cache (`#46719`, `#46720`, `#46732`,
`#46739`, `#53693`, `#50434`, `#47896`, `#48548`, `#46857`), in-process
reconnect state (`#41911`, `#41916`), terminal mouse capture (`#47399`),
background rollout compression metrics (`#44138`, `#46047`), the removed zsh
backend (`#52160`), and terminal-scrollback table reflow (`#50207`).

## Verification

```text
go test ./tui/... -count=1
go test ./app -run 'Agents|Overview|Voice|Worktree|SessionPicker|NamedSession' -count=1
go test ./session ./config -count=1
go test ./appserver -count=1
go vet ./app ./session ./appserver ./tui/...
go build ./...
git diff --check -- <touched files>
```

All of the above passed. A full `go test ./app -count=1` still reports the
pre-existing Windows/environment failures: relative `.` path rejection, private
socket-directory checks under the temp profile, `TERM=dumb` interactive
confirmation, and cloud-task custom HTTPS-origin rejection. The targeted app
tests covering this pass pass.
