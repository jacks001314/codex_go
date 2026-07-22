package config

import (
	"fmt"
	"sort"
	"strings"
)

func ValidateShellEnvironmentPolicy(value any) error {
	if value == nil {
		return nil
	}
	policy, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("shell_environment_policy must be a table")
	}
	_, hasFilters := policy["filters"]
	_, hasExclude := policy["exclude"]
	_, hasInclude := policy["include_only"]
	if hasFilters && (hasExclude || hasInclude) {
		return fmt.Errorf("cannot mix `filters` with legacy `exclude` or `include_only`")
	}
	if !hasFilters {
		return nil
	}
	filters, ok := policy["filters"].(map[string]any)
	if !ok {
		return fmt.Errorf("shell_environment_policy.filters must be a table")
	}
	seen := map[string]string{}
	for pattern, raw := range filters {
		normalized := strings.ToLower(pattern)
		if prior, exists := seen[normalized]; exists {
			return fmt.Errorf("duplicate shell environment filter `%s` ignoring case (conflicts with `%s`)", pattern, prior)
		}
		seen[normalized] = pattern
		action, ok := raw.(string)
		if !ok || (action != "include" && action != "exclude") {
			return fmt.Errorf("shell environment filter `%s` must be `include` or `exclude`", pattern)
		}
	}
	return nil
}

func mergeShellEnvironmentPolicy(base, overlay map[string]any) map[string]any {
	out, _ := cloneConfigValue(base).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	_, overlayFilters := overlay["filters"]
	_, overlayExclude := overlay["exclude"]
	_, overlayInclude := overlay["include_only"]
	if overlayFilters {
		delete(out, "exclude")
		delete(out, "include_only")
	}
	if overlayExclude || overlayInclude {
		delete(out, "filters")
	}
	for key, value := range overlay {
		if key == "filters" {
			baseFilters, _ := out[key].(map[string]any)
			overlayMap, _ := value.(map[string]any)
			merged := map[string]any{}
			for k, v := range baseFilters {
				merged[strings.ToLower(k)] = v
			}
			for k, v := range overlayMap {
				merged[strings.ToLower(k)] = v
			}
			out[key] = merged
			continue
		}
		out[key] = cloneConfigValue(value)
	}
	return out
}
func ShellEnvironmentFilterPatterns(policy map[string]any) (include, exclude []string, err error) {
	if err = ValidateShellEnvironmentPolicy(policy); err != nil {
		return
	}
	filters, _ := policy["filters"].(map[string]any)
	for pattern, raw := range filters {
		if raw == "include" {
			include = append(include, pattern)
		} else if raw == "exclude" {
			exclude = append(exclude, pattern)
		}
	}
	sort.Strings(include)
	sort.Strings(exclude)
	return
}
