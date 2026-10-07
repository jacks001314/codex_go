package appserver

import (
	execserverclient "codex_go/execserver"

	"codex_go/turn"
)

// requiredSkillsPreSamplingValidation builds the turn's required-environment-skill
// gate (Rust #51157): `Session::run_turn` validates the environment snapshot's
// `skills.required` before every sampling request and fails the turn with a fatal
// error when a required skill is unavailable, so no model request is sent.
//
// It returns nil when the turn requires nothing — including an isolated Guardian
// reviewer, which `EnvironmentManager.RequiredSkillsForTurn` exempts — so a turn
// without requirements pays nothing for the hook.
func (r *RuntimeRouter) requiredSkillsPreSamplingValidation(params *turn.TurnStartParams, threadID string, turnID string) turn.AgentPreSamplingValidation {
	if r == nil || r.services.Environment == nil {
		return nil
	}
	requirements := r.services.Environment.RequiredSkillsForTurn(params)
	if len(requirements) == 0 {
		return nil
	}
	// The check reads the same executor skill providers the skills tools read.
	// A configuration or sandbox-context failure leaves the providers without
	// sandbox contexts (the host filesystem view), never skipping the check: an
	// environment whose skills cannot be discovered is exactly the case Rust
	// fails the turn for.
	var sandboxContexts map[string]*execserverclient.FileSystemSandboxContext
	if cfg, err := r.effectiveConfigForTurn(params); err == nil && cfg != nil {
		if contexts, contextErr := r.executorSkillSandboxContextsForTurn(cfg, turnCWD(params), params); contextErr == nil {
			sandboxContexts = contexts
		}
	}
	return turn.NewRequiredSkillsValidation(
		r.executorSkillProviderForThreadWithSandbox(threadID, sandboxContexts),
		r.executorSkillRootEnvironments(threadID),
		turnID,
		requirements,
	)
}
