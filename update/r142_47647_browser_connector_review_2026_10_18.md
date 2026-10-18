# #47647 Guardian computer-use review for the Browser connector

## Upstream change

Rust treats `codex_apps` with the registered `connector_openai_browser`
identity as Node REPL-backed for computer-use confirmation policies, review
evidence, JavaScript execution policy, and initial-call approval. Browser
elicitations are correlated with live invocations so nested actions keep their
own review identity while assessments stay attributed to the originating call.

## Go implementation

Implemented:

- `mcp.IsNodeReplBackedConnector` recognizes `node_repl`, `cua_repl`, and
  `codex_apps` plus `connector_openai_browser` using registered tool metadata.
- MCP confirmation-policy request metadata now uses the connector-aware check.
- Guardian `js` approval policy injection and user-approval-mode initial
  handling recognize the Browser connector.
- Node REPL review-evidence capture resolves the connector through
  `ToolRouter.MCPConnectorInfo`, so Browser connector results are retained.

Remaining:

- Go has no live MCP invocation registry keyed by server/call ID, so Browser
  elicitations cannot yet be correlated to the originating invocation for
  strict nested review. The action carries the request's connector ID, but a
  missing/unknown correlation is not separately rejected as upstream requires.

## Verification

- `go test ./mcp -run 'NodeRepl|ConfirmationPolicies|Browser' -count=1`
- `go test ./appserver -run 'NodeRepl|Browser|Guardian' -count=1`
- `go vet ./mcp ./appserver`
