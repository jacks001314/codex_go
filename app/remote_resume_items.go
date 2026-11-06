package app

import (
	"strings"

	"codex_go/appserver"
	"codex_go/protocol"
)

// remoteResumePayloadStatus renders a stored status with the wire spelling the
// thread item payload builders expect.
func remoteResumePayloadStatus(value string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_")) {
	case "in_progress", "inprogress", "running":
		return "inProgress"
	case "failed", "error":
		return "failed"
	case "declined":
		return "declined"
	case "interrupted":
		return "interrupted"
	default:
		return "completed"
	}
}

// remoteResumeToolItemType reports the persisted thread item types whose rich
// history cell the loaded transcript reuses (Rust #46710).
func remoteResumeToolItemType(itemType string) bool {
	switch itemType {
	case "commandexecution", "mcptoolcall", "filechange", "dynamictoolcall",
		"collabagenttoolcall", "subagentactivity", "websearch", "hookprompt":
		return true
	}
	return false
}

// remoteResumeProtocolItem adapts a persisted app-server thread item to the
// protocol item the shared rich transcript projection renders.
func remoteResumeProtocolItem(item appserver.ThreadItem) protocol.ThreadItem {
	// The payload builder reads the item status from its data map, while a
	// decoded thread item keeps it on the struct field.
	if status := strings.TrimSpace(item.Status); status != "" {
		data := make(map[string]any, len(item.Data)+1)
		for key, value := range item.Data {
			data[key] = value
		}
		if _, ok := data["status"]; !ok {
			data["status"] = remoteResumePayloadStatus(status)
		}
		item.Data = data
	}
	projected := remoteProtocolItemFromPayload(appserver.BuildThreadItemPayload(item), true)
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "filechange":
		status := firstNonEmptyLocal(remoteTUIAnyString(item.Data["status"]), strings.TrimSpace(item.Status))
		changes := resumeProtocolFileChanges(item.Data["changes"])
		projected = protocol.FileChangeItem(firstNonEmptyLocal(strings.TrimSpace(item.ID), strings.TrimSpace(item.CallID)), changes, status)
		projected.CallID = strings.TrimSpace(item.CallID)
		if projected.CallID == "" {
			projected.CallID = strings.TrimSpace(item.ID)
		}
	case "dynamictoolcall":
		projected = remoteResumeDynamicItem(item)
	case "hookprompt":
		projected = protocol.ThreadItem{ID: strings.TrimSpace(item.ID), Type: "hookPrompt", Content: remoteHookPromptTexts(item)}
	}
	return projected
}

func remoteResumeDynamicItem(item appserver.ThreadItem) protocol.ThreadItem {
	callID := firstNonEmptyLocal(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	name := firstNonEmptyLocal(remoteTUIAnyString(item.Data["tool"]), remoteTUIAnyString(item.Data["name"]), strings.TrimSpace(item.Name))
	status := firstNonEmptyLocal(remoteTUIAnyString(item.Data["status"]), strings.TrimSpace(item.Status))
	var arguments *any
	if value, ok := item.Data["arguments"]; ok {
		arguments = &value
	}
	var success *bool
	if value, ok := item.Data["success"].(bool); ok {
		success = &value
	}
	content := []string{}
	if value, ok := item.Data["contentItems"].([]any); ok {
		content = append(content, resumeDynamicContentItems(value)...)
	} else if value, ok := item.Data["content_items"].([]any); ok {
		content = append(content, resumeDynamicContentItems(value)...)
	}
	metadata := map[string]any{}
	for _, key := range []string{"durationMs", "duration_ms"} {
		if value, ok := item.Data[key]; ok {
			metadata["durationMs"] = value
			break
		}
	}
	namespace := lastNamespace(name)
	toolName := name
	if namespace != "" {
		toolName = strings.TrimSpace(name[len(namespace)+1:])
	}
	return protocol.ThreadItem{
		ID:        firstNonEmptyLocal(callID, strings.TrimSpace(item.ID)),
		Type:      "dynamicToolCall",
		CallID:    callID,
		ToolName:  toolName,
		Arguments: arguments,
		Status:    status,
		Success:   success,
		Content:   content,
		Output:    strings.Join(content, "\n"),
		Metadata:  metadata,
	}
}

// lastNamespace returns the namespace prefix of a persisted dynamic tool name,
// or "" when the tool is not namespaced.
func lastNamespace(name string) string {
	index := strings.LastIndex(name, ".")
	if index <= 0 || index >= len(name)-1 {
		return ""
	}
	return strings.TrimSpace(name[:index])
}

func resumeDynamicContentItems(values []any) []string {
	out := make([]string, 0, len(values))
	for _, entry := range values {
		switch typed := entry.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				out = append(out, text)
			}
		case map[string]any:
			if text, ok := typed["text"].(string); ok {
				if text = strings.TrimSpace(text); text != "" {
					out = append(out, text)
				}
			}
		}
	}
	return out
}

func remoteHookPromptTexts(item appserver.ThreadItem) []string {
	values, ok := item.Data["fragments"].([]any)
	if !ok {
		if text := strings.TrimSpace(item.Text); text != "" {
			return []string{text}
		}
		return nil
	}
	texts := make([]string, 0, len(values))
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				texts = append(texts, text)
			}
		case map[string]any:
			if text := remoteTUIAnyString(typed["text"]); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return texts
}
