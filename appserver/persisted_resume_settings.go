package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
)

// persistedResumeSettings is the Go projection of Rust's PersistedResumeSettings
// (Rust #57227): the acknowledged thread settings restored when a thread is
// resumed or forked.
type persistedResumeSettings struct {
	ApprovalPolicy               string
	ApprovalsReviewer            string
	ActivePermissionProfile      *sandbox.ActivePermissionProfile
	UnnamedPermissionProfile     *sandbox.PermissionProfile
	UnnamedPermissionProfileJSON json.RawMessage
}

// latestPersistedResumeSettings loads the settings a resume or fork restores
// from the thread's rollout history. A thread-owned snapshot wins over a stale
// TurnContext, and only an owned snapshot may supply raw unnamed permissions.
func (r *Router) latestPersistedResumeSettings(record *session.Record) (persistedResumeSettings, bool) {
	if r == nil || record == nil {
		return persistedResumeSettings{}, false
	}
	path := r.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return persistedResumeSettings{}, false
	}
	if _, ok := rollout.ExistingRolloutPath(path); !ok {
		return persistedResumeSettings{}, false
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return persistedResumeSettings{}, false
	}
	persisted, ok := rollout.LatestPersistedResumeSettings(string(record.ID), lines)
	if !ok {
		return persistedResumeSettings{}, false
	}
	settings := persistedResumeSettings{
		ApprovalPolicy:    persisted.ApprovalPolicy,
		ApprovalsReviewer: persisted.ApprovalsReviewer,
	}
	if len(persisted.ActivePermissionProfile) > 0 {
		var active sandbox.ActivePermissionProfile
		if err := json.Unmarshal(persisted.ActivePermissionProfile, &active); err == nil &&
			strings.TrimSpace(active.ID) != "" {
			settings.ActivePermissionProfile = &active
		}
	}
	if len(persisted.UnnamedPermissionProfile) > 0 && persistedPermissionProfileIsPortable(persisted.UnnamedPermissionProfile) {
		if profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(persisted.UnnamedPermissionProfile)); err == nil {
			settings.UnnamedPermissionProfile = profile
			settings.UnnamedPermissionProfileJSON = append(json.RawMessage(nil), persisted.UnnamedPermissionProfile...)
		}
	}
	return settings, true
}

// threadSettingsRequestOverrides describes the explicit request values that
// keep persisted settings from being restored (Rust has_permission_override).
type threadSettingsRequestOverrides struct {
	ApprovalPolicy    bool
	ApprovalsReviewer bool
	Permissions       bool
	Sandbox           bool
	Config            map[string]any
}

// restoredThreadSettings reports the values a resume or fork response should
// report and mutates the target record so ongoing turns use the same restored
// settings.
type restoredThreadSettings struct {
	ApprovalPolicy          string
	ApprovalsReviewer       *string
	ActivePermissionProfile *sandbox.ActivePermissionProfile
	changed                 bool
}

// restore applies the persisted settings to record, honoring explicit request
// overrides. An unnamed profile is restored only when no named profile applies,
// matching Rust's apply_persisted_permission_profile (#57227).
func (s persistedResumeSettings) restore(record *session.Record, overrides threadSettingsRequestOverrides) restoredThreadSettings {
	var restored restoredThreadSettings
	if !overrides.ApprovalPolicy && strings.TrimSpace(s.ApprovalPolicy) != "" {
		restored.ApprovalPolicy = strings.TrimSpace(s.ApprovalPolicy)
	}
	if !overrides.ApprovalsReviewer && strings.TrimSpace(s.ApprovalsReviewer) != "" {
		reviewer := strings.TrimSpace(s.ApprovalsReviewer)
		restored.ApprovalsReviewer = &reviewer
		if record != nil {
			setThreadRecordConfigOverride(record, "approvals_reviewer", reviewer)
			restored.changed = true
		}
	}
	if !overrides.Permissions && !overrides.Sandbox && !hasExplicitPermissionConfigOverride(overrides.Config) {
		switch {
		case s.ActivePermissionProfile != nil:
			restored.ActivePermissionProfile = s.ActivePermissionProfile
			if record != nil && strings.TrimSpace(record.Metadata.ActivePermissionProfile) != s.ActivePermissionProfile.ID {
				record.Metadata.ActivePermissionProfile = strings.TrimSpace(s.ActivePermissionProfile.ID)
				restored.changed = true
			}
		case s.UnnamedPermissionProfile != nil && record != nil:
			var decoded map[string]any
			if err := json.Unmarshal(s.UnnamedPermissionProfileJSON, &decoded); err == nil {
				record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
				record.Metadata.Extra["permission_profile"] = decoded
				restored.changed = true
			}
		}
	}
	return restored
}

func setThreadRecordConfigOverride(record *session.Record, key string, value any) {
	if record == nil || strings.TrimSpace(key) == "" {
		return
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	overrides := threadRecordConfigOverrides(record)
	if overrides == nil {
		overrides = map[string]any{}
	}
	overrides[key] = value
	record.Metadata.Extra["config"] = overrides
}

// restorePersistedResumeSettings resolves the settings a cold resume restores
// from the resumed thread's history (Rust #57227).
func (r *Router) restorePersistedResumeSettings(record *session.Record, params *ThreadResumeParams) restoredThreadSettings {
	if r == nil || record == nil || params == nil {
		return restoredThreadSettings{}
	}
	persisted, ok := r.latestPersistedResumeSettings(record)
	if !ok {
		return restoredThreadSettings{}
	}
	return persisted.restore(record, threadSettingsRequestOverrides{
		ApprovalPolicy:    params.ApprovalPolicy != nil,
		ApprovalsReviewer: params.ApprovalsReviewer != nil,
		Permissions:       params.Permissions != nil,
		Sandbox:           params.Sandbox != nil,
		Config:            params.Config,
	})
}

// restorePersistedForkSettings applies the source thread's acknowledged
// settings to a fork target, honoring explicit fork overrides (Rust #57227).
func (r *Router) restorePersistedForkSettings(source, target *session.Record, params *ThreadForkParams) restoredThreadSettings {
	if r == nil || source == nil || target == nil || params == nil {
		return restoredThreadSettings{}
	}
	persisted, ok := r.latestPersistedResumeSettings(source)
	if !ok {
		return restoredThreadSettings{}
	}
	return persisted.restore(target, threadSettingsRequestOverrides{
		ApprovalPolicy:    params.ApprovalPolicy != nil,
		ApprovalsReviewer: params.ApprovalsReviewer != nil,
		Permissions:       params.Permissions != nil,
		Sandbox:           params.Sandbox != nil,
		Config:            params.Config,
	})
}

func (restored restoredThreadSettings) approvalPolicyResponse(explicit any) any {
	if explicit != nil {
		return explicit
	}
	if restored.ApprovalPolicy != "" {
		return restored.ApprovalPolicy
	}
	return nil
}

func (restored restoredThreadSettings) approvalsReviewerResponse(explicit *string) *string {
	if explicit != nil {
		return cloneString(explicit)
	}
	return cloneString(restored.ApprovalsReviewer)
}

func (restored restoredThreadSettings) activePermissionProfileResponse(explicit *string) *sandbox.ActivePermissionProfile {
	if profile := activePermissionProfileFromID(explicit); profile != nil {
		return profile
	}
	return restored.ActivePermissionProfile
}
