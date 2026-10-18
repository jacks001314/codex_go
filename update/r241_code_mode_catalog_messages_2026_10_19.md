# Model-catalog overrides for Code Mode tool messages

Rust upstream: `5aa4939be4 Support model-catalog overrides for Code Mode tool messages (#47118)`.

## Implementation

- Added catalog parsing for `tools.code_mode` with `exec`, `wait`,
  `deferred_nested_tools_guidance`, and `mcp_typescript_preamble`.
- Exec descriptions support the literal `{{ default_exec_yield_time_ms }}` and
  `{{ image_helper }}` placeholders while retaining runtime tool declarations
  and the freeform grammar.
- Wait descriptions and supported object parameter schemas resolve
  independently; invalid or unsupported schemas fall back to bundled values.
- Empty guidance suppresses the corresponding section.
- The registered exec/wait spec snapshots are refreshed when a later sampling
  step selects a different model, so `ToolsForStep` follows mid-turn model
  changes.

## Verification

```text
go test ./model ./codemode -run 'CodeMode|WaitTool|ExecDescription' -count=1
go test ./tool -run 'CodeModeExecAndWaitCatalog|CodeModeWaitCatalog|CodeMode' -count=1
go test ./turn -run 'CodeMode|RebuildsToolsForLaterModel' -count=1
go test ./appserver -run 'CodeMode|Tool' -count=1
go build ./...
go vet ./model ./codemode ./tool ./turn ./appserver
```
