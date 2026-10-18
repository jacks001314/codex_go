# #47873 Measure deferred tool namespace fragments before and after truncation

## Upstream change

The deferred `<tools>` namespace fragment records its bytes before and after
the 4 KiB cap, along with the number of namespaces. Snapshot and delta stages
carry separate metric tags so truncation loss is observable.

## Go status

Complete by equivalent implementation.

`context/tools_state.go` measures the complete wrapper and entry text before
the cap and the retained text after it. `appserver/deferred_tools_metrics.go`
records `codex.thread.tools.fragment_bytes` and
`codex.thread.tools.namespaces_total` with stage before/after and kind
snapshot/delta tags.

## Verification

- `go test ./appserver -run 'TestDeferredToolsFragmentMetricsLikeRust' -count=1`
- `go test ./context -run 'Tools|Snapshot|Fragment' -count=1`
