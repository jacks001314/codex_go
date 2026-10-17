# Round 116 · #52241 subagent capability inheritance

Rust source: `845345b1dd`.

## Landed

- V1 and V2 spawn paths share the same controller-level plugin selection:
  `runtimeAgentController.disabledPluginIDs` captures the spawning turn's
  effective choice.
- `agent.SpawnAgentArgs.DisabledPluginIDs` supports internal explicit overrides.
  Omission inherits the spawning turn; an explicit empty list clears it.
- Child records persist `disabled_plugin_ids` regardless of
  `fork_turns=none|all`, and an immediately triggered child turn receives the
  same list in `TurnStartParams`.
- Inherited environment selections continue to carry ready capability-root
  configs for both fresh and fully forked children, so skill/plugin roots no
  longer depend on copied conversation history.
- `threadDisabledPluginIDs` now reads an explicit persisted child snapshot before
  falling back to an empty default settings object, which previously masked the
  inherited record.

## Verification

```text
go test ./appserver -run 'SpawnInheritsDisabledPlugins|SpawnInheritsCapabilityRoots|DisabledPlugin' -count=1
# ok

go test ./appserver -run 'AgentController|Spawn|DisabledPlugin' -count=1
# ok

go test ./agent -count=1  # ok
go vet ./agent ./appserver
go build ./...
```

RC/failure coverage: the tests run both `fork_turns=none` and `all`, verify the
persisted disabled-plugin list, explicit empty override, and selected capability
root visibility from child environment attachments. Before the record-order fix,
the child persisted selection was hidden by default empty thread settings and
the test failed with `[]string{}`.
