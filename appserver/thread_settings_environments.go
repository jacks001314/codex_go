package appserver

import (
	"strings"

	"codex_go/session"
)

// resolveThreadSettingsEnvironments applies the thread settings environment
// contract (Rust #60655): an explicit list replaces the selection for future
// turns, while cwd without an environment list selects the provider defaults
// for that directory.
func (r *RuntimeRouter) resolveThreadSettingsEnvironments(params *SettingsUpdateParams) error {
	if params == nil {
		return nil
	}
	if params.Environments == nil && params.CWD != nil {
		defaults := r.defaultThreadSettingsEnvironments(params.ThreadID, strings.TrimSpace(*params.CWD), params.RuntimeWorkspaceRoots)
		params.Environments = &defaults
	}
	if params.Environments == nil {
		return nil
	}
	return r.validateTurnEnvironmentSelections(*params.Environments)
}

// defaultThreadSettingsEnvironments mirrors Rust's default environment
// requests: every registered environment, provider default first, bound to the
// retargeted cwd and workspace roots. The result is never nil so an empty
// provider selection clears the thread selection instead of preserving it.
func (r *RuntimeRouter) defaultThreadSettingsEnvironments(threadID string, cwd string, requestedRoots []string) []map[string]any {
	out := []map[string]any{}
	if r == nil || r.services.Environment == nil || cwd == "" {
		return out
	}
	ids := r.services.Environment.DefaultEnvironmentIDs()
	if len(ids) == 0 {
		return out
	}
	var roots []string
	if requestedRoots != nil {
		roots = threadRuntimeWorkspaceRoots(cwd, requestedRoots)
	} else {
		record, _ := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false)
		currentCWD := ""
		if record != nil {
			currentCWD = record.Metadata.CWD
		}
		roots = retargetRuntimeWorkspaceRoots(threadRecordRuntimeWorkspaceRoots(record, currentCWD, nil), currentCWD, cwd)
	}
	out = make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{
			"environmentId":  id,
			"cwd":            cwd,
			"workspaceRoots": append([]string{}, roots...),
		})
	}
	return out
}

// threadSettingsEnvironmentsForNotification reports the effective selection
// after a settings write. Explicit requests win; otherwise the persisted
// future-turn selection is exposed when the thread has one.
func (r *RuntimeRouter) threadSettingsEnvironmentsForNotification(params *SettingsUpdateParams) *[]ThreadEnvironment {
	if params != nil && params.Environments != nil {
		environments := threadEnvironmentsFromSelections(*params.Environments)
		return &environments
	}
	if r == nil || params == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(params.ThreadID)), true, false)
	if err != nil || record == nil || record.Metadata.Extra == nil {
		return nil
	}
	raw, ok := record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey]
	if !ok {
		return nil
	}
	environments := threadEnvironmentsFromSelections(environmentSelectionsFromAny(raw))
	return &environments
}
