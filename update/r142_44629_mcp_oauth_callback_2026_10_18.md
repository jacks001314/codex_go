# Rust #44629: manual callback input for MCP OAuth login

Upstream commit: `f8ab57359dde6b6d5de1aee613c18fe60b661aeb`

## Result

Implemented.

- `cli` parses `mcp login <name> --no-browser`.
- `app.loginCallbackInput` returns the stdin reader only for no-browser login
  and passes the same reader through the discovered-scope retry.
- `app.mcp` prints the authorization URL, starts the HTTP callback listener, and
  concurrently accepts a pasted full redirect URL.
- Pasted callbacks are validated (URL/state) before token exchange, input is
  bounded and hidden, cancellation is honored during callback input and token
  exchange, and successful tokens are stored.

## Verification

```text
go test ./app -run 'MCPLoginNoBrowser|MCPLogin' -count=1
go test ./cli -run 'MCPLoginNoBrowser' -count=1
```

Passed on 2026-10-18.
