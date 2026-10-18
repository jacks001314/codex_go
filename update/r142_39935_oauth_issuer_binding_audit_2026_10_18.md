# Rust #39935 audit: MCP OAuth issuer binding

Upstream commit: `7f9832d0d08c0e2fd56a02421477756474fec95d`

The Go port already contained the required behavior; this audit closes the
symbol-name false negative.

- `validateMCPOAuthAuthorizationServerEndpoints` validates authorization and
  token endpoints before constructing the login hand-off.
- RFC 9207 issuer-bound callbacks require an advertised issuer.
- Without issuer-bound callbacks, the authorization endpoint must share the
  issuer or token origin, with only the exact provider compatibility triples.
- Issuer-less metadata still requires the authorization and token endpoints to
  share an origin.
- Rejections are identifiable and remain fatal during login, preventing fallback
  to a guessed authorization URL.

Regression coverage is in `mcp/oauth_issuer_binding_test.go`, including
lookalike hosts, scheme downgrade, exception matching, issuer-bound callbacks,
and both registered and issuer-less paths.

```text
go test ./mcp -run MCPOAuthDiscovery -count=1
```

Passed on 2026-10-18.
