package agent

import (
	"fmt"
	"sort"
	"strings"
)

const (
	DefaultRoleName             = "default"
	agentTypeUnavailableErrText = "agent type is currently not available"
)

type RoleConfig struct {
	Description        string            `json:"description,omitempty"`
	ConfigFile         string            `json:"config_file,omitempty"`
	NicknameCandidates []string          `json:"nickname_candidates,omitempty"`
	Settings           map[string]string `json:"settings,omitempty"`
}

type RuntimeConfig struct {
	ModelProvider string
	ServiceTier   string
	Model         string
	Settings      map[string]string
	AgentRoles    map[string]RoleConfig
}

type RoleResolver struct {
	builtIns map[string]RoleConfig
}

func NewRoleResolver(builtIns map[string]RoleConfig) *RoleResolver {
	if builtIns == nil {
		builtIns = BuiltInRoles()
	}
	return &RoleResolver{builtIns: cloneRoles(builtIns)}
}

func BuiltInRoles() map[string]RoleConfig {
	return map[string]RoleConfig{
		DefaultRoleName: {
			Description: "General-purpose coding agent.",
		},
		"explorer": {
			Description: "Investigates code and gathers context before implementation.",
			Settings: map[string]string{
				"model_reasoning_effort": "medium",
			},
		},
		"awaiter": {
			Description: "Waits for long-running work and reports completion.",
			Settings: map[string]string{
				"model_reasoning_effort": "low",
			},
		},
	}
}

func (r *RoleResolver) Resolve(config *RuntimeConfig, roleName string) (*RoleConfig, bool) {
	if roleName == "" {
		roleName = DefaultRoleName
	}
	if config != nil {
		if role, ok := config.AgentRoles[roleName]; ok {
			return cloneRole(&role), true
		}
	}
	if role, ok := r.builtIns[roleName]; ok {
		return cloneRole(&role), true
	}
	return nil, false
}

func (r *RoleResolver) Apply(config *RuntimeConfig, roleName string) error {
	if config == nil {
		return fmt.Errorf("%s: nil config", agentTypeUnavailableErrText)
	}
	role, ok := r.Resolve(config, roleName)
	if !ok {
		if roleName == "" {
			roleName = DefaultRoleName
		}
		return fmt.Errorf("unknown agent_type %q", roleName)
	}
	if len(role.Settings) == 0 {
		return nil
	}
	if config.Settings == nil {
		config.Settings = make(map[string]string, len(role.Settings))
	}
	currentProvider := config.ModelProvider
	currentServiceTier := config.ServiceTier
	for key, value := range role.Settings {
		switch key {
		case "model_provider":
			config.ModelProvider = value
		case "service_tier":
			config.ServiceTier = value
		case "model":
			config.Model = value
		default:
			config.Settings[key] = value
		}
	}
	if _, ok := role.Settings["model_provider"]; !ok {
		config.ModelProvider = currentProvider
	}
	if _, ok := role.Settings["service_tier"]; !ok {
		config.ServiceTier = currentServiceTier
	}
	return nil
}

func (r *RoleResolver) SpawnToolDescription(userRoles map[string]RoleConfig) string {
	names := make([]string, 0, len(userRoles)+len(r.builtIns))
	seen := make(map[string]bool, len(userRoles)+len(r.builtIns))
	for name := range userRoles {
		names = append(names, name)
		seen[name] = true
	}
	for name := range r.builtIns {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		role := r.builtIns[name]
		if userRole, ok := userRoles[name]; ok {
			role = userRole
		}
		lines = append(lines, formatRole(name, &role))
	}
	return fmt.Sprintf(
		"Optional type name for the new agent. If omitted, `%s` is used.\nAvailable roles:\n%s",
		DefaultRoleName,
		strings.Join(lines, "\n"),
	)
}

func formatRole(name string, role *RoleConfig) string {
	if role == nil || role.Description == "" {
		return fmt.Sprintf("- `%s`", name)
	}
	notes := lockedSettingsNotes(role.Settings)
	return fmt.Sprintf("- `%s`: %s%s", name, role.Description, notes)
}

func lockedSettingsNotes(settings map[string]string) string {
	if len(settings) == 0 {
		return ""
	}
	model, hasModel := settings["model"]
	effort, hasEffort := settings["model_reasoning_effort"]
	tier, hasTier := settings["service_tier"]
	var notes []string
	switch {
	case hasModel && hasEffort:
		notes = append(notes, fmt.Sprintf(" This role's model is set to `%s` and its reasoning effort is set to `%s`.", model, effort))
	case hasModel:
		notes = append(notes, fmt.Sprintf(" This role's model is set to `%s`.", model))
	case hasEffort:
		notes = append(notes, fmt.Sprintf(" This role's reasoning effort is set to `%s`.", effort))
	}
	if hasTier {
		notes = append(notes, fmt.Sprintf(" This role's service tier is set to `%s`.", tier))
	}
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "")
}

func cloneRole(role *RoleConfig) *RoleConfig {
	if role == nil {
		return nil
	}
	cloned := *role
	cloned.NicknameCandidates = append([]string(nil), role.NicknameCandidates...)
	if role.Settings != nil {
		cloned.Settings = make(map[string]string, len(role.Settings))
		for key, value := range role.Settings {
			cloned.Settings[key] = value
		}
	}
	return &cloned
}

func cloneRoles(roles map[string]RoleConfig) map[string]RoleConfig {
	cloned := make(map[string]RoleConfig, len(roles))
	for key, value := range roles {
		role := cloneRole(&value)
		cloned[key] = *role
	}
	return cloned
}
