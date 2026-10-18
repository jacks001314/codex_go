# Rust #54961: wake idle root sessions for agent messages

Upstream commit: `8c076942cb70fba8e5c4732c23f1faabc4aed484`

## Result

Implemented the disabled-by-default `wake_on_agent_messages` feature.

- Queue-only agent mail starts a new root turn when the feature is enabled and
  the root is idle. The same path covers direct `send_message` delivery and the
  host mailbox used for child completion messages.
- The wake start reuses the previous turn's model, reasoning effort, and cyber
  access program. Normal turn-start preparation restores the remaining
  thread-owned settings.
- Interrupted sessions, Plan mode, and child agents remain queue-only. The
  feature-disabled default also leaves mail queued for the next normal turn.
- Draining the mailbox and starting the automatic turn are serialized per
  runtime. A failed start requeues the consumed mail ahead of newer arrivals,
  and successful/failed/interrupted turn-end paths re-check for mail that raced
  the previous wake decision.

## Verification

```text
go test ./appserver -run 'AgentMessageWake|QueueOnlyAgentMessage|HostMailboxCompletion|PreviousTurnSettings' -count=1
go test -race ./appserver -run 'AgentMessageWake|QueueOnlyAgentMessage|HostMailboxCompletion' -count=1
go test ./agent ./features -count=1
go vet ./appserver ./features
```

Passed on 2026-10-18.
