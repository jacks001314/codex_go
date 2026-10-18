package appserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
	"codex_go/session"
	"codex_go/tool"
)

func emptyRequestPermissionsDecision() tool.RequestPermissionsDecision {
	empty := &sandbox.AdditionalPermissionProfile{}
	return tool.RequestPermissionsDecision{
		Approved:    true,
		Permissions: empty,
		Scope:       string(PermissionGrantScopeTurn),
	}
}

func (r *RuntimeRouter) approvedRequestPermissionsDecision(
	threadID,
	turnID,
	environmentID string,
	profile *sandbox.AdditionalPermissionProfile,
	cwd string,
	scope PermissionGrantScope,
	strictAutoReview bool,
) (tool.RequestPermissionsDecision, error) {
	if profile == nil {
		profile = &sandbox.AdditionalPermissionProfile{}
	}
	normalized, err := sandbox.NormalizeAdditionalPermissions(*profile, cwd)
	if err != nil {
		return tool.RequestPermissionsDecision{}, err
	}
	if scope == "" {
		scope = PermissionGrantScopeTurn
	}
	if r != nil {
		r.recordPermissionGrant(threadID, turnID, environmentID, string(scope), &normalized)
	}
	return tool.RequestPermissionsDecision{
		Approved:         true,
		Permissions:      &normalized,
		Scope:            string(scope),
		StrictAutoReview: strictAutoReview,
	}, nil
}

func additionalPermissionProfileFromJSONMap(values map[string]any) (*sandbox.AdditionalPermissionProfile, error) {
	if len(values) == 0 {
		return &sandbox.AdditionalPermissionProfile{}, nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode request_permissions profile: %w", err)
	}
	var wire struct {
		Network    *AdditionalNetworkPermissions    `json:"network"`
		FileSystem *AdditionalFileSystemPermissions `json:"fileSystem"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return nil, fmt.Errorf("decode request_permissions profile: %w", err)
	}
	return additionalPermissionProfileFromWire(wire.Network, wire.FileSystem), nil
}

func additionalPermissionProfileFromGranted(granted *GrantedPermissionProfile) *sandbox.AdditionalPermissionProfile {
	if granted == nil {
		return &sandbox.AdditionalPermissionProfile{}
	}
	return additionalPermissionProfileFromWire(granted.Network, granted.FileSystem)
}

func additionalPermissionProfileFromWire(network *AdditionalNetworkPermissions, fileSystem *AdditionalFileSystemPermissions) *sandbox.AdditionalPermissionProfile {
	profile := &sandbox.AdditionalPermissionProfile{}
	if network != nil && network.Enabled != nil {
		value := *network.Enabled
		profile.Network = &value
	}
	if fileSystem != nil {
		profile.FileSystem = append([]string(nil), fileSystem.Write...)
		profile.ReadFileSystem = append([]string(nil), fileSystem.Read...)
		for _, raw := range fileSystem.Entries {
			encoded, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			var entry sandbox.FileSystemSandboxEntry
			if err := json.Unmarshal(encoded, &entry); err != nil || entry.Path.Type != "path" || entry.Path.Path == "" {
				continue
			}
			switch entry.Access {
			case sandbox.FileSystemAccessWrite:
				profile.FileSystem = append(profile.FileSystem, entry.Path.Path)
			case sandbox.FileSystemAccessRead:
				profile.ReadFileSystem = append(profile.ReadFileSystem, entry.Path.Path)
			}
		}
	}
	return profile
}

func intersectRequestedPermissions(requested, granted *sandbox.AdditionalPermissionProfile, cwd string) *sandbox.AdditionalPermissionProfile {
	if requested == nil {
		return &sandbox.AdditionalPermissionProfile{}
	}
	requestedNormalized, err := sandbox.NormalizeAdditionalPermissions(*requested, cwd)
	if err != nil {
		return &sandbox.AdditionalPermissionProfile{}
	}
	grantedNormalized, err := sandbox.NormalizeAdditionalPermissions(*granted, cwd)
	if err != nil {
		return &sandbox.AdditionalPermissionProfile{}
	}
	subset := &sandbox.AdditionalPermissionProfile{}
	if requestedNormalized.Network != nil && *requestedNormalized.Network &&
		grantedNormalized.Network != nil && *grantedNormalized.Network {
		enabled := true
		subset.Network = &enabled
	}
	subset.FileSystem = permissionPathsWithin(grantedNormalized.FileSystem, requestedNormalized.FileSystem)
	subset.ReadFileSystem = permissionPathsWithin(grantedNormalized.ReadFileSystem, requestedNormalized.ReadFileSystem)
	return subset
}

func permissionPathsWithin(granted, requested []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, grant := range granted {
		for _, request := range requested {
			if grant != request && !sandboxpath.PathWithin(grant, request) {
				continue
			}
			if !seen[grant] {
				seen[grant] = true
				out = append(out, grant)
			}
			break
		}
	}
	return out
}

func (r *RuntimeRouter) activeTurnEnvironmentCWD(threadID, turnID, environmentID string) string {
	if r == nil {
		return ""
	}
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active != nil && active.Params != nil {
		var fallback string
		for _, selection := range active.Params.Environments {
			cwd := strings.TrimSpace(firstNonEmpty(
				threadItemStringFromAnyMap(selection, "cwd"),
				threadItemStringFromAnyMap(selection, "CWD"),
			))
			if fallback == "" {
				fallback = cwd
			}
			id := strings.TrimSpace(firstNonEmpty(
				threadItemStringFromAnyMap(selection, "environmentId"),
				threadItemStringFromAnyMap(selection, "environment_id"),
			))
			if strings.TrimSpace(environmentID) != "" && id == strings.TrimSpace(environmentID) && cwd != "" {
				return cwd
			}
		}
		if fallback != "" {
			return fallback
		}
		if cwd := strings.TrimSpace(active.Params.CWD); cwd != "" {
			return cwd
		}
	}
	if record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), false, false); err == nil && record != nil {
		return strings.TrimSpace(record.Metadata.CWD)
	}
	return ""
}
