# Parity control plane

This directory separates the last certified baseline from the active alignment target.

- `../parity.json` records the last fully verified Rust/Go SDK baseline.
- `baseline.json` freezes the active Rust target, Go starting commit, platform matrix, and certification thresholds.
- `commits.json` accounts for every Rust commit between the certified and candidate baselines.
- `domains.json` tracks implementation status and acceptance criteria by behavior domain.
- `contracts/manifest.json` maps Rust protocol/data oracles to their Go implementation and verifier.

The L2 record-replay facility lives in `../recordreplay/`: it parses Rust-recorded
traces (e.g. `tui/tests/fixtures/oss-story.jsonl`), freezes a structural digest as
`recordreplay/testdata/oss-story-digest.json`, and replays the recorded task
lifecycle through Go's paginated rollout recorder (event sequence, item states,
recoverability; token-free).

`complete`, `equivalent`, and `not_applicable` are closed states and require evidence. A candidate can be marked `certificationReady` only when every domain and commit is closed, every contract is complete, and no exception status exists.

Run the local gate against the sibling Rust checkout with:

```powershell
$env:CODEX_RUST_ROOT = 'D:\qax\reagent\dev\git\codex\codex-rs'
go test ./parity ./features -count=1
```
