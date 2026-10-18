# Enterprise MCP sign-in and ID-JAG discovery audits

Audited upstream commits:

- `#49276` Enable enterprise MCP sign-in and account-scoped grant cleanup
- `#49478` Discover and validate MCP authorization servers before ID-JAG exchange

## Verdicts

- `#49276` complete: Go implements the enterprise OAuth login/flow, login
  completion notifications, issuer/account checks, pending-login invalidation,
  and account-scoped grant cleanup used by app-server.
- `#49478` complete: authorization-server metadata is discovered and validated
  before ID-JAG exchange; issuer, resource, scope, client, subject, and JWT id
  constraints are enforced, and invalid bearer/refresh responses remain
  terminal with bounded errors.

## Verification

```text
go test ./mcp -run 'Enterprise|EMA|IDJAG|IDJag|Discovery' -count=1
go test ./appserver -run 'OauthLogin|OAuthLogin|Enterprise|Grant' -count=1
```
