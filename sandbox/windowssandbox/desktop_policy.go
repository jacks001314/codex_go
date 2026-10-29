package windowssandbox

import (
	"sort"
	"strings"
)

// DesktopPolicy captures the effective sandbox permissions that determine
// whether a private desktop can be shared. Rust #40672 keeps desktops alive
// only for the same sandbox account and this policy.
type DesktopPolicy struct {
	UsesWriteCapabilities      bool
	CapabilitySIDs             []string
	NetworkEnabled             bool
	NetworkProxyRestrictingSID string
	LegacyReads                bool
	ReadRoots                  []string
	WriteRoots                 []string
	DenyReadPaths              []string
	DenyWritePaths             []string
}

// Key returns a stable cache key for equivalent policies. Paths use the same
// canonical spelling as the ACL helpers and SIDs are case-insensitive.
func (p DesktopPolicy) Key() string {
	return strings.Join([]string{
		boolKey(p.UsesWriteCapabilities),
		strings.Join(normalizedSIDSet(p.CapabilitySIDs), ","),
		boolKey(p.NetworkEnabled),
		strings.ToUpper(strings.TrimSpace(p.NetworkProxyRestrictingSID)),
		boolKey(p.LegacyReads),
		strings.Join(normalizedPathSet(p.ReadRoots), ","),
		strings.Join(normalizedPathSet(p.WriteRoots), ","),
		strings.Join(normalizedPathSet(p.DenyReadPaths), ","),
		strings.Join(normalizedPathSet(p.DenyWritePaths), ","),
	}, "\x00")
}

func NewElevatedDesktopPolicy(request *SandboxSetupRequest, overrides SetupRootOverrides, capabilitySIDs []string, networkProxyRestrictingSID string) (DesktopPolicy, error) {
	if request == nil || request.Permissions == nil {
		return DesktopPolicy{}, ErrInvalidRequest
	}
	if !overrides.ReadRootsSet {
		overrides.ReadRoots = GatherReadRoots(request.CommandCWD, request.Permissions, request.Env, request.CodexHome)
		overrides.ReadRootsSet = true
	}
	readRoots, writeRoots := BuildPayloadRoots(request, overrides)
	denyWritePaths := BuildPayloadDenyWritePaths(request, overrides.DenyWritePaths)
	return DesktopPolicy{
		UsesWriteCapabilities:      request.Permissions.UsesWriteCapabilitiesForCWD(request.CommandCWD, request.Env),
		CapabilitySIDs:             append([]string(nil), capabilitySIDs...),
		NetworkEnabled:             request.Permissions.NetworkEnabled,
		NetworkProxyRestrictingSID: networkProxyRestrictingSID,
		ReadRoots:                  canonicalPolicyPaths(readRoots),
		WriteRoots:                 canonicalPolicyPaths(writeRoots),
		DenyReadPaths:              canonicalPolicyPaths(PlanDenyReadACLPaths(overrides.DenyReadPaths)),
		DenyWritePaths:             canonicalPolicyPaths(denyWritePaths),
	}, nil
}

func NewLegacyDesktopPolicy(permissions *ResolvedWindowsSandboxPermissions, commandCWD string, envMap map[string]string, security *LegacySessionSecurity, additionalDenyWritePaths []string) DesktopPolicy {
	paths := ComputeAllowPathsForPermissions(permissions, commandCWD, envMap)
	capabilitySIDs := []string{}
	usesWriteCapabilities := false
	if security != nil {
		usesWriteCapabilities = security.ReadonlySID == ""
		if security.ReadonlySID != "" {
			capabilitySIDs = append(capabilitySIDs, security.ReadonlySID)
		}
		for _, root := range security.WriteRootSIDs {
			capabilitySIDs = append(capabilitySIDs, root.SID)
		}
	}
	denyWritePaths := append(paths.DenySlice(), additionalDenyWritePaths...)
	return DesktopPolicy{
		UsesWriteCapabilities: usesWriteCapabilities,
		CapabilitySIDs:        capabilitySIDs,
		NetworkEnabled:        permissions != nil && permissions.NetworkEnabled,
		LegacyReads:           true,
		WriteRoots:            canonicalPolicyPaths(paths.AllowSlice()),
		DenyWritePaths:        canonicalPolicyPaths(denyWritePaths),
	}
}

func boolKey(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func normalizedSIDSet(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func canonicalPolicyPaths(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := CanonicalPathKey(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func normalizedPathSet(values []string) []string {
	return canonicalPolicyPaths(values)
}
