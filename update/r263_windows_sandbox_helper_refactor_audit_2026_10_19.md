# Windows sandbox helper refactor audit

Rust upstream: `1a02867bd1 Refactor Windows sandbox setup and service helpers (#45455)`.

## Verdict

Not applicable. The upstream commit extracts Rust helper-copy, token-user,
provisioning-pipe, and service-runtime modules. Go already keeps helper
materialization in `sandbox/windowssandbox/helper_materialization.go`, token-user
queries in `token_windows.go`, and service/provisioning logic in dedicated files,
so the documented module split has no additional Go behavior to port.
