# Rust #39993: keep credentials out of app-server logs

Upstream commit: `79b760680381cf8e615a0ffbdaec7fa9bc6a8988`

## Implemented

- Provider diagnostics redact `experimental_bearer_token`, static HTTP header
  values, query parameter values, command-backed auth arguments, AWS
  credential-export arguments, and auth-refresh arguments.
- API-provider diagnostics apply the same redaction to query parameters,
  headers, and command-backed auth state.
- Attestation responses, ChatGPT auth-token refresh responses, and external
  auth refresh responses redact their access/attestation tokens.
- Redaction is implemented with `fmt.Formatter`, so it applies to `%v`,
  `%+v`, `%#v`, and `slog` values without changing JSON serialization or
  request behavior.
- Server-request broker errors propagate only the client-provided error code,
  not its message, because that message may echo a credential.
- Malformed server-request responses return a generic invalid-response error
  instead of propagating parser text that may quote sensitive input.

## Verification

```text
go test ./model -run ProviderDiagnosticsRedact -count=1
go test ./appserver -run 'CredentialResponsesRedact|ServerRequestErrorsDoNotEcho|MalformedCredentialResponse' -count=1
go test ./appserver -run ServerRequest -count=1
go test ./model -run Provider -count=1
go test ./auth -run Account -count=1
go test ./model -count=1
go test ./auth -count=1
go vet ./appserver
```

Passed on 2026-10-18.

`go vet ./appserver` passed. `go vet ./model` is currently blocked by an
unrelated dirty-worktree diagnostic at `model/responses_agent.go:1530`
(`assignment copies lock value to clone`); the changed redaction code is
covered by the tests above.
