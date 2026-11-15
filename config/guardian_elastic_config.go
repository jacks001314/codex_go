package config

import (
	"fmt"
	"strings"

	codexctx "codex_go/context"
	"codex_go/state"
)

// ValidateGuardianSyncElasticBudget decodes and validates the opt-in section
// policy. Invalid sections are ignored while the switch is off, matching the
// typed Rust config; enabling the switch makes every error fail configuration
// load.
func (c *Config) ValidateGuardianSyncElasticBudget() error {
	if c == nil {
		return nil
	}
	_, err := guardianSyncElasticBudgetFromValues(c.Values)
	return err
}

// ValidateGuardianAsyncElasticBudget decodes and validates the asynchronous
// Guardian section policy (Rust #61345). It is independent of the synchronous
// switch and only fails configuration load when enabled.
func (c *Config) ValidateGuardianAsyncElasticBudget() error {
	if c == nil {
		return nil
	}
	_, err := guardianAsyncElasticBudgetFromValues(c.Values)
	return err
}

// GuardianSyncElasticBudget returns the frozen synchronous Guardian section
// policy, or nil when fixed budgeting is selected.
func (c *Config) GuardianSyncElasticBudget() (*state.GuardianElasticBudgetConfig, error) {
	if c == nil {
		return nil, nil
	}
	return guardianSyncElasticBudgetFromValues(c.Values)
}

// GuardianAsyncElasticBudget returns the frozen asynchronous Guardian section
// policy, or nil when fixed budgeting is selected (Rust #61345).
func (c *Config) GuardianAsyncElasticBudget() (*state.GuardianElasticBudgetConfig, error) {
	if c == nil {
		return nil, nil
	}
	return guardianAsyncElasticBudgetFromValues(c.Values)
}

// GuardianContextBudgeting returns the capture and request policy selected by
// this configuration.
func (c *Config) GuardianContextBudgeting() codexctx.GuardianContextBudgeting {
	if c == nil {
		return codexctx.GuardianContextBudgetingFixed
	}
	features, _ := c.Values["features"].(map[string]any)
	guardian, _ := features["guardianv2"].(map[string]any)
	enabled, _ := guardian["sync_elastic_budgeting"].(bool)
	if enabled {
		return codexctx.GuardianContextBudgetingElastic
	}
	return codexctx.GuardianContextBudgetingFixed
}

func guardianSyncElasticBudgetFromValues(values map[string]any) (*state.GuardianElasticBudgetConfig, error) {
	return guardianElasticBudgetFromValues(values, "sync")
}

func guardianAsyncElasticBudgetFromValues(values map[string]any) (*state.GuardianElasticBudgetConfig, error) {
	return guardianElasticBudgetFromValues(values, "async")
}

func guardianElasticBudgetFromValues(values map[string]any, prefix string) (*state.GuardianElasticBudgetConfig, error) {
	if values == nil {
		return nil, nil
	}
	features, ok := values["features"].(map[string]any)
	if !ok || features == nil {
		return nil, nil
	}
	guardian, ok := features["guardianv2"].(map[string]any)
	if !ok || guardian == nil {
		return nil, nil
	}
	budgetingKey := prefix + "_elastic_budgeting"
	sectionsKey := prefix + "_elastic_sections"
	rawEnabled, present := guardian[budgetingKey]
	if !present || rawEnabled == nil {
		return nil, nil
	}
	enabled, ok := rawEnabled.(bool)
	if !ok {
		return nil, fmt.Errorf("features.guardianv2.%s must be a boolean", budgetingKey)
	}
	if !enabled {
		return nil, nil
	}
	rawSections, ok := guardian[sectionsKey].(map[string]any)
	if !ok || len(rawSections) == 0 {
		return nil, fmt.Errorf("%s is required when %s is enabled", sectionsKey, budgetingKey)
	}
	sections := make(map[state.GuardianElasticSection]state.GuardianElasticSectionLimits, len(rawSections))
	var selectedExecToolNames []string
	for name, rawLimits := range rawSections {
		table, ok := rawLimits.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("features.guardianv2.%s.%s must be a table", sectionsKey, name)
		}
		for field := range table {
			switch field {
			case "min_total_section_tokens", "max_section_entry_tokens", "max_total_section_tokens", "nested_tool_names":
			default:
				return nil, fmt.Errorf("features.guardianv2.%s.%s: unknown field %q", sectionsKey, name, field)
			}
		}
		minimum, err := guardianElasticConfigToken(table, "min_total_section_tokens", sectionsKey, name, true)
		if err != nil {
			return nil, err
		}
		entry, err := guardianElasticConfigToken(table, "max_section_entry_tokens", sectionsKey, name, true)
		if err != nil {
			return nil, err
		}
		limits, err := state.NewGuardianElasticSectionLimits(minimum, entry)
		if err != nil {
			return nil, err
		}
		if rawMaximum, exists := table["max_total_section_tokens"]; exists && rawMaximum != nil {
			maximum, ok := guardianElasticConfigNonNegativeInt(rawMaximum)
			if !ok {
				return nil, fmt.Errorf("features.guardianv2.%s.%s.max_total_section_tokens must be a non-negative integer", sectionsKey, name)
			}
			limits, err = limits.WithMaxTotalSectionTokens(maximum)
			if err != nil {
				return nil, err
			}
		}
		section, err := state.ParseGuardianElasticSection(name)
		if err != nil {
			return nil, err
		}
		if _, duplicate := sections[section]; duplicate {
			return nil, fmt.Errorf("duplicate %s section: %s", sectionsKey, name)
		}
		if prefix == "async" && section == state.GuardianElasticSectionTranscriptSelectedExecResults {
			return nil, fmt.Errorf("transcript_selected_exec_results is only supported for sync elastic budgeting")
		}
		nestedToolNames, nestedErr := guardianElasticNestedToolNames(table, sectionsKey, name, section)
		if nestedErr != nil {
			return nil, nestedErr
		}
		if len(nestedToolNames) > 0 {
			selectedExecToolNames = nestedToolNames
		}
		sections[section] = limits
	}
	policy := &state.GuardianElasticBudgetConfig{Sections: sections, SelectedExecToolNames: selectedExecToolNames}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return policy, nil
}

func guardianElasticConfigToken(table map[string]any, field string, sectionsKey string, section string, required bool) (int, error) {
	raw, present := table[field]
	if !present {
		if required {
			return 0, fmt.Errorf("features.guardianv2.%s.%s.%s is required", sectionsKey, section, field)
		}
		return 0, nil
	}
	value, ok := guardianElasticConfigNonNegativeInt(raw)
	if !ok {
		return 0, fmt.Errorf("features.guardianv2.%s.%s.%s must be a non-negative integer", sectionsKey, section, field)
	}
	return value, nil
}

func guardianElasticConfigNonNegativeInt(raw any) (int, bool) {
	value, ok := configInt(raw)
	return value, ok && value >= 0
}

// guardianElasticNestedToolNames reads the exact nested Code Mode tool names
// that select transcript_selected_exec_results evidence (Rust #60914). Only
// that section may carry them, and the list must be non-empty with no blank
// entries.
func guardianElasticNestedToolNames(table map[string]any, sectionsKey string, name string, section state.GuardianElasticSection) ([]string, error) {
	raw, present := table["nested_tool_names"]
	if section != state.GuardianElasticSectionTranscriptSelectedExecResults {
		if present {
			return nil, fmt.Errorf("nested_tool_names is only supported for transcript_selected_exec_results, found in %s", name)
		}
		return nil, nil
	}
	if !present || raw == nil {
		return nil, fmt.Errorf("transcript_selected_exec_results requires nonempty nested_tool_names")
	}
	values, ok := raw.([]any)
	if !ok {
		if strings, ok := raw.([]string); ok {
			values = make([]any, 0, len(strings))
			for _, value := range strings {
				values = append(values, value)
			}
		} else {
			return nil, fmt.Errorf("features.guardianv2.%s.%s.nested_tool_names must be a list of strings", sectionsKey, name)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("transcript_selected_exec_results requires nonempty nested_tool_names")
	}
	names := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("transcript_selected_exec_results requires nonempty nested_tool_names")
		}
		names = append(names, strings.TrimSpace(text))
	}
	return names, nil
}
