package turn

import (
	"context"

	"codex_go/mcp"
	"codex_go/skillprovider"
)

// Required environment skills (Rust #51157). Rust validates the requirements
// inside `run_turn` before every model request, reading the skills extension's
// discovered state (executor step state plus cloud catalog) and the turn's
// environment snapshot. Go keeps the same split: the requirement list and the
// check live in `mcp` (the environment-selection owner), the discovered skills
// come from the turn's skill providers here, and AgentLoop runs the check
// before each sampling request.

// RequiredSkillsCatalogFromEntries maps discovered skill provider entries to
// the environment-aware entries the requirement check reads. Rust compares the
// environment half of `main_prompt.environment_path()`; Go scopes executor
// skills by capability root instead (Rust #46015), so `authorityEnvironments`
// is the root-to-environment map the wiring reads from the thread's selected
// capability roots — the same provenance the per-environment disablement path
// uses. The mapping is authoritative and the check is fail-closed: an executor
// entry whose authority id is not mapped keeps an empty environment and can
// never satisfy a requirement, and a non-executor authority has no environment
// provenance at all.
func RequiredSkillsCatalogFromEntries(entries []skillprovider.CatalogEntry, authorityEnvironments map[string]string) mcp.RequiredSkillsCatalog {
	catalog := mcp.RequiredSkillsCatalog{Available: true, Entries: make([]mcp.EnvironmentSkillCatalogEntry, 0, len(entries))}
	for _, entry := range entries {
		environmentID := ""
		if entry.Authority.Kind == skillprovider.SourceExecutor {
			environmentID = authorityEnvironments[entry.Authority.ID]
		}
		catalog.Entries = append(catalog.Entries, mcp.EnvironmentSkillCatalogEntry{
			Name:          entry.Name,
			EnvironmentID: environmentID,
			Enabled:       entry.Enabled,
		})
	}
	return catalog
}

// RequiredSkillsCatalogFromProviders lists the skills the required-environment
// check reads from a turn's skill providers. Rust reads the executor step state
// (`ExecutorSkillsStepState`) plus the cloud catalog; Go's executor half is the
// provider catalog the skills tools already read. A nil registry means no skills
// extension state was found, which the check reports as an unavailable
// extension rather than as a missing skill.
func RequiredSkillsCatalogFromProviders(ctx context.Context, providers *skillprovider.Registry, turnID string, authorityEnvironments map[string]string) mcp.RequiredSkillsCatalog {
	if providers == nil {
		return mcp.RequiredSkillsCatalog{}
	}
	catalog := providers.ListKind(ctx, skillprovider.SourceExecutor, skillprovider.ListQuery{TurnID: turnID})
	return RequiredSkillsCatalogFromEntries(catalog.Entries, authorityEnvironments)
}

// ValidateRequiredEnvironmentSkills resolves the discovered skills and applies
// the Rust #51157 check, mirroring `validate_required_skills(thread_store,
// turn_store, environments)`: the discovered skill state comes from the turn's
// providers and the requirements from the caller's environment snapshot.
func ValidateRequiredEnvironmentSkills(ctx context.Context, providers *skillprovider.Registry, turnID string, authorityEnvironments map[string]string, requirements []mcp.EnvironmentSkillRequirements) error {
	return mcp.ValidateRequiredEnvironmentSkills(requirements, RequiredSkillsCatalogFromProviders(ctx, providers, turnID, authorityEnvironments))
}

// NewRequiredSkillsValidation builds the AgentLoop pre-sampling hook that fails
// a turn before its model request when a required environment skill is
// unavailable (Rust #51157, `CodexErr::Fatal`). It returns nil when nothing is
// required, so a caller can assign the result unconditionally to
// AgentLoopRequest.PreSamplingValidation and pay nothing for turns that carry no
// requirements.
func NewRequiredSkillsValidation(providers *skillprovider.Registry, authorityEnvironments map[string]string, turnID string, requirements []mcp.EnvironmentSkillRequirements) AgentPreSamplingValidation {
	if len(requirements) == 0 {
		return nil
	}
	return func(ctx context.Context, _ int) error {
		return ValidateRequiredEnvironmentSkills(ctx, providers, turnID, authorityEnvironments, requirements)
	}
}
