# #43524 Restrict MCP user verification and add workspace-scoped identity

## Upstream change

Rust advertises `userVerification` only to the host-owned apps MCP server and
cancels verification requests from every other registration before prompting the
client. It also exposes the access token's opaque `chatgpt_account_user_id`
only when the token workspace matches the selected account.

## Go implementation

- `mcpClientCapabilitiesWithFeatures` adds `userVerification` to the
  `openai/elicitation` extension only when the per-server host-owned flag is
  set.
- `MCPService.elicitationHandlerForServer` wraps the client handler for all
  other servers and returns `cancel` for verification requests without invoking
  the handler. A configured server that reuses the `codex_apps` name is still
  rejected because ownership, not the name, grants the capability.
- MCP connection and catalog-grace cache keys include the verification flag so
  ownership changes cannot reuse a client with stale capabilities.
- `auth.ChatGPTAccountUserIDFromAuth` parses only the access token's
  `chatgpt_account_user_id` claim, requires the claim workspace to equal the
  explicitly selected account, and rejects missing, malformed, or mismatched
  values without falling back to another user ID.

## Verification

- `go test ./mcp ./auth -count=1`
