# Guardian context budget and telemetry audits

Rust upstream commits:

- `#44164` Add Guardian context cost and request token telemetry
- `#44166` Add aggregate budget enforcement for Guardian context

## Verdicts

- `#44164` complete: Go emits Guardian request-token and section-cost histograms
  with bounded tags alongside review token-usage metrics.
- `#44166` complete: Guardian transcript/context profiles enforce aggregate
  message/tool/entry budgets with deterministic truncation and omission notices,
  including retained/sender evidence.

## Verification

```text
go test ./appserver -run 'Guardian.*Metric|GuardianContext' -count=1
go test ./state -run 'Guardian.*Budget|Guardian.*Profile|Guardian.*Transcript' -count=1
```
