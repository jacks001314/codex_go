# Non-blocking Guardian WebSocket prewarm

Rust upstream: `102ae5e2e6ff63d2e7346138182e7eec2fc0711a` -
`Prewarm Guardian WebSockets without blocking thread startup (#40985)`.

## Alignment

Go's Guardian reviewer keeps the WebSocket handshake off the thread/turn startup
path:

- reviewer creation starts prewarm in a background goroutine;
- prewarm runs once and stores the returned response ID;
- the first review reuses that response ID when available;
- a failed or slow prewarm leaves the first review to connect lazily;
- thread startup and resume do not wait for the Guardian handshake.

Existing tests cover prewarm-once behavior, skip conditions, seeded first review,
WebSocket failure fallback, and non-blocking startup.

## Verification

```text
go test ./appserver -run 'Guardian.*Prewarm|EnsureGuardianReviewerPrewarm|SessionStartupPrewarm' -count=1
go test ./parity -count=1
```
