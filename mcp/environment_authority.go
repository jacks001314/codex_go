package mcp

import (
	"strings"

	managedconfig "codex_go/config"
)

// EnvironmentAuthority captures a thread's turn-environment authority for MCP
// tool availability. It mirrors Rust's McpEnvironmentScope::Selected plus
// McpEnvironmentAuthority (#39335, #46335): the captured selections decide
// whether a registration is unrestricted, restricted by the owner's policy,
// limited to explicitly selected plugins, or unavailable.
//
// A nil authority (or Scoped false) means no selections were captured, so
// attachment-scoped filtering does not apply.
type EnvironmentAuthority struct {
	// Scoped reports whether the thread captured an environment snapshot.
	Scoped bool
	// Unlimited lists selected environments whose configuration comes from the
	// thread or is a ready owner config without an MCP policy.
	Unlimited map[string]bool
	// Restricted maps a selected environment to its owner-supplied MCP policy.
	Restricted map[string]*managedconfig.EnvironmentMCPPolicy
	// Unavailable lists selected environments whose owner configuration is
	// still pending or has failed, so its owner policy is not available.
	Unavailable map[string]bool
}

// Clone deep-copies the authority so runtime configs never alias it.
func (a *EnvironmentAuthority) Clone() *EnvironmentAuthority {
	if a == nil {
		return nil
	}
	clone := &EnvironmentAuthority{Scoped: a.Scoped}
	if a.Unlimited != nil {
		clone.Unlimited = make(map[string]bool, len(a.Unlimited))
		for id, value := range a.Unlimited {
			clone.Unlimited[id] = value
		}
	}
	if a.Unavailable != nil {
		clone.Unavailable = make(map[string]bool, len(a.Unavailable))
		for id, value := range a.Unavailable {
			clone.Unavailable[id] = value
		}
	}
	if a.Restricted != nil {
		clone.Restricted = make(map[string]*managedconfig.EnvironmentMCPPolicy, len(a.Restricted))
		for id, policy := range a.Restricted {
			clone.Restricted[id] = policy.Clone()
		}
	}
	return clone
}

// environmentAuthorityKind mirrors Rust's McpEnvironmentAuthority variants.
type environmentAuthorityKind int

const (
	environmentUnrestricted environmentAuthorityKind = iota
	environmentRestricted
	environmentSelectedPluginsOnly
	environmentUnavailable
)

// outcomeFor mirrors Rust's McpEnvironmentScope::authority_for: a selected
// environment uses its captured configuration state, an unknown non-default
// environment is limited to explicitly selected plugins, and the default
// environment stays unrestricted.
func (a *EnvironmentAuthority) outcomeFor(environmentID string) (environmentAuthorityKind, *managedconfig.EnvironmentMCPPolicy) {
	if a == nil || !a.Scoped {
		return environmentUnrestricted, nil
	}
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		environmentID = DefaultMCPServerEnvironmentID
	}
	if a.Unavailable[environmentID] {
		return environmentUnavailable, nil
	}
	if policy, ok := a.Restricted[environmentID]; ok && policy != nil {
		return environmentRestricted, policy
	}
	if a.Unlimited[environmentID] {
		return environmentUnrestricted, nil
	}
	if environmentID == DefaultMCPServerEnvironmentID {
		return environmentUnrestricted, nil
	}
	return environmentSelectedPluginsOnly, nil
}

// AllowsRegistration mirrors Rust's
// McpCatalogBuilder::build_with_environment_authority: the controller-owned Apps
// registration is exempt, and every other registration is filtered by the
// authority of the environment it belongs to.
func (a *EnvironmentAuthority) AllowsRegistration(name string, registration ServerRegistration) bool {
	if isHostOwnedCodexAppsRegistration(name, registration) {
		return true
	}
	outcome, policy := a.outcomeFor(registration.Config.EffectiveEnvironmentID())
	switch outcome {
	case environmentUnavailable:
		return false
	case environmentSelectedPluginsOnly:
		return SourceFromRegistration(&registration) == CatalogSourceSelectedPlugin
	case environmentRestricted:
		return environmentPolicyAllowsRegistration(name, registration, policy)
	default:
		return true
	}
}

// environmentPolicyAllowsRegistration mirrors the Restricted branch of Rust's
// environment authority filter. Configured, compatibility, and extension
// servers are allowed when the policy declares no configured-server allowlist,
// or when their identity matches the entry for their name. Plugin registrations
// are denied outright by an explicitly empty configured-server allowlist, and
// otherwise follow the per-plugin allowlist when one exists.
func environmentPolicyAllowsRegistration(name string, registration ServerRegistration, policy *managedconfig.EnvironmentMCPPolicy) bool {
	if policy == nil {
		return true
	}
	if name == "" {
		name = strings.TrimSpace(registration.Name)
	}
	switch SourceFromRegistration(&registration) {
	case CatalogSourcePlugin, CatalogSourceSelectedPlugin:
		if policy.MCPServers != nil && len(policy.MCPServers) == 0 {
			return false
		}
		pluginFiltering := false
		for _, requirement := range policy.Plugins {
			if requirement.MCPServers != nil {
				pluginFiltering = true
				break
			}
		}
		if !pluginFiltering {
			return true
		}
		pluginRequirement, ok := policy.Plugins[strings.TrimSpace(registration.PluginID)]
		if !ok || pluginRequirement.MCPServers == nil {
			return false
		}
		requirement, ok := (*pluginRequirement.MCPServers)[name]
		if !ok {
			return false
		}
		return managedMCPRequirementMatches(requirement, &registration.Config)
	default:
		if policy.MCPServers == nil {
			return true
		}
		requirement, ok := policy.MCPServers[name]
		if !ok {
			return false
		}
		return managedMCPRequirementMatches(requirement, &registration.Config)
	}
}

// mcpRegistrationEnvironmentAvailable decides whether a registration is
// available under the runtime's environment authority. The captured authority
// takes precedence; the legacy AvailableEnvironment list keeps the pre-#39335
// behavior for configs that only carry the value-based list.
func mcpRegistrationEnvironmentAvailable(name string, registration ServerRegistration, runtime *RuntimeConfig) bool {
	if runtime == nil {
		return true
	}
	if runtime.EnvironmentAuthority != nil {
		return runtime.EnvironmentAuthority.AllowsRegistration(name, registration)
	}
	return mcpServerEnvironmentAvailable(&registration.Config, runtime.AvailableEnvironment)
}
