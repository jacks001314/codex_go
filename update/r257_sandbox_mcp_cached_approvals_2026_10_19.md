# Sandbox globs, MCP errors, cached approvals

Rust upstream: `035295b46ee4a5962d0e01a66a888d5bf5da4de4` -
`Improve sandboxing, MCP errors, and cached approvals (#41196)`.

## Alignment

Windows deny-read globs:

- resolve existing directory matches across protected roots;
- reject unbounded filesystem-root globs unless a depth bound is configured;
- canonicalize exact targets while preserving missing paths;
- apply deny-read ACLs and keep read operations sandboxed under full-write
  profiles when deny rules are present.

MCP errors:

- structured MCP protocol errors retain their original code, message and data;
- authentication metadata under `data._meta` is preserved;
- `isError: true` tool results remain successful protocol results with `_meta`.

Cached Guardian approvals:

Go has no cached-score auto-approval path. The reviewer samples a full review
and fails closed on stale metadata. Required-model and token-budget guards cover
the remaining pre-review fast-path risk, so Rust's score-authorization binding
has no separate Go approval carrier.

## Verification

```text
go test ./appserver -run 'MCPRemoteError|JSONRPCErrorData|MCP.*Error|StructuredMCP' -count=1
go test ./sandbox/windowssandbox -run 'DenyRead|Glob|ResolvedPermissions|Walker' -count=1
go test ./appserver -run 'RequiresFullReviewForProtectedModel|GuardianTokenBudgetRollover' -count=1
go test ./parity -count=1
```
