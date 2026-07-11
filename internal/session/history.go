package session

import (
	"encoding/json"
	"strings"

	"codex_go/internal/model"
)

type HistoryBuildOptions struct {
	IncludeToolOutputs bool
}

func InputItemsFromRecord(record *Record, options *HistoryBuildOptions) []any {
	if record == nil {
		return nil
	}
	return InputItemsFromItems(record.Items, options)
}

func InputItemsFromItems(items []Item, options *HistoryBuildOptions) []any {
	out := make([]any, 0, len(items))
	for i := range items {
		if input := InputItemFromItem(&items[i], options); input != nil {
			out = append(out, input)
		}
	}
	return out
}

func InputItemFromItem(item *Item, options *HistoryBuildOptions) any {
	if item == nil {
		return nil
	}
	if len(item.Raw) > 0 {
		var raw any
		if err := json.Unmarshal(item.Raw, &raw); err == nil {
			return sanitizeHistoryInputItem(raw)
		}
	}
	if nonModelVisibleHistoryItemType(item.Type) || item.Type == "reasoning" {
		return nil
	}
	switch item.Type {
	case "message", "user_message", "agent_message", "assistant_message":
		return messageInputItem(item)
	case "imageGeneration", "image_generation", "image_generation_call":
		return imageGenerationInputItem(item)
	case "function_call", "custom_tool_call", "tool_search_call":
		return toolCallInputItem(item)
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		if options != nil && !options.IncludeToolOutputs {
			return nil
		}
		return toolOutputInputItem(item)
	default:
		if strings.TrimSpace(item.Text) == "" {
			return nil
		}
		return messageInputItem(item)
	}
}

func imageGenerationInputItem(item *Item) map[string]any {
	result := firstNonEmpty(stringValue(item.Data, "result"), item.Text)
	status := model.NormalizeImageGenerationStatus(firstNonEmpty(item.Status, stringValue(item.Data, "status")), result)
	out := map[string]any{
		"id":     item.ID,
		"type":   "image_generation_call",
		"status": status,
		"result": result,
	}
	if revised := firstNonEmpty(stringValue(item.Data, "revised_prompt"), stringValue(item.Data, "revisedPrompt")); revised != "" {
		out["revised_prompt"] = revised
	}
	return out
}

func messageInputItem(item *Item) map[string]any {
	role := firstNonEmpty(item.Role, roleForSessionItemType(item.Type), "user")
	content := contentBlocksFromSessionItem(item, role)
	if len(content) == 0 {
		return nil
	}
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	}
}

func toolCallInputItem(item *Item) map[string]any {
	callID := firstNonEmpty(item.CallID, stringValue(item.Data, "call_id"), item.ID)
	switch item.Type {
	case "custom_tool_call":
		out := map[string]any{
			"id":      item.ID,
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    firstNonEmpty(item.Name, stringValue(item.Data, "name"), stringValue(item.Metadata, "toolName")),
			"input":   firstNonEmpty(item.Text, stringValue(item.Data, "input")),
		}
		if namespace := firstNonEmpty(item.Namespace, stringValue(item.Data, "namespace")); namespace != "" {
			out["namespace"] = namespace
		}
		return out
	case "tool_search_call":
		out := map[string]any{
			"id":        item.ID,
			"type":      "tool_search_call",
			"call_id":   callID,
			"execution": firstNonEmpty(item.Status, stringValue(item.Data, "execution"), "client"),
		}
		if search := mapValue(item.Data, "arguments"); search != nil {
			out["arguments"] = search
		} else if search := mapValue(item.Data, "search"); search != nil {
			out["arguments"] = search
		} else if strings.TrimSpace(item.Text) != "" {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(item.Text), &decoded); err == nil {
				out["arguments"] = decoded
			}
		}
		if _, ok := out["arguments"]; !ok {
			out["arguments"] = nil
		}
		return out
	default:
		out := map[string]any{
			"id":        item.ID,
			"type":      "function_call",
			"call_id":   callID,
			"name":      firstNonEmpty(item.Name, stringValue(item.Data, "name"), stringValue(item.Metadata, "toolName")),
			"arguments": firstNonEmpty(item.Text, stringValue(item.Data, "arguments")),
		}
		if namespace := firstNonEmpty(item.Namespace, stringValue(item.Data, "namespace")); namespace != "" {
			out["namespace"] = namespace
		}
		return out
	}
}

func sanitizeHistoryInputItem(input any) any {
	switch typed := input.(type) {
	case map[string]any:
		itemType, _ := typed["type"].(string)
		if nonModelVisibleHistoryItemType(itemType) {
			return nil
		}
		if itemType == "image_generation_call" {
			result, _ := typed["result"].(string)
			status, _ := typed["status"].(string)
			typed["status"] = model.NormalizeImageGenerationStatus(status, result)
		}
		if namespace, ok := typed["namespace"]; ok {
			switch value := namespace.(type) {
			case string:
				if strings.TrimSpace(value) == "" {
					delete(typed, "namespace")
				}
			case nil:
				delete(typed, "namespace")
			}
		}
		return typed
	default:
		return input
	}
}

func nonModelVisibleHistoryItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "command_execution", "file_change", "mcp_tool_call", "collab_tool_call", "todo_list", "error":
		return true
	default:
		return false
	}
}

func toolOutputInputItem(item *Item) map[string]any {
	callID := firstNonEmpty(item.CallID, stringValue(item.Data, "call_id"), stringValue(item.Metadata, "callId"), item.ID)
	output := any(firstNonEmpty(item.Text, stringValue(item.Data, "output")))
	if value, ok := item.Data["content_items"]; ok {
		output = value
	}
	outputType := item.Type
	if outputType == "tool_output" {
		outputType = "function_call_output"
		if kind := stringValue(item.Metadata, "payloadKind"); kind == "custom" {
			outputType = "custom_tool_call_output"
		}
		if kind := stringValue(item.Metadata, "payloadKind"); kind == "tool_search" {
			outputType = "tool_search_output"
		}
	}
	out := map[string]any{
		"type":    outputType,
		"call_id": callID,
	}
	if outputType == "tool_search_output" {
		out["status"] = firstNonEmpty(item.Status, "completed")
		out["execution"] = firstNonEmpty(stringValue(item.Data, "execution"), "client")
		if tools, ok := model.ResponsesLoadableToolsFromValue(item.Data["tools"]); ok {
			out["tools"] = tools
		} else if tools, ok := item.Data["tools"]; ok {
			out["tools"] = tools
		} else {
			out["tools"] = []any{}
		}
		return out
	}
	out["output"] = output
	return out
}

func contentBlocksFromSessionItem(item *Item, role string) []map[string]any {
	if len(item.Content) > 0 {
		blocks := make([]map[string]any, 0, len(item.Content))
		for i := range item.Content {
			contentType := historyContentType(item.Content[i].Type, role, item.Content[i].ImageURL)
			block := map[string]any{"type": contentType}
			if item.Content[i].Text != "" {
				block["text"] = item.Content[i].Text
			}
			if item.Content[i].ImageURL != "" {
				block["image_url"] = item.Content[i].ImageURL
			}
			if item.Content[i].Detail != nil {
				block["detail"] = *item.Content[i].Detail
			}
			blocks = append(blocks, block)
		}
		return blocks
	}
	text := firstNonEmpty(item.Text, stringValue(item.Data, "text"))
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []map[string]any{{
		"type": defaultContentTypeForRole(role),
		"text": text,
	}}
}

func roleForSessionItemType(itemType string) string {
	switch itemType {
	case "agent_message", "assistant_message":
		return "assistant"
	case "user_message", "message":
		return "user"
	default:
		return ""
	}
}

func historyContentType(value string, role string, imageURL string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "image", "inputImage", "input_image", "localImage", "local_image":
		return "input_image"
	case "":
		if strings.TrimSpace(imageURL) != "" {
			return "input_image"
		}
		return defaultContentTypeForRole(role)
	default:
		return value
	}
}

func defaultContentTypeForRole(role string) string {
	if role == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func mapValue(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	value, _ := values[key].(map[string]any)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
