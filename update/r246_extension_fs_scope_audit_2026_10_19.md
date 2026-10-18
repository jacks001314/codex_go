# Extension filesystem scope audit

Rust upstream: `da2e174a66 Scope extension filesystem access to callback permissions (#49898)`.

## Verdict

Not applicable. Go has no extension runtime or extension tool callback that
receives a scoped filesystem accessor. Skill reads already flow through the
app-server/turn handlers with the selected host or executor filesystem
authority, so there is no separate extension callback boundary to constrain.

## Evidence

- No `ext/` runtime or extension contributor registry exists in the Go tree.
- Skill reads are implemented by app-server/turn handlers rather than an
  extension callback.
