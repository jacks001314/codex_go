package mcp

import "strings"

const openAIFileParamsMetadataKey = "openai/fileParams"

const openAIFileLocalPathGuidance = "This parameter expects an absolute local file path. If you want to upload a file, provide the absolute path to that file here."

type openAIFileSchemaInfo struct {
	acceptsMimeType bool
	acceptsFileName bool
}

func DeclaredOpenAIFileInputParamNames(meta any) []string {
	values := metadataMap(meta)
	if values == nil {
		return nil
	}
	raw, ok := values[openAIFileParamsMetadataKey].([]any)
	if !ok {
		if stringsValue, ok := values[openAIFileParamsMetadataKey].([]string); ok {
			raw = make([]any, len(stringsValue))
			for i := range stringsValue {
				raw[i] = stringsValue[i]
			}
		}
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		name, ok := value.(string)
		if ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

func prepareOpenAIFileParamsForModel(tool *RuntimeTool, meta any) map[string][]string {
	if tool == nil {
		return nil
	}
	fileParams := DeclaredOpenAIFileInputParamNames(meta)
	if len(fileParams) == 0 {
		return nil
	}
	tool.InputSchema = deepCloneRuntimeAnyMap(tool.InputSchema)
	optionalFields := supportedOpenAIFileInputOptionalFields(tool.InputSchema, fileParams)
	rewriteOpenAIFileInputSchemaForLocalPaths(tool.InputSchema, fileParams)
	return optionalFields
}

func supportedOpenAIFileInputOptionalFields(root map[string]any, fileParams []string) map[string][]string {
	properties, _ := root["properties"].(map[string]any)
	out := make(map[string][]string, len(fileParams))
	for _, fieldName := range fileParams {
		var optional []string
		if schema, ok := properties[fieldName]; ok {
			info := inspectOpenAIFileSchema(schema, root)
			if info.acceptsMimeType {
				optional = append(optional, "mime_type")
			}
			if info.acceptsFileName {
				optional = append(optional, "file_name")
			}
		}
		out[fieldName] = optional
	}
	return out
}

func inspectOpenAIFileSchema(schema any, root map[string]any) openAIFileSchemaInfo {
	info := openAIFileSchemaInfo{}
	pending := []any{schema}
	visitedRefs := map[string]bool{}
	for len(pending) > 0 {
		last := len(pending) - 1
		current, _ := pending[last].(map[string]any)
		pending = pending[:last]
		if current == nil {
			continue
		}
		if schemaRef, _ := current["$ref"].(string); schemaRef != "" && !visitedRefs[schemaRef] {
			visitedRefs[schemaRef] = true
			if resolved, ok := resolveOpenAIFileLocalSchemaRef(root, schemaRef); ok {
				pending = append(pending, resolved)
			}
		}
		for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
			if variants, ok := current[keyword].([]any); ok {
				pending = append(pending, variants...)
			}
		}
		_, hasItems := current["items"]
		if current["type"] == "array" || hasItems {
			if hasItems {
				pending = append(pending, current["items"])
			}
			continue
		}
		properties, hasProperties := current["properties"].(map[string]any)
		_, hasAdditionalProperties := current["additionalProperties"]
		if current["type"] != "object" && !hasProperties && !hasAdditionalProperties {
			continue
		}
		acceptsAdditional := true
		switch additional := current["additionalProperties"].(type) {
		case bool:
			acceptsAdditional = additional
		case map[string]any:
			acceptsAdditional = false
		}
		info.acceptsMimeType = info.acceptsMimeType || acceptsAdditional || hasOpenAIFileProperty(properties, "mime_type")
		info.acceptsFileName = info.acceptsFileName || acceptsAdditional || hasOpenAIFileProperty(properties, "file_name")
	}
	return info
}

func hasOpenAIFileProperty(properties map[string]any, name string) bool {
	if properties == nil {
		return false
	}
	_, ok := properties[name]
	return ok
}

func resolveOpenAIFileLocalSchemaRef(root map[string]any, schemaRef string) (any, bool) {
	pointer, ok := strings.CutPrefix(schemaRef, "#/")
	if !ok {
		return nil, false
	}
	var current any = root
	for _, rawSegment := range strings.Split(pointer, "/") {
		segment := strings.ReplaceAll(strings.ReplaceAll(rawSegment, "~1", "/"), "~0", "~")
		switch typed := current.(type) {
		case map[string]any:
			current, ok = typed[segment]
			if !ok {
				return nil, false
			}
		case []any:
			index := 0
			if segment == "" {
				return nil, false
			}
			for _, ch := range segment {
				if ch < '0' || ch > '9' {
					return nil, false
				}
				index = index*10 + int(ch-'0')
			}
			if index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func rewriteOpenAIFileInputSchemaForLocalPaths(root map[string]any, fileParams []string) {
	properties, _ := root["properties"].(map[string]any)
	for _, fieldName := range fileParams {
		schema, _ := properties[fieldName].(map[string]any)
		if schema == nil {
			continue
		}
		description, _ := schema["description"].(string)
		switch {
		case description == "":
			description = openAIFileLocalPathGuidance
		case !strings.Contains(description, openAIFileLocalPathGuidance):
			description += " " + openAIFileLocalPathGuidance
		}
		_, hasItems := schema["items"]
		isArray := schema["type"] == "array" || hasItems
		for key := range schema {
			delete(schema, key)
		}
		schema["description"] = description
		if isArray {
			schema["type"] = "array"
			schema["items"] = map[string]any{"type": "string"}
		} else {
			schema["type"] = "string"
		}
	}
}
