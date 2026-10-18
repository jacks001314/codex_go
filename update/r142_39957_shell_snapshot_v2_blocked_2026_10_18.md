# Rust #39957: in-memory shell snapshots precondition audit

Upstream commit: `3882ced09c4917b0bb528f597abd87f3c905fe47`

Go has the feature registration and protocol capability bit, but not the
executor-managed in-memory snapshot subsystem this commit depends on:

- Local unified exec launches directly spawn the child process instead of
  entering the executor-managed process path.
- `execserver/shell_snapshot.go` only captures and replays file-backed
  snapshots; it has no bounded in-memory environment/file replay cache.
- The Rust feature additionally requires startup-file suppression, policy
  filtering, prewarm integration, local/remote capability routing, and
  command-level metrics.

The row is therefore `blocked_precondition` until the executor-managed local
launch path and in-memory snapshot cache are added. This is an architectural
prerequisite rather than a test-only omission.
