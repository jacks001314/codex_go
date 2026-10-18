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

- Go now keeps a live MCP invocation registry keyed by server/call ID with the
  host-registered connector, tool, argument, and thread/turn identity.
  `ToolExecutor.Execute` registers the call for its lifetime.
- Browser nested elicitations read `_codex_apps.call_id` and inherit the
  originating call as the review target while keeping their own action
  identity (Rust #47647).
- Strict `codex_apps` review uses the live Browser invocation for nested
  actions. Other hosted connectors must match their registered outer
  invocation's connector and tool; missing, unknown, completed, or mismatched
  correlations fail closed with Rust's strict decline message and never prompt
  the user or reach the reviewer.
- The outer-invocation review action comes from registered host metadata,
  including the model-supplied arguments, instead of the elicitation payload.

## Verification

- `go test ./mcp -count=1`
- `go test ./mcp -run Invocation -count=1 -race`
- `go test ./appserver -run 'Browser|MCPElicitation|GuardianMCPElicitation' -count=1`
- `go vet ./mcp ./appserver`
- `go build ./...`
