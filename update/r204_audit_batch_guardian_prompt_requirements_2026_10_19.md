# Round 759c: Guardian, prompt kinds, and browser/computer requirements

## Complete

- `#39995`: browser/computer-use requirements cover the top-level allow flag,
  history/auto-review/persistent approvals, default and per-origin policies,
  and macOS/Windows app restrictions with requirement parsing/cloning.
- `#40177`: context prompt fragments carry stable producer-owned content kinds
  for skills, memories, permissions, plugins/apps, time, Guardian evidence, and
  model/developer instructions.

## Blocked

- `#39994`: no host-owned extension internal-session spawner or
  `ThreadReadyInput` lifecycle.
- `#40013`: no Guardian v2 async risk-scoring sampler/evidence-retention
  subsystem.

## Verification

- `go test ./config -run 'BrowserComputerUse|Requirements' -count=1`
- `go test ./context -run 'Fragment|ContentKind|Skill' -count=1`
- `go test ./parity -count=1`
