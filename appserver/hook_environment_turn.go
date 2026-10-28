package appserver

import (
	"strings"

	"codex_go/tool"
	"codex_go/turn"
)

// environmentHookSourcesForTurn resolves the hook declarations of a turn's
// ready environments, mirroring Rust environment_hook_sources (#56304). Pending
// and failed attachments contribute nothing; environments without hooks are
// skipped. The returned remote map lists the environments that must be driven
// through an exec-server client rather than this process.
func (r *RuntimeRouter) environmentHookSourcesForTurn(params *turn.TurnStartParams) ([]environmentHookSource, map[string]tool.UnifiedExecEnvironment) {
	if r == nil || params == nil || r.services.Environment == nil {
		return nil, nil
	}
	environments := r.unifiedExecEnvironmentsForTurn(params)
	if len(environments) == 0 {
		return nil, nil
	}
	byID := make(map[string]tool.UnifiedExecEnvironment, len(environments))
	for _, environment := range environments {
		byID[environment.ID] = environment
	}
	selections := params.Environments
	if len(selections) == 0 {
		defaultID, ok := r.services.Environment.DefaultEnvironmentID()
		if !ok {
			return nil, nil
		}
		selections = []map[string]any{{"environmentId": defaultID}}
	}
	threadConfig := r.threadEnvironmentConfigForTurn(params)
	sources := make([]environmentHookSource, 0, len(selections))
	remote := map[string]tool.UnifiedExecEnvironment{}
	for _, selection := range selections {
		environmentID := selectionEnvironmentID(selection)
		environment, ok := byID[environmentID]
		if !ok {
			continue
		}
		state, _ := resolveEnvironmentConfig(selection, threadConfig)
		if state.Kind == EnvironmentConfigPending || state.Kind == EnvironmentConfigFailed || state.Config == nil {
			continue
		}
		if state.Config.EnvironmentHooks.IsEmpty() {
			continue
		}
		sources = append(sources, environmentHookSource{
			EnvironmentID: environmentID,
			CWD: firstNonEmpty(environment.CWD, strings.TrimSpace(firstNonEmpty(
				threadItemStringFromAnyMap(selection, "cwd"),
				threadItemStringFromAnyMap(selection, "CWD"),
			))),
			Hooks: state.Config.EnvironmentHooks,
		})
		if environment.Remote() {
			remote[environmentID] = environment
		}
	}
	if len(sources) == 0 {
		return nil, nil
	}
	return sources, remote
}
