# Rust #49796: deduplicate Guardian retained-context omission notices

Upstream commit: `5ca55db09cc9c88165c94063275f619f228556a0`

## Result

Equivalent implementation was already present.

- `GuardianRetainedOmissions` records whether user-instruction and
  assistant-context omission notices have been delivered for a thread.
- The reviewer applies the previous delivery state before composing the next
  retained section, suppressing repeated notices and emitting the
  availability-changed message when availability recovers.
- Only exact host-generated notice parts inside the retained section count as
  proof; matching text coalesced into tool or Node REPL evidence cannot suppress
  a required notice.

## Verification

```text
go test ./appserver -run 'GuardianRetainedOmissions|RetainedOmission' -count=1
```

Passed on 2026-10-18.
