package appserver

import (
	"encoding/json"
	"fmt"

	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// prepareSubscribedIdleResume preserves the configuration observed by existing
// subscribers. Decide before dispatch persists overrides or adds a subscriber.
func (r *RuntimeRouter) prepareSubscribedIdleResume(request *Request) (*Request, error) {
	// Work on a copy: DecodeParams normalizes an empty Params buffer in place.
	copyRequest := *request
	if request.InternalParams != nil {
		encoded, err := json.Marshal(request.InternalParams)
		if err != nil {
			return nil, err
		}
		copyRequest.Params = encoded
		copyRequest.InternalParams = nil
	}
	request = &copyRequest
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if !r.threadIsLoaded(params.ThreadID) || r.activeRuntimeTurnSnapshot(params.ThreadID) != nil || len(r.threads.Subscribers(params.ThreadID)) == 0 {
		return request, nil
	}
	// Reject corrupt live state before dispatch can persist metadata or clear
	// the active plugin selection. Response construction validates it again.
	if _, err := r.subscribedIdlePermissionProfile(params.ThreadID, r.threadSettingsForTurn(params.ThreadID)); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(request.Params, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"cwd", "model", "modelProvider", "approvalPolicy", "approvalsReviewer", "baseInstructions", "developerInstructions", "config", "personality", "sandbox", "permissions", "serviceTier", "runtimeWorkspaceRoots"} {
		delete(fields, key)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	updated := *request
	updated.Params = encoded
	updated.InternalParams = nil
	return &updated, nil
}

// applySubscribedIdleResumeModelSnapshot reports the live selection rather than
// the older rollout metadata when rejoining a subscribed idle runtime.
func (r *RuntimeRouter) applySubscribedIdleResumeModelSnapshot(result any) error {
	response, ok := result.(*ThreadResumeResponse)
	if !ok || response == nil || response.Thread == nil {
		return nil
	}
	id := response.Thread.ID
	if !r.threadIsLoaded(id) || r.activeRuntimeTurnSnapshot(id) != nil || len(r.threads.Subscribers(id)) == 0 {
		return nil
	}
	settings := r.threadSettingsForTurn(id)
	if settings == nil {
		return nil
	}
	if settings.Model != "" {
		response.Model = settings.Model
	}
	if settings.ModelProvider != "" {
		response.ModelProvider = settings.ModelProvider
		response.Thread.ModelProvider = settings.ModelProvider
	}
	if settings.CWD != "" {
		response.CWD = settings.CWD
		response.Thread.CWD = settings.CWD
	}
	if settings.RuntimeWorkspaceRoots != nil {
		response.RuntimeWorkspaceRoots = append([]string{}, settings.RuntimeWorkspaceRoots...)
	}
	response.ServiceTier = cloneString(settings.ServiceTier)
	if settings.Effort != nil {
		response.ReasoningEffort = cloneString(settings.Effort)
	}
	if settings.ApprovalPolicy != "" {
		response.ApprovalPolicy = threadSettingsApprovalPolicy(settings.ApprovalPolicy)
	}
	if settings.ApprovalsReviewer != "" {
		response.ApprovalsReviewer = stringPtrIfNotEmpty(settings.ApprovalsReviewer)
	}
	profile, err := r.subscribedIdlePermissionProfile(id, settings)
	if err != nil {
		return err
	}
	if profile != nil {
		response.Sandbox = profile.LegacySandboxPolicy()
		response.ActivePermissionProfile = activePermissionProfileFromID(settings.ActivePermissionProfile)
	} else if settings.SandboxPolicy != "" {
		response.Sandbox = threadSettingsSandboxPolicy(settings.SandboxPolicy)
		response.ActivePermissionProfile = nil
	}
	applyThreadModelSettings(response.Thread, settings)
	return nil
}

// subscribedIdlePermissionProfile prefers the current selection over older
// rollout permission metadata, using the same resolver as subsequent turns.
func (r *RuntimeRouter) subscribedIdlePermissionProfile(id string, settings *Settings) (*sandbox.PermissionProfile, error) {
	if settings == nil {
		return nil, nil
	}
	if len(settings.normalizedPermissionProfile) > 0 {
		profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(settings.normalizedPermissionProfile))
		if err != nil {
			return nil, fmt.Errorf("invalid live permission snapshot: %w", err)
		}
		return profile, nil
	}
	if settings.ActivePermissionProfile == nil {
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(id), true, false)
	if err != nil {
		return nil, err
	}
	params := &turn.TurnStartParams{
		ThreadID: id, CWD: settings.CWD, Model: settings.Model,
		Permissions:           cloneString(settings.ActivePermissionProfile),
		RuntimeWorkspaceRoots: append([]string(nil), settings.RuntimeWorkspaceRoots...),
		Config:                threadRecordConfigOverrides(record),
	}
	if params.CWD == "" && record != nil {
		params.CWD = record.Metadata.CWD
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, err
	}
	resolved, err := threadDefaultSandboxPermissionProfile(cfg, params.CWD, params)
	if err != nil {
		return nil, fmt.Errorf("resolve live permission profile: %w", err)
	}
	if resolved == nil || resolved.Profile == nil {
		return nil, fmt.Errorf("live permission profile is unavailable")
	}
	return resolved.Profile, nil
}
