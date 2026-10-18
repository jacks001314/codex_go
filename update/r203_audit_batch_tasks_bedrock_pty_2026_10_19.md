# Round 759b: audit batch for task tools, Bedrock, broker config, ACLs, and PTY

## Complete

- `#40308`: the `codex_tui` dynamic-tool namespace implements bounded task
  list/read/wait/create/fork/message/rename/archive/restore operations and is
  hosted as the local task-tools MCP server for daemon sessions.
- `#40007`: Bedrock discover/setup validates profile/environment credentials,
  persists region/profile selection, rejects conflicts, and clears stale
  profile configuration.
- `#40466`: network-proxy credential-broker config is parsed/preserved,
  project variables are sanitized, trusted provider context is propagated, and
  shell snapshots/Windows env handling recognize broker state.
- `#40475`: Windows ACL handles request `READ_CONTROL | WRITE_DAC`.

## Partial

- `#40481`: API-key Bedrock auth, discovery, SigV4, and auth-mode plumbing
  exist, but managed `amazonBedrockAccessKeys` login/storage/provider selection
  and logout cleanup are still missing.

## Blocked

- `#40460`: Go PTY I/O uses blocking `creack/pty` file reads/writes; Rust's
  cancel-safe AsyncFd `PtyIo` behavior requires a nonblocking PTY I/O
  subsystem.

## Verification

- `go test ./app -run DynamicTool -count=1`
- `go test ./appserver -run Bedrock -count=1`
- `go test ./config -run CredentialBroker -count=1`
- `go test ./sandbox/windowssandbox -run 'ACL|Acl|WindowsPermission' -count=1`
- `go test ./parity -count=1`
