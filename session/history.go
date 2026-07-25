package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"codex_go/model"
)

type HistoryBuildOptions struct {
	IncludeToolOutputs bool
	CWD                string
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
	return normalizeHistoryToolPairs(out)
}

// normalizeHistoryToolPairs mirrors Rust ConversationHistory normalization:
// orphan client tool outputs are removed, while calls interrupted before an
// output was persisted receive a synthetic "aborted" output immediately after
// the call. User and assistant messages are never removed by this pass.
func normalizeHistoryToolPairs(items []any) []any {
	functionCalls := map[string]bool{}
	customCalls := map[string]bool{}
	toolSearchCalls := map[string]bool{}
	functionOutputs := map[string]bool{}
	customOutputs := map[string]bool{}
	toolSearchOutputs := map[string]bool{}
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		callID := historyString(item["call_id"])
		switch historyString(item["type"]) {
		case "function_call", "local_shell_call":
			if callID != "" {
				functionCalls[callID] = true
			}
		case "custom_tool_call":
			if callID != "" {
				customCalls[callID] = true
			}
		case "tool_search_call":
			if callID != "" {
				toolSearchCalls[callID] = true
			}
		case "function_call_output":
			if callID != "" {
				functionOutputs[callID] = true
			}
		case "custom_tool_call_output":
			if callID != "" {
				customOutputs[callID] = true
			}
		case "tool_search_output":
			if callID != "" {
				toolSearchOutputs[callID] = true
			}
		}
	}

	cleaned := make([]any, 0, len(items))
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			cleaned = append(cleaned, value)
			continue
		}
		callID := historyString(item["call_id"])
		switch historyString(item["type"]) {
		case "function_call_output":
			if callID == "" || !functionCalls[callID] {
				continue
			}
		case "custom_tool_call_output":
			if callID == "" || !customCalls[callID] {
				continue
			}
		case "tool_search_output":
			if historyString(item["execution"]) != "server" && callID != "" && !toolSearchCalls[callID] {
				continue
			}
		}
		cleaned = append(cleaned, value)
	}

	out := make([]any, 0, len(cleaned)+4)
	for _, value := range cleaned {
		out = append(out, value)
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		callID := historyString(item["call_id"])
		if callID == "" {
			continue
		}
		switch historyString(item["type"]) {
		case "function_call", "local_shell_call":
			if !functionOutputs[callID] {
				out = append(out, map[string]any{"type": "function_call_output", "call_id": callID, "output": "aborted"})
			}
		case "custom_tool_call":
			if !customOutputs[callID] {
				out = append(out, map[string]any{"type": "custom_tool_call_output", "call_id": callID, "output": "aborted"})
			}
		case "tool_search_call":
			if !toolSearchOutputs[callID] {
				out = append(out, map[string]any{"type": "tool_search_output", "call_id": callID, "status": "completed", "execution": "client", "tools": []any{}})
			}
		}
	}
	return out
}

func historyString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
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
		return messageInputItem(item, options)
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
		return messageInputItem(item, options)
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

func messageInputItem(item *Item, options *HistoryBuildOptions) map[string]any {
	role := firstNonEmpty(item.Role, roleForSessionItemType(item.Type), "user")
	content := contentBlocksFromSessionItem(item, role, options)
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
		"id":      item.ID,
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

func contentBlocksFromSessionItem(item *Item, role string, options *HistoryBuildOptions) []map[string]any {
	if len(item.Content) > 0 {
		blocks := make([]map[string]any, 0, len(item.Content))
		for i := range item.Content {
			if isLocalImageContentType(item.Content[i].Type) {
				blocks = append(blocks, localImageHistoryBlocks(item.Content[i], options)...)
				continue
			}
			if isLocalAudioContentType(item.Content[i].Type) {
				blocks = append(blocks, localAudioHistoryBlocks(item.Content[i], options)...)
				continue
			}
			contentType := historyContentType(item.Content[i].Type, role, item.Content[i].ImageURL)
			block := map[string]any{"type": contentType}
			if item.Content[i].Text != "" {
				block["text"] = item.Content[i].Text
			}
			if item.Content[i].ImageURL != "" {
				block["image_url"] = item.Content[i].ImageURL
			}
			if item.Content[i].AudioURL != "" {
				block["audio_url"] = item.Content[i].AudioURL
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

func isLocalImageContentType(value string) bool {
	return value == "localImage" || value == "local_image"
}

func isLocalAudioContentType(value string) bool {
	return value == "localAudio" || value == "local_audio"
}

func localAudioHistoryBlocks(part ContentPart, options *HistoryBuildOptions) []map[string]any {
	path := strings.TrimSpace(part.AudioURL)
	resolved := path
	if !filepath.IsAbs(resolved) && options != nil && strings.TrimSpace(options.CWD) != "" {
		resolved = filepath.Join(options.CWD, resolved)
	}
	data, err := os.ReadFile(filepath.Clean(resolved))
	if err != nil {
		return []map[string]any{{"type": "input_text", "text": fmt.Sprintf("[Local audio unavailable: %s]", path)}}
	}
	mime := http.DetectContentType(data)
	return []map[string]any{{"type": "input_audio", "audio_url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}}
}

func localImageHistoryBlocks(part ContentPart, options *HistoryBuildOptions) []map[string]any {
	path := strings.TrimSpace(part.ImageURL)
	resolved := path
	if !filepath.IsAbs(resolved) && options != nil && strings.TrimSpace(options.CWD) != "" {
		resolved = filepath.Join(options.CWD, resolved)
	}
	data, err := os.ReadFile(filepath.Clean(resolved))
	if err != nil {
		return []map[string]any{{"type": "input_text", "text": fmt.Sprintf("[Local image unavailable: %s]", path)}}
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return []map[string]any{{"type": "input_text", "text": fmt.Sprintf("[Local image could not be decoded: %s]", path)}}
	}
	mime := historyImageMIME(format)
	if mime == "" {
		return []map[string]any{{"type": "input_text", "text": fmt.Sprintf("[Unsupported local image: %s]", path)}}
	}
	block := map[string]any{
		"type":      "input_image",
		"image_url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
	}
	if part.Detail != nil {
		block["detail"] = *part.Detail
	}
	return []map[string]any{block}
}

func historyImageMIME(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return ""
	}
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
	case "audio", "inputAudio", "input_audio", "localAudio", "local_audio":
		return "input_audio"
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
