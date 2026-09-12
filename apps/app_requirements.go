package apps

import (
	"fmt"
	"strings"
)

// This file ports Rust codex_config's app requirement types
// (AppsRequirementsToml / AppRequirementToml / AppToolsRequirementsToml /
// AppToolRequirementToml) and merge_app_requirements_descending, which managed
// sources (Cloud/MDM) use to enforce app disablement across config layers.

// AppToolResultSourceRequirement mirrors Rust
// AppToolResultSourceRequirementToml.
type AppToolResultSourceRequirement struct {
	Format     string
	SourceType string
}

// AppToolRequirement mirrors Rust AppToolRequirementToml: a requirement for one
// app tool. It intentionally has no `enabled` field (Rust ignores unknown keys
// there and only carries the approval mode and analytics source).
type AppToolRequirement struct {
	ApprovalMode          *AppToolApproval
	AnalyticsResultSource *AppToolResultSourceRequirement
}

// AppRequirement mirrors Rust AppRequirementToml.
type AppRequirement struct {
	Enabled *bool
	Tools   map[string]AppToolRequirement
}

// AppsRequirements mirrors Rust AppsRequirementsToml (keyed by app/connector
// id).
type AppsRequirements map[string]AppRequirement

// IsEmpty reports whether every app requirement is empty (Rust
// AppsRequirementsToml::is_empty).
func (r AppsRequirements) IsEmpty() bool {
	for _, requirement := range r {
		if !requirement.isEmpty() {
			return false
		}
	}
	return true
}

func (r AppRequirement) isEmpty() bool {
	if r.Enabled != nil {
		return false
	}
	for _, tool := range r.Tools {
		if tool.ApprovalMode != nil || tool.AnalyticsResultSource != nil {
			return false
		}
	}
	return true
}

// AppsRequirementsFromMap parses the `apps` requirements table. An absent or
// empty table yields nil, mirroring Rust's Option<AppsRequirementsToml>.
func AppsRequirementsFromMap(values any) (AppsRequirements, error) {
	table, ok := values.(map[string]any)
	if !ok || len(table) == 0 {
		return nil, nil
	}
	out := AppsRequirements{}
	for appID, raw := range table {
		appID = strings.TrimSpace(appID)
		if appID == "" {
			continue
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("apps.%s must be a table", appID)
		}
		requirement, err := appRequirementFromMap(appID, entry)
		if err != nil {
			return nil, err
		}
		out[appID] = requirement
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func appRequirementFromMap(appID string, entry map[string]any) (AppRequirement, error) {
	out := AppRequirement{}
	if enabled, ok := entry["enabled"].(bool); ok {
		out.Enabled = &enabled
	}
	tools, ok := entry["tools"].(map[string]any)
	if !ok || len(tools) == 0 {
		return out, nil
	}
	out.Tools = map[string]AppToolRequirement{}
	for toolName, raw := range tools {
		toolName = strings.TrimSpace(toolName)
		if toolName == "" {
			continue
		}
		toolEntry, ok := raw.(map[string]any)
		if !ok {
			return AppRequirement{}, fmt.Errorf("apps.%s.tools.%s must be a table", appID, toolName)
		}
		tool := AppToolRequirement{}
		if mode, ok := toolEntry["approval_mode"].(string); ok && strings.TrimSpace(mode) != "" {
			value := AppToolApproval(strings.TrimSpace(mode))
			tool.ApprovalMode = &value
		}
		if source, ok := toolEntry["analytics_result_source"].(map[string]any); ok {
			format := strings.TrimSpace(stringFromAnyMap(source, "format"))
			sourceType := strings.TrimSpace(stringFromAnyMap(source, "type"))
			if format != "" || sourceType != "" {
				tool.AnalyticsResultSource = &AppToolResultSourceRequirement{Format: format, SourceType: sourceType}
			}
		}
		out.Tools[toolName] = tool
	}
	if len(out.Tools) == 0 {
		out.Tools = nil
	}
	return out, nil
}

// MergeAppRequirementsDescending merges a lower-precedence requirement set into
// a higher-precedence one (Rust merge_app_requirements_descending): either
// layer can disable an app, while an exact tool approval mode keeps the
// higher-precedence value when it is present.
func MergeAppRequirementsDescending(base AppsRequirements, incoming AppsRequirements) AppsRequirements {
	if len(incoming) == 0 {
		return base
	}
	if base == nil {
		base = AppsRequirements{}
	}
	for appID, incomingRequirement := range incoming {
		baseRequirement := base[appID]
		switch {
		case baseRequirement.Enabled != nil && !*baseRequirement.Enabled,
			incomingRequirement.Enabled != nil && !*incomingRequirement.Enabled:
			disabled := false
			baseRequirement.Enabled = &disabled
		case baseRequirement.Enabled != nil:
			// keep the higher-precedence value
		default:
			baseRequirement.Enabled = incomingRequirement.Enabled
		}
		if len(incomingRequirement.Tools) > 0 {
			if baseRequirement.Tools == nil {
				baseRequirement.Tools = map[string]AppToolRequirement{}
			}
			for toolName, incomingTool := range incomingRequirement.Tools {
				baseTool := baseRequirement.Tools[toolName]
				if baseTool.ApprovalMode == nil {
					baseTool.ApprovalMode = incomingTool.ApprovalMode
				}
				if baseTool.AnalyticsResultSource == nil {
					baseTool.AnalyticsResultSource = incomingTool.AnalyticsResultSource
				}
				baseRequirement.Tools[toolName] = baseTool
			}
		}
		base[appID] = baseRequirement
	}
	return base
}

// CloneAppsRequirements deep-copies a requirement set.
func CloneAppsRequirements(values AppsRequirements) AppsRequirements {
	if values == nil {
		return nil
	}
	out := make(AppsRequirements, len(values))
	for appID, requirement := range values {
		cloned := AppRequirement{}
		if requirement.Enabled != nil {
			enabled := *requirement.Enabled
			cloned.Enabled = &enabled
		}
		if len(requirement.Tools) > 0 {
			cloned.Tools = make(map[string]AppToolRequirement, len(requirement.Tools))
			for toolName, tool := range requirement.Tools {
				clonedTool := tool
				if tool.ApprovalMode != nil {
					mode := *tool.ApprovalMode
					clonedTool.ApprovalMode = &mode
				}
				if tool.AnalyticsResultSource != nil {
					source := *tool.AnalyticsResultSource
					clonedTool.AnalyticsResultSource = &source
				}
				cloned.Tools[toolName] = clonedTool
			}
		}
		out[appID] = cloned
	}
	return out
}

// stringFromAnyMap reads a string value from a loosely typed map.
func stringFromAnyMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}
