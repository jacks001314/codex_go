package mcp

import (
	"strings"

	"codex_go/sandbox"
)

// SetServerPermissionProfiles mirrors Rust
// McpConfig::set_server_permission_profiles (#40728): each enabled runtime
// server is resolved against the exact attachment permissions being published.
// A server whose authority cannot be resolved gets no entry, so its calls and
// elicitations are rejected instead of inheriting another owner's authority.
func (r *RuntimeConfig) SetServerPermissionProfiles(environmentProfiles map[string]*sandbox.PermissionProfile) {
	if r == nil {
		return
	}
	resolved := map[string]*sandbox.PermissionProfile{}
	for name, registration := range r.Servers {
		name = strings.TrimSpace(name)
		if name == "" {
			name = strings.TrimSpace(registration.Name)
		}
		if name == "" || !registration.Config.Enabled {
			continue
		}
		// Attachment-scoped servers stay out of the effective set while their
		// environment is unselected or unavailable (Rust #39335).
		if !mcpRegistrationEnvironmentAvailable(name, registration, r) {
			continue
		}
		profile, ok := resolveMCPServerPermissionProfile(name, registration, r.PermissionProfile, environmentProfiles)
		if !ok {
			continue
		}
		resolved[name] = cloneMCPPermissionProfile(profile)
	}
	r.ServerPermissionProfiles = resolved
}

// resolveMCPServerPermissionProfile mirrors Rust's resolution rules: the
// controller-owned Apps server uses the thread authority, an attached executor
// environment uses its own published authority, local and selected-plugin
// servers use the thread authority, and anything else is unresolved.
func resolveMCPServerPermissionProfile(
	name string,
	registration ServerRegistration,
	threadProfile *sandbox.PermissionProfile,
	environmentProfiles map[string]*sandbox.PermissionProfile,
) (*sandbox.PermissionProfile, bool) {
	if IsCodexAppsMCPServerName(name) {
		return threadProfile, threadProfile != nil
	}
	if environmentProfiles != nil {
		if profile, ok := environmentProfiles[registration.Config.EffectiveEnvironmentID()]; ok && profile != nil {
			return profile, true
		}
	}
	if registration.Config.IsLocalEnvironment() || SourceFromRegistration(&registration) == CatalogSourceSelectedPlugin {
		return threadProfile, threadProfile != nil
	}
	return nil, false
}

// PermissionProfileForServer returns the authority published for an enabled MCP
// server. A false result means the server has no published authority.
func (r *RuntimeConfig) PermissionProfileForServer(name string) (*sandbox.PermissionProfile, bool) {
	if r == nil {
		return nil, false
	}
	profile, ok := r.ServerPermissionProfiles[strings.TrimSpace(name)]
	return profile, ok
}

// ForThreadlessOperations mirrors Rust McpConfig::for_threadless_operations:
// standalone discovery and resource reads must never inherit thread execution
// authority, so both the thread profile and every enabled server get the
// restrictive default.
//
// Residual: Rust's default is `Managed/Restricted` with no file-system entries;
// Go's sandbox model has no representation for that profile, so the closest
// existing restrictive profile (read-only) is used.
func (r *RuntimeConfig) ForThreadlessOperations() *RuntimeConfig {
	if r == nil {
		return nil
	}
	clone := *r
	clone.PermissionProfile = threadlessMCPPermissionProfile()
	clone.ServerPermissionProfiles = map[string]*sandbox.PermissionProfile{}
	for name, registration := range r.Servers {
		name = strings.TrimSpace(name)
		if name == "" {
			name = strings.TrimSpace(registration.Name)
		}
		if name == "" || !registration.Config.Enabled {
			continue
		}
		if !mcpRegistrationEnvironmentAvailable(name, registration, r) {
			continue
		}
		clone.ServerPermissionProfiles[name] = cloneMCPPermissionProfile(threadlessMCPPermissionProfile())
	}
	return &clone
}

func threadlessMCPPermissionProfile() *sandbox.PermissionProfile {
	profile := sandbox.ReadOnlyPermissionProfile()
	return &profile
}

// PermissionProfile returns the published thread execution authority, or nil
// when none was published.
func (s *MCPService) PermissionProfile() *sandbox.PermissionProfile {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneMCPPermissionProfile(s.permissionProfile)
}

// PermissionProfileForServer returns the authority published for an enabled MCP
// server. A false result means the server has no published authority.
func (s *MCPService) PermissionProfileForServer(name string) (*sandbox.PermissionProfile, bool) {
	if s == nil {
		return nil, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.serverPermissionProfiles[name]
	if !ok {
		return nil, false
	}
	return cloneMCPPermissionProfile(profile), true
}

// HasPublishedPermissionAuthority reports whether this service's runtime
// published per-server authority. When true, a server absent from the published
// set has no authority and must be rejected (Rust #40728).
func (s *MCPService) HasPublishedPermissionAuthority() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverPermissionProfiles != nil
}

func cloneMCPPermissionProfile(profile *sandbox.PermissionProfile) *sandbox.PermissionProfile {
	if profile == nil {
		return nil
	}
	clone := *profile
	clone.SandboxPolicy = cloneMCPSandboxPolicy(profile.SandboxPolicy)
	clone.DeniedReadEntries = append([]sandbox.FileSystemSandboxEntry(nil), profile.DeniedReadEntries...)
	return &clone
}

func cloneMCPSandboxPolicy(policy *sandbox.SandboxPolicy) *sandbox.SandboxPolicy {
	if policy == nil {
		return nil
	}
	clone := *policy
	clone.WritableRoots = append([]string(nil), policy.WritableRoots...)
	return &clone
}
