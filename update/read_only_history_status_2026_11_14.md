# Read-only history status visibility (#61219)

## Behavior

- `statusLineContent` now returns no content for `readOnlyThread` snapshots and unavailable conversations. This hides the status line and its context-usage items for saved history and closed threads.
- Token-usage updates still populate usage state, but the history gate keeps the status row hidden. An interactive resume clears both states and restores the row, including when the thread uses a read-only permission profile.
- The read-only permission profile itself never triggers the history gate; only the view/attach state does.

## Regression

`TestReadOnlyHistoryHidesStatusLineUntilInteractiveResumeLikeRust` covers a live thread, a read-only snapshot, token updates while viewing, a live resume with `sandbox = read-only`, and an unavailable conversation.

## Evidence

- `go test ./tui/tea -run TestReadOnlyHistoryHidesStatusLineUntilInteractiveResumeLikeRust -count=1` passed.
- Full `go test ./tui/tea -count=1` passed in 47.601s.
- Log: `update/tui_tea_after_status_gate_2026_11_14.log`.
