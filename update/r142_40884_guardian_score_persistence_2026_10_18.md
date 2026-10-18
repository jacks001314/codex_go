# Rust #40884: persist Guardian V2 risk scores without restoring them

Upstream commit: `10d5a603aecbd73a38f3a6576cce69a78f8d6f1d`

## Result

Not applicable by architecture.

- Rust appends accepted Guardian V2 asynchronous classification results to the
  rollout for non-ephemeral threads and removes the session-resume path that
  seeded the active extension state from a persisted score.
- Go has no Guardian V2 async scorer. Its Guardian path is the synchronous
  reviewer, whose assessment is returned directly to the approval flow.
- `rollout.SecurityRiskScore` and its recorder methods are a persistence/parse
  carrier only; there is no production caller that writes a score.
- Persisted `security_risk_score` lines are not projected into session history
  (`sessionItemsFromRolloutLines` handles response items and events only), so
  resumed and forked threads never restore a score into active Guardian state.
  Rust's removed restore block therefore has no Go counterpart to delete.

## Verification

```text
rg -n 'security_risk_score|SecurityRiskScore' --glob '*.go'
go test ./rollout -run 'SecurityRiskScore' -count=1
```

The search shows only the rollout persistence/parse carrier and its tests; no
reviewer, session, or app-server code reads a persisted score.

Passed on 2026-10-18.
