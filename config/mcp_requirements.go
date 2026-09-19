package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const MCPDisabledByRequirements = "disabled by managed requirements"

type MCPServerIdentity struct {
	Command *string
	URL     *string
}

type MCPServerValueMatcher struct {
	Match      string
	Value      string
	Expression string
}

type MCPServerCommandMatcher struct {
	Executable string
	Args       []MCPServerValueMatcher
}

type MCPServerRequirement struct {
	Identity *MCPServerIdentity
	Command  *MCPServerCommandMatcher
	URL      *MCPServerValueMatcher
}

type PluginRequirements struct {
	// A pointer preserves Rust's distinction between no allowlist and an
	// explicitly empty allowlist (deny all).
	MCPServers *map[string]MCPServerRequirement
}

// EnvironmentMCPPolicy is the owner-supplied MCP restriction for one environment
// attachment (Rust protocol::EnvironmentMcpPolicy, #39335). It has the same
// shape as the managed requirements' MCP half and reuses the same matcher
// evaluation.
//
// A nil MCPServers map means the owner supplied no configured-server allowlist
// (unrestricted); a non-nil empty map denies every configured server, while
// plugin registrations are denied only when an allowlist exists at all. Plugins
// carries the per-plugin allowlists.
type EnvironmentMCPPolicy struct {
	MCPServers map[string]MCPServerRequirement
	Plugins    map[string]PluginRequirements
}

// EnvironmentMCPPolicyFromMap parses an `mcp_policy` table (Rust
// EnvironmentMcpPolicy's serde shape: `servers` and `plugins`). A table with
// neither key yields a nil policy, matching Rust's `Option` being absent.
func EnvironmentMCPPolicyFromMap(values map[string]any) (*EnvironmentMCPPolicy, error) {
	if values == nil {
		return nil, nil
	}
	policy := &EnvironmentMCPPolicy{}
	if raw, present := anyKey(values, "servers"); present && raw != nil {
		servers, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid environment mcp_policy servers: expected table")
		}
		parsed, err := mcpServerRequirementsFromMap(servers)
		if err != nil {
			return nil, fmt.Errorf("invalid environment mcp_policy servers: %w", err)
		}
		policy.MCPServers = parsed
	}
	if raw, present := anyKey(values, "plugins"); present && raw != nil {
		plugins, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid environment mcp_policy plugins: expected table")
		}
		parsed, err := pluginRequirementsFromMap(plugins)
		if err != nil {
			return nil, fmt.Errorf("invalid environment mcp_policy plugins: %w", err)
		}
		policy.Plugins = parsed
	}
	if policy.MCPServers == nil && policy.Plugins == nil {
		return nil, nil
	}
	return policy, nil
}

// Clone deep-copies the policy so resolved environment configs never alias the
// maps they were built from.
func (p *EnvironmentMCPPolicy) Clone() *EnvironmentMCPPolicy {
	if p == nil {
		return nil
	}
	clone := &EnvironmentMCPPolicy{}
	if p.MCPServers != nil {
		clone.MCPServers = cloneMCPServerRequirements(p.MCPServers)
		if clone.MCPServers == nil {
			clone.MCPServers = map[string]MCPServerRequirement{}
		}
	}
	if p.Plugins != nil {
		clone.Plugins = make(map[string]PluginRequirements, len(p.Plugins))
		for pluginID, requirement := range p.Plugins {
			if requirement.MCPServers == nil {
				clone.Plugins[pluginID] = PluginRequirements{}
				continue
			}
			servers := cloneMCPServerRequirements(*requirement.MCPServers)
			if servers == nil {
				servers = map[string]MCPServerRequirement{}
			}
			clone.Plugins[pluginID] = PluginRequirements{MCPServers: &servers}
		}
	}
	return clone
}

// ToMap serializes the policy back into its `mcp_policy` table form so a
// resolved environment config can be stored and re-parsed without losing the
// owner restriction.
func (p *EnvironmentMCPPolicy) ToMap() map[string]any {
	if p == nil {
		return nil
	}
	out := map[string]any{}
	if p.MCPServers != nil {
		out["servers"] = mcpServerRequirementsToMap(p.MCPServers)
	}
	if p.Plugins != nil {
		plugins := make(map[string]any, len(p.Plugins))
		for pluginID, requirement := range p.Plugins {
			entry := map[string]any{}
			if requirement.MCPServers != nil {
				entry["mcp_servers"] = mcpServerRequirementsToMap(*requirement.MCPServers)
			}
			plugins[pluginID] = entry
		}
		out["plugins"] = plugins
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mcpServerRequirementsToMap(values map[string]MCPServerRequirement) map[string]any {
	out := make(map[string]any, len(values))
	for name, requirement := range values {
		out[name] = mcpServerRequirementToMap(requirement)
	}
	return out
}

func mcpServerRequirementToMap(requirement MCPServerRequirement) map[string]any {
	identity := map[string]any{}
	switch {
	case requirement.Identity != nil:
		if requirement.Identity.Command != nil {
			identity["command"] = *requirement.Identity.Command
		} else if requirement.Identity.URL != nil {
			identity["url"] = *requirement.Identity.URL
		}
	case requirement.Command != nil:
		args := make([]any, 0, len(requirement.Command.Args))
		for _, matcher := range requirement.Command.Args {
			args = append(args, mcpServerValueMatcherToMap(matcher))
		}
		identity["command"] = map[string]any{"executable": requirement.Command.Executable, "args": args}
	case requirement.URL != nil:
		identity["url"] = mcpServerValueMatcherToMap(*requirement.URL)
	}
	return map[string]any{"identity": identity}
}

func mcpServerValueMatcherToMap(matcher MCPServerValueMatcher) map[string]any {
	if strings.TrimSpace(matcher.Match) == "regex" {
		return map[string]any{"match": matcher.Match, "expression": matcher.Expression}
	}
	return map[string]any{"match": matcher.Match, "value": matcher.Value}
}

func (r MCPServerValueMatcher) Validate() error {
	switch strings.TrimSpace(r.Match) {
	case "exact", "prefix":
		return nil
	case "regex":
		if _, err := regexp.Compile(r.Expression); err != nil {
			return fmt.Errorf("invalid regex %q: %w", r.Expression, err)
		}
		if _, err := regexp.Compile("^(?:" + r.Expression + ")$"); err != nil {
			return fmt.Errorf("regex %q cannot be used for full-value matching: %w", r.Expression, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported matcher %q", r.Match)
	}
}

func (r MCPServerValueMatcher) Matches(candidate string) bool {
	switch strings.TrimSpace(r.Match) {
	case "exact":
		return candidate == r.Value
	case "prefix":
		return strings.HasPrefix(candidate, r.Value)
	case "regex":
		re, err := regexp.Compile("^(?:" + r.Expression + ")$")
		return err == nil && re.MatchString(candidate)
	default:
		return false
	}
}

func (r MCPServerRequirement) Validate() error {
	variants := 0
	if r.Identity != nil {
		variants++
		if (r.Identity.Command == nil) == (r.Identity.URL == nil) {
			return fmt.Errorf("identity requires exactly one of command or url")
		}
	}
	if r.Command != nil {
		variants++
		if strings.TrimSpace(r.Command.Executable) == "" {
			return fmt.Errorf("command matcher executable is required")
		}
		for index, matcher := range r.Command.Args {
			if err := matcher.Validate(); err != nil {
				return fmt.Errorf("invalid argument matcher at index %d: %w", index, err)
			}
		}
	}
	if r.URL != nil {
		variants++
		if err := r.URL.Validate(); err != nil {
			return err
		}
	}
	if variants != 1 {
		return fmt.Errorf("requirement requires exactly one identity matcher")
	}
	return nil
}

func (r MCPServerRequirement) Matches(command string, args []string, rawURL string) bool {
	if r.Identity != nil {
		if r.Identity.Command != nil {
			return rawURL == "" && command == *r.Identity.Command
		}
		if r.Identity.URL != nil {
			return command == "" && rawURL == *r.Identity.URL
		}
	}
	if r.Command != nil {
		if rawURL != "" || command != r.Command.Executable || len(args) != len(r.Command.Args) {
			return false
		}
		for index := range args {
			if !r.Command.Args[index].Matches(args[index]) {
				return false
			}
		}
		return true
	}
	return r.URL != nil && command == "" && r.URL.Matches(rawURL)
}

func MCPRequirementsFingerprint(requirements *ConfigRequirements) string {
	if requirements == nil {
		return ""
	}
	data, err := json.Marshal(struct {
		MCPServers map[string]MCPServerRequirement
		Plugins    map[string]PluginRequirements
	}{requirements.MCPServers, requirements.Plugins})
	if err != nil {
		return fmt.Sprintf("%#v|%#v", requirements.MCPServers, requirements.Plugins)
	}
	return string(data)
}

func CloneConfigRequirements(requirements *ConfigRequirements) *ConfigRequirements {
	return cloneRequirements(requirements)
}
