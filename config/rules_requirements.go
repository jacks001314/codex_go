package config

import (
	"fmt"
	"strings"

	"codex_go/execpolicy"
)

// rulesRequirementFromAny parses the managed `[rules]` requirements section
// (Rust #58045 ConfigRequirementsToml.rules / RequirementsExecPolicyToml). The
// section is a list of `prefix_rule` tables that share the execpolicy DSL's
// matching semantics and additionally allow the managed-only `prompt-user`
// decision. `allow` stays forbidden because Codex merges these rules with the
// rest of the configuration and keeps the most restrictive result.
func rulesRequirementFromAny(value any) (*execpolicy.RequirementsPolicy, error) {
	table, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid type for rules: expected table")
	}
	rawRules, present, err := rawListAnyKey(table, "prefix_rules", "prefixRules")
	if err != nil {
		return nil, err
	}
	if !present || len(rawRules) == 0 {
		return nil, fmt.Errorf("rules prefix_rules cannot be empty")
	}
	rules := make([]execpolicy.PrefixRule, 0, len(rawRules))
	for ruleIndex, rawRule := range rawRules {
		ruleTable, ok := rawRule.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rules prefix_rule at index %d must be a table", ruleIndex)
		}
		pattern, err := requirementsPrefixPattern(ruleTable, ruleIndex)
		if err != nil {
			return nil, err
		}
		decisionValue, present := stringAnyKey(ruleTable, "decision")
		if !present {
			return nil, fmt.Errorf("rules prefix_rule at index %d is missing a decision", ruleIndex)
		}
		decision := execpolicy.Decision(strings.ToLower(strings.TrimSpace(decisionValue)))
		switch decision {
		case execpolicy.DecisionAllow:
			return nil, fmt.Errorf("rules prefix_rule at index %d has decision 'allow', which is not permitted in requirements.toml: Codex merges these rules with other config and uses the most restrictive result (use 'prompt', 'prompt-user', or 'forbidden')", ruleIndex)
		case execpolicy.DecisionPrompt, execpolicy.DecisionPromptUser, execpolicy.DecisionForbidden:
		default:
			return nil, fmt.Errorf("rules prefix_rule at index %d has unsupported decision %q", ruleIndex, decisionValue)
		}
		justification := ""
		if value, present := stringAnyKey(ruleTable, "justification"); present {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("rules prefix_rule at index %d has an empty justification", ruleIndex)
			}
			justification = value
		}
		rules = append(rules, execpolicy.PrefixRule{
			Pattern:       pattern,
			Decision:      decision,
			Justification: justification,
			Source:        "requirements",
		})
	}
	return execpolicy.NewRequirementsPolicy(&execpolicy.Policy{Rules: rules}), nil
}

func requirementsPrefixPattern(rule map[string]any, ruleIndex int) ([][]string, error) {
	rawTokens, present, err := rawListAnyKey(rule, "pattern")
	if err != nil {
		return nil, fmt.Errorf("rules prefix_rule at index %d has an empty pattern", ruleIndex)
	}
	if !present || len(rawTokens) == 0 {
		return nil, fmt.Errorf("rules prefix_rule at index %d has an empty pattern", ruleIndex)
	}
	pattern := make([][]string, 0, len(rawTokens))
	for tokenIndex, rawToken := range rawTokens {
		token, ok := rawToken.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rules prefix_rule at index %d has an invalid pattern token at index %d: expected table", ruleIndex, tokenIndex)
		}
		single, hasSingle := stringAnyKey(token, "token")
		alternatives, hasAlternatives := stringListAnyKey(token, "any_of", "anyOf")
		switch {
		case hasSingle && hasAlternatives:
			return nil, fmt.Errorf("rules prefix_rule at index %d has an invalid pattern token at index %d: set either token or any_of, not both", ruleIndex, tokenIndex)
		case hasSingle:
			if strings.TrimSpace(single) == "" {
				return nil, fmt.Errorf("rules prefix_rule at index %d has an invalid pattern token at index %d: token cannot be empty", ruleIndex, tokenIndex)
			}
			pattern = append(pattern, []string{single})
		case hasAlternatives:
			if len(alternatives) == 0 {
				return nil, fmt.Errorf("rules prefix_rule at index %d has an invalid pattern token at index %d: any_of cannot be empty", ruleIndex, tokenIndex)
			}
			for _, alternative := range alternatives {
				if strings.TrimSpace(alternative) == "" {
					return nil, fmt.Errorf("rules prefix_rule at index %d has an invalid pattern token at index %d: any_of cannot include empty tokens", ruleIndex, tokenIndex)
				}
			}
			pattern = append(pattern, append([]string(nil), alternatives...))
		default:
			return nil, fmt.Errorf("rules prefix_rule at index %d has an invalid pattern token at index %d: set either token or any_of", ruleIndex, tokenIndex)
		}
	}
	return pattern, nil
}

// rawListAnyKey reads a TOML array under either of its accepted spellings.
// A key that is present but not an array is a type error.
func rawListAnyKey(values map[string]any, keys ...string) ([]any, bool, error) {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok || raw == nil {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, true, fmt.Errorf("invalid type for %s: expected array", key)
		}
		return list, true, nil
	}
	return nil, false, nil
}
