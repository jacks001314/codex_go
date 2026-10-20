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
- `#40679` Amazon Bedrock onboarding.
  - The auth picker exposes `Set up Amazon Bedrock` behind the
    `bedrock_setup_wizard` feature and the API-login policy.
  - Discovery lists AWS profiles and environment credentials; the wizard
    supports profile selection, manual profile entry, access keys, environment
    instructions, Bedrock API keys, and explicit region entry.
  - Profile/environment setup uses `account/bedrock/setup`; managed API keys and
    access keys use `account/login/start`, including provider configuration and
    credential persistence.
  - Successful setup runs `account/bedrock/checkGovCloudRequirements`; GovCloud
    accounts see the acknowledgment screen before onboarding completes.
- `#52176` GovCloud startup guidance.
  - Startup checks the resolved provider (local Bedrock or connected server),
    shows the blocking guidance, persists `notice.hide_gov_cloud_guidance` on
    acknowledgment, and exits without saving when cancelled.
- `#46837` command-center rows and paging completed.
  - Added title/status/updated columns, relative update times, `Error`/`Inactive`
    wording, and viewport-based PageUp/PageDown.
- `#47377` realtime V3 reasoning status.
  - `backendReasoningStatus` round-trips through start params, completed public
    reasoning summaries are bounded to 256 bytes, and active server-managed V3
    delegations receive `[STATUS] ...` on the commentary channel.
- `#46882` command-center help and footer.
  - Added the grouped Task shortcuts reference, `?` help toggle, compact footer,
    Backspace delete default, and editing-safe Backspace handling.
- `#45799` Windows sandbox profile cleanup.
  - Sandbox profiles are deleted before accounts, accounts are preserved when
    profile removal fails, and cleanup reports each outcome.
- `#44243` originating model history metadata.
  - Response session items now carry the model of the sampling step that
    produced them; a two-step regression test proves the turn-start model is not
    reused for later responses.
- `#46731` dynamic tool activity and transcript ordering.
  - Added `DynamicToolCallCell`, retained rows by call id, and update-in-place
    completion handling so concurrent calls keep their original transcript
    position.

## Ledger result

The TUI `audit_pending` set is now empty. Current r109 distribution:

| verdict | count |
| --- | ---: |
| complete | 1391 |
| not_applicable | 1308 |
| audit_pending | 0 |
| partial | 0 |
| blocked_precondition | 0 |
| implementation_pending | 0 |

All `audit_pending` and `implementation_pending` rows are closed.

Remaining partials:

- `#46731` dynamic tool activity and persisted history ordering.
- `#44243` per-step originating model metadata during history recording.

Remaining blocked preconditions:

`#49106` and `#42428` were rechecked against Go's architecture:

- `#49106`: Go already pages `thread/loaded/list` up to 1000 sessions and seeds
  recent sessions before rendering, so there is no Rust-style 10-row cap,
  per-source cursor, or deferred `Show more` discovery state to reproduce.
- `#42428`: the Rust shared command-center composer was removed by later
  upstream restructuring; Go's command center intentionally manages tasks and
  starts/resumes sessions without composing turns.

The ledger now has no pending, partial, blocked, or implementation rows.

Architecture-specific TUI rows marked `not_applicable` include the account
analytics dashboard lane (`#45740`-`#45769`, `#46863`, `#46866`), Rust's
app-owned transcript selection/layout cache (`#46719`, `#46720`, `#46732`,
`#46739`, `#53693`, `#50434`, `#47896`, `#48548`, `#46857`), in-process
reconnect state (`#41911`, `#41916`), terminal mouse capture (`#47399`),
background rollout compression metrics (`#44138`, `#46047`), the removed zsh
backend (`#52160`), and terminal-scrollback table reflow (`#50207`).

## Verification

```text
go test ./... -count=1
go vet ./...
go build ./...
git diff --check -- <touched files>
```

`go vet ./...` and `go build ./...` pass. The full `go test ./...` run passed
every package except one transient Windows keyring round-trip failure; rerunning
`go test ./keyring -count=1` passed. `go test ./app -count=1` also passes.
