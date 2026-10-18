# Rust #41936: attach failed Guardian reviews to diagnostic reports

Upstream commit: `13d75cd1c3e9bbb69c1b9d5a73c28ef5e5e807a6`

## Result

Implemented.

- `appserver.guardian_feedback` captures failed-review records only when
  feedback collection is enabled and the thread is non-ephemeral.
- Records are bounded (per record, per thread, global count, and total bytes),
  persisted in the state runtime, and selected only for the requested thread
  set when preparing a diagnostic snapshot.
- Feedback attachments include `auto-review-failures.jsonl` with the
  `application/x-ndjson` content type.
- `guardian_feedback_like_rust_test.go` and the feedback tests cover capture
  gating, attachment presence/absence, and persistence.

## Verification

```text
go test ./appserver -run 'GuardianFeedback|Feedback' -count=1
```

Passed on 2026-10-18.
