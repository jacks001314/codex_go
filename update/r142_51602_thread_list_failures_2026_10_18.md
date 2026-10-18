# Rust #51602: distinguish thread listing failures from exhaustion

Upstream commit: `406eb53c074a29bd65aa7a8bd21d574234faf315`

## Result

Equivalent behavior was already present.

- A `useStateDbOnly` query that cannot be served by the state database returns
  JSON-RPC `-32603` with the `failed to list threads from state database`
  prefix; it is not confused with an empty history.
- An explicitly present but empty cwd filter returns a successful empty page.
- A successful page with a nil `nextCursor` is the exhaustion signal.
- Go's local listing does not run the remote provider's multi-page refill loop,
  so the repeated-cursor failure mode does not exist in this path.

## Verification

```text
go test ./appserver -run 'ThreadList|ThreadListing' -count=1
```

Passed on 2026-10-18.
