# Escalated terminal input approval

Rust upstream: `a57b398351a803c9ec94e38042bc82f527bed2a4` -
`Require approval for input to escalated terminals (#40978)`.

## Alignment

Go's `write_stdin` path reviews non-empty input before writing to an escalated
terminal:

- the terminal's original approval/profile is compared with current authority;
- matching non-escalated terminals bypass the extra prompt;
- escalated or drifted terminals require a fresh `writeStdin` approval;
- denied approval rejects the write;
- NUL bytes and oversized reviewed input are rejected;
- hooks and Guardian review run before the input is committed;
- policy drift fails closed.

## Verification

```text
go test ./appserver -run 'WriteStdinApproval' -count=1
go test ./tool -run 'WriteStdinApproval|UnifiedExecWriteStdin' -count=1
go test ./parity -count=1
```
