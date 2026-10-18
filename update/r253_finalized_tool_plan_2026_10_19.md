# Finalized model-specific tool plans

Rust upstream: `0182ff34802c068815d5e0990ee94ec2d080b4fe` -
`Finalize model-specific tool plans in ToolRouter (#41195)`.

## Alignment

Go's `tool.Router` now stores the finalized plan selected for the issuing step:

- effective `ToolMode`;
- normalized Code Mode tool names;
- Responses Lite namespace inventory;
- child-management capability;
- `RequiresCodeModeWorker`;
- `HasTerminalControls`.

`turn.BuildToolRouter` finalizes these values from the exact registry and
model-specific options used to build that router. `PrepareToolMode` updates the
effective mode after Code Mode availability is resolved. The dispatcher records
the router's mode, and Responses Lite metadata consumes the frozen namespace
inventory instead of rebuilding it from a potentially different candidate plan.

## Verification

```text
go test ./tool -run 'RouterFinalizedPlan|CodeModeToolNames|ToolNamespacesInfo' -count=1
go test ./turn -run 'ToolMode|ToolNamespaces|CodeMode|StepInputItems' -count=1
go test ./appserver -run 'ToolRouter|ToolNamespaces|ResponsesLite|MCPTurnMetadata' -count=1
go test ./parity -count=1
```
