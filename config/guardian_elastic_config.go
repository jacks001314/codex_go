package config

import (
	"fmt"

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

// GuardianSyncElasticBudget returns the frozen synchronous Guardian section
// policy, or nil when fixed budgeting is selected.
func (c *Config) GuardianSyncElasticBudget() (*state.GuardianElasticBudgetConfig, error) {
	if c == nil {
		return nil, nil
	}
	return guardianSyncElasticBudgetFromValues(c.Values)
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
	rawEnabled, present := guardian["sync_elastic_budgeting"]
	if !present || rawEnabled == nil {
		return nil, nil
	}
	enabled, ok := rawEnabled.(bool)
	if !ok {
		return nil, fmt.Errorf("features.guardianv2.sync_elastic_budgeting must be a boolean")
	}
	if !enabled {
		return nil, nil
	}
	rawSections, ok := guardian["sync_elastic_sections"].(map[string]any)
	if !ok || len(rawSections) == 0 {
		return nil, fmt.Errorf("sync_elastic_sections is required when sync_elastic_budgeting is enabled")
	}
	sections := make(map[state.GuardianElasticSection]state.GuardianElasticSectionLimits, len(rawSections))
	for name, rawLimits := range rawSections {
		table, ok := rawLimits.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("features.guardianv2.sync_elastic_sections.%s must be a table", name)
		}
		for field := range table {
			switch field {
			case "min_total_section_tokens", "max_section_entry_tokens", "max_total_section_tokens":
			default:
				return nil, fmt.Errorf("features.guardianv2.sync_elastic_sections.%s: unknown field %q", name, field)
			}
		}
		minimum, err := guardianElasticConfigToken(table, "min_total_section_tokens", name, true)
		if err != nil {
			return nil, err
		}
		entry, err := guardianElasticConfigToken(table, "max_section_entry_tokens", name, true)
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
				return nil, fmt.Errorf("features.guardianv2.sync_elastic_sections.%s.max_total_section_tokens must be a non-negative integer", name)
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
			return nil, fmt.Errorf("duplicate sync_elastic_sections section: %s", name)
		}
		sections[section] = limits
	}
	policy := &state.GuardianElasticBudgetConfig{Sections: sections}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return policy, nil
}

func guardianElasticConfigToken(table map[string]any, field string, section string, required bool) (int, error) {
	raw, present := table[field]
	if !present {
		if required {
			return 0, fmt.Errorf("features.guardianv2.sync_elastic_sections.%s.%s is required", section, field)
		}
		return 0, nil
	}
	value, ok := guardianElasticConfigNonNegativeInt(raw)
	if !ok {
		return 0, fmt.Errorf("features.guardianv2.sync_elastic_sections.%s.%s must be a non-negative integer", section, field)
	}
	return value, nil
}

func guardianElasticConfigNonNegativeInt(raw any) (int, bool) {
	value, ok := configInt(raw)
	return value, ok && value >= 0
}
