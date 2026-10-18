# TUI current-head alignment（2026-10-18）

## Baseline

- Rust mirror: `D:\qax\reagent\dev\git\codex` at `7b44ac1661433ae3a6f375fde873fa7123c935fe`
  (`#54949`). No fetch or pull was performed.
- Go worktree: `main` with existing unrelated app-server/model changes preserved.

## Landed

### #54742 prompt-prefix state with structured elements

Rust stores a leading `/` or `!` in composer prompt state. Go already rendered
that marker, but tracked text elements in body coordinates while validating and
submitting them in canonical coordinates. Mentions could therefore be dropped
whenever a slash prompt carried structured text.

- `model.tea` composer elements are now maintained against the editable body and
  translated to canonical prompt ranges only at submission, history, queue and
  backtrack boundaries.
- History entries retain their canonical text elements; recalling either a
  submitted message or the pre-history draft restores the marker and body-relative
  ranges.
- Queued follow-ups restore their elements when edited, and backtracking converts
  stored prompt ranges back onto the marker-free body.
- Vim Replace absorbs a typed `/` or `!` prefix without losing the overwritten
  grapheme; Backspace at the start restores that grapheme and removes the marker.

Regression coverage:

- `TestComposerSlashPromptKeepsTextElementOffsetsLikeRust`
- `TestComposerHistoryRecallRestoresTextElementsLikeRust`
- `TestEditQueuedMessagePreservesSlashPromptElementsLikeRust`
- `TestVimReplaceModeAbsorbsPromptPrefixLikeRust`

### #54949 daemon feature allowlist

Rust `daemon_startup::allowed_feature` accepts
`app_server_websocket_compression`, and also already accepted `transcript_v2`.
Go's local-daemon replay allowlist was missing both.

- `daemonAllowedFeatureKeys` now accepts both boolean settings while retaining
  the shared-server disable checks for the four server-owned features.
- `TestDaemonConfigExclusionOverridesLikeRust` covers enabled and disabled values
  for both keys.

### #49037 fullscreen Plan mode cycle hint

Rust keeps `Plan mode (shift+tab to cycle)` at the right edge of an idle,
editable fullscreen status line. Go rendered only the legacy mode text and had
no cycle hint.

- The status header now right-aligns the full collaboration-mode label when it
  fits and degrades to `Plan mode` on narrow terminals.
- Running turns, popups, interactive footer states, read-only input and disabled
  collaboration modes suppress the cycle hint as upstream does.
- `TruncateWithEllipsis` now treats SGR/OSC sequences as zero-width. Without
  this, a colored status prefix inflated the measured width and replaced the
  fitting hint with `Plan …`.

Regression coverage:

- `TestStatusHeaderShowsPlanModeCycleHintLikeRust`
- `TestStatusHeaderHidesPlanCycleHintWhenUnavailableLikeRust`
- `TestTruncateWithEllipsisCountsOnlyVisibleANSIColumns`

### #49089 follow-up directive labels

Rust renders `:codex-followup[label]{prompt=...}` as ordinary Markdown using
`label`, and copies the same visible label without exposing the private prompt.
Go previously rendered the entire directive literally.

- The assistant-directive parser now accepts the follow-up `[label]` form,
  including nested/escaped brackets and balanced code spans.
- `FollowupLabels` rewrites only complete, valid follow-up directives outside
  code, HTML, links, images and escaped markers.
- The rewrite is shared by completed Markdown rendering, assistant response
  normalization, whole-response copy and streaming previews.
- Unterminated follow-up directives are withheld from the disposable streaming
  preview until their label and attributes close.

Regression coverage:

- `TestParseAssistantDirectiveFollowupLabelsLikeRust`
- `TestFollowupLabelsPreserveLiteralAndMalformedSources`
- `TestFollowupLabelsDoNotConsumeNeighborsOrNestedDirectives`
- `TestFollowupDirectivesRenderAsOrdinaryMarkdownLikeRust`
- `TestFollowupDirectivesCopyAsLabelsLikeRust`
- `TestStreamingPreviewHidesUnfinishedFollowupDirective`
- `TestFollowupDirectiveLabelsRenderPTY` (deterministic Responses API + ConPTY)

### #49290 `/mcp login <name>` in the TUI

Rust lets an active thread start MCP OAuth from the composer, guards overlapping
starts, tracks attempts per server, and retains thread-scoped completion
notifications. Go previously answered `/mcp login <name>` with a usage error.

- `/mcp login <name>` now starts the host OAuth flow while `/mcp` and
  `/mcp verbose` retain their inventory behavior.
- Overlapping starts are rejected with Rust's message; stale completions from an
  older attempt are ignored without disturbing another server's outstanding
  login.
- Completions retain the originating thread and surface when that thread is
  active again.
- Local MCP services support additive OAuth completion listeners without
  replacing the existing app-server completion handler.
- The remote TUI keeps a dedicated app-server client open for the login attempt
  and filters the OAuth completion notification by server name and login id.

Regression coverage:

- `TestMCPLoginCommandStartsAndCompletesLikeRust`
- `TestMCPLoginCompletionIgnoresStaleAttemptsLikeRust`
- `TestMCPLoginResultWaitsForOriginThreadLikeRust`
- `TestMCPLoginCommandErrorsAndUsageLikeRust`
- `TestOAuthLoginCompletionBroadcastPreservesAppServerHandler`
- `TestRemoteMCPLoginMonitorWaitsForMatchingAttemptLikeRust`

### #47329 permission menu wording

The `permission_preset_description` surface from Rust's menu-copy
standardization was still missing, so Go pickers and inline validation used
different, longer descriptions.

- Built-in read-only, workspace, and full-access descriptions now come from one
  shared `PermissionPresetDescription` function.
- Permission pickers and inline profile discovery use the same copy.
- Concise disabled/custom-profile explanations no longer carry trailing
  periods, including the `/permissions` rejection message.

Regression coverage:

- `TestPermissionPresetDescriptionsMatchRustMenuWording`
- `TestInlinePermissionPresetDescriptionsShareMenuCopy`

### #50720 Windows Terminal mapped Shift+Enter

Bubble Tea's Windows coninput path delivers Windows Terminal's
`ESC[13;2u` mapping as individual key events; the Go TUI consumed the events
without inserting a newline.

- A 50 ms bounded state machine recognizes `Esc [ 1 3 ; 2 u` only when the
  launch is under Windows Terminal (`WT_SESSION`, `WT_PROFILE_ID`, or
  `TERM_PROGRAM=Windows_Terminal`).
- Completed sequences execute the configured `editor.insert_newline` action.
- Mismatching prefixes are replayed through normal key handling, and standalone
  Esc outside Windows Terminal is not delayed.
- Exact unknown-CSI fallback remains for Bubble Tea's ANSI-reader path.

Regression coverage:

- `TestWindowsMappedShiftEnterInsertsNewline`
- `TestWindowsMappedShiftEnterIgnoresOtherCSIAndModals`
- `TestWindowsMappedShiftEnterDoesNotDelayOtherTerminals`

### #52721 command-center session creation during shutdown

The command center previously prefixed the raw draining error with a generic
"Failed to start session" message.

- The exact legacy draining message is classified as a server-shutdown state.
- Users see a dedicated title, the one-minute shutdown wait explanation, and
  automatic-reconnect guidance for daemon/remote servers only.
- Retained managed-worktree cleanup guidance is preserved after the shutdown
  explanation, including the app layer's existing `Failed to start session:`
  prefix.

Regression coverage:

- `TestAgentsOverviewServerShutdownErrorLikeRust`
- `TestAgentsOverviewShutdownErrorPreservesRetainedWorktreeGuidance`

### #50199 restore `/status` account email after account updates

The remote TUI only captured account identity during bootstrap. Later
`account/updated` notifications therefore left `/status` stale and could not
restore the email because the notification carries only auth mode and plan.

- `account/updated` now replaces the account mode and plan immediately.
- ChatGPT auth modes start a dedicated `account/read` request with a 30-second
  timeout so the interactive read loop and terminal input never block.
- The asynchronous email result restores the current plan label in `/status`.
- Request IDs are invalidated by newer account updates and connection close, so
  a stale identity cannot overwrite a newer account.
- API-key, unsupported auth mode, external ChatGPT tokens, agent identity, and
  personal access token modes match Rust's display mapping.
- ChatGPT account capability is updated alongside the display, preserving the
  correct usage/status surface.

Regression coverage:

- `TestRemoteAccountStatusFromAuthModeLikeRust`
- `TestAccountUpdatedRefreshesEmailLikeRust`
- `TestAccountEmailRefreshOnlyAppliesCurrentRequestLikeRust`
- `TestAccountStatusUpdateReplacesAPIKeyModeLikeRust`
- `TestAccountDisplayWithEmailRestoresRustStatusIdentity`

### #42380 confirm safety-buffered retries

Go already had the retry confirmation route, but the buffering surface still
used older copy, skipped the no-retry dismiss view, left the modal open when
response text arrived, and did not reject stale buffering updates.

- The prompt now uses Rust's current "Giving this request a little extra
  thought" header and faster-model explanation.
- Both retry-capable and side-conversation/no-retry updates show the dismiss
  view with "Learn more"; the footer explains that waiting requires no action.
- Confirmation copy uses the model catalog's display name rather than the raw
  model id.
- Response deltas or final text dismiss the buffering/confirmation view and
  mark the attempt started, so later buffering notifications cannot reopen it.
- Turn completion, interruption, and failure clear safety-buffering state.
- Dismissing the prompt or confirmation keeps the turn running and waiting,
  matching Rust's "Dismiss and keep waiting" behavior.
- Safety-buffering modal headers, bodies, and footer notes wrap at narrow
  terminal widths.

Regression coverage:

- `TestSafetyBufferingPromptCopyAndNoRetryViewMatchRust`
- `TestSafetyBufferingNoRetryUpdateStillShowsDismissViewLikeRust`
- `TestSafetyBufferingIgnoresUpdatesAfterAgentMessageStartsLikeRust`
- `TestModelSafetyBufferingResponseStartDismissesAndBlocksStalePromptLikeRust`
- `TestModelSafetyBufferingKeepWaitingRetainsRunningStateLikeRust`
- `TestModelSafetyBufferingConfirmationWrapsNarrowLikeRust`
- `TestModelSafetyBufferingLearnMoreOpensHelpWithoutRetry`

### #47423 preserve history search during background draft recovery

The main Bubble Tea composer had input-history navigation but no Ctrl+R search.
This also meant turn-end question recovery could only directly replace the
composer, with no way to preserve an active search.

- Ctrl+R now opens the shared composer history-search session over submitted
  input history; Ctrl+R/Ctrl+S traverse older/newer unique matches.
- Printable input edits the query, Backspace removes one rune, Ctrl+U clears it,
  Enter accepts a match, and Esc/Ctrl+C restores the saved draft.
- Enter without a match keeps search open, matching Rust's no-match behavior.
- Background question-draft recovery updates the saved cancellation draft
  without changing the query, current preview, or no-match fallback.
- Canceling restores recovered text plus the original text elements,
  attachments, and mention bindings; accepting a history match discards the
  recovered text.
- A fresh composer reset ends search so a later cancel cannot resurrect a
  discarded draft.
- The active query and match hints render in the composer footer.

Regression coverage:

- `TestHistorySearchSavedDraftRecoveryKeepsPreviewFrozenLikeRust`
- `TestModelHistorySearchNavigationAcceptAndCancelLikeRust`
- `TestHistorySearchBackgroundRecoveryPreservesSearchLikeRust`

### #46493 allow `/review` during MCP startup

MCP startup was already excluded from the user-turn busy gate, but a live
`/review` submitted during foreground work cleared the composer before the
busy-state check and could also lose attachments.

- `/review` availability is now validated before the running-turn slash path
  consumes the draft.
- The review picker and inline review instructions remain available while MCP
  startup is the only active background work.
- A running or pending turn, active review mode, queued follow-up, or pending
  steer rejects `/review` with Rust's task-in-progress message.
- Rejected review commands retain the composer draft and attachments.
- Quick-dial review commands use the same foreground-work guard.
- `startReview` itself rejects stale dispatch if foreground work became active
  after the picker was opened.

Regression coverage:

- `TestReviewPickerOpensDuringMCPStartupLikeRust`
- `TestInlineReviewSubmitsDuringMCPStartupLikeRust`
- `TestReviewBlockedDuringForegroundWorkPreservesDraftLikeRust`
- `TestReviewBlockedByQueuedFollowUpPreservesDraftLikeRust`

### #41938 clarify resume guidance in exit summaries

The Go exit-summary model accepted only a preformatted resume line, so it could
not distinguish direct-ID and named-picker recovery.

- `AppExitInfo` now carries a structured `ResumableThread`.
- The direct resume command is printed on its own two-column-indented line.
- A non-empty thread name adds `Or run codex resume and select <name>.`
- Empty names fall back to the direct-ID guidance only.
- `FormatExitMessagesWithColor` highlights both resume commands and the thread
  name while the existing no-color wrapper retains plain output.

Regression coverage:

- `TestAppExitInfoFormatsResumeGuidanceLikeRust`
- `TestAppExitInfoFormatsDisconnectGuidance`

### #39873 cycle TUI permission modes with keybindings

The configurable keymap descriptors for previous/next permission mode existed,
but no runtime action consumed them and printable bindings were accepted.

- `chat.previous_permission_mode` and `chat.next_permission_mode` now cycle the
  enabled built-in permission modes with wraparound.
- Modes disabled by requirements or platform checks are skipped.
- Full access still opens the existing warning confirmation before applying.
- Shortcut selections remain session-only and do not write `config.toml`.
- Printable and shift-only printable bindings are rejected as reserved for
  composer text input.
- Project-level keymap entries for these actions remain sanitized by the config
  loader.
- The keymap setup picker lists both actions as unbound defaults.

Regression coverage:

- `TestPermissionShortcutBindingsRejectPrintableKeysLikeRust`
- `TestModelPermissionShortcutsCycleSessionOnlyLikeRust`
- `TestModelPermissionShortcutFullAccessStillRequiresConfirmationLikeRust`

### #44711 return to command center after deleting the current session

Deleting the active session still unconditionally quit the Go TUI. Shared
servers can keep the client alive and show the agents overview instead.

- Local-daemon and remote sessions now replace the delete confirmation's
  success action with "Yes, delete and return to command center".
- Successful deletion runs the existing session action and then opens the
  agents overview instead of issuing Bubble Tea quit.
- Embedded sessions retain the existing delete-and-exit behavior.
- Failed deletion continues to leave the current session and command path
  unchanged because the quit replacement is only used after a successful
  session action.

Regression coverage:

- `TestDeleteCurrentSessionReturnsToCommandCenterOnSharedServerLikeRust`

### #47954 render fullscreen startup tips in the transcript

Go already attached the startup tip to the session-header transcript cell, but
it rendered the resolved tip as plain text and re-wrapped it as one composer-like
row. Rust #47954 renders the tip through Markdown below the splash, with
width-aware wrapping and styled links.

- Startup tips are now rendered through the transcript Markdown renderer at the
  available transcript width.
- The rendered tip retains ANSI emphasis and OSC 8 web links.
- Narrow terminals wrap the tip beneath the header rather than leaking raw
  Markdown syntax or moving it back above the composer.
- The first-event getting-started instructions retain their existing transcript
  layout.

Regression coverage:

- `TestStartupTooltipLinesWrapLikeRust`
- `TestStartupTooltipLinesKeepLinksLikeRust`
- `TestModelStartupSessionHeaderTooltipHonorsShowTooltipsLikeRust`

### #47424 clear async question notifications when turns end

Rust collects pending async-question drafts at every live turn terminal status
before notifying the user. Go left both the questions and their pending
notification behind, so a turn completion could be suppressed by an obsolete
`AsyncQuestion` notification.

- Successful, interrupted, failed, and live protocol turn endings now consume
  pending async questions.
- Only a pending `AsyncQuestion` notification is cleared; a higher-priority
  approval notification is retained.
- Non-empty unsent answers are recovered in question order as quoted
  question/answer Markdown and appended to the main composer draft.
- The focused question's live answer and the stashed main draft are both
  preserved. Main-draft text elements survive the composer handoff.
- Shell and slash-command drafts are escaped before recovered answers are
  appended; path-like slash drafts retain their normal action.
- Question titles are sanitized, quoted line-by-line, and bounded to Rust's
  512-byte UTF-8 boundary with an ellipsis.

Regression coverage:

- `TestAsyncQuestionNotificationExpiresWhenTurnEndsLikeRust`
- `TestTurnEndKeepsHigherPriorityNotificationLikeRust`
- `TestTurnEndRecoversAsyncQuestionDraftsLikeRust`
- `TestTurnEndEscapesCommandDraftBeforeRecoveryLikeRust`
- `TestThreadTurnEndClearsAsyncQuestionNotificationLikeRust`
- `TestAsyncQuestionsTakePendingDraftsMatchesRust`
- `TestAsyncQuestionsTakePendingDraftsBoundsUTF8Title`

### #41917 open the agents overview from an empty composer

Rust replaced the configurable `Alt+A` agents default with a contextual `Left`
shortcut that is only active on an empty composer in a local-daemon session.
Go had neither the eligibility gate nor the footer hint, and still reserved
`Alt+A` for `global.open_agents`.

- `global.open_agents` is now unbound by default; a user-configured binding
  continues to work.
- A plain `Left` press opens the agents overview when the composer is empty and
  focused, no attachments or completion popup is active, no paste burst or
  pending Vim operator is in progress, and the session is local-daemon.
- The editor or Vim-normal `move_left` binding must still claim `Left`; an
  explicit remap keeps the key inside the active keymap. Global/chat/voice
  actions that claim `Left` continue to win.
- The passive footer shows `← for agents` only while the shortcut is available.
- The shared footer library renders `← for agents · ? for shortcuts`, appends
  `· ← for agents` to an existing status line, keeps a compact `← for agents`
  layout beside the context indicator, and lists
  `← for agents (empty prompt)` in the shortcut overlay.

Regression coverage:

- `TestAgentsNavigationOpensOverviewFromEmptyComposerLikeRust`
- `TestAgentsNavigationRequiresLocalDaemonLikeRust`
- `TestAgentsNavigationPreservesTransientInputLikeRust`
- `TestAgentsNavigationRespectsEditorAndVimRemapsLikeRust`
- `TestAgentsNavigationFooterHintFollowsAvailabilityLikeRust`
- `TestAgentsNavigationOpensOverviewPTY` (real local-daemon ConPTY smoke)
- `TestOpenAgentsDefaultBindingUnboundLikeRust`
- `TestFooterAgentsNavigationHintLikeRust`

### #42584 recover Vim escape input in legacy terminals

Legacy Unix terminals encode `Alt+character` the same way as `Esc` followed by
that character. In Vim insert/replace mode or with an operator pending, Go
treated the combined stroke as a single Alt chord, and backtrack handling could
also consume an Esc that Vim needed.

- `Esc` now stays with Vim in insert, replace, and operator-pending modes
  instead of priming backtrack.
- An ambiguous `Alt+character` is replayed as `Esc` and then the plain
  character when Vim can consume the escape.
- Active global/chat/voice/composer/editor/Vim bindings keep their Alt stroke,
  including a user-configured binding.
- Image paste (`v`/`V`), the empty-composer agent word-motion fallback,
  overlays, modals, popups, warnings, transcript find/focus, the command
  center, the read-only notice, and Windows sessions are excluded.
- The platform predicate lists Unix targets explicitly, matching Rust's
  `cfg!(unix)` gate instead of treating every non-Windows target as Unix.

Regression coverage:

- `TestRecoverVimInsertEscapeGatingMatchesRust`
- `TestShouldRecoverVimInsertEscapeLikeRust`
- `TestVimEscapeRecoveryPreservesAltBindingsAndSurfacesLikeRust`
- `TestLegacyVimEscapeRecoveryReplaysEscapeThenCharacterLikeRust`
- `TestVimInsertEscapeWinsOverBacktrackLikeRust`
- `TestVimEscapeRecoveryRespectsCustomAltBindingLikeRust`

### #43889 transcript pager half-page rounding

The alternate-screen entry path already guards repeated entry with
`overlayAltScreen`, so closing the pager restores the inline viewport instead of
entering it twice. Go's overlay keeps its own message buffer and re-renders raw
output immediately, so Rust's deferred-reflow queue has no separate counterpart.
The remaining observable difference was half-page scrolling: the viewport
library rounds down while Rust uses `(rendered_height + 1) / 2`.

- Half-page up/down now scroll by `(rendered viewport height + 1) / 2` through
  `ScrollUp`/`ScrollDown`, matching Rust's ceiling division and clamping.

Regression coverage:

- `TestTranscriptOverlayHalfPageRoundsUpLikeRust`

### #45549 preserve streamed answers and plans when turns terminate

The live tea transcript already appends answer deltas directly to its message
source and renders plan deltas into retained history, so an interruption does
not discard them. The standalone chatwidget parity runtime still cleared both
stream controllers inside `FinalizeTurn` without finalizing them, losing
unfinished math or plan source on interrupt, failure, and policy errors.

- Added `FlushAnswerAndPlanStreams` to finalize the answer stream and, when
  present, the plan stream while retaining `FinalizedAnswerSource`,
  `FinalizedPlanSource`, and the latest proposed plan.
- `FinalizeTurn` now consolidates both streams before clearing turn state, and
  transfers the queued usage-insertion request.
- Normal `OnTaskComplete` uses the same helper, so completion and termination
  share one consolidation path.
- `OnError`, `OnServerOverloadedError`, `OnCyberPolicyError`, safety-access
  termination, and misalignment termination all inherit the preservation.
- Added a tea-level interrupt regression proving the streamed answer remains in
  `State.Messages` and the streamed plan remains in the rendered transcript.

Regression coverage:

- `TestTurnRuntimeFinalizeTurnPreservesStreamedAnswerAndPlanLikeRust`
- `TestTurnRuntimeErrorPreservesStreamedPlanLikeRust`
- `TestTurnRuntimePolicyErrorPreservesAnswerAndPlanLikeRust`
- `TestTurnInterruptionPreservesStreamedAnswerAndPlanLikeRust`

### #40787 respect paginated history when stopping background tasks

Command Center's remote stop path always read the thread with embedded turns.
Paginated threads keep their turns behind `thread/turns/list`, so the active
turn could be missed and the stop action would do nothing.

- Stop now reads the thread without turns first to discover `historyMode`.
- Paginated threads fetch the first `thread/turns/list` page and search it for
  the in-progress turn.
- Legacy threads keep using embedded turn history.
- A server that rejects `turns/list` falls back to the embedded-history read,
  matching Rust's negotiated capability fallback.
- The interrupt request still uses the discovered thread and turn IDs, and an
  idle thread sends no interrupt.

Regression coverage:

- `TestRemoteAgentsStopRespectsPaginatedHistoryLikeRust`

### #46838 refine the agent command center layout

The Go command center rendered the search/rename editor below the task list,
omitted the active grouping from the header, hid the search affordance while
browsing, and clipped long metadata input by rune rather than grapheme cluster.
Notices were appended outside the dashboard body instead of occupying the
footer.

- The header now renders `Group: <Project|Status|Model>` with the grouping
  shortcut.
- The search hint/editor row sits directly above the task list; browsing shows
  `Search tasks · <key>` while search/rename shows the active field.
- Connection/refresh/lifecycle notices replace the footer hints, with the same
  reserved footer height.
- An empty initial refresh shows `Loading tasks…`; an empty list shows
  `No tasks yet`; a filtered list shows `No matching tasks`.
- Search/rename input is clipped on grapheme boundaries, keeping combining
  marks and joined emoji intact.
- The production ConPTY smoke now verifies `Group: Project` and `Search tasks`
  appear after Left opens the command center.

Regression coverage:

- `TestAgentCenterHeaderShowsGroupingLikeRust`
- `TestAgentCenterSearchRowAboveTaskListLikeRust`
- `TestAgentCenterEmptyAndLoadingStatesLikeRust`
- `TestAgentCenterNoticeReplacesFooterLikeRust`
- `TestSingleLineViewportClipsGraphemesLikeRust`
- `TestAgentsNavigationOpensOverviewPTY`

Remaining for this PR: the standard agent sign-in picker defaults and the
separate key-chord hint priority state. Go currently has no command-center
key-chord hint surface, so a notice always owns the footer when present.

### #44946 forward only the personality opt-out from the TUI

Rust retired Friendly/Pragmatic personality selection while keeping an explicit
`personality = "none"` opt-out. Go still forwarded the configured
Friendly/Pragmatic value through thread start, resume/fork config, turn start,
side-conversation forks, and remote config overrides.

- Added `PersonalityOptOutOnly`, which returns only `none` and treats every
  other value as unset.
- Embedded turn config overrides now append `personality=none` only for the
  explicit opt-out.
- Remote thread start/turn start no longer set `Personality` or
  `PersonalitySet` for Friendly/Pragmatic states; `none` remains forwarded.
- Remote request config values drop retired personality overrides while
  preserving the explicit opt-out.
- Local side-conversation forks apply the same filter.
- Config parsing still accepts the legacy values for compatibility, matching
  Rust's catalog/config compatibility behavior.

Regression coverage:

- `TestPersonalityOptOutOnlyLikeRust`
- `TestTUIRequestsForwardPersonalityOptOutOnlyLikeRust`
- `TestRemoteConfigDropsRetiredPersonalityOverridesLikeRust`

### #43698 make older app-server notices configurable

Go allowed `tui.show_server_version_notice` through config validation but never
read it, and no startup/command-center server-version notice was wired.

- Added the preference to the TUI settings record and options, defaulting to
  enabled.
- Local-daemon and remote sessions now add a retained
  `ServerVersionNotice` warning when the service version differs from the CLI,
  with the same older/different wording and optional `/daemon` guidance.
- The command center footer renders
  `Service v<server> <|≠ Codex CLI v<client> · /daemon`.
- Disabling the setting immediately removes the retained warning and clears the
  command-center notice state; re-enabling it restores the notice without
  duplicating warning entries.
- Embedded sessions and equal versions produce no notice.
- Compatibility errors and normal version status remain independent of this
  preference.

Regression coverage:

- `TestShowServerVersionNoticeConfigLikeRust`
- `TestServerVersionNoticeConfigGateLikeRust`
- `TestServerVersionNoticeOnlyForConnectedNonEmbeddedSessionsLikeRust`
- `TestServerVersionOverviewNoticeUsesDifferentComparisonLikeRust`
- `TestServerVersionNoticeRendersInCommandCenterFooterLikeRust`

### #46883 add `/tui` for the next launch's renderer

Go accepted `tui.fullscreen_transcript` in configuration validation but never
read it, and `/tui` had no command carrier. The new picker persists the
preference without changing the running renderer.

- Added `/tui` to the slash-command enum/parser, popup inventory, and
  task-blocked availability list.
- The picker offers Scrollback and Fullscreen, marks the stored preference, and
  requires explicit confirmation.
- Confirmed selections write `tui.fullscreen_transcript`; failures report
  `Failed to save TUI mode: …` and leave the stored preference unchanged.
- Successful saves append
  `Saved TUI mode: <mode>. Restart Codex to apply; launch overrides still apply.`
- Launch resolution now maps the saved preference and `tui.alternate_screen`
  (`always`/`never`/`auto`) to terminal ownership, with `--no-alt-screen`
  always winning. The same resolution is used by the embedded TUI, remote TUI,
  local-settings staging, and auth onboarding.
- The production ConPTY test opens `/tui`, confirms Scrollback, verifies the
  config file receives `fullscreen_transcript = false`, and keeps the running
  `--no-alt-screen` session alive.

Regression coverage:

- `TestTUIModePickerLikeRust`
- `TestTUIModePickerPersistsNextLaunchOnlyLikeRust`
- `TestTUIModePickerFailureKeepsStoredPreferenceLikeRust`
- `TestTUIModePickerCancelWritesNothingLikeRust`
- `TestTUIModePickerMarksStoredPreferenceLikeRust`
- `TestInteractiveNoAltScreenResolutionLikeRust`
- `TestInteractiveTUIModeConfigParsingLikeRust`
- `TestTUIModePickerPersistsNextLaunchPTY`

Not covered here: Terminal.app-over-SSH detection for `alternate_screen=auto`
(the separate #47417 device-attribute probe); Go's `auto` continues to use the
alternate screen.

### #44925 accept quiet-turn voice audio before captions

Go suppressed the realtime speaker whenever a user caption began, even on a
quiet turn with no older output. Response audio arriving before assistant
captions was therefore discarded.

- Split speaker suppression into an unconditional typed-input path and a
  conditional `suppressActiveVoiceSpeaker` path for voice turns.
- A user caption now interrupts only when an assistant caption stream, pending
  spoken answer, audible speaker level/window, or existing suppression is
  active.
- Quiet incremental and final-only user transcripts keep the speaker ready for
  the first response audio packet.
- Typed input still suppresses unconditionally.

Regression coverage:

- `TestVoiceQuietTurnDoesNotSuppressSpeakerLikeRust`
- `TestVoiceUserCaptionInterruptsOlderOutputLikeRust`
- `TestTypedInputStillSuppressesVoiceSpeakerLikeRust`

### #41913 preserve TUI status timing when the row is hidden

Go recreated the status indicator on every draw and used `taskStartedAt`
directly, so modal approval wait time entered the turn clock, and a turn
completion without a protocol duration produced no local elapsed fallback.

- Added an independent pause-aware `StatusTimer` with reset/pause/resume/elapsed
  semantics matching Rust's `StatusTimer`.
- The working row now renders the independent clock and keeps it across status
  row visibility changes.
- Opening a modal pauses the timer; closing or replacing it resumes with the
  accumulated elapsed time intact. A new turn resets elapsed time without
  clearing an outstanding modal's pause state.
- Replayed in-progress turns seed the timer from their original start time.
- A successful completion without `DurationMS` falls back to the local status
  clock for the `Worked for` footer.
- Windows sandbox setup starts a fresh clock.

Regression coverage:

- `TestStatusTimerPauseResumeAndResetLikeRust`
- `TestStatusTimerPausesForModalsLikeRust`
- `TestStatusTimerFeedsWorkingRowAndCompletionFallbackLikeRust`
- `TestWindowsSandboxSetupStartsFreshStatusClockLikeRust`

### #47321 honor the system clock preference across TUI timestamps

Several Go TUI surfaces hard-coded either a twelve-hour or twenty-four-hour
timestamp. Rust detects the host short-time preference once and threads it
through the views.

- Added a shared `ClockFormat` with twelve/twenty-four-hour layouts and
  time/date-time/year layouts.
- Detection is cached once: Windows reads `LOCALE_SSHORTTIME` through
  `GetLocaleInfoEx`; other platforms inspect `LC_ALL`/`LC_TIME`/`LANG` with the
  C/POSIX and English-US/CA cases; unknown preferences fall back to twelve-hour.
- Completion footers (`done <time>`), rate-limit reset labels, status reset
  labels, and resume-picker expanded timestamps now use the detected format.
- Timestamp tests pin an explicit format so midnight/noon and next-day output
  are deterministic on both host settings.

Regression coverage:

- `TestClockFormatLayoutsLikeRust`
- `TestParseNativeClockPatternLikeRust`
- `TestFinalMessageSeparatorCompletionTimestampLikeRust`

Remaining for this PR: analytics, backend banners, usage notices, and
reset-credit expiration labels still have their existing formatters and need
the same clock preference threaded through their view construction.

### #54381 add the presets shortcut surface

The preset HUD and menu actions already existed, but Go lacked the
`chat.open_presets` keymap action and the leader chord that opens it.

- Added `chat.open_presets` with the implicit default `leader p`; the action is
  listed in the keymap catalog/schema and displays its resolved chord.
- Added a minimal persistent leader chord runtime:
  - pressing the resolved leader prefix arms the chord;
  - the completion key opens the preset HUD;
  - another key or the one-second timeout cancels the chord and dispatches the
    key normally;
  - paste, mouse input, focus gain/loss cancel the pending chord.
- Explicit remaps to a plain key still work through the normal keymap path.
- Added a preset shortcut tooltip using the resolved action label.
- The production ConPTY test sends Ctrl-X then `p`, waits for the empty preset
  HUD, closes it, then continues the `/tui` flow.

Regression coverage:

- `TestPresetsLeaderChordOpensQuickDialLikeRust`
- `TestPresetsLeaderChordCancelsOnOtherKeyAndTimeoutLikeRust`
- `TestTUIModePickerPersistsNextLaunchPTY`

Go still has no general multi-action chord menu/HUD; this lands the
`open_presets` chord used by #54381 without broadening the chord matcher to
every configurable action.

### #47847 keep the TUI responsive during clipboard copies

Go wrote clipboard content synchronously inside the Bubble Tea update loop, so
a slow clipboard backend could block input and rendering.

- Copy target selection, rich/plain response copies, and warning copies now
  return a `bubbletea.Cmd` that performs the write outside the update loop.
- `ClipboardWriteResultMsg` applies the success/failure notice and refreshes the
  transcript; warnings copies update the viewer flash independently.
- Each write carries a monotonically increasing request id. Concurrent copies
  are rejected with a busy notice, stale completions are ignored, and image or
  text paste reports a retry message while a copy is in flight.
- The copy picker still validates backend availability before opening.
- Existing copy tests drive the returned command and result message so the
  asynchronous path, rich clipboard writer, hard-break preservation, status
  copies, and warnings copy remain covered.

Regression coverage:

- `TestClipboardCopyRunsOffUpdateLoopLikeRust`
- `TestClipboardCopyFailureReturnsNoticeLikeRust`
- `TestClipboardCopyRejectsConcurrentWritesLikeRust`
- `TestClipboardBusyNoticeLikeRust`
- Existing `/copy`, status-copy, hard-break, rich-copy, and warnings-view tests
  updated to execute the deferred command.

### #46680 compact composer shortcut typography

Go's passive composer footer still used title-case spellings and `|` separators
while Rust uses compact lowercase shortcut labels and `·` between actions.

- The footer now reads
  `enter send · ctrl+j newline · ctrl+g editor · ctrl+c quit · /help commands`.
- The contextual agents hint uses the same separator:
  `← for agents · enter send …`.
- Plain, approval-modal, request-user-input, and VT100 snapshots were updated.

Regression coverage:

- `TestModelViewRendersState`
- `TestModelTerminalSnapshots`
- `TestModelVT100TerminalSnapshotMainView`
- `TestModelVT100TerminalSnapshotApprovalModal`
- `TestModelStartupSessionHeaderTooltipHonorsShowTooltipsLikeRust`

Remaining for this PR: contrast-aware palette conversion and the opt-in picker
presentation (full-width selection, filled tabs, overflow indicators, visible-row
navigation) are still separate UI work.

### #46680 preserve syntax-highlighted output contrast

Exec cells wrapped every output row in the dim SGR, including rows that already
carried syntax-highlighting ANSI colors. Dimming those rows undid the foreground
contrast correction Rust #46680 introduced.

- Added `dimOutputUnlessStyled`: plain output rows remain dimmed; rows containing
  SGR styling pass through unchanged.
- Applied it to the three-row agent preview, empty-output row, and user-shell
  output rows.
- Added a direct red/green-styled output regression proving styled rows are not
  dimmed while plain rows still are.

Regression coverage:

- `TestExecCellDoesNotDimSyntaxHighlightedOutputLikeRust`

### #54097 show Daybreak notices after the session header

The standalone chatwidget model had the Daybreak notice text, but the live tea
transcript never emitted it at startup, on `/new`, or when resuming a
Daybreak-enabled thread.

- After the startup session header, a Daybreak-enabled model appends
  `Daybreak is on for new turns.` exactly once.
- `/new` retains the current session's Daybreak preference across the
  thread-scoped reset and emits the notice for the fresh thread.
- Resuming a thread whose summary enables Daybreak prepends the notice ahead of
  replayed messages, matching Rust's header → notice → history order.
- Disabled Daybreak emits no notice, and duplicate notices are suppressed.

Regression coverage:

- `TestDaybreakNoticeFollowsSessionHeaderLikeRust`
- `TestDaybreakNoticeOnNewThreadAndResumeLikeRust`

### #46680 opt-in full-width picker selection

The keymap picker used the compact selected-row fill, leaving the selection
highlight shorter than the popup width. Rust #46680 reserves a full-width
selection for its opt-in picker presentation.

- Added `RenderSelectedRowFilled`, which pads to the popup width before applying
  the contrast-aware selection SGR.
- Added `ColumnWidthConfig.FullWidthSelection` /
  `WithFullWidthSelection`.
- Enabled it for the `/keymap` action list, action menu, and replace-binding
  menu; other selection views keep compact highlights.
- Wrapped selected rows are padded on every rendered line.

Regression coverage:

- `TestGenericRowsFullWidthSelectionLikeRust`

### #54216 transcript image preview foundation

The fullscreen transcript image-preview feature was blocked on missing
loading/layout pieces. This turn lands the terminal-independent half:

- Added `tui/transcript_image` with the reserved first Kitty image ID, bounded
  encoded input (32 MiB) and decoded allocation (32 MiB; RGBA-safe pixel
  bound).
- Local PNG/JPEG/GIF files decode into aspect-preserving bounded thumbnails:
  Kitty caps at 48 columns / 6 rows / 384x160 pixels; Sixel uses the terminal
  cell size and caps at 160x80 pixels.
- Sixel refuses to load without a measured cell size; missing files,
  non-regular files, and oversized inputs fall back to the existing text entry.
- Added `Placement` viewport clipping and source-row mapping for both Sixel
  physical-pixel clipping and Kitty scaled-row clipping.

Regression coverage:

- `TestLoadBoundsThumbnailLikeRust`
- `TestLoadFallbacksAndLimitsLikeRust`
- `TestPlacementClipsToViewportAndSourceRowsLikeRust`

The next section wires the loaded thumbnails into the fullscreen transcript's
Kitty/Sixel encoder, retains graphics across redraws, and removes placements
when overlays open.

### #54216 render image previews in the fullscreen transcript

The loading/layout foundation is now wired into the owned fullscreen
transcript, closing the remaining user-visible half of the PR.

- History cells expose optional `ImagePath()` and `ActivityIDs()` metadata.
  Viewed-image cells carry `image-view:<id>`; generated-image cells expose
  their saved path unless the generation failed.
- Live and replayed `imageView`/`imageGeneration` thread items retain their
  local artifact path on `codextui.Message`, including app-server history
  replay through `remoteTUIMessageFromThreadItem`.
- A completed built-in `view_image` tool call is projected into the same
  ImageView disclosure cell, while its in-progress generic tool row is
  suppressed (matching Rust's core item projection).
- Generated images reserve preview rows beside their saved-path entry at all
  times. Viewed images are disclosure entries: the preview appears only when
  the activity is manually expanded (or in detailed presentation), matching
  Rust's `reveal_image` rule. Find may expand the text temporarily but never
  reveals inspected pixels.
- `transcript_image.TranscriptImage.Encode` produces Kitty PNG transfer
  payloads (including the local-file transport variant) and Sixel pixel
  payloads. The transcript runtime paints them through the shared output
  wrapper used by ambient pets.
- Visible slices are clipped to the transcript viewport and to the terminal
  rectangle, including partially scrolled Kitty scaled rows and Sixel physical
  pixel rows.
- Kitty graphics are retained across unrelated redraws, deleted when their
  placement changes, and repainted after text clears. Local-file transport
  files are removed with their placement.
- Preview rows are layout-only blank rows: they are not part of searchable or
  selectable transcript text, and they never replace the existing text entry.
- Transcript range bookkeeping now counts the single blank separator between
  messages once, keeping image placement, Find anchors, and activity scrolling
  on the same content coordinates.
- Raw mode, remote workspaces, unsupported terminals, Sixel without a measured
  cell size, missing files, non-regular files, encoded inputs over 32 MiB, and
  decoded allocations over 32 MiB all fall back to the text entry. Cached previews
  revalidate the artifact's size and modification time, so a deleted or
  replaced file stops reserving rows.
- Full-screen overlays (transcript pager, command center, session picker)
  remove placements before painting and restore them when the main transcript
  returns; bottom-pane modals keep the visible transcript's graphics.
- Inline TUI geometry anchors the frame above the terminal's reserved cursor
  row, so absolute Kitty/Sixel coordinates follow Bubble Tea's scrolling frame
  instead of assuming an alternate-screen origin.

Regression coverage:

- `TestEncodeKittyAndSixelPayloadsLikeRust`
- `TestGeneratedImageReservesPreviewRowsLikeRust`
- `TestViewedImagePreviewFollowsDisclosureLikeRust`
- `TestTranscriptImageFallbacksLikeRust`
- `TestTranscriptImageOverlayHidesPlacementsLikeRust`
- `TestTranscriptImageSurvivesBottomPaneModalLikeRust`
- `TestTranscriptImageRewrapsOnResizeLikeRust`
- `TestTranscriptImageKeepsCompletionTipBelowPreviewLikeRust`
- `TestTranscriptImageFindDoesNotRevealViewedPixelsLikeRust`
- `TestTranscriptImageCacheInvalidatesDeletedFileLikeRust`
- `TestTranscriptImagePlacementClipsToViewportLikeRust`
- `TestTranscriptImageWriterRetainsAndRemovesGraphicsLikeRust`
- `TestTranscriptImageSixelClearsOnRemovalLikeRust`
- `TestTranscriptImageLocalFileCleansTransportLikeRust`
- `TestTranscriptImagePositionMatchesInlineRendererLikeRust`
- `TestTranscriptImageChromeTopOffsetLikeRust`
- `TestViewImageToolCallBecomesPreviewCellLikeRust`
- `TestImageHistoryCellsExposeLocalPreviewPathsLikeRust`
- `TestRemoteImageViewItemCarriesLocalArtifactPathLikeRust`
- `TestRemoteHistoryImageItemsKeepPreviewPathsLikeRust`

Known platform limit: Sixel previews stay text-only where the console API does
not expose a pixel cell size (including Windows); Kitty terminals remain fully
supported.

### #46680 compact picker and footer hints

The filled-tab and overflow-marker carriers are already present (including the
plugin picker). This round closes the shared footer-typography half of the
picker presentation:

- `openSelectionViewModal` now mirrors Rust's `apply_standard_popup_hint`: a
  cancellable selection view that uses the standard confirmation placeholder or
  no hint is rewritten to `enter select · esc back`. Explicit custom hints are
  preserved, and the generic modal renderer no longer discards the hint supplied
  by its selection view.
- Status-line and terminal-title setup use Rust's compact lowercase
  `space toggle · enter save · esc cancel`; the experimental-features popup uses
  `space toggle · enter save · esc save/close` because Escape saves and closes.
- The theme picker uses the same compact picker footer.
- Ctrl+R history search now uses the compact `enter accept · esc cancel`
  separator and renders Rust's contrasting spans: dim prompt/action words, cyan
  query, bold action keys, and a red no-match result. The styled line bypasses
  the outer dim footer style so those contrasts survive.
- Generic selection modals now reserve a result viewport instead of emitting
  every option: long lists keep their title, body, and footer on screen, render
  `↑`/`↓` overflow markers when rows are hidden, and page with
  PgUp/PgDn visible rows. Home/End select the first/last enabled option.
- The responsive description-column renderer measures rendered wrapped rows and
  shrinks the visible item count until the viewport budget is respected, so
  long descriptions cannot push the controls off screen.
- The theme picker now uses the same stable result viewport: ↑/↓ markers,
  PgUp/PgDn by visible rows, Home/End edges, and a scroll position that only
  moves when the selection leaves the window. In narrow layouts the preview
  yields before the result rows, keeping the title, search prompt, footer, and
  at least a few themes visible.
- The Ctrl+T transcript pager now reserves two compact hint rows like Rust:
  `↑/↓ to scroll · pgup/pgdn to page · home/end to jump` and
  `q close · f3 find · esc browse prompts`. Close/find labels follow the
  resolved pager bindings, and an active Find bar replaces the actions row.

Regression coverage:

- `TestSelectionViewsUseCompactPickerHintLikeRust`
- `TestHistorySearchFooterCursorCompatibilityAndCaseInsensitiveRangesMatchRustCore`
- `TestModelStatusControlsSetupHistoryAndInvalidItem`
- `TestModelThemeAndPetsPickersUseCurrentSelections`
- `TestModelWindowsSandboxStartupPromptMatchesRustFlow`
- `TestModalPickerWindowsLongListsLikeRust`
- `TestModalPickerShortListKeepsAllRowsLikeRust`
- `TestResponsiveModalPickerUsesVisibleRowsLikeRust`
- `TestResponsiveModalPickerCountsWrappedRowsLikeRust`
- `TestThemePickerNarrowViewportDropsPreviewAndKeepsControlsLikeRust`
- `TestThemePickerWidePreviewSurvivesWindowLikeRust`
- `TestPagerHintsUseTwoCompactRowsLikeRust`
- `TestPagerHintsUseConfiguredBindingsLikeRust`
- Production ConPTY `/theme` footer check inside
  `TestTUIModePickerPersistsNextLaunchPTY`

Still remaining for #46680: the full contrast-aware palette conversion, and an
audit of the remaining custom renderers (hooks/browser-style views) for the
same result viewport. The audit ledger entry remains `audit_pending`.

### #50039 keep transcript Find results readable

Go previously cancelled Find on the first Escape even when a nonempty query
had a selected match, losing the expanded result and its surrounding context.

- With a nonempty query, the first Escape now closes query editing and enters
  the retained reading mode: the query, match, expanded activity context, and
  `Find · … · esc latest` footer remain visible.
- A second Escape returns to the latest output and clears the retained Find
  presentation.
- Ctrl+C cancels Find and restores the pre-search offset/presentation before
  the global interrupt/quit handler sees it.
- Page navigation and Ctrl+P/Ctrl+N remain available while reading results.

Regression coverage:

- `TestOwnedTranscriptFindExpandsOnlyCurrentMatchLikeRust`
- `TestGlobalFindTranscriptStartsOwnedFindLikeRust`
- `TestOwnedTranscriptFindCtrlCCancelsWithoutQuittingLikeRust`

### #52990 `/config` settings panel carrier

Go previously advertised `/config` in the Rust command order but deliberately
omitted its frame. This round lands the first functional panel:

- `/config` is now a real slash command, blocked during an active task like
  Rust, and opens a searchable panel with filled section tabs.
- Appearance exposes Theme, Effects, Status line, and Terminal title. Theme,
  Status line, Terminal title, and Keyboard hand off to the existing editors.
- Effects opens a Rust-style sub-picker with Animations and Composer stars;
  Composer stars is unavailable while Animations is off.
- Notifications cycles
  `tui.notification_condition`; Input cycles `tui.right_click_paste`; Sessions
  cycles `tui.session_picker_view`. Writes use the shared settings handler and
  apply the returned live values.
- Text selection exposes `tui.copy_on_select` (`Auto`/`On`/`Off`) and
  `tui.copy_on_select_clear_selection`.
- Additional live preferences now match the Rust catalog: Tips
  (`tui.show_tooltips`), Status line colors
  (`tui.status_line_use_colors`), Notifications
  (`tui.notifications`, with custom event filters reported unavailable),
  Paste detection (`tui.disable_paste_burst`), and Automatic recaps
  (`tui.auto_recap`).
- Start in Vim mode toggles `tui.vim_mode_default`; the default seeds fresh
  TUI composers while `/vim` remains the current-composer switch.
- Resume directory exposes Ask / Current directory / Session directory:
  Ask opens a cwd prompt when the current and saved directories differ;
  Current keeps the launch directory; Session applies the saved thread cwd.
  The prompt's Always options persist `tui.resume_cwd`, Esc selects the saved
  session directory, and Ctrl+C quits, matching Rust's prompt contract.
- Test notification schedules the Rust-style five-second delayed notification.
- Deselect after copy is dependent on Copy on select and reports Rust's
  unavailable reason when copy-on-select is off.
- A failed configuration write keeps the panel open and reports the underlying
  error plus retry guidance without interrupting the running task.
- Search filters the current section by label, description, keywords, and
  section title. Up/Down, PgUp/PgDn, Home/End, and Left/Right navigate without
  leaving the panel.
- Long filtered lists use the shared result viewport with ↑/↓ markers, and the
  panel keeps its title, tabs, search row, and footer on screen.

Regression coverage:

- `TestConfigCommandOpensSearchablePanelLikeRust`
- `TestConfigPanelEditorsAndPreferenceCyclesLikeRust`
- `TestInteractiveCopyOnSelectConfigLikeRust`
- `TestConfigPanelRemainingTogglesLikeRust`
- `TestConfigPanelSaveErrorKeepsPanelAndExplainsRetryLikeRust`
- `TestResumeCWDModesAndPromptLikeRust`
- `TestResumeCWDPromptEscUsesSessionDirectoryLikeRust`
- `TestConfigPanelResumeDirectoryCyclesLikeRust`
- Production ConPTY `/config` open/footer check inside
  `TestTUIModePickerPersistsNextLaunchPTY`

The follow-up below closes the remote-resume cwd nuance and the config-panel
save-notice variants. The dedicated Rust config-panel file otherwise remains
`audit_pending` for broader visual parity review.

### #52990 follow-up: resolve resume cwd before the request

Rust resolves `tui.resume_cwd` before rebuilding config and calling
`thread/resume` (`session_resume.rs`, `app/resume_config.rs`), and
`thread_cwd_from_config` sends the chosen config cwd for Embedded params but
only `remote_cwd_override` (`--cd`) for Remote params. Go previously applied
the prompt after the resume response and always forwarded the launch cwd, so a
Session or Ask choice could not reach the server.

Go now carries the selection-time cwd on `SessionTarget.CWD`, resolves the
mode before invoking `OnResumeSession` (`tui/tea/resume_cwd.go`), and passes
the chosen directory as `SessionSelection.ResumeCWD`. The local daemon sends
that resolved cwd; an explicit remote workspace sends only `--cd` and leaves
the server-owned saved directory alone. Agent switching reads the target
thread's cwd instead of forwarding the launcher's. A remote Current choice
without `--cd` returns Rust's exact error. The config panel also marks Current
unavailable in that case, and its header now renders save failures plus
`Saved · overridden by config or system setting` and
`Saved · applies to new composers` notices.

Tests:

- `TestResumeCWDResolvedBeforeResumeLikeRust`
- `TestResumeCWDCurrentUnavailableForRemoteLikeRust`
- `TestConfigPanelResumeDirectoryRemoteAvailabilityLikeRust`
- `TestConfigPanelSaveNoticesLikeRust`
- `TestRemoteTUIResumeParamsResumeDirectoryLikeRust`
- `TestInteractiveRemoteResumeSessionForwardsResolvedCWD`
- `TestInteractiveRemoteSwitchAgentThreadReadsTranscript`
- `TestInteractiveRemoteSwitchAgentThreadFallsBackToReadOnlyHistory`

### #46694 completion popup result identity

Rust's unified mention picker measures the primary column across the full
result set in terminal cells, then caps it at half of the row's content width
before truncating primary text. That keeps a long off-screen plugin name from
crowding out the parent path that distinguishes two files. Selected rows use
the shared `›` marker and full-width selection fill. File-search rows are
single-line: long paths are shortened around the filename, visible fuzzy
matches are recomputed against that shortened text, and the full path remains
the insertion identity.

Go now mirrors those rules in `tui/bottom_pane/mentions_v2/render.go`,
`tui/bottom_pane/file_search_popup.go`, and
`tui/bottom_pane/command_popup.go`. File search restores Rust's `+2` picker
padding in `CalculateRequiredHeight`; command rows own their selection prefix
instead of carrying the retired two-column left inset. The unified popup now
has the fixed 17-row frame (heading, filled filter tabs, query, scroll
indicators, result viewport, footer), type-colored names/tags and bold fuzzy
matches. Command wrapping keeps the description column gap, and the skill popup
keeps right-aligned category tags when a narrow description column is hidden.

Tests:

- `TestMentionPopupFixedFrameMatchesRust`
- `TestMentionPopupEmptyFilterClearsResultsAndKeepsFrame`
- `TestMentionRenderBoundedPrimaryColumnKeepsFileIdentityLikeRust`
- `TestMentionRenderStylesNamesTagsAndMatchesLikeRust`
- `TestFileSearchPopupPreservesDistinguishingFilenamesLikeRust`
- `TestFileSearchPopupShortensWidePathsByDisplayWidth`
- `TestCommandPopupWrapBoundaryKeepsFollowingChoiceLikeRust`
- `TestCommandPopupRowsUseSelectedColorBarAndScroll`
- `TestSkillPopupCategoryTagsSurviveHiddenDescriptionsLikeRust` (bottom-pane carrier)
- `TestSkillPopupCategoryTagsSurviveHiddenDescriptionsLikeRust` (tea legacy renderer)

### #46691 picker wrapping and hint audit

The recorded wrapped-column remainder is closed: when a two-column selection
row exceeds the popup width, Go now wraps only the description column instead
of re-tokenizing the flattened row. The name prefix, two-column gap, and
following choice stay in place. Widths 32/33/34 reproduce Rust's
`command_popup_wrap_boundary` snapshot.

The picker-hint audit also corrected the memories settings footer to
`space toggle · enter save/select · esc cancel`; cancellable pickers, status and
terminal-title setup, Ctrl+R history search, and the pager already use their
compact Rust hints.

Tests:

- `TestCommandPopupWrapBoundaryKeepsFollowingChoiceLikeRust`
- `TestSelectionViewsUseCompactPickerHintLikeRust`
- `TestConfiguredKeyChordSubmitsLikeRust`
- `TestConfiguredGlobalChordSubmitFallbackLikeRust`
- `TestModelKeymapChordCaptureWritesTwoStrokeSpecLikeRust`
- `TestKeymapActionMenuShowsChordEntriesLikeRust`

The generic two-stroke chord runtime is now present: a configured first stroke
creates a pending prefix, the completed chord is carried through the existing
action dispatch, timeout/Esc cancel it, and capture stores the normalized
two-stroke spec. The action menu exposes Set/Replace/Add key chord entries.

### #46697 shared picker layout

Go now has the `PickerAreas` allocator that Rust uses to fit picker chrome
before assigning result rows: header, optional header gap, tabs, optional tab
gap, search, paired overflow indicators, rows, and optional side content. The
unified mention frame consumes the allocator instead of maintaining a second
copy of the sizing rules.

Tests:

- `TestPickerAreasMatchesRustAllocation`
- `TestPickerAreasNeverOverallocate`
- `TestMentionPopupFixedFrameMatchesRust`

The existing session-picker identity color suite remains green for dense and
selected rows, color suppression, and selection styling.

### #46692 shared dedicated-picker rows

Go now has the `picker_rows` adapter that Rust uses for apps, plugins,
settings, skills, hooks, and multi-select views: measurement includes the two
spacer rows, single-line and wrapped renderers share the hide-when-narrow
columns, and selection paints the full width.

Multi-select and skills now consume the adapter. Their hints match Rust's
compact form, with multi-select adding the reorder chord only when ordering is
enabled.

Tests:

- `TestPickerRowsUseSharedColumnsAndSelection`
- `TestMultiSelectPickerFooterHintsMatchRust`
- Updated `TestMultiSelectPickerSearchToggleConfirmCancelRowsMatchRustCore`
- Updated `TestSkillsToggleViewFiltersSortsAndRowsMatchRust`

### #54388 terminal-title and status-line quick dials

The current-settings preset picker already carried both Rust options after
model and permissions. The audit confirmed that generated `/title` and
`/statusline` commands preserve item order and duplicates, normalize aliases,
use `--clear` for empty layouts, retain the status-line color mode, and disable
entries with unavailable items using Rust's guidance.

Tests:

- `TestQuickDialStatusSurfaceLayoutsNormalizeItemsLikeRust`
- `TestQuickDialPrefillsUseCurrentSettingsLikeRust`
- `TestQuickDialPrefillsDisableUnavailableSettingsLikeRust`

### #54081 centralized vertical navigation

The shared list navigation was present across experimental features, hooks,
memories, skills, and multi-select, but the resolver only accepted `ctrl+`
spellings while TUI dispatch supplies `ctrl-`. Ctrl+P/N/U/D/A/E therefore did
not reach the shared actions. The resolver now normalizes both spellings.

Tests:

- `TestVerticalActionForAndApplySharedLikeRust`
- `TestMultiSelectVerticalActionBeatsReorderLikeRust`

### #46883 `/tui` next-launch mode

The picker already marks the stored Scrollback/Fullscreen preference, requires
explicit confirmation, and writes only `tui.fullscreen_transcript`. The running
renderer is untouched, launch overrides remain authoritative, failures preserve
the previous preference, and cancellation writes nothing.

Tests:

- `TestTUIModePickerPersistsNextLaunchOnlyLikeRust`
- `TestTUIModePickerFailureKeepsStoredPreferenceLikeRust`
- `TestTUIModePickerCancelWritesNothingLikeRust`
- `TestTUIModePickerMarksStoredPreferenceLikeRust`
- production `TestTUIModePickerPersistsNextLaunchPTY`

### #49089 follow-up directive labels

Completed and copied assistant Markdown renders `:codex-followup[...]` labels
without prompt attributes, while code fences, inline code, links, images, HTML,
escaped markers, nested neighbors and malformed directives remain literal.
Streaming previews withhold unfinished directives.

Tests:

- `TestFollowupLabelsRenderMarksAndHidePrompts`
- `TestFollowupLabelsPreserveLiteralAndMalformedSources`
- `TestFollowupLabelsDoNotConsumeNeighborsOrNestedDirectives`
- `TestNormalizeCompletedAssistantMarkdownUsesFollowupLabels`
- `TestHideUnfinishedFollowupDirectiveForStreamingPreview`
- production follow-up directive PTY harness

### #49290 `/mcp login`

Go starts one per-server login attempt for the active thread, blocks overlaps,
tracks attempt ids, ignores stale completions, retains results for inactive
origin threads until they return, and filters remote completion notifications
by login id.

Tests:

- `TestMCPLoginCommandStartsAndCompletesLikeRust`
- `TestMCPLoginCompletionIgnoresStaleAttemptsLikeRust`
- `TestMCPLoginResultWaitsForOriginThreadLikeRust`
- `TestMCPLoginCommandErrorsAndUsageLikeRust`
- `TestRemoteMCPLoginMonitorWaitsForMatchingAttemptLikeRust`

### #50389 configured keys before transcript navigation

Only explicit custom composer/global bindings take transcript navigation keys;
default PageUp/PageDown/Home/End still scroll. Configured submit bindings win
before movement, and Ctrl+Space is normalized from all terminal encodings.

Tests:

- `TestConfiguredNavKeyWinsOverTranscriptScrollLikeRust`
- `TestDefaultNavKeyStillScrollsLikeRust`
- `TestCtrlSpaceNormalizationLikeRust`
- `TestConfiguredCtrlSpaceSubmitLikeRust`

### #47322 Markdown list preference

`tui.rendering.lists` was parsed but never consulted. Enabled rendering now
uses `•` bullets and `☑`/`☐` task markers while preserving ordered numbering
and indentation. Disabled rendering escapes task brackets so glamour keeps the
original `- [x]` / `- [ ]` text and hyphen bullets, with literal code untouched.

Tests:

- `TestRenderingListsPreferenceLikeRust`
- `TestTaskListMarkersLikeRust`
- ordered/empty-list marker tests

### Recent TUI entries closed by audit

The following entries were already implemented with direct regression
coverage and are now marked complete in the ledger:

- `#41917` empty-composer agents overview: hint availability, local-daemon
  gating, transient-input suppression, editor/Vim remaps, footer hint and PTY.
- `#42584` legacy Vim escape recovery: Esc replay, custom Alt bindings,
  platform gating.
- `#47954` startup tips in the transcript: wrapping, links, plan/announcement/
  override/random tooltip branches.
- `#49037` Plan mode status hint: visible when collaboration mode is available,
  hidden otherwise.
- `#50720` Windows mapped Shift+Enter: newline insertion, unrelated CSI/modal
  isolation, no delay on other terminals.

Relevant tests:

```text
go test ./tui/tea -run 'AgentsNavigationOpensOverviewFromEmptyComposer|LegacyVimEscape|VimEscapeRecovery|StartupTooltipLines|StatusHeaderShowsPlanMode|StatusHeaderHidesPlanCycle|WindowsMappedShiftEnter' -count=1
go test ./tui -run 'SelectStartupTooltip|PrepareStartupTooltip' -count=1
```

## Verification

```text
go test ./tui/... -count=1       # ok (including #41917)
go test ./tui/tea -run 'TestAgentsNavigation' -count=1  # ok
go test ./tui/bottom_pane -run 'TestFooterAgentsNavigationHintLikeRust' -count=1  # ok
$env:CODEX_TUI_AGENTS_NAV_PTY='1'; go test ./tui/tea -run TestAgentsNavigationOpensOverviewPTY -count=1  # ok
go test ./tui/app -run TestRecoverVimInsertEscapeGatingMatchesRust -count=1  # ok
go test ./tui/tea -run 'TestVimEscape|TestLegacyVimEscape|TestShouldRecoverVimInsertEscape' -count=1  # ok
go test ./tui/chatwidget -run TestTranscriptOverlayHalfPageRoundsUpLikeRust -count=1  # ok
go test ./tui/chatwidget -run 'TestTurnRuntimeFinalizeTurnPreserves|TestTurnRuntimeErrorPreserves|TestTurnRuntimePolicyErrorPreserves' -count=1  # ok
go test ./tui/tea -run TestTurnInterruptionPreservesStreamedAnswerAndPlanLikeRust -count=1  # ok
go test ./app -run 'TestRemoteAgentsStopRespectsPaginatedHistoryLikeRust|TestRemoteAgentsDashboardReadsLastMessage' -count=1  # ok
go test ./tui/agents_overview -run 'TestAgentCenter|TestSingleLineViewportClips' -count=1  # ok
go test ./tui/chatwidget -run TestPersonalityOptOutOnlyLikeRust -count=1  # ok
go test ./app -run 'TestTUIRequestsForwardPersonalityOptOutOnlyLikeRust|TestRemoteConfigDropsRetiredPersonalityOverridesLikeRust' -count=1  # ok
go test ./tui/tea -run TestServerVersion -count=1  # ok
go test ./app -run TestShowServerVersionNoticeConfigLikeRust -count=1  # ok
go test ./tui ./tui/chatwidget ./tui/tea -run TUIMode -count=1  # ok
go test ./app -run 'TestInteractiveNoAltScreenResolutionLikeRust|TestInteractiveTUIModeConfigParsingLikeRust' -count=1  # ok
$env:CODEX_TUI_MODE_PTY='1'; go test ./tui/tea -run TestTUIModePickerPersistsNextLaunchPTY -count=1  # ok
go test ./tui/chatwidget -run 'VoiceQuietTurn|VoiceUserCaption|TypedInputStill' -count=1  # ok
go test ./tui -run TestStatusTimer -count=1  # ok
go test ./tui/tea -run 'TestStatusTimer|TestWindowsSandboxSetupStartsFresh' -count=1  # ok
go test ./tui -run ClockFormat -count=1  # ok
go test ./tui/history_cell -run FinalMessageSeparator -count=1  # ok
go test ./tui/tea -run TestPresetsLeaderChord -count=1  # ok
go test ./tui -run 'TestKeymapCatalogRustInventoryLedger|TestLeader' -count=1  # ok
go test ./tui/tea -run 'TestClipboardCopy|Copy|WarningsViewerKeep' -count=1  # ok
go test ./tui/tea -run 'TestModelViewRendersState|TestModelTerminalSnapshots|TestModelVT100TerminalSnapshot' -count=1  # ok
go test ./tui/exec_cell -run 'Dim|Display' -count=1  # ok
go test ./tui/tea -run TestDaybreakNotice -count=1  # ok
go test ./tui/bottom_pane ./tui/chatwidget -run 'FullWidth|Keymap' -count=1  # ok
go test ./tui/transcript_image -count=1  # ok
go test ./tui/tea -run 'TestTranscriptImage|TestGeneratedImage|TestViewedImage' -count=1  # ok
go test ./tui/history_cell -run TestImageHistoryCells -count=1  # ok
go test ./app -run 'TestRemote.*Image' -count=1  # ok
go test ./tui/tea -count=1       # ok
go test ./app -run 'TestDaemonConfigExclusion|TestDaemonStartupExclusion|TestDaemonStartupServerFeatures' -count=1  # ok
go vet ./tui/... ./app           # ok
gofmt -l <touched files>         # empty
go build ./...                   # ok
scripts/build.ps1                # ok; bin/codex.exe rebuilt
$env:CODEX_TUI_FOLLOWUP_PTY='1'; go test ./tui/tea -run TestFollowupDirectiveLabelsRenderPTY -count=1  # ok
$env:TMP='C:\t'; $env:TEMP='C:\t'; $env:CODEX_GO_EXE=(Resolve-Path 'bin\codex.exe').Path; go test ./tui/tea -run TestFollowupDirectiveLabelsRenderPTY -count=1  # ok
go test ./tui/bottom_pane ./tui/chatwidget -count=1  # ok
go test ./tui/tea -run 'AsyncQuestion|TurnCompleted|TurnInterrupted|QueuesPromptWhileRunning|RejectedSteer|Goal' -count=1  # ok
go test ./tui/status ./tui/tea -run Account -count=1  # ok
go test ./app -run 'AccountUpdated|RemoteAccountStatus|RemoteStatusAccount|AccountDisplay' -count=1  # ok
go test -race ./app -run 'TestAccountUpdatedRefreshesEmailLikeRust|TestRemoteAccountStatusFromAuthModeLikeRust' -count=1  # ok
go test ./tui/tea -run 'TestStartupTooltipLines|TestModelStartupSessionHeaderTooltip' -count=1  # ok
go test ./tui/chatwidget ./tui/tea -run SafetyBuffering -count=1  # ok
go test ./tui/bottom_pane/chat_composer ./tui/tea -run HistorySearch -count=1  # ok
go test ./tui/tea -run 'TestReview.*MCPStartup|TestReviewBlocked' -count=1  # ok
go test ./tui/bottom_pane/chat_composer ./tui/tea -run 'HistorySearch|TestSelectionViewsUseCompactPickerHint' -count=1  # ok
go test ./tui/tea -run 'TestModelStatusControlsSetupHistoryAndInvalidItem|TestModelThemeAndPetsPickersUseCurrentSelections' -count=1  # ok
go test ./tui/tea -run 'TestResumeCWD|TestConfigPanel|TestModelResumeCommand' -count=1  # ok
go test ./tui/... -count=1       # ok after resume-cwd and config-notice changes
go test ./app -run 'TestRemoteTUIResumeParamsResumeDirectoryLikeRust|TestInteractiveRemoteResumeSessionForwardsResolvedCWD|TestInteractiveRemoteSwitchAgentThread' -count=1  # ok
gofmt -l <touched files>         # empty
go vet ./tui/... ./app           # ok
go build ./...                   # ok
scripts/build.ps1                # ok
$env:TMP='C:\t'; $env:TEMP='C:\t'; $env:CODEX_GO_EXE=(Resolve-Path 'bin\codex.exe').Path; go test ./tui/tea -run 'TestTUIModePickerPersistsNextLaunchPTY|TestAgentsNavigationOpensOverviewPTY|TestFollowupDirectiveLabelsRenderPTY' -count=1  # ok
go test ./tui/bottom_pane/mentions_v2 ./tui/bottom_pane -run 'Mention|FileSearch|Popup|CommandPopup' -count=1  # ok
go test ./tui/... -count=1       # ok after #46694 result-identity changes
go test ./tui/bottom_pane/... -count=1  # ok after fixed frame, styles and skill tags
go test ./tui/tea -run 'SkillPopupCategoryTags' -count=1  # ok
go test ./tui/bottom_pane -run 'TestCommandPopupWrapBoundaryKeepsFollowingChoiceLikeRust|TestGenericRowsHideNarrowDescriptionColumnsWithoutStacking' -count=1  # ok
go test ./tui/tea -run TestSelectionViewsUseCompactPickerHintLikeRust -count=1  # ok
go test ./tui/bottom_pane ./tui/bottom_pane/mentions_v2 -run 'PickerAreas|MentionPopupFixedFrame' -count=1  # ok
go test ./tui/bottom_pane -run 'PickerRows|MultiSelect|SkillsToggle' -count=1  # ok
go test ./tui/tea -run 'QuickDialStatusSurfaceLayoutsNormalizeItems|QuickDialPrefillsUseCurrentSettings|QuickDialPrefillsDisableUnavailable' -count=1  # ok
go test ./tui/bottom_pane -run 'VerticalActionForAndApplyShared|MultiSelectVerticalActionBeatsReorder' -count=1  # ok
go test ./tui/tea -run 'TestTUIModePicker' -count=1  # ok
go test ./tui -run 'FollowupLabels|NormalizeCompletedAssistantMarkdown|HideUnfinishedFollowup' -count=1  # ok
go test ./tui/tea ./app -run 'MCPLogin' -count=1  # ok
go test ./tui/tea -run 'ConfiguredNavKeyWinsOverTranscriptScroll|DefaultNavKeyStillScrolls|CtrlSpace' -count=1  # ok
go test ./tui/tea -run 'AgentsNavigationOpensOverviewFromEmptyComposer|LegacyVimEscape|VimEscapeRecovery|StartupTooltipLines|StatusHeaderShowsPlanMode|StatusHeaderHidesPlanCycle|WindowsMappedShiftEnter' -count=1  # ok
go test ./tui -run 'SelectStartupTooltip|PrepareStartupTooltip' -count=1  # ok
go test ./tui/markdown -run 'TestRenderingListsPreferenceLikeRust|TestTaskListMarkersLikeRust|ListMarkers' -count=1  # ok
go test ./tui/tea ./tui/chatwidget -run 'ConfiguredKeyChord|ConfiguredGlobalChord|KeymapChordCapture|KeymapActionMenuShowsChord' -count=1  # ok
go vet ./tui/... ./app           # ok
go build ./...                   # ok
scripts/build.ps1                # ok
$env:TMP='C:\t'; $env:TEMP='C:\t'; $env:CODEX_GO_EXE=(Resolve-Path 'bin\codex.exe').Path; go test ./tui/tea -run 'TestTUIModePickerPersistsNextLaunchPTY|TestAgentsNavigationOpensOverviewPTY|TestFollowupDirectiveLabelsRenderPTY' -count=1  # ok
```

Production PTY check: `bin/codex.exe --no-alt-screen` rendered the composer,
accepted `/help`, and displayed `/ help` with the slash owned by the prompt
marker. After `/statusline model` and `/plan`, the status row displayed
`DeepSeek-Flash` followed by `Plan mode (shift+tab to cycle)`; the session then
cleared OSC 7501 state and exited normally. `/mcp login` produced
`Usage: /mcp [verbose | login <name>]`, confirming the new command branch in
the production binary. `/permissions` rendered the shared concise permission
descriptions with no trailing periods.

The final production ConPTY check used the existing deterministic follow-up
harness with `--no-daemon` and a short Windows temp root; it passed against the
rebuilt binary. The harness now also waits for the transcript `Tip:` row before
driving the follow-up directive, then sends Ctrl+R, enters a no-match query, and
cancels the live search. The generic slash parity harness was not usable from
this shell because its daemon temp socket exceeded `SUN_LEN`.

A full `go test ./app` run is currently red for unrelated tree/environment
reasons: other in-flight changes require absolute paths or private socket
directories, several tests launch under `TERM=dumb`, and cloud tests reject the
local HTTPS endpoint. The paginated-stop test and the neighboring dashboard
last-message test pass in isolation.

## Remaining

This closes the current-head regressions above, not the complete TUI backlog.
The r109 ledger still reports a large set of older TUI implementation gaps,
including the configuration panel, footer selection, and history-search
presentation. Those remain candidates for subsequent alignment rounds.

### #47929 / #48621 prompt-suggestion cancellation

Ledger-only audit, no code change. Go never implemented the follow-up prompt
suggestions added by `#47929` (no suggestion state, renderer, acceptance key or
generation path exists), so the feature had no behavior to remove. The later
`#48621` removal is therefore already reflected in Go.

- `#47929` is marked `not_applicable`; `#48621` is marked `complete`.
- `parity/commits.json` still parses (`ConvertFrom-Json` succeeds).

### #48562 consistent borderless session header

Rust renders one compact `>_ OpenAI Codex (vX)` / directory header for fresh,
resume, fork, and clear-screen sessions, drops the boxed model row from the
rich view, and keeps the raw header in raw output mode. Go already had the
borderless cell shape, but only inserted it at initial startup: `/clear`,
resume, and agent switches replaced the transcript without a header.

- `sessionHeaderCell` now centralizes model, reasoning effort, cwd, version and
  the YOLO indicator (approval `never` plus unrestricted full access).
- `/clear` and `/new` reinsert the header after resetting the thread, and
  resume plus agent switches prepend it before replayed messages. Stored raw
  lines keep the header correct when `/raw` is toggled afterwards.
- Daybreak notices now insert directly after the header on resumed threads.
- Narrow and halfwidth paths stay width-clamped through the existing
  center-truncate/ellipsis path. The Go brand `gcode` is the documented identity
  mapping for Rust's `OpenAI Codex`.

Regression coverage: `TestClearReinsertsBorderlessSessionHeaderLikeRust`,
`TestClearHeaderHonorsRawOutputModeLikeRust`,
`TestResumeAndAgentSwitchInsertBorderlessHeaderLikeRust`,
`TestSessionHeaderYoloIndicatorLikeRust`, the updated clear/Daybreak tests, and
the production `TestClearReinsertsHeaderPTY`. Full `go test ./tui/... -count=1`,
`go vet ./tui/... ./app`, `go build ./...`, a packaged build to
`C:\t\bin\codex.exe`, and the mode-picker/agents-navigation/follow-up ConPTY
set pass against that package.

### #46832 independent visual-effect controls

Go already loaded all six `tui.effects` booleans and exposed the same `/config`
Effects rows as Rust (`Animations` plus `Composer stars`, with stars unavailable
while animations are off). The missing runtime piece was the animated terminal
title: Go wrote a static title and never used the existing
`ActionRequired`/`ActionHidden` render options.

- The activity title spinner now advances under `tui.animations &&
  effects.progress`, matching Rust's 100 ms frame cadence.
- A blocked request renders `[ ! ] Action Required`; with animations and
  `effects.title` on, it blinks to `[ . ] Action Required` on the one-second
  phase. Disabling either switch keeps the static prefix.
- `StatusControlsState` now carries the spinner/action state into
  `TerminalTitleText`, so explicit title refreshes (`/title`, status-surface
  updates) keep the same animated state.

Welcome artwork, effort ignition, and the one-time Astra composer starfield do
not exist as Go renderers yet; they remain covered by their own upstream
commits (`#48513`, `#46096`, `#45137`) rather than this control commit.

Regression coverage: `TestTerminalTitleActionRequiredBlinkHonorsTitleEffectLikeRust`,
`TestTerminalTitleSpinnerHonorsProgressEffectLikeRust`,
`TestWorkingHeaderShimmerHonorsTheEffectLikeRust`,
`TestProgressEffectsGateLikeRust`, `config/tui_effects_test.go`, and the existing
config-panel editor test.

### Nearby ledger audits

Five previously pending entries were rechecked against the current tree:

- `#47908` `tui.whimsy` -> `tui.effects.starfield` is already implemented, with
  canonical-key precedence and strict-load acceptance (`complete`).
- `#45137` removed the multi-phase Astra sparkle; Go never carried that state,
  so the removal is already reflected (`complete`). The one-time starfield
  remains tracked by `#46096`.
- `#46859` limits the welcome logo animation to onboarding; Go has no welcome
  logo renderer at all (`not_applicable`, with `#48513` tracking the screen).
- `#52203` iTerm2 lifecycle status and `#52184` retained side conversations are
  implemented and covered by existing unit and ConPTY tests (`complete`).

`parity/commits.json` still parses after these edits.

### #46096 one-time Astra composer starfield

Rust arms a one-time deterministic starfield when a fresh Astra task is
confirmed with an untouched prompt. Go previously only had the
`tui.effects.starfield` preference with no renderer.

- `composer_sparkle.go` now arms on fresh sessions and model-picker changes to
  an Astra slug, and only while `tui.animations && tui.effects.starfield`.
- Empty composer cells receive the same hash-selected braille dots, with the
  placeholder and cursor cells protected. Existing ANSI/OSC sequences are
  copied intact.
- Ordinary draft input dismisses the opportunity; slash-command drafts hide the
  field without consuming it. The field fades during the final second and is
  finished at 15 seconds.
- A bootstrap thread that receives its model after the initial state reset
  re-establishes the fresh opportunity.

Regression coverage: `TestFreshAstraTaskShowsComposerStarfieldLikeRust`,
`TestComposerStarfieldHonorsEffectsAndModelLikeRust`,
`TestComposerStarfieldDismissalRulesLikeRust`,
`TestComposerStarfieldExpiresLikeRust`,
`TestComposerStarfieldArmsWhenModelBecomesAstraLikeRust`,
`TestComposerStarfieldSurvivesBootstrapThreadStart`, and production
`TestComposerSparkleRendersPTY`.

### #48513 fullscreen welcome blossom

Rust's fresh-session welcome screen centers a deterministic Codex/OpenAI
blossom in unused fullscreen space, shows a settled dim pose, and replays one
spin when clicked. Go previously had no welcome renderer.

- `tui/welcome` ports the signed-distance fields, Braille projection and studio
  lighting from Rust. A frame was checked against the expected blossom shape.
- `tui/tea/welcome_blossom.go` reclaims the transcript viewport's trailing blank
  rows, centers the stage above the composer, and respects
  `tui.animations && effects.welcome`, empty-input, overlay and space gates.
- The settled pose uses the upstream 0.18 opacity blend; clicking the stage
  restarts one spin and fades back to the settled pose.

Regression coverage: `TestBlossomFrameProjectsCells`,
`TestBlossomMorphChangesPattern`, `TestBlossomFrameClampsToRendererBounds`,
`TestFreshFullscreenShowsSettledBlossomLikeRust`,
`TestWelcomeBlossomEligibilityLikeRust`,
`TestWelcomeBlossomClickReplaysLikeRust`, and production
`TestWelcomeBlossomRendersPTY`.

Startup tooltip rows are now tagged `StartupTip` and removed from the
fullscreen transcript while remaining in `State.Messages`, raw output and
inline scrollback. Greeting preservation is obsolete after `#49395`; Go has no
startup-draft pump or in-TUI daemon-startup phase, so those sub-behaviors are
N/A by architecture.

### #52270 footer/status rendered selection

Added `rendered_selection.go`: it snapshots painted status/footer rows, maps
mouse drags to terminal cells, extracts Unicode text, preserves ANSI/OSC
sequences while highlighting, copies on release for `copy_on_select`
auto/always, keeps the selection for Ctrl+C when copy-on-select is disabled,
invalidates on region content/geometry changes, ignores stationary clicks, and
never touches the composer draft.

`Options.usesOwnedScreen` now enables Bubble Tea's alternate screen and
cell-motion mouse reporting for the explicit `tui.alternate_screen = "always"`
mode, making the handlers live in the packaged TUI. Go's `auto`/`never` modes
remain inline with terminal-owned selection and native scrollback, which is the
documented Go architecture mapping. Production
`TestOwnedScreenFooterSelectionPTY` verifies the packaged binary enables mouse
reporting and paints a reverse-video footer selection after a drag.

### #52778 clickable pinned transcript prompts

`tui/tea/pinned_prompt.go` now replaces the first visible transcript row with
the nearest preceding user prompt when that prompt has scrolled out of view.
The target is computed from per-message line ranges, so prepended history does
not shift the jump destination. A left click cancels find, clears activity
focus and rendered selection, and scrolls the original prompt into view. The
header yields while the prompt itself is visible and below 16 columns.

Regression coverage: `TestPinnedPromptHeaderJumpsToOriginalPromptLikeRust` and
`TestPinnedPromptHeaderVisibilityLikeRust`.

### #46040 screen-reader animation default

`app/screen_reader_animation.go` now probes once at local/remote TUI launch with
a 450ms bound. A present `tui.screen_reader_detection_done` key (either value)
skips detection; an explicit `tui.animations` preference always wins. When a
screen reader is detected and animations are unconfigured, the session default
becomes false and both keys are persisted through a comment-preserving `[tui]`
text edit. Save failures keep the session default non-fatally.

Platform probes: Windows SPI_GETSCREENREADER plus Narrator in the current login
session; macOS VoiceOver process; Linux Orca/speech-dispatcher or the GNOME
screen-reader gsettings flag.

Regression coverage: `TestScreenReaderMarkerSkipsDetectionLikeRust`,
`TestScreenReaderDetectionSeedsAnimationDefaultLikeRust`,
`TestScreenReaderDetectionPreservesExplicitAnimationsLikeRust`,
`TestScreenReaderDetectionTimeoutLikeRust`,
`TestScreenReaderPersistenceFailureKeepsSessionDefaultLikeRust`, and
`TestScreenReaderPersistenceRejectsInlineTuiTable`.

### Nearby audits

- `#46530` already gates reasoning-effort `configuration_update` items on
  explicit model support while leaving history intact; the targeted request
  filter test passes (`complete`).
- `#44198` is satisfied structurally: the starfield only decorates
  `composer.View()`, while voice strip rows are rendered separately and keep
  their styles (`complete`).
- `#53156` only simplifies Rust's custom terminal wrapper API; Go uses Bubble
  Tea's `View`/`Program` and has no equivalent abstraction (`not_applicable`).
- `#43699` already preserves split-flap tile arrivals across the 1,024-byte
  sliding transcript window and starts fresh on speaker/text mismatch
  (`complete`).
- `#44276` and `#44285` are N/A: Go has no suspended-turn replay/early-rollback
  state machine or runtime effort pin to recover or deduplicate.
- `#46531` is N/A: Go has no ephemeral title-worker session and memory
  consolidation sends its request-level effort directly, bypassing the managed
  override machinery.
- `#52968` is N/A: Bubble Tea owns foreground input decoding; there is no
  startup input-drain timeout that can abort modal handling.

### #53173 managed daemon restart

`/daemon` now offers `Restart background server` for a verified managed local
endpoint. Verification uses LifecycleVersion and requires a non-nil backend.
The confirmation warns about interrupted work in other chats, sets
`PendingDaemonRestart`, exits the program, runs LifecycleRestart synchronously,
and re-enters the remote TUI to reconnect. Unmanaged servers show the upstream
disabled reason. The first managed-daemon startup configuration warning also
adds `restart with /daemon` to the summary while preserving the diagnostic in
raw/transcript; centered Command Center dialogs already use the backdrop
renderer from `#53804`.

### #53496 pending Effects toggles across save refreshes

Rust's Effects picker toggles checkboxes immediately, keeps newer choices when
an older save completes, and uses the shared list toggle handling for Space.
Go already updated the live values before saving and ignored all but the newest
`SettingsWriteResultMsg` request ID; the missing pieces were Space handling and
regression coverage for the pending-value behavior.

- Space now toggles the highlighted Effects row. A configured list binding for
  Space takes precedence: binding it to cancel returns to the main panel
  without changing the preference.
- Rapid Animations toggles keep the newest live choice; a stale save result
  with an older value is ignored.
- Composer stars retains its underlying value while Animations disables the
  row, shows `Unavailable`, and returns to the preserved On/Off state when
  animations are re-enabled.

Regression coverage: `TestEffectsSpaceTogglesSelectedRowLikeRust`,
`TestEffectsSpaceHonorsConfiguredCancelBindingLikeRust`,
`TestEffectsRapidTogglesIgnoreStaleSaveResultLikeRust`,
`TestEffectsComposerStarsPendingValueSurvivesDisabledRowLikeRust`, and the full
`go test ./tui/... -count=1` suite.

### More ledger rechecks

- `#51471` pending input previews already link web URLs in pending, rejected and
  queued sections with their complete OSC 8 destination (`complete`).
- `#51472` generic selection rows already link labels/descriptions, keep the
  destination through wrapping/truncation, and leave the ellipsis unlinked
  (`complete`).
- `#52228` Daybreak already requires `api_key_cyber_access_programs` for known
  API-key accounts and refreshes eligibility on new/resumed turns
  (`complete`).
- `#53201` unavailable/read-only threads already keep Left-arrow agents
  navigation when the composer's move-left binding is intact, while a remap
  leaves Esc as the command-center key (`complete`).
- `#53804` archive/delete progress already renders as a centered dialog over the
  retained Command Center with the selected task and scroll/filter state
  intact (`complete`).
- `#53148` is `not_applicable`: Go has no startup-draft pump or recovery
  snapshot, while live Vim Replace semantics are implemented and tested.
- `#54048` is `not_applicable`: Go resolves folder trust through the config
  layer and never performs the interactive startup consent step that the
  upstream change defers.
- `#50375` already converts the finished terminal title to printable ASCII under
  GNU Screen (STY), maps spinner/status glyphs, and preserves Unicode elsewhere
  (`complete`).
- `#49861` already exposes Daybreak as a status-line and terminal-title item
  backed by the live thread preference (`complete`).
- `#40618` title animation now refreshes through the foreground update loop and
  writes only deduplicated OSC sequences, without forcing a transcript redraw
  (`complete`, together with the #46832 implementation).
- `#50727` task details already place Model and Reasoning before project,
  usage and activity fields, with `Unknown` and ellipsis handling (`complete`).
- `#51463` sub-agent activity already carries model/reasoning fields and the
  legacy JSON shape decodes with nil settings; a new protocol test pins this.
  Full persisted/paginated history migration coverage remains pending
  (`regression_pending`).

Targeted tests for the implemented items pass, and the full TUI suite remains
green.

### Additional ledger rechecks

- `#50045` owned-transcript Find expands only the current match and reveals
  transcript-only matches (`complete`).
- `#50199` account-status updates restore the displayed email/plan and API-key
  fallback (`complete`).
- `#50433` API-key accounts can use Daybreak when the API-key cyber gate is on
  (`complete`).
- `#54380` `/presets` quick dials, persistence, shortcuts and
  unavailable-before-startup handling are implemented (`complete`).
- `#48626` is N/A: Go never rendered previous-session usage/resume summaries.
- `#47422` turn-end recovery of unsent async-question drafts is already
  implemented (`complete`).
- `#48761` compact activity line counts and configurable transcript hints are
  already implemented (`complete`).
- `#50345` subagent picker archive visibility/selection is already implemented
  (`complete`).
- `#49472` server-authoritative permission profiles and presets are already
  implemented (`complete`).
- `#49857` is complete: cyber-refusal guidance is selected from the current
  model's catalog access programs, preferring the refreshed connected catalog.
  Astra with standard access and no Daybreak gets the Astra copy, advertised
  Daybreak access gets the limited copy, and missing metadata or non-OpenAI/
  non-ChatGPT sessions get application guidance. The old account-scoped
  `/accounts/verified_access` prefetch and cache were removed.
- `#49105` is N/A: Go does not implement Rust's in-process reconnect with a
  recovered input queue; daemon restart re-enters with a fresh model.
- `#50781` is N/A: Go MCP startup updates are local process events without a
  thread id, so another thread's startup state cannot leak into this TUI.
- `#49859` is complete: model catalog cyber programs reach normal turns, goal
  continuations, embedded ExecOptions and remote turn/start params;
  `/daybreak` validates and persists the thread preference; and cyber refusals
  offer a non-retrying Enable Daybreak picker for eligible live primary threads.
- `#49858` is complete: `/daybreak` persists through local/remote metadata,
  selects standard/daybreak_blue/daybreak_red correctly, follows
  account/catalog eligibility and cli_daybreak gates, updates status/header
  surfaces across resume/new/clear/agent switches, and both local and remote
  fresh-thread starts carry the configured default when the feature is enabled.
- `#46867` is complete: activity arriving during an assistant stream is
  deferred and flushed after the final answer or at error/interruption/turn end.
- `#50109` is complete for the owned-screen TUI: the composer grows with the
  draft, caps the painted section at two thirds of the screen (minimum eight
  rows), paints hidden-row arrows, and lets the wheel browse three prompt rows
  per event without moving the caret or transcript selection/search/activity
  focus. Key input, paste, external draft edits, and resizes resume or clamp
  caret following. Inline mode keeps the fixed three-row composer; remote-image
  row selection is not applicable because Go lists attachments on one line.
- The URL-link batch is complete: `#51449` async-question titles, `#51450` MCP
  elicitation prompts, `#51451` warnings viewer, `#51452` wrapped banner links,
  `#51457` status usage links, `#51458` user-verification title/details, and
  `#51459` Windows sandbox help links all retain complete OSC-8 destinations.
  `#51458` needed a new verification marker on the modal so its title and body
  are wrapped and annotated through the same URL-preserving path as the form.
- `#50564` is not applicable: Go keeps transcript body selection terminal-owned,
  and opening a bottom modal neither disables mouse reporting nor suppresses
  modifier-drag selection. App-owned status/footer selection is independent of
  the modal, and modal wheel scrolling is already covered.
- `#50788` is complete: an unmodified `/` on an empty Vim Normal draft switches
  to Insert and opens slash completion, while nonempty drafts, `?`, and pending
  operators keep Vim search.
- `#53152` is complete: working and completion tip rows are registered as
  read-only selection regions. Copy-on-select, retained highlights, the
  `Selecting text · esc cancel` hint, Esc cancellation, and invalidation when a
  working tip disappears are covered.
- `#53754` is partial: config normalization accepts `super`/`cmd`/`command`,
  labels and leader/chord validation understand super, and the macOS-style
  super-left/right/backspace defaults are present. Live Super key dispatch
  remains blocked on Bubble Tea v1 exposing no Super modifier and dropping
  unknown CSI-u input.
- `#51235` is complete: model picker labels append only `(current)`, the
  catalog default remains available for preselection, and the workspace-owner
  notification prompt keeps its explicit `No (default)` row.
- `#51510` is complete: failed configuration reloads keep the live staged
  preference set and launcher-owned alternate-screen state; only a successful
  reload replaces them.
- `#50811` is complete: explicit launch/profile reasoning-summary and
  concurrent-summary choices (including disabled values) reach thread/start,
  while implicit client values no longer override server/model defaults.
- `#50913` is complete: connected fresh starts read destination configuration
  before bootstrap, preserve explicit overrides, clear unsupported implicit
  client settings, use the correct launch/server directory, and allow managed
  new-thread defaults to supply a model when the catalog is empty.
- The Vim composer batch is complete: `#41586` search motions/operator
  searches/query footer, `#41941` bounded undo, `#42140` redo ordering, and
  `#42194` replace mode (including Backspace recovery, prompt-prefix
  absorption, and dot-repeat) all have passing regression tests.
- `#39873` is complete: previous/next permission-mode shortcuts are configurable,
  session-only, exclude unavailable modes, require the existing full-access
  confirmation, reject printable bindings, and are stripped from project config.
- `#40622` is complete: an active goal owns a deduplicated status tick that
  updates elapsed time and only requests another frame when the label changes.
- `#53753` is partial: composer drag selections map wrapped rows to source
  rune ranges, copy through the shared clipboard path, retain their editable
  highlight, support Ctrl+C, and are replaced by typing/paste/Enter/Delete.
  Esc, focus loss, and external edits clear them. Go has no X11 PRIMARY
  publication/ownership path, which is the remaining part of the upstream
  `SelectionCopy` behavior.
- `#39997` is complete: `/copy` offers the whole latest response plus fenced
  code blocks and nested blockquotes with labels/previews, and copies exact
  source text through the async clipboard path.
- `#39999` is complete: Fast mode status is omitted for catalogued models that
  do not support it, while supported and uncatalogued models keep Fast on/off.
- `#40068` is complete: compact `/mcp` output reports runtime connection state
  and tool counts across the full status matrix, with verbose inventory and
  legacy authentication fallback retained.
- `#40166` is not applicable: Go delegates cursor positioning/visibility
  ordering to Bubble Tea's renderer instead of owning a custom terminal draw
  implementation.
- `#39992` is complete: keymap action descriptions are single binding-aware
  values rather than selection-dependent swaps, so navigating the menu leaves
  its content stable.
- `#40720` is complete: composer URLs are cached by draft value and mapped
  through grapheme-aware source offsets, so every wrapped or scrolled fragment
  carries the complete OSC-8 destination in owned-screen and inline layouts.
  Selection highlighting survives annotation, and destinations over 8 KiB are
  left unlinked.
- `#41937` was implemented but is superseded by `#42107`: the preview limit and
  transcript hint were intentionally removed, and Go now renders complete
  background-terminal input inline, in the transcript, and in raw output.
- `#41143` was implemented but is superseded by `#42107`: `CreateDiffPreview`
  and its row/omission behavior were removed, and patch history now renders the
  complete diff inline.
- `#40502` is complete: server `instructionSources` now populate the
  `/status` Agents.md summary for local and remote starts, resumes, and agent
  switches. Paths inside or one level above cwd stay project-relative, other
  home paths collapse to `~`, and partial settings updates leave the summary
  intact.
- `#40751` is complete for Go's content-rebuild pager model: SetContent keeps a
  scrolled reading position or follows the tail when already at bottom, and
  resizes preserve both behaviors. Regression tests cover repeated height
  changes and history replacement.
- `#41285` is complete: keymap conflict validation walks the shared action
  registry in declaration order, so the first reported context/action conflict
  is deterministic while existing error wording stays unchanged.
- `#41976` is complete: `tui.disable_paste_burst` is the preferred setting,
  the top-level key remains a fallback, and CLI overrides preserve layer
  precedence over the file's `[tui]` value.
- `#42107` is complete: patch history and background-terminal input render
  complete content inline with no preview row/byte limit or transcript hint;
  inline, transcript, and raw views agree.
- `#42118` is complete: running hooks fold into the working row with
  animation-gated shimmer when they fit, reflow to a separate activity line
  when too wide, and work outside an active turn. Per-run 300ms reveal delays,
  shared concurrent summaries, context suppression, and quiet-success
  suppression match the upstream lifecycle.
- `#42319` is complete: live context compaction tracks active id/start time,
  renders elapsed animated activity, survives transient retry status, restores
  from startedAt snapshots, clears on matching completion/turn end/reset, and
  writes the completion marker.
- `#41940` is not applicable: Go's pager overlay renders one content string
  rather than Rust's per-cell renderables with cached heights, so backtrack
  highlight changes have no unrelated cell-layout cache to preserve.
- `#41911`, `#41916`, and `#41918` are not applicable to Go's lifecycle: the
  TUI does not implement Rust's out-of-process reconnect/read-only snapshot
  loop. Remote failures return to the launcher and managed-daemon restart
  re-enters with a fresh model, so there is no reconnect draft, unavailable
  thread snapshot, or cached command-center selection to restore.
- `#41913` is complete: the pause-aware status timer lives independently of the
  optional working row, survives hiding/restoring, pauses for approvals, resets
  for new turns and Windows sandbox setup, and supplies the final duration
  fallback.
- `#40494` is complete: remote TUI notifications for ephemeral `system` and
  `thread_title` feature workers are dropped before active-thread selection or
  `thread.started` emission, so they cannot enter visible routing or request a
  command-center refresh. Persisted system threads and ephemeral subagents
  remain visible. Generated-title parsing now strips wrapping quotes and
  trailing `.?!`, preserves meaningful leading punctuation such as `.NET` and
  `!important`, rejects unknown fields, and truncates by rune.
- `#42202` is complete: local TUI preference staging/reload/trust behavior stays
  independent of server thread settings. Remote initialize metadata retains
  `platformFamily`/`platformOs` verbatim, Windows sandbox readiness follows the
  server OS, and the account response's `requiresOpenaiAuth` now gates the
  `/status` usage link instead of relying on the provider name when the server
  value is available.
- `#52226` is complete: iTerm2 OSC 21337 includes the foreground activity
  header, omits the generic `Working` label, and leaves detail empty for idle,
  waiting, and background-only work. Detail sanitization, the 240-character
  bound, OSC escaping, and detail-aware emission caching are covered.
- `#42204` is not applicable: Rust projects a receipt-verified macOS
  GStreamer/Mach-O runtime prefix. Go's voicehost package contains a Go-built
  helper and Opus codec and has no native receipt, Mach-O relocation, or ad-hoc
  signing pipeline to mirror.
- `#52725` is complete: OSC 7501 reports Codex lifecycle state on any TTY,
  while direct iTerm2 keeps OSC 21337 and multiplexers receive generic status
  only. Blocked, exit clear, focus/resize re-emission, and cache deduplication
  are covered. External editors now clear program status before the command
  runs; the normal update path re-emits it on return.
- `#47424` is complete: turn completion, interruption, failure, and the error
  path all drop a pending async-question notification before recovering answer
  drafts. Drafts are consumed once in order, the main composer draft and text
  elements survive, and higher-priority approval notifications remain queued.
- `#47417` is complete by Go's screen-mode architecture: `auto` remains inline
  for every terminal, including Terminal.app over SSH, so native scrollback is
  always retained. `always` opts into the owned screen, while `never` and
  `--no-alt-screen` keep their explicit overrides; no Apple-specific device
  attribute probe is required.
- `#45262` is complete: an active Ctrl+R history search receives bracketed
  paste text before ordinary composer paste handling. Nonempty text appends to
  the query and restarts matching; empty pastes preserve the selected match;
  newlines/tabs remain visible in the footer; large text and image paths become
  search text without creating attachments.
- `#45549` is complete: streamed answer and plan source is retained through
  interruption, failure, and normal completion, including narrow-width reflow
  and raw-output mode. The termination paths no longer discard already-received
  content before transcript finalization.
- `#52967` is complete: WSL clipboard reads reuse one framed PowerShell process,
  preserving whitespace, empty text, UTF-8, and large payloads without spawning
  per paste. Reads obey a deadline, failed processes restart on demand, SSH
  bypasses the WSL path, and eligible owned-screen sessions prewarm the reader
  without touching clipboard contents.
- `#49809` is complete for Go's session model: fresh remote TUI sessions carry
  the latest live approval/sandbox permissions over the original launch values,
  so `--yolo` persists until a later picker choice replaces it. Constrained
  profiles stay server-owned, and Rust's reconnect reviewer restoration has no
  Go carrier because Go has no in-process reconnect loop.
- `#49836` is complete: `audio.microphone_channel` accepts scalar/list
  one-based channels, the TUI exposes the input-channel picker for multichannel
  devices, saves/clears selections locally with override diagnostics, and the
  voice helper mixes only the selected channels with fail-closed validation.
- `#48205` is complete: closing the warnings viewer dismisses only rendered
  pages, changed details reappear, and transcript reset now clears retained
  warning cells, dismissal decisions, cached counts, and any open viewer so the
  same diagnostic can appear in the next transcript.
- `#47321` is complete for Go's timestamp surfaces: status/reset times,
  backend banners, reset credits, history separators, and expanded resume
  details all use the detected system clock preference. Go has no analytics
  view, so Rust's analytics-only UTC clock branch has no separate carrier.
- `#47639` is complete for Go's selection surfaces: `copy_on_select`
  auto/always/never, terminal defaults, config-panel persistence, composer and
  passive-row copy-on-release, highlight retention, and conditional
  clear-after-confirmed-copy are implemented. Transcript/overlay selection
  remains terminal-owned, so Rust's app-owned transcript view has no direct
  carrier.
- `#47191` is complete: Rust's private `display_sum` helper (now
  `display_operator`) needs no separate Go symbol; Go's parser owns
  sum/bigwedge limits and the structured layouts directly. Display-only
  `aligned`/`boxed`, stacked limits, `underset`/`overset`/`substack`, uppercase
  `mathcal`, `widehat`, grouped scripts, and bounded fallback are covered by
  exact layout assertions for fraction geometry, boxed optimization rows, and
  stacked limits. Rejected or too-wide display spans now also restore their TeX
  source verbatim, so narrow fallback keeps `\[`, `\]`, and row separators
  instead of letting Markdown escaping strip them.
- `#46845` is complete: completion footers use the detected system clock
  preference for live and restored turns. Detection now matches Rust's locale
  precedence and native-pattern rules, including quoted literals, the first
  Windows alternative, C/POSIX locales, unavailable locales, and glibc
  `T_FMT` parsing. `linux+cgo` uses an independent `newlocale`/
  `nl_langinfo_l` lookup; CGO-disabled and other platforms keep a documented
  fallback and retain 12-hour output when the preference is unknown.
- `#50467` is not applicable: Rust's `MarkdownSelection`/`literal_selection`
  path belongs to its app-owned transcript selection, which serializes
  Markdown for rich HTML while copying visible cells literally. Go's
  in-transcript selection is terminal-owned, so no Markdown selection payload
  exists; its app-owned status/footer selection writes plain text. Whole-response
  copy keeps Markdown source plus HTML, matching Rust's unchanged
  `CopyFormat::Markdown` path.
- `#46884` is complete for Go's terminal-owned links: bare URLs now borrow the
  Markdown link color and underline in prose, long-URL restoration, and tables,
  with punctuation, tabs, Unicode, and surrounding emphasis preserved and
  code-block URLs left alone. Clicking is handled by the terminal through
  OSC-8 hyperlinks, so Rust's app-owned stationary-click state machine has no
  separate Go carrier.
- `#46864` and `#46866` are not applicable: Rust's analytics usage dashboard
  (charts, report tabs, responsive layouts, per-report reading positions, and
  mouse hit targets) has no Go counterpart. Go's `/usage` is a limit-state and
  reset menu without report surfaces, scroll offsets, or tab/control rectangles.
- `#46895` is complete for Go's app-owned composer selection: right-clicking
  inside the selected editable text copies it without pasting, preserves the
  draft and cursor, and clears the highlight only after a successful clipboard
  write; failures retain it. Ctrl+C now also retains the selection, matching
  Rust. Transcript right-click copy has no carrier because transcript selection
  remains terminal-owned.
- `#46899` is complete: streaming lists stay compact; completed
  assistant/reasoning/plan lists separate every sibling when any item wraps;
  nested/quoted/code-item groups and list-item paragraphs retain their
  structure; resizing recomputes separators; and completion redraws anchor the
  reader's top visible line. Native-scrollback AfterMultiline is not applicable
  because Go's inline transcript still repaints app-owned rows.
- `#46902` is not applicable: Go has no rendered "Back to bottom" follow
  control; transcript navigation is key-driven and following is tracked by the
  viewport's bottom state.
- `#46929` is not applicable: Go does not emit Rust's
  "Error loading config.toml:" startup message, so there is no prefix to
  remove.
- `#46931` is complete: the composer-gap layer is implemented:
  fullscreen idle/empty input selects a stable keymap-resolved catalog tip,
  Markdown-renders it with OSC 8 links, requires a single fitting row, and
  yields to usage notices, warnings, drafts, running turns, and interactions.
  Ctrl-click now resolves OSC 8 ranges and opens them through the app URL
  opener; Super-click is unavailable in Bubble Tea v1. A host-supplied startup
  announcement is preferred until the first user turn and then permanently
  dismissed, with the app prewarming and caching announcement_tip.toml.
  fullscreen composer-gap catalog rotation, announcement lifecycle, single-row
  fitting, and composer-gap link hit testing are not ported.
- `#46910` is complete: settings pickers no longer disturb the reader's
  transcript offset, while settings-write, model-selection, memory-reset, and
  keymap outcomes resume following so confirmations and errors are visible.
  Picker anchoring and failure visibility have regression coverage.
- `#46938` is complete: Mermaid, math, tables, and lists have independent
  rendering preferences, default to enabled, reload from live settings, and
  preserve their Markdown source when disabled; coverage includes fence/table
  source preservation and math gating.
- `#46946` is complete: dynamic-tool lifecycle metadata immediately registers
  missing root rows and folds children into loaded roots, while archived/deleted
  tombstones block delayed responses and clear on thread start/unarchive.
- `#47096` is complete: owned/fullscreen layouts place the hint and
  reading-control row immediately above the composer, reserve preview/HUD rows
  when sizing the transcript, and clip the hint → HUD → activity → preview in
  that order when the terminal is still too short while keeping composer/status
  visible. The composer gap now also renders the quota usage notice with Rust's
  half-row/compact width policy, thresholds, credits suppression, and reset
  details.
- `#46912` is complete: the quota notice stays visible in the composer gap while
  typing and running, honors interaction/backend-banner gates, merges sparse
  rolling windows and metadata, accepts recovery only when the reset window
  advances, and clears quota/plan/warning state on account changes. Go has no
  separate account-usage read request-id path, so stale-read rejection is not
  applicable.
- `#41467` is complete: the model picker refreshes from the app server with
  stale-request rejection, catalog constraints, in-place picker/reasoning
  refresh, and fallback when the current model disappears.
- `#42791` is not applicable: Go has no message-history metadata/log subsystem
  whose app-server lifecycle response could disagree with local settings.
- `#42847` is complete: whole-response copies provide Markdown plain text plus
  rich HTML, and clipboard HTML now escapes raw HTML, keeps image alt text
  without sources, and renders non-web link destinations as visible text.
- `#47082` is not applicable: Go's live tea TUI has no shortcut-overlay mode;
  the footer overlay helper is test-only.
- `#48776` and `#48800` are complete: task rows omit the stale `current`
  badge, and ordered Markdown markers use the terminal LightBlue palette with
  the marker/text split covered by regression tests.
- `#48805` is complete: a modal keeps keyboard ownership while wheel input over
  the transcript still scrolls it; completion popups retain every gesture.
- `#48352` is complete: working and completion turn tips use the shared
  cadence, leading spacer, dismissal, and selection behavior.
- `#47319` is complete: the selected agents-overview row applies its cyan/bold
  selection style to the status symbol, while unselected symbols keep group
  colors.
- `#47382` is complete: the owning root task shows a styled `voice` badge that
  clears on shutdown, and refresh folds descendant thread ids so a child-owned
  voice conversation badges its root task.
- `#47441` is not applicable: the live tea footer has no appended shortcut-hint
  suffix to style.
- `#48623` is complete: empty ordered/nested/blockquoted list markers are
  preserved, and a trailing bare marker remains mutable in the streaming tail.
- `#47847` remains partial: clipboard writes now reject concurrent copies,
  match request ids, and block busy pastes, but Go still lacks Rust's dedicated
  session worker with setup expiry and bounded shutdown/ownership handoff.
- `#47360` is complete: user prompt URLs retain full OSC-8 destinations across
  adaptive wrapping and forced splits, including parenthesized URLs, with
  width-bound regression coverage.
- `#47399` is not applicable: Go does not enable terminal mouse capture, so
  tmux retains control of its mouse policy.
- `#47717` is not applicable: Go's model picker resolves through modal state
  rather than Rust's recursive AppEvent dispatch.
- `#47384` is not applicable: Go has no rendered `esc latest` footer to leave
  stale after a transcript selection clears.
- `#47896` and `#49153` are not applicable: Go's in-transcript selection is
  terminal-owned, so there is no semantic Markdown selection serializer or
  blockquote-marker stripping path.
- `#49112` remains partial: X11 PRIMARY publication/ownership and middle-click
  paste are still absent, consistent with the existing transcript-selection
  terminal-ownership gap.
- `#47975` is complete: pending voice speech carries its input generation and
  awaiting-turn fallback, so recovery drops answers superseded by newer input
  while retaining unspoken text for an unsettled turn.
- `#48116` is complete: reasoning shortcuts raise through Max in normal and Plan
  modes, while Ultra still routes to the explicit picker guidance.
- `#48118` and `#48469` are complete: configurable right-click paste and
  terminal-aware copy-on-select behavior are implemented with config,
  eligibility, delivery/invalidations, and selection tests.
- `#48548` is not applicable to terminal-owned transcript selection; Go has no
  table-selection copy metadata path.
- `#48628` and `#48757` are complete: blank dashboard sessions are retained and
  cleaned up correctly, and working-header shimmer timing/settings match the
  desktop behavior.
- `#49073` is complete: realtime voice catalog failures stop picker/startup,
  close the helper before sending a start request, and report a saved-but-
  unconfirmed preference without changing in-memory voice settings.
- `#48560` is complete: an already-painted working tip survives transcript
  interaction/scrolling, while initial and completion tips still require an
  idle following viewport.
- `#49136` is complete: Option glyphs attach directly to the key (`⌥t`,
  `shift+⌥t`) while text modifiers retain `+`.
- `#49144` and `#49145` are complete: launch-scoped reasoning summary/verbosity
  overrides preserve server defaults, and `/status` hides the summary setting
  when a server/daemon owns the session.
- `#48830` is complete: interrupted turns show the short neutral feedback notice
  with secondary styling; budget-limited turns keep the error notice.
- `#49043` is not applicable at current Rust head: the Pro Extra/Standard/Max
  labels were superseded and Rust again uses Pro 200/100/500, matching Go.
- `#49395` is complete: session headers no longer bind randomized greeting state
  and consistently expose model/directory fields.
- `#49564` is not applicable: terminal-owned transcript selection copies literal
  displayed path text without a Markdown-selection serializer.
- `#49810` is not applicable: Go inserts burst runes immediately and has no held
  paste buffer to flush before Enter; expired windows submit directly.
- `#49816` is complete by behavior: URL openers add errors but no browser-open
  success history entry.
- `#50215` is complete: Bubble Tea's undecoded Ctrl+Insert sequence is mapped to
  app-owned selection copy, with confirmed composer copies clearing selection.
- `#50359` is complete: hook system-message ANSI SGR is parsed into styled lines
  with prefixes, indentation, combining characters, and trailing blanks intact.
- `#50503` is complete: Enter accepts Find results, Ctrl+P/Ctrl+N navigate, and
  Escape cancels editing or returns to latest after acceptance.
- `#50504` remains partial: confirmations still replace the bottom-pane section
  instead of rendering as centered dialogs over a retained composer/picker
  backdrop.
- `#50396` is complete: transcript PageUp/PageDown honor pager single-row,
  half-page, full-page, and unbound bindings.
- `#51433` is complete: hook browser rows use trimmed status-message titles with
  aligned dim indices, trust suffixes, and wide-title-safe selection styling.
- `#50431` is complete: agents-overview previews retain OSC-8 links through
  wrapping/truncation while ellipses stay unlinked.
- `#50434` is not applicable: Go's transcript selection is terminal-owned; no
  app-owned transcript copy-mode state machine exists.
- `#52330` is not applicable: Go has no wrapped LogicalLineSource remapping path
  that can index past the flattened source.
- `#52557` is complete: Herdr, tmux, and SSH copies forward OSC 52 to the
  attached terminal while preserving a successful native clipboard lease.
- `#51207` is complete: `cli_daybreak` defaults off, gates live preference and
  thread/start staging, hides `/daybreak` from the slash catalog when disabled
  or account-ineligible, and keeps typed dispatch resolvable with a silent
  no-op under the gate.
- `#48775` is not applicable to Go's spoken-caption architecture: pinned headers
  already add `› ` and truncate by display width, while spoken user input is a
  separate SpokenHistoryCell rather than a spoken RoleUser prompt.
