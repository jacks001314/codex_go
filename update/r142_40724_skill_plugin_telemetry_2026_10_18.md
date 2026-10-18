# #40724 Plugin-attributed skill telemetry

## Upstream change

Rust adds `plugin_id`, `model_slug`, and `reasoning_effort` dimensions to
`codex.skill.injected` for explicit and implicit invocations. Successful
invocations attribute the skill's plugin to the current turn, and
`codex.skill.turn.duration_seconds` is emitted once per plugin used by that
turn with completed, aborted, or error status. Orchestrator resource metadata
now carries `plugin_id` so resource-backed skills receive the same attribution.

## Go implementation

- `appserver/skill_telemetry.go` tracks per-turn model, reasoning effort, start
  time, used plugins, and failure state. The histogram is emitted once per
  plugin when the turn reaches a terminal state, with Rust's
  completed/aborted/error mapping and integer-second ceiling.
- Explicit and implicit injection paths now emit the full six-tag
  `codex.skill.injected` metric. Successful explicit injections use `ok`
  instead of the previous `success` value, and empty plugin attribution becomes
  `unattributed`.
- `turn.OrchestratorSkillMetadata` preserves `plugin_id`, and the app-server
  metadata projection carries it into `InstructionsSkillMetadata`.
- `appserver.notifyTurnCompletedOnce` owns the terminal duration emission, so
  completed, failed, and interrupted turns are all covered.

## Verification

- `go test ./appserver -run 'TestSkillTurnDuration|TestImplicitSkillInjectionMetric|TestExplicitHostSkill|TestExplicitSkillInputItemsWarns|TestResourceBackedSkillInvocationTrackingLikeRust' -count=1`
- `go test ./turn -count=1`
- `go vet ./appserver ./turn`
