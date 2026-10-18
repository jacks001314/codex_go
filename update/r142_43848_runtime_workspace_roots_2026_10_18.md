# Rust #43848: preserve runtime workspace roots across thread resume

Upstream commit: `6515a72db7a82e8cdebed940aad4ba1a159ce245`

## Result

Partially implemented.

Already present:

- Runtime workspace roots are stored in thread metadata
  (`record.Metadata.Extra["runtime_workspace_roots"]`), cleaned and deduplicated.
- Resume restores stored roots; fork applies explicit override roots.
- `TestRouterResumeAndForkRuntimeWorkspaceRootsUseEffectiveRoots` covers stored
  resume and explicit fork roots.

Still missing for full parity:

- The protocol uses `[]string` with `omitempty`, so an omitted override and an
  explicit empty list are indistinguishable; empty currently falls back to the
  stored roots or cwd. Rust preserves explicit empty selections.
- A cwd change does not retarget the old cwd entry in the root list
  (`replace_path_and_deduplicate`).
- Restored-path validation for the current host and the full resume/revert
  settings-checkpoint behavior are not covered end to end.

## Verification

```text
go test ./appserver -run 'RuntimeWorkspaceRoots' -count=1
```

Passed on 2026-10-18.
