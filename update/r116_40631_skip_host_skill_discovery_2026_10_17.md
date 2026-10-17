# Round 116 · #40631 extensions can skip host skill discovery

Rust source: `304c8de4c6`.

## Landed

- The `skip_host_skill_discovery` feature (already listed as an
  under-development key in `features/features.go`) now actually gates host
  skill discovery during turn setup and the non-app-server prompt builder.
- Added `SkillInvocationContributor` with `RequiresHostSkillDiscovery`, plus
  `RuntimeServices.SkillInvocationContributors` and
  `HostSkillsPromptOptions.Contributors`. Any contributor that requires
  host-owned skills keeps host discovery enabled; an empty registry (the
  default) allows skipping. This is the Go carrier for Rust's extension
  registry check.
- When skipping, the host `Skills.List` call and plugin skill discovery are
  bypassed, so no host catalog, warnings, or explicit host skill items are
  produced. Executor, orchestrator, custom, and selected-capability catalogs are
  still built, and base instruction loading is untouched.
- `skillMetadataForMCPRequirements` follows the same gate, so a turn cannot
  prewarm MCP servers from host skills that were not discovered. `BuildHostSkillsPromptContext`
  returns an empty snapshot under the same conditions.

## Verification

```text
go test ./appserver -run 'TestTurnSkipsHostSkillDiscoveryFeatureLikeRust|TestBuildHostSkillsPromptContextSkipsHostDiscoveryLikeRust' -count=1  # ok
go test ./appserver -run 'Skill|Guardian' -count=1  # ok
go vet ./appserver  # ok
go build ./...      # ok
```

Coverage includes the legacy (feature off) host catalog, feature-on skipping,
executor-only contributors, mixed host-dependent contributors, base
instruction retention, and the direct prompt builder.
