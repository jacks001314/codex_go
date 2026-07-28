package codexapi

import (
	"fmt"
	"strings"
)

type WebSearchMode string

const (
	WebSearchModeDisabled WebSearchMode = "disabled"
	WebSearchModeCached   WebSearchMode = "cached"
	WebSearchModeIndexed  WebSearchMode = "indexed"
	WebSearchModeLive     WebSearchMode = "live"
)

// WebSearchModeFromValue mirrors Rust's cached default while accepting the
// legacy boolean/enabled forms still produced by older clients.
func WebSearchModeFromValue(value any) WebSearchMode {
	switch typed := value.(type) {
	case bool:
		if typed {
			return WebSearchModeLive
		}
		return WebSearchModeDisabled
	case *bool:
		if typed == nil {
			return WebSearchModeCached
		}
		return WebSearchModeFromValue(*typed)
	}
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "disabled", "false", "off", "0":
		return WebSearchModeDisabled
	case "indexed":
		return WebSearchModeIndexed
	case "live", "enabled", "true", "on", "1":
		return WebSearchModeLive
	case "cached", "", "<nil>":
		return WebSearchModeCached
	default:
		return WebSearchModeCached
	}
}

func SearchSettingsForMode(mode WebSearchMode, toolConfig map[string]any) *SearchSettings {
	settings := &SearchSettings{
		AllowedCallers:    []AllowedCaller{AllowedCallerDirect},
		ExternalWebAccess: externalWebAccessForMode(mode),
	}
	if contextSize := stringFromSearchConfig(toolConfig, "context_size", "contextSize"); contextSize != "" {
		size := SearchContextSize(contextSize)
		settings.SearchContextSize = &size
	}
	if domains := stringSliceFromSearchConfig(toolConfig, "allowed_domains", "allowedDomains"); len(domains) > 0 {
		settings.Filters = &SearchFilters{AllowedDomains: domains}
	}
	if location := searchLocationFromConfig(toolConfig); location != nil {
		settings.UserLocation = location
	}
	return settings
}

func externalWebAccessForMode(mode WebSearchMode) *ExternalWebAccess {
	switch mode {
	case WebSearchModeIndexed:
		indexed := ExternalWebIndexed
		return &ExternalWebAccess{Mode: &indexed}
	case WebSearchModeLive:
		allowed := true
		return &ExternalWebAccess{Boolean: &allowed}
	default:
		allowed := false
		return &ExternalWebAccess{Boolean: &allowed}
	}
}

func searchLocationFromConfig(config map[string]any) *ApproximateLocation {
	location, ok := mapFromSearchConfig(config["location"])
	if !ok || len(location) == 0 {
		return nil
	}
	return &ApproximateLocation{
		Type:     LocationApproximate,
		Country:  stringPtrFromSearchConfig(location, "country"),
		Region:   stringPtrFromSearchConfig(location, "region"),
		City:     stringPtrFromSearchConfig(location, "city"),
		Timezone: stringPtrFromSearchConfig(location, "timezone"),
	}
}

func stringFromSearchConfig(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func stringPtrFromSearchConfig(values map[string]any, key string) *string {
	value := stringFromSearchConfig(values, key)
	if value == "" {
		return nil
	}
	return &value
}

func stringSliceFromSearchConfig(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		switch typed := values[key].(type) {
		case []string:
			return append([]string(nil), typed...)
		case []any:
			out := make([]string, 0, len(typed))
			for _, value := range typed {
				if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
					out = append(out, text)
				}
			}
			return out
		}
	}
	return nil
}

func mapFromSearchConfig(value any) (map[string]any, bool) {
	values, ok := value.(map[string]any)
	return values, ok
}
