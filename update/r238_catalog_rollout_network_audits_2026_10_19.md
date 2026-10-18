# Catalog overrides, rollout durations, network socket audits

Audited upstream commits:

- `#46123` Allow model catalogs to override the V2 `spawn_agent` description
- `#45966` Measure rollout read and materialization durations
- `#46302` Validate network socket policies using the executor OS
- `#47679` Add extension hooks for model requests and response streams

## Verdicts

- `#46123` complete: catalog `tools.multi_agent.<name>` descriptions are
  resolved per tool, empty overrides suppress bundled text, missing entries fall
  back, and clones remain independent.
- `#45966` complete: rollout read and compression/materialization durations use
  bounded outcome tags and are covered by recorder tests.
- `#46302` complete: socket allowlists are validated against the executor
  platform's path grammar, including Windows/POSIX/unknown behavior and managed
  requirements.
- `#47679` not applicable: Go has no extension runtime or model-request
  contributor/interceptor registry; the model runner owns requests directly.

## Verification

```text
go test ./model -run 'Catalog|MultiAgent' -count=1
go test ./turn -run 'MultiAgent|Spawn' -count=1
go test ./rollout -run 'Compression|Duration|LineReader' -count=1
go test ./network -run 'ExecutorOS|SocketPath|SocketAllowlist' -count=1
go test ./execserver -run 'NetworkPolicy|PlatformOS' -count=1
```
