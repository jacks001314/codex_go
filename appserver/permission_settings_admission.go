package appserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// normalizeLegacySettingsPermissionProfile produces a detached permission
// snapshot; neither the caller's compatibility input nor current record changes.
func normalizeLegacySettingsPermissionProfile(policy string, record *session.Record) (json.RawMessage, error) {
	_, profile, err := turnSandboxPolicyPermissionProfile(policy)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, fmt.Errorf("sandbox policy is required")
	}
	if previous := recordPermissionProfileJSON(record); len(previous) > 0 {
		current, err := sandbox.ParseRuntimePermissionProfileJSON(string(previous))
		if err != nil {
			return nil, err
		}
		profile, err = sandbox.PreserveDeniedReads(*profile, *current)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := sandbox.RuntimePermissionProfileJSON(*profile)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

// normalizeThreadLegacySettings also retains denials from the currently named
// profile, which may not yet have a materialized rollout snapshot.
func (r *RuntimeRouter) normalizeThreadLegacySettings(params *SettingsUpdateParams, record *session.Record) (json.RawMessage, error) {
	settings := r.threadSettingsForTurn(params.ThreadID)
	if settings != nil && len(settings.normalizedPermissionProfile) > 0 {
		// A previous accepted update can be newer than the persisted record.
		// Do not resurrect its old named identity or lose its accumulated denies.
		current := &session.Record{Metadata: session.Metadata{Extra: map[string]any{
			"permission_profile": settings.normalizedPermissionProfile,
		}}}
		return normalizeLegacySettingsPermissionProfile(*params.SandboxPolicy, current)
	}
	raw, err := normalizeLegacySettingsPermissionProfile(*params.SandboxPolicy, record)
	if err != nil {
		return nil, err
	}
	id, cwd := "", ""
	if record != nil {
		id, cwd = record.Metadata.ActivePermissionProfile, record.Metadata.CWD
	}
	if settings != nil {
		if settings.ActivePermissionProfile != nil {
			id = *settings.ActivePermissionProfile
		}
		if settings.CWD != "" {
			cwd = settings.CWD
		}
	}
	if strings.TrimSpace(id) == "" || r.services.Config == nil {
		return raw, nil
	}
	currentParams := &turn.TurnStartParams{ThreadID: params.ThreadID, CWD: cwd, Permissions: &id}
	if record != nil {
		currentParams.Config = threadRecordConfigOverrides(record)
	}
	if settings != nil {
		currentParams.RuntimeWorkspaceRoots = append([]string(nil), settings.RuntimeWorkspaceRoots...)
	}
	cfg, err := r.effectiveConfigForTurn(currentParams)
	if err != nil {
		return nil, err
	}
	current, err := threadDefaultSandboxPermissionProfile(cfg, cwd, currentParams)
	if err != nil {
		return nil, err
	}
	if current == nil || current.Profile == nil {
		return nil, fmt.Errorf("current permission profile is unavailable")
	}
	requested, err := sandbox.ParseRuntimePermissionProfileJSON(string(raw))
	if err != nil {
		return nil, err
	}
	merged, err := sandbox.PreserveDeniedReads(*requested, *current.Profile)
	if err != nil {
		return nil, err
	}
	encoded, err := sandbox.RuntimePermissionProfileJSON(*merged)
	return json.RawMessage(encoded), err
}
