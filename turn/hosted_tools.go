package turn

import (
	"strings"

	"codex_go/codexapi"
)

const (
	HostedImageGenerationToolType = "image_generation"
	HostedWebSearchToolType       = "web_search"
)

func HostedImageGenerationTool(outputFormat string) map[string]any {
	outputFormat = strings.TrimSpace(outputFormat)
	if outputFormat == "" {
		outputFormat = "png"
	}
	return map[string]any{
		"type":          HostedImageGenerationToolType,
		"output_format": outputFormat,
	}
}

func HostedWebSearchTool(mode codexapi.WebSearchMode, settings *codexapi.SearchSettings, modelToolType string) map[string]any {
	if mode == codexapi.WebSearchModeDisabled {
		return nil
	}
	tool := map[string]any{
		"type":                HostedWebSearchToolType,
		"external_web_access": mode == codexapi.WebSearchModeLive || mode == codexapi.WebSearchModeIndexed,
	}
	if mode == codexapi.WebSearchModeIndexed {
		tool["indexed_web_access"] = true
	}
	if settings != nil {
		if settings.Filters != nil && len(settings.Filters.AllowedDomains) > 0 {
			tool["filters"] = map[string]any{"allowed_domains": append([]string(nil), settings.Filters.AllowedDomains...)}
		}
		if settings.UserLocation != nil {
			location := map[string]any{"type": settings.UserLocation.Type}
			if settings.UserLocation.Country != nil {
				location["country"] = *settings.UserLocation.Country
			}
			if settings.UserLocation.Region != nil {
				location["region"] = *settings.UserLocation.Region
			}
			if settings.UserLocation.City != nil {
				location["city"] = *settings.UserLocation.City
			}
			if settings.UserLocation.Timezone != nil {
				location["timezone"] = *settings.UserLocation.Timezone
			}
			tool["user_location"] = location
		}
		if settings.SearchContextSize != nil {
			tool["search_context_size"] = *settings.SearchContextSize
		}
	}
	if strings.EqualFold(strings.TrimSpace(modelToolType), "text_and_image") || strings.EqualFold(strings.TrimSpace(modelToolType), "text-and-image") {
		tool["search_content_types"] = []string{"text", "image"}
	}
	return tool
}

func MergeHostedTools(tools []any, hostedTools []any) []any {
	if len(hostedTools) == 0 {
		return append([]any(nil), tools...)
	}
	out := append([]any(nil), tools...)
	for _, hostedTool := range hostedTools {
		hostedMap, ok := hostedTool.(map[string]any)
		if !ok {
			out = append(out, hostedTool)
			continue
		}
		if hostedToolAlreadyPresent(out, hostedMap) {
			continue
		}
		out = append(out, cloneHostedToolMap(hostedMap))
	}
	return out
}

func hostedToolAlreadyPresent(tools []any, hostedTool map[string]any) bool {
	toolType := strings.TrimSpace(hostedToolString(hostedTool["type"]))
	if toolType == "" {
		return false
	}
	namespace := strings.TrimSpace(hostedToolString(hostedTool["name"]))
	if strings.EqualFold(toolType, "namespace") && namespace != "" {
		return hostedNamespaceToolAlreadyPresent(tools, namespace)
	}
	for _, toolValue := range tools {
		toolMap, ok := toolValue.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(hostedToolString(toolMap["type"])), toolType) {
			return true
		}
	}
	return false
}

func hostedNamespaceToolAlreadyPresent(tools []any, namespace string) bool {
	for _, toolValue := range tools {
		toolMap, ok := toolValue.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(hostedToolString(toolMap["type"])), "namespace") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(hostedToolString(toolMap["name"])), namespace) {
			return true
		}
	}
	return false
}

func cloneHostedToolMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func hostedToolString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case *string:
		if typed == nil {
			return ""
		}
		return *typed
	default:
		return ""
	}
}
