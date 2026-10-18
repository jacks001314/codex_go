# Rust #43918: expose the queued event count on CodexThread

Upstream commit: `78932f4493995cb1236bcad411c926a9f5c179b2`

## Result

Not applicable by architecture.

- The helper exists only to support a finite drain before transferring a tokio
  event receiver to another reader.
- Go's RuntimeRouter delivers notifications directly through its sink and uses
  explicit buffers/locks. There is no transferable event receiver whose
  snapshot length must be consumed by the current owner.

No verification command is needed; the carrier is absent by design.
