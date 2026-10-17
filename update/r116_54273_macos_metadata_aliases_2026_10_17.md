# Round 116 · #54273 macOS protected metadata aliases

Rust source: `3bae578af3`.

## Landed

- Added native macOS filesystem case-sensitivity detection through
  `pathconf(_PC_CASE_SENSITIVE)`, walking to the nearest existing ancestor for
  missing paths.
- Protected metadata components are normalized to their canonical `.git`,
  `.agents`, `.gcode`, or `.aws` spelling when the filesystem reports an ASCII
  case alias.
- Workspace-root spelling aliases are handled component-by-component, so a
  broader parent grant cannot bypass metadata protection through a root such as
  `WORKSPACE` versus `workspace`.
- Read-only metadata subpaths are normalized with the target before containment,
  preserving explicit metadata grants and narrower restrictions.
- If case sensitivity cannot be established, the alias is treated as protected
  and remains non-writable rather than failing open.

## Verification

```text
go test ./sandbox/sandboxpath -count=1  # ok
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go test -c ./sandbox/sandboxpath  # ok
go build ./...  # ok
```

The macOS-only test covers metadata spelling aliases and workspace-root aliases.
Windows keeps its existing case-insensitive `filepath.Rel` behavior; other Unix
filesystems report no aliases.
