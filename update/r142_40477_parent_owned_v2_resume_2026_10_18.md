# Rust #40477: reload Multi-Agent V2 children through their parent

Upstream commit: `d21794d6ba794673f2f754cc01bdb7dabc538f8c`

## Result

Implemented owner-mediated resume for parent-owned Multi-Agent V2 children.

- `thread/resume` detects recorded V2 thread-spawn children before normal
  dispatch.
- If the immediate parent is not currently loaded, the request fails with
  `-32600` and the Rust guidance to resume the parent first or inspect the child
  without loading it.
- If the parent is loaded, the child resume retains only thread ID,
  client-name/redaction, turn-exclusion, and initial-page preferences. Public
  model/config/permission overrides are ignored, so the stored child settings
  and parent-owned runtime state remain authoritative.
- The child is then marked loaded normally and remains available for owned
  operations. V1 or non-thread-spawn children keep their existing resume path.

## Verification

```text
go test ./appserver -run 'ParentOwnedV2ChildResume|ThreadResume' -count=1
go vet ./appserver
```

Passed on 2026-10-18.
