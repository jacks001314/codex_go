# Agent-switch input receipts (#61216)

The actual TUI applyAgentSwitchResult path now reconciles saved input using both resumed history and buffered user-message completion receipts before discarding the destination's event buffer. Receipt reconciliation is independent of transcript replay, so a populated history snapshot or read-only view still confirms delivery. It does not submit new input.

Only matching client IDs in user messages confirm delivery. Buffered item.started events, assistant messages, and events explicitly naming another thread are excluded. Unconfirmed steers remain pending and continue to hold the queue.

Regression first failed because an acknowledged steer remained pending after switching. The new matrix covers persisted/buffered receipts on writable/read-only destinations, same-text steers with distinct IDs, queued and rejected input, and populated snapshots. An additional test excludes wrong-thread and uncommitted events.

Validation:
- Focused agent-switch/resume/pending-steer/dashboard tests: exit 0, 0.502s.
- Final go test ./tui/tea -count=1 -timeout 5m: exit 0, 60.150s.
- Subsequent go vet ./tui/tea: exit 0, no output (same completed command session 76928).
- Logs: agent_receipts_red_2026_11_14.log, agent_receipts_green_2026_11_14.log, agent_receipts_full_2026_11_14.log.

Code and tests were synchronized before the interruption; this report and the final full-test log complete the evidence delivery afterward. Overall synchronization is incomplete. #61216 still needs outgoing event draining, automatic idle dispatch, full draft ownership, backend identity handling and Cloud shell rejection; no commit/push or certification update performed.
