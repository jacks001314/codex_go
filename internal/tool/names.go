package tool

import "strings"

func ResponsesAPIName(name ToolName) string {
	if name.Namespace == "" {
		return strings.TrimSpace(name.Name)
	}
	namespace := strings.TrimSpace(name.Namespace)
	toolName := strings.TrimSpace(name.Name)
	if namespace == "" {
		return toolName
	}
	if strings.HasSuffix(namespace, "_") || strings.HasPrefix(toolName, "_") {
		return namespace + toolName
	}
	return namespace + "__" + toolName
}
