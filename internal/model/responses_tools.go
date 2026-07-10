package model

import (
	"sort"
	"strings"

	"codex_go/internal/tool"
)

func ResponsesToolsFromSpecs(specs []tool.Spec) []any {
	tools := make([]any, 0, len(specs))
	namespacedTools := map[string][]map[string]any{}
	namespaceDescriptions := map[string]string{}
	for i := range specs {
		if isResponsesNamespaceTool(&specs[i]) {
			namespace := strings.TrimSpace(specs[i].Name.Namespace)
			namespacedTools[namespace] = append(namespacedTools[namespace], responsesNamespacedFunctionTool(&specs[i]))
			if namespaceDescriptions[namespace] == "" && strings.TrimSpace(specs[i].NamespaceDescription) != "" {
				namespaceDescriptions[namespace] = strings.TrimSpace(specs[i].NamespaceDescription)
			}
			continue
		}
		if item, ok := responsesToolFromSpec(&specs[i]); ok {
			tools = append(tools, item)
		}
	}
	namespaces := make([]string, 0, len(namespacedTools))
	for namespace := range namespacedTools {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	for _, namespace := range namespaces {
		sort.SliceStable(namespacedTools[namespace], func(i int, j int) bool {
			return namespacedTools[namespace][i]["name"].(string) < namespacedTools[namespace][j]["name"].(string)
		})
		description := namespaceDescriptions[namespace]
		if description == "" {
			description = "Tools provided by the current Codex thread."
		}
		tools = append(tools, map[string]any{
			"type":        "namespace",
			"name":        namespace,
			"description": description,
			"tools":       namespacedTools[namespace],
		})
	}
	return tools
}

func responsesToolFromSpec(spec *tool.Spec) (map[string]any, bool) {
	if spec == nil || spec.Exposure == tool.ExposureHidden || spec.Exposure == tool.ExposureDiscoverable {
		return nil, false
	}
	name := tool.ResponsesAPIName(spec.Name)
	if name == "" {
		return nil, false
	}
	if name == tool.ToolSearchName {
		return map[string]any{
			"type":        "tool_search",
			"execution":   "client",
			"description": spec.Description,
			"parameters":  responsesInputSchema(spec.InputSchema),
		}, true
	}
	if spec.Freeform != nil {
		return map[string]any{
			"type":        "custom",
			"name":        name,
			"description": spec.Description,
			"format": map[string]any{
				"type":       "grammar",
				"syntax":     spec.Freeform.Syntax,
				"definition": spec.Freeform.Definition,
			},
		}, true
	}
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": spec.Description,
		"strict":      false,
		"parameters":  responsesInputSchema(spec.InputSchema),
	}, true
}

func isResponsesNamespaceTool(spec *tool.Spec) bool {
	if spec == nil || spec.Exposure == tool.ExposureHidden || spec.Exposure == tool.ExposureDiscoverable {
		return false
	}
	if strings.TrimSpace(spec.Name.Namespace) == "" {
		return false
	}
	if spec.Name.Namespace == "web" && spec.Name.Name == "run" {
		return true
	}
	if spec.Name.Namespace == "clock" {
		return true
	}
	return spec.Search != nil && spec.Search.Source != nil && spec.Search.Source.Name == "Dynamic tools"
}

func responsesNamespacedFunctionTool(spec *tool.Spec) map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        strings.TrimSpace(spec.Name.Name),
		"description": spec.Description,
		"parameters":  responsesInputSchema(spec.InputSchema),
	}
}

func responsesInputSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return cloneMapAny(schema)
}

func cloneAnySlice(values []any) []any {
	if values == nil {
		return nil
	}
	return append([]any(nil), values...)
}

func cloneMapAny(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMapAny(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneAny(typed[i])
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
