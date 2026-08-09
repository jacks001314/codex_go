package model

import (
	"encoding/json"
	"sort"
	"strings"

	"codex_go/tool"
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

func ResponsesLoadableToolsFromSpecs(specs []tool.Spec) []any {
	tools := make([]any, 0, len(specs))
	namespaceIndexes := map[string]int{}
	for i := range specs {
		spec := specs[i]
		if spec.Exposure == tool.ExposureHidden {
			continue
		}
		namespace := strings.TrimSpace(spec.Name.Namespace)
		if namespace != "" {
			child := responsesLoadableNamespacedFunctionTool(&spec)
			if child == nil {
				continue
			}
			if index, ok := namespaceIndexes[namespace]; ok {
				if existing, ok := tools[index].(map[string]any); ok {
					children, _ := existing["tools"].([]map[string]any)
					existing["tools"] = append(children, child)
				}
				continue
			}
			description := strings.TrimSpace(spec.NamespaceDescription)
			if description == "" {
				description = "Tools in the " + namespace + " namespace."
			}
			namespaceIndexes[namespace] = len(tools)
			tools = append(tools, map[string]any{
				"type":        "namespace",
				"name":        namespace,
				"description": description,
				"tools":       []map[string]any{child},
			})
			continue
		}
		if item, ok := responsesLoadableToolFromSpec(&spec); ok {
			tools = append(tools, item)
		}
	}
	return tools
}

func ResponsesLoadableToolsFromValue(value any) ([]any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case []tool.Spec:
		return ResponsesLoadableToolsFromSpecs(typed), true
	case []any:
		specs := make([]tool.Spec, 0, len(typed))
		for i := range typed {
			switch item := typed[i].(type) {
			case tool.Spec:
				specs = append(specs, item)
			case *tool.Spec:
				if item == nil {
					return normalizeResponseToolParameters(cloneAnySlice(typed)), false
				}
				specs = append(specs, *item)
			default:
				if loadableToolsHaveResponsesType(typed) {
					return normalizeResponseToolParameters(cloneAnySlice(typed)), true
				}
				if specs, ok := toolSpecsFromJSONValue(typed); ok {
					return ResponsesLoadableToolsFromSpecs(specs), true
				}
				return normalizeResponseToolParameters(cloneAnySlice(typed)), false
			}
		}
		return ResponsesLoadableToolsFromSpecs(specs), true
	default:
		if specs, ok := toolSpecsFromJSONValue(value); ok {
			return ResponsesLoadableToolsFromSpecs(specs), true
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var tools []any
		if err := json.Unmarshal(data, &tools); err != nil {
			return nil, false
		}
		if loadableToolsHaveResponsesType(tools) {
			return normalizeResponseToolParameters(tools), true
		}
		return normalizeResponseToolParameters(tools), false
	}
}

func toolSpecsFromJSONValue(value any) ([]tool.Spec, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var specs []tool.Spec
	if err := json.Unmarshal(data, &specs); err != nil || !toolSpecsHaveNames(specs) {
		return nil, false
	}
	return specs, true
}

func responsesToolFromSpec(spec *tool.Spec) (map[string]any, bool) {
	if spec == nil || !tool.IsModelVisible(spec.Exposure) {
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
	result := map[string]any{
		"type":        "function",
		"name":        name,
		"description": spec.Description,
		"strict":      false,
		"parameters":  responsesInputSchema(spec.InputSchema),
	}
	addResponsesOutputSchema(result, spec.OutputSchema)
	return result, true
}

func responsesLoadableToolFromSpec(spec *tool.Spec) (map[string]any, bool) {
	if spec == nil || spec.Exposure == tool.ExposureHidden || spec.Freeform != nil {
		return nil, false
	}
	name := tool.ResponsesAPIName(spec.Name)
	if name == "" || name == tool.ToolSearchName {
		return nil, false
	}
	result := responsesLoadableFunctionTool(name, spec.Description, spec.InputSchema)
	addResponsesOutputSchema(result, spec.OutputSchema)
	return result, true
}

func isResponsesNamespaceTool(spec *tool.Spec) bool {
	if spec == nil || !tool.IsModelVisible(spec.Exposure) {
		return false
	}
	if strings.TrimSpace(spec.Name.Namespace) == "" {
		return false
	}
	if spec.Name.Namespace == "web" && spec.Name.Name == "run" {
		return true
	}
	if spec.Name.Namespace == "image_gen" && spec.Name.Name == "imagegen" {
		return true
	}
	if spec.Name.Namespace == "clock" {
		return true
	}
	if spec.Name.Namespace == "collaboration" {
		return true
	}
	if strings.HasPrefix(spec.Name.Namespace, "mcp__") {
		return true
	}
	return spec.Search != nil && spec.Search.Source != nil && spec.Search.Source.Name == "Dynamic tools"
}

func responsesNamespacedFunctionTool(spec *tool.Spec) map[string]any {
	result := map[string]any{
		"type":        "function",
		"name":        strings.TrimSpace(spec.Name.Name),
		"description": spec.Description,
		"parameters":  responsesInputSchema(spec.InputSchema),
	}
	addResponsesOutputSchema(result, spec.OutputSchema)
	return result
}

func responsesLoadableNamespacedFunctionTool(spec *tool.Spec) map[string]any {
	if spec == nil || spec.Freeform != nil {
		return nil
	}
	name := strings.TrimSpace(spec.Name.Name)
	if name == "" || name == tool.ToolSearchName {
		return nil
	}
	result := responsesLoadableFunctionTool(name, spec.Description, spec.InputSchema)
	addResponsesOutputSchema(result, spec.OutputSchema)
	return result
}

func addResponsesOutputSchema(target map[string]any, schema map[string]any) {
	if target == nil || len(schema) == 0 {
		return
	}
	target["output_schema"] = cloneMapAny(schema)
}

func responsesLoadableFunctionTool(name string, description string, schema map[string]any) map[string]any {
	return map[string]any{
		"type":          "function",
		"name":          strings.TrimSpace(name),
		"description":   description,
		"strict":        false,
		"defer_loading": true,
		"parameters":    responsesInputSchema(schema),
	}
}

func responsesInputSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := cloneMapAny(schema)
	if schemaType, ok := out["type"]; !ok || schemaType == nil || schemaType == "" {
		// Responses function tools require an object-typed JSON Schema.
		// Providers such as DeepSeek reject schemas without an explicit type,
		// so normalize here instead of failing the whole request.
		out["type"] = "object"
	}
	return out
}

// normalizeResponseToolParameters sanitizes raw Responses tool definitions so a
// request can never reach the API with an invalid function parameters schema
// (missing or null "type", or null "required"). Stale persisted definitions
// (session history, tool search output, dynamic tools) can carry the pre-fix
// update_plan shape {"required": ["plan"]} or explicit nulls, which strict
// providers reject with 400 ("schema must be a JSON Schema of 'type: object'",
// "None is not of type 'array'").
func normalizeResponseToolParameters(tools []any) []any {
	if len(tools) == 0 {
		return tools
	}
	out := make([]any, 0, len(tools))
	for _, value := range tools {
		item, ok := value.(map[string]any)
		if !ok {
			out = append(out, value)
			continue
		}
		clone := cloneMapAny(item)
		switch responseToolString(clone["type"]) {
		case "function", "tool_search":
			if params, ok := clone["parameters"].(map[string]any); ok {
				clone["parameters"] = normalizeResponseSchema(params)
			}
		case "namespace":
			switch children := clone["tools"].(type) {
			case []any:
				clone["tools"] = normalizeResponseToolParameters(children)
			case []map[string]any:
				converted := make([]any, 0, len(children))
				for i := range children {
					converted = append(converted, children[i])
				}
				clone["tools"] = normalizeResponseToolParameters(converted)
			}
		}
		out = append(out, clone)
	}
	return out
}

// normalizeResponseSchema rewrites a function parameters schema so strict
// providers never see null "type"/"required" values. Schema containers such as
// "properties" are traversed through their values; they are not schemas
// themselves and must never receive schema keywords.
func normalizeResponseSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := cloneMapAny(schema)
	normalizeResponseSchemaNode(out, true)
	return out
}

func normalizeResponseSchemaNode(schema map[string]any, requireObjectType bool) {
	if schema == nil {
		return
	}
	if schemaType, ok := schema["type"]; !ok || schemaType == nil || schemaType == "" {
		if requireObjectType {
			schema["type"] = "object"
		} else {
			delete(schema, "type")
		}
	}
	if required, ok := schema["required"]; ok && required == nil {
		delete(schema, "required")
	}
	for _, key := range []string{"properties", "patternProperties", "dependentSchemas", "dependencies", "$defs", "definitions"} {
		if children, ok := schema[key].(map[string]any); ok {
			for name, child := range children {
				children[name] = normalizeResponseSchemaValue(child)
			}
		}
	}
	for _, key := range []string{"additionalProperties", "unevaluatedProperties", "additionalItems", "unevaluatedItems", "propertyNames", "items", "contains", "contentSchema", "not", "if", "then", "else"} {
		if child, ok := schema[key]; ok {
			schema[key] = normalizeResponseSchemaValue(child)
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if children, ok := schema[key].([]any); ok {
			schema[key] = normalizeResponseSchemaValues(children)
		}
	}
}

func normalizeResponseSchemaValue(value any) any {
	switch schema := value.(type) {
	case map[string]any:
		normalizeResponseSchemaNode(schema, false)
	case []any:
		return normalizeResponseSchemaValues(schema)
	}
	return value
}

func normalizeResponseSchemaValues(values []any) []any {
	out := make([]any, len(values))
	for i := range values {
		out[i] = normalizeResponseSchemaValue(values[i])
	}
	return out
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

func loadableToolsHaveResponsesType(values []any) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return false
		}
		if strings.TrimSpace(responseToolString(item["type"])) == "" {
			return false
		}
	}
	return true
}

func toolSpecsHaveNames(specs []tool.Spec) bool {
	if len(specs) == 0 {
		return false
	}
	for i := range specs {
		if strings.TrimSpace(specs[i].Name.Name) == "" && strings.TrimSpace(specs[i].Name.Namespace) == "" {
			return false
		}
	}
	return true
}

func responseToolString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
