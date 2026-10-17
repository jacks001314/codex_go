# Round 116 · #47867 remote permission paths use executor context

Rust source: `2deb542491`.

## Landed

- Added `sandbox.PermissionPromptExecutorContext` (`CWD`, `UserHomeDir`,
  `PlatformOS`) and `PermissionPromptProfileOptions.ExecutorContext`. A nil
  context keeps the existing local host-filesystem resolution.
- With an executor context, writable roots and deny entries resolve against the
  executor's cwd/home and render in its native spelling: `file:///C:/workspace`
  displays as `C:\workspace`, relative entries join the executor cwd, and
  `~` entries expand the executor home. Windows/POSIX conventions are chosen
  from the reported platform, falling back to the cwd shape.
- Deny entries resolve individually: an unresolved `~` entry, a drive-relative
  path, or an executor special whose fact is missing (tmpdir, slash-tmp,
  project roots) is omitted without hiding the other entries.
- Sandbox mode follows the executor-resolved roots (workspace-write when roots
  exist, read-only otherwise, danger-full-access for unrestricted profiles).
- `BuildPermissionPrompt` now applies Rust's shared 32 KiB display budget across
  writable roots, deny paths, and deny globs. Entries are omitted whole and the
  omission warning says every restriction still applies.
- `EnvironmentManager` caches the last successful `environment/info` snapshot
  (`CachedInfo`) and drops it when the environment is replaced or removed.
  `permissionPromptExecutorContextForTurn` uses that snapshot plus the primary
  selection's cwd, so rendering instructions never opens a new executor
  connection.

Go stores the legacy sandbox policy instead of Rust's filesystem entry list, so
the context-aware half lives in `permissionPromptSandboxModeForExecutorContext`
and `permissionPromptDeniedReadsForExecutorContext` rather than a
`resolved_entries` method.

## Verification

```text
go test ./sandbox -count=1  # ok
go test ./appserver -run 'TestPermissionsInstructions' -count=1  # ok
go test ./appserver -run 'Permission|WorldState|ExecPolicy|Guardian' -count=1
# only the known platform newline-fixture failures
go vet ./sandbox ./appserver  # ok
go build ./...  # ok
```

Coverage: remote Windows writable roots/deny paths/globs, Linux executor cwd,
missing-home per-entry omission, whole-entry 32 KiB omission with multibyte
paths, local resolution without an executor, and the appserver cache/wiring.
