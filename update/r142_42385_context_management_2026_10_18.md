# Rust #42385: experimental context management activation

Upstream commit: `cff76fa96f70f9f3b63d221446fd02cfd87e6d2e`

## Result

Implemented.

- `config.ContextManagementConfig` parses the optional
  `features.context_management` table and its `experimental_mode` flag.
- `runtime_router.experimentalContextManagementEligible` requires the feature,
  opt-in mode, OpenAI provider, `supports_experimental_context`, ChatGPT auth,
  and a Plus/Pro/Prolite plan.
- Eligible threads activate token-budget context, history-notes tools, and the
  `new_context` tool.

## Verification

```text
go test ./config ./mcp -run 'ContextManagement|OAuthStore|OAuth' -count=1
go test ./appserver -run 'ContextManagement|TokenBudget' -count=1
```

Passed on 2026-10-18.
