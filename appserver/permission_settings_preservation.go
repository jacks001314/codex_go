package appserver

import (
	"strings"

	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// permissionProfileForSettingsPreservation resolves the permission policy
// currently in force before an update replaces it. The admitted deny-read
// entries then survive both legacy and named profile replacements (#61134).
func (r *RuntimeRouter) permissionProfileForSettingsPreservation(id string, settings *Settings) (*sandbox.PermissionProfile, error) {
	if r == nil || settings == nil {
		return nil, nil
	}
	if profile, err := r.subscribedIdlePermissionProfile(id, settings); profile != nil || err != nil {
		return profile, err
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(id)), true, false)
	if err != nil {
		return nil, err
	}
	params := &turn.TurnStartParams{
		ThreadID:              strings.TrimSpace(id),
		CWD:                   settings.CWD,
		Model:                 settings.Model,
		RuntimeWorkspaceRoots: append([]string(nil), settings.RuntimeWorkspaceRoots...),
	}
	if record != nil {
		params.Config = threadRecordConfigOverrides(record)
		if params.CWD == "" {
			params.CWD = record.Metadata.CWD
		}
	}
	if strings.TrimSpace(settings.SandboxPolicy) != "" {
		params.SandboxPolicy = settings.SandboxPolicy
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, err
	}
	resolved, err := threadDefaultSandboxPermissionProfile(cfg, params.CWD, params)
	if err != nil {
		// Rust merges the admitted live snapshot, which is always present. Go can
		// reach admission before a profile snapshot exists, so an unresolvable
		// configuration keeps the previous behavior and only skips preservation.
		return nil, nil
	}
	if resolved == nil {
		return nil, nil
	}
	return resolved.Profile, nil
}
