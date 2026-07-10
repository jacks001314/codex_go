package turn

import "strings"

const HostedImageGenerationToolType = "image_generation"

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
