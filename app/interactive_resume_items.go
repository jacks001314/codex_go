package app

import (
	"encoding/json"
	"strconv"
	"strings"

	"codex_go/protocol"
	"codex_go/session"
)

// Rust parity: codex-rs/tui/src/thread_transcript.rs (#46710).
//
// interactiveSessionResumeToolItems folds the persisted response items of a
// loaded record into the typed thread items the rich transcript projection
// renders. Tool calls and their outputs are paired by call id, and the
// classification comes from the markers the runtime recorded on the call.
func interactiveSessionResumeToolItems(items []session.Item) map[int]protocol.ThreadItem {
	outputs := map[string]session.Item{}
	for _, item := range items {
		if !interactiveSessionItemIsToolOutput(item) {
			continue
		}
		callID := resumeToolItemCallID(item)
		if callID == "" {
			continue
		}
		if _, ok := outputs[callID]; !ok {
			outputs[callID] = item
		}
	}
	folded := map[int]protocol.ThreadItem{}
	for index, item := range items {
		if interactiveSessionItemIsToolOutput(item) || interactiveSessionItemIsHiddenContextInstruction(item) {
			continue
		}
		projected, ok := resumeToolItem(item, outputs)
		if !ok {
			continue
		}
		folded[index] = projected
	}
	return folded
}

func interactiveSessionItemIsToolOutput(item session.Item) bool {
	switch normalizeInteractiveSessionItemType(item.Type) {
	case "functioncalloutput", "customtoolcalloutput", "toolsearchoutput", "tooloutput":
		return true
	}
	return false
}

func resumeToolItemCallID(item session.Item) string {
	return strings.TrimSpace(firstNonEmptyLocal(
		strings.TrimSpace(item.CallID),
		interactiveSessionItemDataString(item, "call_id", "callId"),
		remoteTUIAnyString(item.Metadata["callId"]),
		strings.TrimSpace(item.ID),
	))
}

// resumeToolName is the tool identity recorded on the call. Namespaced tools
// keep the joined "server.tool" spelling; the dynamic cell splits it again.
func resumeToolName(item session.Item) string {
	return strings.TrimSpace(firstNonEmptyLocal(
		strings.TrimSpace(item.Name),
		interactiveSessionItemDataString(item, "name", "tool", "toolName"),
		strings.TrimSpace(item.Namespace),
	))
}

func resumeToolItem(item session.Item, outputs map[string]session.Item) (protocol.ThreadItem, bool) {
	callID := resumeToolItemCallID(item)
	output, hasOutput := outputs[callID]
	switch normalizeInteractiveSessionItemType(item.Type) {
	case "commandexecution":
		return resumeCommandExecutionItem(item, output, hasOutput), true
	case "filechange":
		return resumeFileChangeItem(item, output, hasOutput), true
	case "mcptoolcall":
		return resumeMCPToolCallItem(item, output, hasOutput), true
	case "dynamictoolcall":
		return resumeDynamicToolCallItem(item, output, hasOutput), true
	case "collabagenttoolcall", "collabtoolcall":
		return resumeCollabAgentItem(item), true
	case "subagentactivity":
		return resumeSubAgentActivityItem(item), true
	case "websearch":
		return resumeWebSearchItem(item), true
	case "imageview":
		return resumeImageViewItem(item, output), true
	case "imagegeneration", "imagegenerationcall":
		return resumeImageGenerationItem(item), true
	case "functioncall", "customtoolcall", "toolsearchcall", "toolcall":
		return resumeFunctionCallItem(item, output, hasOutput)
	}
	return protocol.ThreadItem{}, false
}

// resumeFunctionCallItem classifies a persisted function/custom tool call the
// same way the live event path does, using the runtime's recorded markers and
// the tool name.
func resumeFunctionCallItem(item session.Item, output session.Item, hasOutput bool) (protocol.ThreadItem, bool) {
	name := resumeToolName(item)
	switch {
	case resumeDataBool(item.Data, "fileChange", "file_change") || resumeIsApplyPatchName(name):
		return resumeFileChangeItem(item, output, hasOutput), true
	case resumeDataBool(item.Data, "mcpToolCall", "mcp_tool_call") || resumeLooksNamespacedMCP(name):
		return resumeMCPToolCallItem(item, output, hasOutput), true
	case resumeDataBool(item.Data, "dynamicToolCall", "dynamic_tool_call"):
		return resumeDynamicToolCallItem(item, output, hasOutput), true
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "exec_command", "shell", "local_shell", "container.exec", "unified_exec", "shell_command":
		return resumeCommandExecutionItem(item, output, hasOutput), true
	case "view_image":
		return resumeImageViewItem(item, output), true
	case "web_search":
		return resumeWebSearchItem(item), true
	case "image_generation", "imagegen":
		return resumeImageGenerationItem(item), true
	}
	if name == "" {
		return protocol.ThreadItem{}, false
	}
	return resumeDynamicToolCallItem(item, output, hasOutput), true
}

func resumeIsApplyPatchName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "apply_patch" || strings.HasSuffix(name, ".apply_patch")
}

// resumeLooksNamespacedMCP mirrors the runtime's split of "mcp__server__tool"
// and "server.tool" names; a bare tool name stays a plain tool call.
func resumeLooksNamespacedMCP(name string) bool {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "mcp__") {
		return true
	}
	index := strings.Index(name, ".")
	return index > 0 && index < len(name)-1
}

func resumeStatusForCall(item session.Item, output session.Item, hasOutput bool) (string, *bool) {
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyLocal(
		strings.TrimSpace(item.Status),
		interactiveSessionItemDataString(item, "status"),
	)))
	if status != "" {
		return status, resumeBoolPtr(output.Data, "success")
	}
	success := resumeBoolPtr(output.Data, "success")
	switch {
	case !hasOutput:
		return "in_progress", nil
	case success != nil && !*success:
		return "failed", success
	default:
		return "completed", success
	}
}

func resumeCommandExecutionItem(item session.Item, output session.Item, hasOutput bool) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	command := interactiveSessionItemDataString(item, "command", "cmd")
	if command == "" {
		command = resumeCommandFromArguments(item.Data["arguments"])
	}
	if command == "" && strings.TrimSpace(item.Text) != "" && len(item.Data) == 0 {
		command = strings.TrimSpace(item.Text)
	}
	status, _ := resumeStatusForCall(item, output, hasOutput)
	outputText := ""
	if hasOutput {
		outputText = firstNonEmptyLocal(
			interactiveSessionItemDataString(output, "output", "aggregated_output", "aggregatedOutput"),
			strings.TrimSpace(output.Text),
		)
	}
	if outputText == "" {
		// Older records keep the output on the call item itself (Rust's
		// ThreadItemPayload::CommandExecution carries aggregated_output).
		outputText = firstNonEmptyLocal(
			interactiveSessionItemDataString(item, "aggregated_output", "aggregatedOutput", "output", "result"),
			strings.TrimSpace(item.Text),
		)
	}
	exitCode := resumeIntPtr(output.Data, "exit_code", "exitCode")
	projected := protocol.CommandExecutionItem(callID, command, outputText, exitCode, status)
	projected.CallID = callID
	metadata := map[string]any{}
	if source := interactiveSessionItemDataString(item, "source", "origin"); source != "" {
		metadata["source"] = source
	}
	if duration := resumeDurationMS(item, output); duration >= 0 {
		metadata["durationMs"] = duration
	}
	if actions := resumeCommandActions(item.Data["commandActions"], item.Data["command_actions"]); len(actions) > 0 {
		projected.CommandActions = &actions
	}
	projected.Metadata = metadata
	return projected
}

func resumeFileChangeItem(item session.Item, output session.Item, hasOutput bool) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	changes := resumeProtocolFileChanges(item.Data["changes"])
	if len(changes) == 0 && hasOutput {
		changes = resumeProtocolFileChanges(output.Data["changes"])
	}
	status, _ := resumeStatusForCall(item, output, hasOutput)
	projected := protocol.FileChangeItem(callID, changes, status)
	projected.CallID = callID
	return projected
}

func resumeMCPToolCallItem(item session.Item, output session.Item, hasOutput bool) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	server := firstNonEmptyLocal(interactiveSessionItemDataString(item, "server"), resumeMCPNamespace(item))
	tool := firstNonEmptyLocal(interactiveSessionItemDataString(item, "tool"), resumeMCPToolName(item))
	status, _ := resumeStatusForCall(item, output, hasOutput)
	arguments := item.Data["arguments"]
	if arguments == nil {
		arguments = item.Data["input"]
	}
	var result *protocol.MCPToolResult
	if hasOutput {
		result = resumeMCPResult(output)
	}
	if result == nil || (len(result.Content) == 0 && result.StructuredContent == nil) {
		if stored := resumeMCPStoredResult(item); stored != nil {
			result = stored
		}
	}
	var callErr *protocol.MCPToolError
	if hasOutput {
		if message := firstNonEmptyLocal(
			interactiveSessionItemDataString(output, "error"),
			remoteTUIAnyString(output.Metadata["error"]),
		); message != "" {
			callErr = &protocol.MCPToolError{Message: message}
		}
	}
	projected := protocol.MCPToolCallItem(callID, server, tool, arguments, result, callErr, status)
	projected.CallID = callID
	return projected
}

func resumeMCPNamespace(item session.Item) string {
	name := resumeToolName(item)
	if index := strings.Index(name, "."); index > 0 {
		return strings.TrimSpace(name[:index])
	}
	return ""
}

func resumeMCPToolName(item session.Item) string {
	name := resumeToolName(item)
	if index := strings.LastIndex(name, "."); index > 0 && index < len(name)-1 {
		return strings.TrimSpace(name[index+1:])
	}
	return name
}

// resumeMCPStoredResult reads a result retained on the call item itself, which
// older local records use instead of a separate output item.
func resumeMCPStoredResult(item session.Item) *protocol.MCPToolResult {
	if content, ok := item.Data["content"].([]any); ok && len(content) > 0 {
		return &protocol.MCPToolResult{Content: append([]any(nil), content...)}
	}
	if text := strings.TrimSpace(item.Text); text != "" {
		return &protocol.MCPToolResult{Content: []any{text}}
	}
	return nil
}

func resumeMCPResult(output session.Item) *protocol.MCPToolResult {
	if value, ok := output.Data["result"].(map[string]any); ok {
		result := &protocol.MCPToolResult{}
		if content, ok := value["content"].([]any); ok {
			result.Content = append([]any(nil), content...)
		}
		if structured, ok := value["structured_content"]; ok {
			result.StructuredContent = structured
		}
		if meta, ok := value["_meta"]; ok {
			result.Meta = meta
		}
		if len(result.Content) > 0 || result.StructuredContent != nil {
			return result
		}
	}
	if content, ok := output.Data["content"].([]any); ok && len(content) > 0 {
		return &protocol.MCPToolResult{Content: append([]any(nil), content...)}
	}
	if text := strings.TrimSpace(output.Text); text != "" {
		return &protocol.MCPToolResult{Content: []any{text}}
	}
	return &protocol.MCPToolResult{}
}

func resumeDynamicToolCallItem(item session.Item, output session.Item, hasOutput bool) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	name := resumeToolName(item)
	namespace := strings.TrimSpace(item.Namespace)
	toolName := name
	if namespace == "" {
		if index := strings.LastIndex(name, "."); index > 0 && index < len(name)-1 {
			namespace = strings.TrimSpace(name[:index])
			toolName = strings.TrimSpace(name[index+1:])
		}
	}
	status, success := resumeStatusForCall(item, output, hasOutput)
	content := []string{}
	if hasOutput {
		if text := strings.TrimSpace(output.Text); text != "" {
			content = append(content, text)
		}
	}
	if len(content) == 0 {
		for _, key := range []string{"output", "result", "body"} {
			if text := strings.TrimSpace(interactiveSessionItemDataString(item, key)); text != "" {
				content = append(content, text)
				break
			}
		}
	}
	if len(content) == 0 {
		if value, ok := item.Data["content_items"].([]any); ok {
			for _, entry := range value {
				switch typed := entry.(type) {
				case string:
					if text := strings.TrimSpace(typed); text != "" {
						content = append(content, text)
					}
				case map[string]any:
					if text, ok := typed["text"].(string); ok {
						if text = strings.TrimSpace(text); text != "" {
							content = append(content, text)
						}
					}
				}
			}
		}
	}
	var arguments *any
	if value, ok := item.Data["arguments"]; ok {
		arguments = &value
	} else if value, ok := item.Data["input"]; ok {
		arguments = &value
	}
	projected := protocol.ThreadItem{
		ID:        firstNonEmptyLocal(callID, strings.TrimSpace(item.ID)),
		Type:      "dynamicToolCall",
		CallID:    callID,
		ToolName:  toolName,
		Arguments: arguments,
		Status:    status,
		Success:   success,
		Content:   content,
		Output:    strings.Join(content, "\n"),
	}
	metadata := map[string]any{}
	if duration := resumeDurationMS(item, output); duration >= 0 {
		metadata["durationMs"] = duration
	}
	projected.Metadata = metadata
	return projected
}

func resumeCollabAgentItem(item session.Item) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyLocal(strings.TrimSpace(item.Status), interactiveSessionItemDataString(item, "status"))))
	if status == "" {
		status = "completed"
	}
	projected := protocol.CollabToolCallItem(
		callID,
		firstNonEmptyLocal(interactiveSessionItemDataString(item, "tool"), resumeToolName(item)),
		firstNonEmptyLocal(interactiveSessionItemDataString(item, "senderThreadId", "sender_thread_id")),
		resumeStringSlice(item.Data["receiverThreadIds"], item.Data["receiver_thread_ids"]),
		resumeOptionalString(item.Data["prompt"]),
		resumeCollabAgentStates(item.Data["agentsStates"], item.Data["agents_states"]),
		status,
	)
	projected.CallID = callID
	return projected
}

func resumeSubAgentActivityItem(item session.Item) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	projected := protocol.SubAgentActivityItem(
		firstNonEmptyLocal(callID, strings.TrimSpace(item.ID)),
		firstNonEmptyLocal(interactiveSessionItemDataString(item, "kind", "activityKind"), strings.TrimSpace(item.Text)),
		firstNonEmptyLocal(interactiveSessionItemDataString(item, "agentThreadId", "agent_thread_id")),
		firstNonEmptyLocal(interactiveSessionItemDataString(item, "agentPath", "agent_path", "path")),
	)
	projected.CallID = callID
	return projected
}

func resumeWebSearchItem(item session.Item) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	query := firstNonEmptyLocal(interactiveSessionItemDataString(item, "query"), strings.TrimSpace(item.Text))
	action, _ := item.Data["action"].(map[string]any)
	projected := protocol.WebSearchItem(firstNonEmptyLocal(callID, strings.TrimSpace(item.ID)), query, action)
	projected.CallID = callID
	return projected
}

func resumeImageViewItem(item session.Item, output session.Item) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	path := interactiveSessionItemDataString(item, "path")
	if path == "" {
		if value, ok := item.Data["arguments"].(map[string]any); ok {
			path = remoteTUIAnyString(value["path"])
		}
	}
	if path == "" {
		if arguments := strings.TrimSpace(remoteTUIAnyString(item.Data["arguments"])); arguments != "" {
			var decoded map[string]any
			if json.Unmarshal([]byte(arguments), &decoded) == nil {
				path = remoteTUIAnyString(decoded["path"])
			}
		}
	}
	if path == "" {
		path = strings.TrimSpace(output.Text)
	}
	return protocol.ThreadItem{
		ID:     firstNonEmptyLocal(callID, strings.TrimSpace(item.ID)),
		Type:   "imageView",
		CallID: callID,
		Path:   path,
	}
}

func resumeImageGenerationItem(item session.Item) protocol.ThreadItem {
	callID := resumeToolItemCallID(item)
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyLocal(strings.TrimSpace(item.Status), interactiveSessionItemDataString(item, "status"))))
	revisedPrompt := firstNonEmptyLocal(
		interactiveSessionItemDataString(item, "revisedPrompt", "revised_prompt"),
		strings.TrimSpace(item.Text),
	)
	savedPath := interactiveSessionItemDataString(item, "savedPath", "saved_path")
	projected := protocol.ImageGenerationItem(firstNonEmptyLocal(callID, strings.TrimSpace(item.ID)), status, revisedPrompt, savedPath)
	projected.CallID = callID
	return projected
}

// resumeHookPromptItem keeps the persisted hook prompt fragments, which the
// loaded transcript prints as plain rows (Rust other_items::cells).
func resumeHookPromptItem(item session.Item) protocol.ThreadItem {
	texts := []string{}
	switch values := item.Data["fragments"].(type) {
	case []string:
		for _, text := range values {
			if text = strings.TrimSpace(text); text != "" {
				texts = append(texts, text)
			}
		}
	case []any:
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
	}
	if len(texts) == 0 {
		if text := strings.TrimSpace(item.Text); text != "" {
			texts = append(texts, text)
		}
	}
	return protocol.ThreadItem{
		ID:      strings.TrimSpace(item.ID),
		Type:    "hookPrompt",
		CallID:  resumeToolItemCallID(item),
		Content: texts,
	}
}

func resumeCommandFromArguments(value any) string {
	switch typed := value.(type) {
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(typed), &decoded) == nil {
			return resumeCommandFromArguments(decoded)
		}
	case map[string]any:
		if command, ok := typed["cmd"].(string); ok && strings.TrimSpace(command) != "" {
			return strings.TrimSpace(command)
		}
		if command, ok := typed["command"].(string); ok && strings.TrimSpace(command) != "" {
			return strings.TrimSpace(command)
		}
		if parts := resumeStringSlice(typed["command"]); len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return ""
}

func resumeDataBool(data map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := data[key].(bool); ok && value {
			return true
		}
	}
	return false
}

func resumeIntPtr(data map[string]any, keys ...string) *int {
	for _, key := range keys {
		switch value := data[key].(type) {
		case int:
			valueCopy := value
			return &valueCopy
		case int64:
			valueCopy := int(value)
			return &valueCopy
		case float64:
			valueCopy := int(value)
			return &valueCopy
		case json.Number:
			if parsed, err := value.Int64(); err == nil {
				valueCopy := int(parsed)
				return &valueCopy
			}
		}
	}
	return nil
}

func resumeBoolPtr(data map[string]any, keys ...string) *bool {
	for _, key := range keys {
		if value, ok := data[key].(bool); ok {
			valueCopy := value
			return &valueCopy
		}
	}
	return nil
}

func resumeDurationMS(items ...session.Item) int64 {
	for _, item := range items {
		for _, key := range []string{"durationMs", "duration_ms"} {
			if value, ok := item.Data[key]; ok {
				if parsed, ok := resumeInt64(value); ok && parsed >= 0 {
					return parsed
				}
			}
			if value, ok := item.Metadata[key]; ok {
				if parsed, ok := resumeInt64(value); ok && parsed >= 0 {
					return parsed
				}
			}
		}
	}
	return -1
}

func resumeInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	}
	return 0, false
}

func resumeStringSlice(values ...any) []string {
	for _, value := range values {
		switch typed := value.(type) {
		case []string:
			return append([]string(nil), typed...)
		case []any:
			out := make([]string, 0, len(typed))
			for _, entry := range typed {
				if text := strings.TrimSpace(remoteTUIAnyString(entry)); text != "" {
					out = append(out, text)
				}
			}
			return out
		}
	}
	return nil
}

func resumeOptionalString(value any) *string {
	text := strings.TrimSpace(remoteTUIAnyString(value))
	if text == "" {
		return nil
	}
	return &text
}

func resumeCollabAgentStates(values ...any) map[string]protocol.CollabAgentState {
	for _, value := range values {
		raw, ok := value.(map[string]any)
		if !ok {
			continue
		}
		states := make(map[string]protocol.CollabAgentState, len(raw))
		for key, entry := range raw {
			fields, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			state := protocol.CollabAgentState{Status: remoteTUIAnyString(fields["status"])}
			if message := remoteTUIAnyString(fields["message"]); message != "" {
				state.Message = &message
			}
			states[key] = state
		}
		if len(states) > 0 {
			return states
		}
	}
	return nil
}

func resumeCommandActions(values ...any) []protocol.CommandAction {
	for _, value := range values {
		entries, ok := value.([]any)
		if !ok {
			continue
		}
		actions := make([]protocol.CommandAction, 0, len(entries))
		for _, entry := range entries {
			fields, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			actions = append(actions, protocol.CommandAction{
				Type:    remoteTUIAnyString(fields["type"]),
				Command: remoteTUIAnyString(fields["command"]),
				Name:    remoteTUIAnyString(fields["name"]),
				Path:    remoteTUIAnyString(fields["path"]),
				Query:   remoteTUIAnyString(fields["query"]),
			})
		}
		if len(actions) > 0 {
			return actions
		}
	}
	return nil
}

func resumeProtocolFileChanges(value any) []protocol.FileChange {
	entries, ok := value.([]any)
	if !ok {
		switch typed := value.(type) {
		case []protocol.FileChange:
			return append([]protocol.FileChange(nil), typed...)
		}
		return nil
	}
	changes := make([]protocol.FileChange, 0, len(entries))
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		change := protocol.FileChange{
			Path: remoteTUIAnyString(fields["path"]),
		}
		switch kind := fields["kind"].(type) {
		case string:
			change.Kind = kind
			change.MovePath = remoteTUIAnyString(fields["movePath"])
			if change.MovePath == "" {
				change.MovePath = remoteTUIAnyString(fields["move_path"])
			}
		case map[string]any:
			change.Kind = remoteTUIAnyString(kind["type"])
			change.MovePath = remoteTUIAnyString(kind["movePath"])
			if change.MovePath == "" {
				change.MovePath = remoteTUIAnyString(kind["move_path"])
			}
		}
		change.Diff = remoteTUIAnyString(fields["diff"])
		if change.Path == "" {
			continue
		}
		changes = append(changes, change)
	}
	return changes
}
