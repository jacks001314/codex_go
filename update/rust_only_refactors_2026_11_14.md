# Rust-only refactor audit (2026-11-14)

The following upstream commits change Rust internals or tests without a Go behavior contract:

- #61133: permission-profile migration in Rust sandbox documentation and Rust tests. Go has no equivalent test-only API.
- #61200: internal `FooterContent`/`StatusLine` layout-cache refactor. Go's `statusControls` and `renderStatusHeader` already own status content, wrapping and theme rendering; #61219 added the history visibility gate on that surface.
- #61201: Rust `Box<CodexErr>`, `bail!` punctuation and OTLP assertion cleanup only.
- #61222: rollout-trace compressed shards belong to the Rust rollout-trace writer. Go currently implements bundle replay/reduce only; there is no writer/shard subsystem to migrate.
- #61253: background rollout-trace recording is likewise absent from the current Go trace scope; replay/reduce validation is covered by #61199.
- #61263: Rust session-state lock access and test-environment setup refactor; Go async compaction and thread management have separate implementations and tests.

These are recorded as not applicable to the current Go codebase, not as successful Rust test executions.
