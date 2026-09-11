package appserver

import (
	"strings"

	"codex_go/config"
	promptctx "codex_go/prompt"
	"codex_go/session"
	"codex_go/turn"
)

// globalInstructionsRefreshManager keeps the last successful global AGENTS.md
// snapshot and one-warning-per-episode state across refreshes (Rust #44675).
var globalInstructionsRefreshManager = config.NewGlobalInstructionsManager()

// refreshGlobalInstructions reloads the global instructions for a Codex home,
// retaining the last successful snapshot on read failures.
func (r *RuntimeRouter) refreshGlobalInstructions(codexHome string) *config.LoadedUserInstructions {
	return globalInstructionsRefreshManager.Load(codexHome)
}

// instructionsText returns the trimmed instruction text carried by a load.
func instructionsText(loaded *config.LoadedUserInstructions) string {
	if loaded == nil || loaded.Instructions == nil {
		return ""
	}
	return strings.TrimSpace(loaded.Instructions.Text)
}

// joinInstructionsParts composes global and project instruction text with the
// separator thread start uses.
func joinInstructionsParts(global string, project string) string {
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(global); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(project); text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, promptctx.InstructionsAgentsMDSeparator)
}

// emitInstructionWarnings surfaces global-instruction warnings for a thread.
func (r *RuntimeRouter) emitInstructionWarnings(threadID string, warnings []string) {
	if r == nil {
		return
	}
	for _, message := range warnings {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		r.notify(NotificationWarning, &WarningNotification{
			ThreadID: stringPtrIfNotEmpty(strings.TrimSpace(threadID)),
			Message:  message,
		})
	}
}

// refreshThreadGlobalInstructions reloads a thread's global AGENTS.md
// instructions at a turn boundary and recomposes the applied base instructions
// from the fresh global snapshot and the repository snapshot captured at thread
// start (Rust #44675). Unchanged instructions are left untouched, and a removed
// or blank global source clears the applied instructions.
func (r *RuntimeRouter) refreshThreadGlobalInstructions(params *turn.TurnStartParams, record *session.Record) {
	if r == nil || params == nil || record == nil || params.BaseInstructions != nil {
		return
	}
	if !boolFromMap(record.Metadata.Extra, "instructions_from_agents_md") {
		return
	}
	if runtimeRecordIsSubagent(record) {
		// Rust #44675: subagents inherit the parent's applied snapshot instead of
		// refreshing global instructions themselves.
		return
	}
	projectText, _ := record.Metadata.Extra["instructions_project"].(string)
	globalText, _ := record.Metadata.Extra["instructions_global"].(string)
	if codexHome := r.codexHomeForInstructions(); codexHome != "" {
		refreshed := r.refreshGlobalInstructions(codexHome)
		if refreshed != nil {
			// Report fresh warnings even when the retained snapshot is unchanged.
			r.emitInstructionWarnings(params.ThreadID, refreshed.Warnings)
			globalText = instructionsText(refreshed)
		}
	}
	combined := joinInstructionsParts(globalText, projectText)
	if strings.TrimSpace(combined) == strings.TrimSpace(record.Metadata.BaseInstructions) {
		return
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	if strings.TrimSpace(combined) == "" {
		// The source was removed or blank: fall back to model/config instructions.
		record.Metadata.BaseInstructions = ""
		record.Metadata.BaseInstructionsProvenance = nil
		record.Metadata.Extra["instructions_global"] = ""
		_ = r.runtimeSaveThreadRecord(record)
		return
	}
	record.Metadata.BaseInstructions = combined
	record.Metadata.Extra["instructions_global"] = strings.TrimSpace(globalText)
	_ = r.runtimeSaveThreadRecord(record)
}

// turnInstructionsProvider returns a per-request instruction provider for a
// thread whose instructions come from AGENTS.md files, or nil otherwise, so a
// long turn can refresh them between steps (Rust #44675).
func (r *RuntimeRouter) turnInstructionsProvider(threadID string, fallback string) func() string {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil || !boolFromMap(record.Metadata.Extra, "instructions_from_agents_md") {
		return nil
	}
	if runtimeRecordIsSubagent(record) {
		return nil
	}
	return func() string {
		return r.refreshedThreadInstructionText(threadID, fallback)
	}
}

// refreshedThreadInstructionText recomposes the thread's applied instructions
// from the freshly loaded global snapshot and the repository snapshot captured
// at thread start (Rust #44675).
func (r *RuntimeRouter) refreshedThreadInstructionText(threadID string, fallback string) string {
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil || !boolFromMap(record.Metadata.Extra, "instructions_from_agents_md") || runtimeRecordIsSubagent(record) {
		return fallback
	}
	projectText, _ := record.Metadata.Extra["instructions_project"].(string)
	globalText, _ := record.Metadata.Extra["instructions_global"].(string)
	if codexHome := r.codexHomeForInstructions(); codexHome != "" {
		refreshed := r.refreshGlobalInstructions(codexHome)
		if refreshed != nil {
			r.emitInstructionWarnings(threadID, refreshed.Warnings)
			globalText = instructionsText(refreshed)
		}
	}
	return joinInstructionsParts(globalText, projectText)
}
