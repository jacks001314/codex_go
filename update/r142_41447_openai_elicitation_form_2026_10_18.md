# Rust #41447: openai/elicitation form requests

Upstream commit: `eec4a23cb16de16e0c8cff7c913eed943f223df7`

## Result

Implemented.

- `mcp/elicitation_runtime.go` already dispatches
  `openai/elicitation/create` with `mode: "form"` and preserves the request
  message, metadata, and requested schema.
- `appserver.mcp_elicitation` already forwards form mode as `openaiForm`.
- Added the missing client capability: initialization now advertises
  `openai/elicitation: {form: {}}` alongside the legacy `openai/form`
  extension when OpenAI form elicitation is enabled. Disabled clients advertise
  neither.

## Verification

```text
go test ./mcp -run 'OpenAIForm|Elicitation|Capabilities' -count=1
go test ./appserver -run 'MCPElicitation|Elicitation' -count=1
```

Passed on 2026-10-18.
