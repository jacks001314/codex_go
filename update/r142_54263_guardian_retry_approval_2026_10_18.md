# Rust #54263: fresh Guardian approval for escalated retries

Upstream commit: `4b9c24f98c6d886134fd2fc237355064708ff21f`

## Result

Equivalent behavior was already present.

- Go has no orchestrator-level in-place sandbox retry. A sandbox-denied command
  is returned to the model, and the escalated rerun is a new tool invocation.
- Every invocation enters `shellApprovalForTurn` with its own call ID and
  requested sandbox permissions. A Guardian approval for the sandboxed attempt
  therefore cannot authorize the escalated attempt.
- The retry is reviewed with `require_escalated` and its sandbox-denial
  justification. A denied retry carries the reviewer rationale and never reaches
  the executor.
- Guardian approval does not create a reusable exec-policy prefix. Only the
  existing user/hook preapproval paths participate in retry bypass.

## Verification

```text
go test ./appserver -run 'GuardianSandboxRetry|Guardian.*Escalated|Escalated.*Guardian' -count=1
```

Passed on 2026-10-18.
