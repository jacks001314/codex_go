package config

// ManagedAuthPolicy captures the local requirements.toml authentication
// allowlists (Rust 2994f545a7, #37132). Cloud-provided requirements ignore
// these fields; only the locally managed requirements file may restrict login
// methods and ChatGPT workspaces.
type ManagedAuthPolicy struct {
	AllowedLoginMethods      []ForcedLoginMethod
	AllowedChatGPTWorkspaces []string
}

// AllowsLoginMethod combines the forced login method, the locally allowed
// login methods, and the ChatGPT workspace intersection. ChatGPT logins are
// rejected when the effective workspace list is restricted to an empty set.
func (p *ManagedAuthPolicy) AllowsLoginMethod(method ForcedLoginMethod, forcedLoginMethod ForcedLoginMethod, forcedWorkspaces []string) bool {
	if forcedLoginMethod != "" && forcedLoginMethod != method {
		return false
	}
	if p != nil && p.AllowedLoginMethods != nil {
		found := false
		for _, allowed := range p.AllowedLoginMethods {
			if allowed == method {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if method == ForcedLoginMethodChatGPT {
		if workspaces, restricted := p.EffectiveChatGPTWorkspaces(forcedWorkspaces); restricted && len(workspaces) == 0 {
			return false
		}
	}
	return true
}

// EffectiveChatGPTWorkspaces returns the intersection of the forced workspaces
// with the locally allowed workspaces. The bool result reports whether any
// workspace restriction applies at all (false means unrestricted).
func (p *ManagedAuthPolicy) EffectiveChatGPTWorkspaces(forcedWorkspaces []string) ([]string, bool) {
	var allowed []string
	if p != nil {
		allowed = p.AllowedChatGPTWorkspaces
	}
	switch {
	case len(forcedWorkspaces) > 0 && allowed != nil:
		var intersection []string
		for _, workspace := range forcedWorkspaces {
			if stringSliceContains(allowed, workspace) {
				intersection = append(intersection, workspace)
			}
		}
		return intersection, true
	case len(forcedWorkspaces) > 0:
		return append([]string(nil), forcedWorkspaces...), true
	case allowed != nil:
		return append([]string(nil), allowed...), true
	default:
		return nil, false
	}
}

// ManagedAuthPolicy returns the requirements-backed auth policy for a config.
func (c *Config) ManagedAuthPolicy() *ManagedAuthPolicy {
	if c == nil || c.Requirements == nil {
		return &ManagedAuthPolicy{}
	}
	return &ManagedAuthPolicy{
		AllowedLoginMethods:      forcedLoginMethodsOrNil(c.Requirements.AllowedLoginMethods),
		AllowedChatGPTWorkspaces: stringSliceOrNil(c.Requirements.AllowedChatGPTWorkspaces),
	}
}

// IsLoginMethodAllowed reports whether the login method may be used with this
// config's forced method, requirements allowlist, and workspace restrictions.
func (c *Config) IsLoginMethodAllowed(method ForcedLoginMethod) bool {
	if c == nil {
		return true
	}
	return c.ManagedAuthPolicy().AllowsLoginMethod(method, c.ForcedLoginMethod(), c.ForcedChatGPTWorkspaceIDs())
}

// EffectiveChatGPTWorkspaces applies the requirements workspace allowlist to
// the configured forced workspaces by intersection.
func (c *Config) EffectiveChatGPTWorkspaces() []string {
	if c == nil {
		return nil
	}
	workspaces, _ := c.ManagedAuthPolicy().EffectiveChatGPTWorkspaces(c.ForcedChatGPTWorkspaceIDs())
	return workspaces
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
