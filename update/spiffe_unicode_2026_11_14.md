# SPIFFE environment validation

Rust reference: update/upstream_workload_auth_61207.rs, resolve_config / required_unicode / optional_unicode (#61207).

Added endpoint presence tracking and preserved raw endpoint/audience input until validation. Missing required values report `workload identity requires <VAR>`; present empty, whitespace-only and invalid UTF-8 report `workload identity variable <VAR> is invalid`. Optional source/ID invalid UTF-8 is rejected before platform construction, without including values in errors. Required values are trimmed; optional values remain unchanged. Updated Unix integration fixtures for endpoint presence.

Validation on Windows:
- `go test ./auth -count=1 -timeout 3m`: exit 0, 20.173s. Log: spiffe_unicode_auth_2026_11_14.log.
- `go vet ./auth`: exit 0, no output.
- GOOS=linux GOARCH=amd64 CGO_ENABLED=0 `go test -c ./auth -o update/spiffe_auth_linux.test`: exit 0, no output. Compilation only, not Unix runtime validation.

Scope: SPIFFE fields and source validation only. Existing federation-rule/file/context behavior is unchanged. Overall upstream synchronization is incomplete; Unix native verification and other items in plan_2026_11_13.md remain open. No commit or push performed.
