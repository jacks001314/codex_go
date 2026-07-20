# Go parity release notes: `1e20272fa5`

Candidate: `go-1e20272fa5-parity.1`

This synchronization covers the Rust range `315195492c..1e20272fa5`.

## Included

- App-server protocol additions for installed apps and occurrence search.
- Audio and local-audio input/output content items.
- Paginated history, thread names, inherited rollout prefixes, resume CWD, and world state.
- SessionEnd lifecycle hooks and external-agent import diagnostics and memory provenance.
- Executor capability discovery, managed network proxy behavior, and legacy exec-policy migration.
- TUI read-only parent-agent behavior, bounded command output, inline visualization, and history search.
- Realtime V3 initial items and streamed Codex handoff output with bounded UTF-8-safe truncation.
- Compressed rollout inventory and doctor matching.

## Verification

The Windows workspace passed the parity, vet, full test, race, and diff checks recorded in this repository's plan.

## Platform status

Linux Landlock/bwrap evidence is recorded in `plan.md`. macOS seatbelt, native trust-root, Unix-socket, and desktop-app gates require a native macOS runner. Those results are intentionally not inferred from Windows or cross-compilation.
