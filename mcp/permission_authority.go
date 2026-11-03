package mcp

import (
	"strings"

	"codex_go/sandbox"
)

// MCPServerPermissionSource is the permission policy for one MCP server and the
// directory context used to interpret it. Rust #59020 distinguishes a
// thread-owned profile (interpreted at the host cwd) from an attached
// environment's profile (interpreted at that environment's captured cwd).
// Prepared calls retain this source even when the live MCP runtime refreshes.
type MCPServerPermissionSource struct {
	Profile       *sandbox.PermissionProfile
	CWD           string
	EnvironmentID string
	Thread        bool
}

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
	sources := map[string]MCPServerPermissionSource{}
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
		source, ok := resolveMCPServerPermissionSource(name, registration, r, environmentProfiles)
		if !ok {
			continue
		}
		resolved[name] = cloneMCPPermissionProfile(source.Profile)
		sources[name] = cloneMCPServerPermissionSource(source)
	}
	r.ServerPermissionProfiles = resolved
	r.ServerPermissionSources = sources
}

// resolveMCPServerPermissionSource mirrors Rust's resolution rules: the
// controller-owned Apps server uses the thread authority and host cwd, an
// attached executor environment uses its own published authority and cwd,
// local and selected-plugin servers use the thread authority, and anything else
// is unresolved.
func resolveMCPServerPermissionSource(
	name string,
	registration ServerRegistration,
	runtime *RuntimeConfig,
	environmentProfiles map[string]*sandbox.PermissionProfile,
) (MCPServerPermissionSource, bool) {
	threadSource := MCPServerPermissionSource{Profile: runtime.PermissionProfile, CWD: strings.TrimSpace(runtime.HostCWD), Thread: true}
	if IsCodexAppsMCPServerName(name) {
		return threadSource, threadSource.Profile != nil
	}
	environmentID := registration.Config.EffectiveEnvironmentID()
	if environmentProfiles != nil {
		if profile, ok := environmentProfiles[environmentID]; ok && profile != nil {
			return MCPServerPermissionSource{
				Profile:       profile,
				CWD:           firstNonEmptyMCP(strings.TrimSpace(runtime.EnvironmentCWDs[environmentID]), strings.TrimSpace(registration.Config.CWD)),
				EnvironmentID: environmentID,
			}, true
		}
	}
	if registration.Config.IsLocalEnvironment() || SourceFromRegistration(&registration) == CatalogSourceSelectedPlugin {
		return threadSource, threadSource.Profile != nil
	}
	return MCPServerPermissionSource{}, false
}

// PermissionProfileForServer returns the authority published for an enabled MCP
// server. A false result means the server has no published authority.
func (r *RuntimeConfig) PermissionProfileForServer(name string) (*sandbox.PermissionProfile, bool) {
	if r == nil {
		return nil, false
	}
	if source, ok := r.ServerPermissionSources[strings.TrimSpace(name)]; ok {
		return cloneMCPPermissionProfile(source.Profile), true
	}
	profile, ok := r.ServerPermissionProfiles[strings.TrimSpace(name)]
	return profile, ok
}

// PermissionSourceForServer returns the permission policy and the captured
// directory context for an enabled server. The source is copied so callers
// cannot mutate the published runtime.
func (r *RuntimeConfig) PermissionSourceForServer(name string) (MCPServerPermissionSource, bool) {
	if r == nil {
		return MCPServerPermissionSource{}, false
	}
	name = strings.TrimSpace(name)
	if source, ok := r.ServerPermissionSources[name]; ok {
		return cloneMCPServerPermissionSource(source), true
	}
	if profile, ok := r.ServerPermissionProfiles[name]; ok {
		return MCPServerPermissionSource{Profile: cloneMCPPermissionProfile(profile), CWD: strings.TrimSpace(r.HostCWD), Thread: true}, true
	}
	return MCPServerPermissionSource{}, false
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
	clone.ServerPermissionSources = map[string]MCPServerPermissionSource{}
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
		profile := threadlessMCPPermissionProfile()
		clone.ServerPermissionProfiles[name] = cloneMCPPermissionProfile(profile)
		clone.ServerPermissionSources[name] = MCPServerPermissionSource{Profile: profile, CWD: strings.TrimSpace(clone.HostCWD), Thread: true}
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
	if source, ok := s.serverPermissionSources[name]; ok {
		return cloneMCPPermissionProfile(source.Profile), true
	}
	profile, ok := s.serverPermissionProfiles[name]
	if !ok {
		return nil, false
	}
	return cloneMCPPermissionProfile(profile), true
}

// PermissionSourceForServer returns the policy and directory context published
// for a server. A prepared binding freezes this value before any runtime
// refresh (Rust #59020).
func (s *MCPService) PermissionSourceForServer(name string) (MCPServerPermissionSource, bool) {
	if s == nil {
		return MCPServerPermissionSource{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return MCPServerPermissionSource{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if source, ok := s.serverPermissionSources[name]; ok {
		return cloneMCPServerPermissionSource(source), true
	}
	if profile, ok := s.serverPermissionProfiles[name]; ok {
		return MCPServerPermissionSource{Profile: cloneMCPPermissionProfile(profile)}, true
	}
	return MCPServerPermissionSource{}, false
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

func cloneMCPServerPermissionSource(source MCPServerPermissionSource) MCPServerPermissionSource {
	source.Profile = cloneMCPPermissionProfile(source.Profile)
	return source
}

func cloneMCPSandboxPolicy(policy *sandbox.SandboxPolicy) *sandbox.SandboxPolicy {
	if policy == nil {
		return nil
	}
	clone := *policy
	clone.WritableRoots = append([]string(nil), policy.WritableRoots...)
	return &clone
}
