# Draft shell mode across resume (#61216)

Actual TUI resume-picker and dashboard capture paths stored only draft text. setComposerPrompt then inferred a leading ! as shell mode, converting a literal bang on restore. Added per-thread mode capture and pending-restore mode; both resume picker and dashboard restore the captured mode, remove consumed state, and clear pending mode on cancellation/new draft. Existing text-only entries retain their previous inference fallback.

Regression: a literal !echo original draft round-tripped through the real picker selection path failed before the fix (shell=false became true). Both literal and shell drafts now pass picker round trips and dashboard switch command/result handling. Related dashboard/paste/bang regressions pass (0.439s).

Full `go test ./tui/tea -count=1 -timeout 5m`: exit 0, 59.920s. `go vet ./tui/tea`: exit 0. Logs: resume_shell_red_2026_11_14.log, resume_shell_green_2026_11_14.log, resume_shell_full_2026_11_14.log.

#61183 integration audit: app/remote_tui.go ThreadStarted forwards protocol.ThreadStarted(threadID) only; tui/tea does not currently consume the tui/app ThreadSessionState fork-parent cache. The previous reconstruction helper fix is not full production metadata integration. Remains partial.

#61216 remains partial: full draft/queued-input ownership, outgoing event draining, interrupted retry handling, missing-receipt gating, and Cloud shell rejection still require implementation/audit. No whole-module final certification or commit/push. Overall synchronization remains incomplete.
