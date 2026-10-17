# Round 116 · #49312 notify parents when Guardian stops a subagent

Rust source: `63475131ce`.

## Landed

- Added `agent.FormatGuardianInterruptionMessage`, the Go port of
  `format_guardian_interruption_message`: the denial text is truncated to
  `ErrorMaxTokens` (leaving envelope headroom) and followed by Rust's
  `GUARDIAN_NEXT_ACTION` guidance inside the normal FINAL_ANSWER envelope.
- Added `config.GuardianCircuitBreakAction` for
  `[auto_review] circuit_break_action` (`default`/`strict`, default otherwise),
  the gate Rust uses before forwarding the interruption.
- `RuntimeRouter.interruptTurnForGuardianCircuitBreaker` now receives the
  denial reason and, in strict mode for a MultiAgentV2 child, delivers the
  Guardian interruption message to the child's parent through the same steer
  mailbox / queued-agent-message path as ordinary agent completion. The child's
  turn still finishes as interrupted; the default action and ordinary
  interruptions stay silent.
- The reviewer's `recordDecision` passes the assessment rationale into the
  interrupt callback so the parent sees why Guardian stopped the agent.

## Verification

```text
go test ./appserver -run 'TestGuardianSubagentInterruptionNotifiesParentOnlyInStrictModeLikeRust|TestModelGuardianReviewerInterruptsAfterDenialThreshold|TestGuardianDenialLimit' -count=1  # ok
go test ./agent -run 'TestGuardianInterruptionMessageBoundsReasonAndKeepsGuidanceLikeRust' -count=1  # ok
go test ./agent ./config -count=1  # ok
go test ./appserver -run 'Guardian' -count=1  # ok
go vet ./agent ./appserver ./config  # ok
go build ./...  # ok
```

Coverage: strict-mode parent delivery with the rationale and Guardian guidance,
silence under the default circuit-break action, and long-denial truncation that
keeps the guidance within the notification limit.
