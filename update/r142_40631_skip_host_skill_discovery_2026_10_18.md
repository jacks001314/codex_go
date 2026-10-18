# #40631 Allow extensions to skip host skill discovery

## Upstream change

Rust adds the under-development `skip_host_skill_discovery` feature. It bypasses
host skill snapshots during warmup and turn setup unless an extension skill
invocation contributor requires host-owned skills. Executor and orchestrator
catalogs and instruction loading stay available.

## Go carriers

- `features/features.go` registers `skip_host_skill_discovery` as an
  under-development, default-off feature.
- `appserver.SkillInvocationContributor` exposes `RequiresHostSkills`.
- `requiresHostSkillDiscovery` preserves host discovery when any contributor
  requires it.
- `BuildHostSkillsPromptContext` and turn setup both skip host/plugin discovery
  under the same feature-plus-contributor gate while retaining base
  instructions and executor/orchestrator catalogs.

## Verification

- `go test ./appserver -run 'SkipHostSkill|SkipHostSkillDiscovery|HostSkills' -count=1`
