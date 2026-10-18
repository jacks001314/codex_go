package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
	"codex_go/utils"
)

// sandboxPermissionProfileJSON returns the canonical runtime JSON for a
// resolved thread profile without changing its identity.
func sandboxPermissionProfileJSON(resolution *config.SandboxPermissionProfileResolution) string {
	if resolution == nil {
		return ""
	}
	if raw := strings.TrimSpace(resolution.ProfileJSON); raw != "" {
		return raw
	}
	if resolution.Profile == nil {
		return ""
	}
	raw, err := sandbox.RuntimePermissionProfileJSON(*resolution.Profile)
	if err != nil {
		return ""
	}
	return raw
}

// persistedPermissionProfileFromRecord recovers the unnamed runtime profile
// stored by a thread-owned turn-context snapshot. The profile is fallback
// state: an explicit permission selection, a sandbox policy, or a
// sandbox_workspace_write override keeps the request authoritative (Rust
// #53419).
func persistedPermissionProfileFromRecord(record *session.Record, params *turn.TurnStartParams) *sandbox.PermissionProfile {
	if record == nil || params == nil {
		return nil
	}
	if params.PersistedPermissionProfile != nil {
		return params.PersistedPermissionProfile
	}
	if strings.TrimSpace(record.Metadata.ActivePermissionProfile) != "" ||
		params.Permissions != nil ||
		turnStartSandboxPolicyPresent(params.SandboxPolicy) ||
		hasExplicitPermissionConfigOverride(params.Config) {
		return nil
	}
	raw, ok := record.Metadata.Extra["permission_profile"]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	if !persistedPermissionProfileIsPortable(encoded) {
		return nil
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(encoded))
	if err != nil {
		return nil
	}
	return profile
}

func applyPersistedPermissionProfileToTurnParams(record *session.Record, params *turn.TurnStartParams) {
	if params == nil || params.PersistedPermissionProfile != nil {
		return
	}
	params.PersistedPermissionProfile = persistedPermissionProfileFromRecord(record, params)
}

func hasExplicitPermissionConfigOverride(values map[string]any) bool {
	for key, value := range values {
		if value == nil {
			continue
		}
		key = strings.TrimSpace(key)
		switch {
		case strings.EqualFold(key, "permission_profile"),
			strings.EqualFold(key, "default_permissions"),
			strings.EqualFold(key, "sandbox_mode"):
			return true
		case strings.HasPrefix(key, "sandbox_workspace_write"):
			return true
		}
	}
	return false
}

func hasPermissionProfileConfigOverride(values map[string]any) bool {
	for key, value := range values {
		if value == nil {
			continue
		}
		key = strings.TrimSpace(key)
		if strings.EqualFold(key, "permission_profile") || strings.EqualFold(key, "default_permissions") {
			return true
		}
	}
	return false
}

// persistedPermissionProfileIsPortable rejects snapshots that describe an
// executor's enforcement boundary or materialized filesystem grants. Those
// grants may outlive the workspace they came from and are not thread defaults
// (Rust #53419). Managed deny-read entries are preserved by the caller.
func persistedPermissionProfileIsPortable(raw []byte) bool {
	var candidate struct {
		Type    string `json:"type"`
		FileSys *struct {
			Entries []struct {
				Path struct {
					Type string `json:"type"`
				} `json:"path"`
				Access string `json:"access"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(candidate.Type), "external") {
		return false
	}
	if candidate.FileSys == nil {
		return true
	}
	for _, entry := range candidate.FileSys.Entries {
		if !strings.EqualFold(strings.TrimSpace(entry.Access), string(sandbox.FileSystemAccessRead)) {
			continue
		}
		switch strings.TrimSpace(entry.Path.Type) {
		case "", "path", "glob_pattern":
			return false
		}
	}
	return true
}

// restoredPermissionProfileResolution materializes a saved unnamed profile for
// the current turn, adding the effective managed deny-read entries so restoring
// a broader saved profile cannot weaken current policy (Rust #53419).
func restoredPermissionProfileResolution(cfg *config.Config, saved *sandbox.PermissionProfile) (*config.SandboxPermissionProfileResolution, bool) {
	if cfg == nil || saved == nil || !cfg.PermissionProfileSatisfiesRequirements(saved) {
		return nil, false
	}
	restored := *saved
	restored.DeniedReadEntries = append([]sandbox.FileSystemSandboxEntry(nil), saved.DeniedReadEntries...)
	if cfg.Requirements != nil {
		if denials, err := config.FilesystemDenyReadEntriesFromPermissions(cfg.Requirements.Permissions, utils.NativePathConvention()); err == nil {
			restored.DeniedReadEntries = config.AppendFilesystemDenyReadEntries(restored.DeniedReadEntries, denials)
		}
	}
	raw, err := sandbox.RuntimePermissionProfileJSON(restored)
	if err != nil {
		return nil, false
	}
	return &config.SandboxPermissionProfileResolution{Profile: &restored, ProfileJSON: raw}, true
}
