package app

import "codex_go/appserver"

// Rust parity subset: codex-rs/tui/src/app/thread_settings.rs.

type ThreadSettings struct {
	Model       string
	Personality string
}

func ThreadModelSettingUpdateParams(threadID string, model string, collaborationMode map[string]any) *appserver.SettingsUpdateParams {
	if threadID == "" {
		return nil
	}
	return &appserver.SettingsUpdateParams{
		ThreadID:              threadID,
		Model:                 &model,
		CollaborationMode:     cloneAnyMapForThreadSettings(collaborationMode),
		RuntimeWorkspaceRoots: nil,
	}
}

func ThreadReasoningSettingUpdateParams(threadID string, effort *string, collaborationMode map[string]any) *appserver.SettingsUpdateParams {
	if threadID == "" {
		return nil
	}
	return &appserver.SettingsUpdateParams{
		ThreadID:          threadID,
		Effort:            cloneStringPtrForThreadSettings(effort),
		CollaborationMode: cloneAnyMapForThreadSettings(collaborationMode),
	}
}

func ThreadPlanModeSettingUpdateParams(threadID string, collaborationMode map[string]any) *appserver.SettingsUpdateParams {
	if threadID == "" {
		return nil
	}
	return &appserver.SettingsUpdateParams{
		ThreadID:          threadID,
		CollaborationMode: cloneAnyMapForThreadSettings(collaborationMode),
	}
}

func ThreadPersonalitySettingUpdateParams(threadID string, personality string) *appserver.SettingsUpdateParams {
	if threadID == "" {
		return nil
	}
	return &appserver.SettingsUpdateParams{
		ThreadID:    threadID,
		Personality: &personality,
	}
}

func ThreadSettingsUpdateHasChanges(params *appserver.SettingsUpdateParams) bool {
	if params == nil {
		return false
	}
	return params.CWD != nil ||
		params.ApprovalPolicy != nil ||
		params.ApprovalsReviewer != nil ||
		params.SandboxPolicy != nil ||
		params.Permissions != nil ||
		params.Model != nil ||
		params.ServiceTier != nil ||
		params.Effort != nil ||
		params.Summary != nil ||
		params.CollaborationMode != nil ||
		params.Personality != nil ||
		params.MultiAgentMode != nil ||
		len(params.RuntimeWorkspaceRoots) > 0 ||
		len(params.Extra) > 0
}

func cloneAnyMapForThreadSettings(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringPtrForThreadSettings(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
