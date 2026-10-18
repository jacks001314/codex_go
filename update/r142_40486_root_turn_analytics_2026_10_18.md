# Rust #40486: root turn IDs in turn and tool analytics

Upstream commit: `49880081fe9d6cf89ec86e3d31c99bbe26a736c4`

## Implemented

- Turn analytics and every tool-item event implemented by Go now serialize
  `root_turn_id`.
- A top-level turn reports its own turn ID. A subagent turn reports the causal
  root inherited through its run configuration.
- Command, file-change, MCP, dynamic-tool, Code Mode exec/wait, collab-agent,
  web-search, and image-generation events use the same live root resolver.
- Event construction reads the active turn's current analytics root. If that
  root is explicitly unknown or ambiguous, the event reports JSON `null` rather
  than retaining a stale value.
- Queued correlated events refresh their root immediately before release or
  flush. Overflow and thread-close flushes use the same rule.
- Once a turn has completed, a later background tool event uses the root
  captured by that turn's run configuration, so delayed events retain their
  causal owner.
- Tool-item `root_turn_id` no longer uses `omitempty`; an unknown root is an
  explicit `null`, matching the upstream event contract.

The Go port does not have a separate `codex_control_tool_call_event` carrier;
built-in control tools are not emitted as dynamic-tool events. There is
therefore no Go control-tool fact on which to extend this commit's
`ControlToolCallFact.turn_metadata` field.

## Verification

```text
go test ./appserver -run 'TestToolItemAnalyticsEventsCarryRootTurnIDLikeRust|TestAnalyticsRootTurnIDReadsLiveTurnMetadataLikeRust|TestQueuedToolEventDropsInvalidatedRootLikeRust' -count=1
go test ./appserver -run Analytics -count=1
go test ./telemetry -count=1
go vet ./appserver ./telemetry
```

Passed on 2026-10-18.

The full `go test ./appserver -count=1` run reached these unrelated failures in
the dirty shared worktree:

- `TestRustAppServerV2SuiteManifestCoversRustModules`
  (`connection_handling_websocket_compression` manifest drift)
- `TestRuntimeRouterThreadStartProjectlessDoesNotPersistProjectTrustLikeRust`
- `TestRuntimeRouterThreadStartProjectTrustWriteGuards`

No analytics or root-turn test failed.
