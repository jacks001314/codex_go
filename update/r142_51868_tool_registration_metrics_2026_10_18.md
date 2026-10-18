# Rust #51868: record tool registration metrics per sampling request

Upstream commit: `fbe97c79290821e75a817adb7059adfca4404fef`

## Result

Equivalent implementation was already present.

- Registration counts are derived from the finalized tool router inventory and
  recorded once per sampling request, before transport retries.
- `codex.tools.registered` uses Rust's histogram boundaries and tags every
  exposure with the resolved `tool_mode`, including zero-valued exposures.
- Hosted tools are not part of the registered inventory.
- Empty inventories still emit all exposure samples.

## Verification

```text
go test ./turn -run ToolRegistration -count=1
```

Passed on 2026-10-18.
