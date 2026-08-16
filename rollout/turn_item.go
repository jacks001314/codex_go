package rollout

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex_go/session"
	"codex_go/shell"
	"codex_go/utils"
)

// CoreTurnItemJSONFromSessionItem converts the session-store representation to
// the canonical codex-protocol TurnItem persisted by paginated Rust rollouts.
func CoreTurnItemJSONFromSessionItem(item *session.Item) (json.RawMessage, string, error) {
	if item == nil {
		return nil, "", errors.New("session item is nil")
	}
	turnID := sessionItemExactTurnID(item)
	if turnID == "" {
		return nil, "", errors.New("paginated thread item is missing turn id")
	}
	if sessionItemHiddenFromThread(item) {
		return nil, turnID, nil
	}
	if raw, ok := existingCoreTurnItem(item.Raw); ok {
		return raw, turnID, nil
	}
	values := mergedSessionItemValues(item)
	kind := sessionItemCoreKind(item, values)
	id := strings.TrimSpace(item.ID)
	if coreKindUsesExternalID(kind) {
		id = firstNonEmptyString(anyString(values, "itemId", "item_id", "callId", "call_id"), item.CallID, id)
	}
	if id == "" || kind == "" {
		return nil, "", errors.New("paginated thread item is missing id or type")
	}
	core := map[string]any{"type": kind, "id": id}
	switch kind {
	case "UserMessage":
		if clientID := anyString(values, "clientId", "client_id"); clientID != "" {
			core["client_id"] = clientID
		}
		core["content"] = coreUserContent(item, values)
	case "HookPrompt":
		core["fragments"] = coreHookFragments(item, values)
	case "AgentMessage":
		core["content"] = []any{map[string]any{"type": "Text", "text": item.Text}}
		copyOptional(core, "phase", firstAny(values, "phase", "messagePhase"))
		if citation := firstAny(values, "memory_citation", "memoryCitation"); citation != nil {
			core["memory_citation"] = coreMemoryCitation(citation)
		}
	case "Plan":
		core["text"] = item.Text
	case "Reasoning":
		core["summary_text"] = anyStringSliceDefault(firstAny(values, "summary", "summary_text"), splitNonEmptyLines(item.Text))
		core["raw_content"] = anyStringSliceDefault(firstAny(values, "reasoningContent", "raw_content", "content"), []string{})
	case "CommandExecution":
		populateCoreCommand(core, item, values)
	case "DynamicToolCall":
		populateCoreDynamicTool(core, item, values)
	case "CollabAgentToolCall":
		populateCoreCollab(core, values)
	case "SubAgentActivity":
		core["kind"] = snakeEnum(anyString(values, "kind"))
		core["agent_thread_id"] = anyString(values, "agentThreadId", "agent_thread_id")
		core["agent_path"] = anyString(values, "agentPath", "agent_path", "path")
	case "WebSearch":
		core["query"] = firstNonEmptyString(anyString(values, "query"), item.Text)
		core["action"] = coreWebSearchAction(firstAny(values, "action", "webSearchAction", "web_search_action"), core["query"].(string))
		copyOptional(core, "results", firstAny(values, "results", "webSearchResults", "web_search_results"))
	case "ImageView":
		core["path"] = pathURIString(anyString(values, "path"))
	case "Extension":
		populateCoreExtension(core, item, values)
	case "ImageGeneration":
		populateCoreImageGeneration(core, item, values)
	case "EnteredReviewMode":
		core["target"] = firstNonNil(firstAny(values, "target"), map[string]any{"type": "custom", "instructions": ""})
		core["user_facing_hint"] = firstNonEmptyString(anyString(values, "review", "user_facing_hint"), item.Text)
	case "ExitedReviewMode":
		copyOptional(core, "review_output", firstAny(values, "review_output", "reviewOutput"))
	case "FileChange":
		populateCoreFileChange(core, item, values)
	case "McpToolCall":
		populateCoreMCP(core, item, values)
	case "ContextCompaction":
	}
	encoded, err := json.Marshal(core)
	return encoded, turnID, err
}

func (r *Recorder) appendPaginatedItem(item Item, now time.Time) error {
	sessionItem := session.Item{
		ID: item.ID, Type: item.Type, Role: item.Role, Text: item.Text, Name: item.Name,
		CallID: item.CallID, Data: cloneAnyMap(item.Data), Raw: append(json.RawMessage(nil), item.Raw...),
		CreatedAt: now, ResponseID: item.ResponseID, Metadata: cloneAnyMap(item.Metadata),
	}
	for i := range item.Content {
		sessionItem.Content = append(sessionItem.Content, session.ContentPart{Type: item.Content[i].Type, Text: item.Content[i].Text, ImageURL: item.Content[i].ImageURL, Detail: cloneStringPtr(item.Content[i].Detail)})
	}
	raw, turnID, err := CoreTurnItemJSONFromSessionItem(&sessionItem)
	if err != nil || len(raw) == 0 {
		return err
	}
	return r.AppendItemCompleted(raw, turnID, now, now)
}

// PublicThreadItemJSONFromCore applies the same presentation conversion as
// codex-app-server-protocol's From<CoreTurnItem> implementation.
func PublicThreadItemJSONFromCore(raw json.RawMessage) (json.RawMessage, string, string, error) {
	var core map[string]any
	if err := json.Unmarshal(raw, &core); err != nil {
		return nil, "", "", err
	}
	kind := anyString(core, "type")
	id := anyString(core, "id")
	publicType := publicTypeForCore(core)
	if publicType == "" {
		publicType = existingPublicType(kind)
		if id != "" && publicType != "" {
			core["type"] = publicType
			encoded, err := json.Marshal(core)
			return encoded, id, publicType, err
		}
	}
	if id == "" || publicType == "" {
		return nil, "", "", errors.New("completed thread item is missing id or type")
	}
	out := map[string]any{"type": publicType, "id": id}
	switch kind {
	case "UserMessage":
		out["clientId"] = nullableAny(core, "client_id", "clientId")
		out["content"] = publicUserContent(firstAny(core, "content"))
	case "HookPrompt":
		out["fragments"] = camelObjectSlice(firstAny(core, "fragments"))
	case "AgentMessage":
		out["text"] = agentMessageText(firstAny(core, "content"))
		out["phase"] = nullableAny(core, "phase")
		out["memoryCitation"] = publicMemoryCitation(firstAny(core, "memory_citation", "memoryCitation"))
	case "Plan":
		out["text"] = anyString(core, "text")
	case "Reasoning":
		out["summary"] = anyStringSliceDefault(firstAny(core, "summary_text", "summary"), []string{})
		out["content"] = anyStringSliceDefault(firstAny(core, "raw_content", "content"), []string{})
	case "CommandExecution":
		populatePublicCommand(out, core)
	case "DynamicToolCall":
		populatePublicDynamic(out, core)
	case "CollabAgentToolCall":
		populatePublicCollab(out, core)
	case "SubAgentActivity":
		out["kind"] = camelEnum(anyString(core, "kind"))
		out["agentThreadId"] = anyString(core, "agent_thread_id", "agentThreadId")
		out["agentPath"] = anyString(core, "agent_path", "agentPath")
	case "WebSearch":
		out["query"] = anyString(core, "query")
		out["action"] = publicWebSearchAction(firstAny(core, "action"))
		copyOptional(out, "results", firstAny(core, "results"))
	case "ImageView":
		out["path"] = legacyPathString(anyString(core, "path"))
	case "Extension":
		populatePublicExtension(out, core)
	case "ImageGeneration":
		populatePublicImageGeneration(out, core)
	case "EnteredReviewMode":
		out["review"] = anyString(core, "user_facing_hint", "review")
	case "ExitedReviewMode":
		out["review"] = reviewOutputText(firstAny(core, "review_output", "reviewOutput"))
	case "FileChange":
		populatePublicFileChange(out, core)
	case "McpToolCall":
		populatePublicMCP(out, core)
	case "ContextCompaction":
	default:
		return nil, "", "", fmt.Errorf("unsupported core turn item type %q", kind)
	}
	encoded, err := json.Marshal(out)
	return encoded, id, publicType, err
}

func existingPublicType(kind string) string {
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(kind)))
	switch compact {
	case "usermessage":
		return "userMessage"
	case "hookprompt":
		return "hookPrompt"
	case "agentmessage", "assistantmessage":
		return "agentMessage"
	case "plan":
		return "plan"
	case "reasoning":
		return "reasoning"
	case "commandexecution":
		return "commandExecution"
	case "dynamictoolcall":
		return "dynamicToolCall"
	case "collabagenttoolcall":
		return "collabAgentToolCall"
	case "subagentactivity":
		return "subAgentActivity"
	case "websearch":
		return "webSearch"
	case "imageview":
		return "imageView"
	case "sleep":
		return "sleep"
	case "imagegeneration":
		return "imageGeneration"
	case "enteredreviewmode":
		return "enteredReviewMode"
	case "exitedreviewmode":
		return "exitedReviewMode"
	case "filechange":
		return "fileChange"
	case "mcptoolcall":
		return "mcpToolCall"
	case "contextcompaction":
		return "contextCompaction"
	default:
		return ""
	}
}

func sessionItemCoreKind(item *session.Item, values map[string]any) string {
	kind := normalizeRolloutItemType(item.Type)
	switch kind {
	case "message":
		if strings.EqualFold(strings.TrimSpace(item.Role), "assistant") {
			return "AgentMessage"
		}
		return "UserMessage"
	case "agent_message":
		return "AgentMessage"
	case "hookPrompt":
		return "HookPrompt"
	case "plan":
		return "Plan"
	case "reasoning":
		return "Reasoning"
	case "commandExecution":
		return "CommandExecution"
	case "dynamicToolCall":
		return "DynamicToolCall"
	case "collabAgentToolCall":
		return "CollabAgentToolCall"
	case "subAgentActivity":
		return "SubAgentActivity"
	case "webSearch":
		return "WebSearch"
	case "imageView":
		return "ImageView"
	case "sleep":
		return "Extension"
	case "imageGeneration":
		return "ImageGeneration"
	case "enteredReviewMode":
		return "EnteredReviewMode"
	case "exitedReviewMode":
		return "ExitedReviewMode"
	case "fileChange":
		return "FileChange"
	case "mcpToolCall":
		return "McpToolCall"
	case "contextCompaction":
		return "ContextCompaction"
	}
	if rolloutBoolFromAny(firstAny(values, "fileChange", "file_change")) || strings.EqualFold(item.Name, "apply_patch") {
		return "FileChange"
	}
	if rolloutBoolFromAny(firstAny(values, "mcpToolCall", "mcp_tool_call")) {
		return "McpToolCall"
	}
	if rolloutBoolFromAny(firstAny(values, "dynamicToolCall", "dynamic_tool_call")) {
		return "DynamicToolCall"
	}
	if isToolSessionType(kind) {
		if firstAny(values, "command", "cmd") != nil {
			return "CommandExecution"
		}
		// Rust's canonical paginated thread vocabulary has no plain
		// function_call / tool_output variants: non-command tool exchanges are
		// represented as DynamicToolCall items (codex-rs protocol TurnItem).
		// This fallback makes exec-generated tool items paginated-compatible.
		return "DynamicToolCall"
	}
	return ""
}

func publicTypeForCore(core map[string]any) string {
	switch anyString(core, "type") {
	case "UserMessage":
		return "userMessage"
	case "HookPrompt":
		return "hookPrompt"
	case "AgentMessage":
		return "agentMessage"
	case "Plan":
		return "plan"
	case "Reasoning":
		return "reasoning"
	case "CommandExecution":
		return "commandExecution"
	case "DynamicToolCall":
		return "dynamicToolCall"
	case "CollabAgentToolCall":
		return "collabAgentToolCall"
	case "SubAgentActivity":
		return "subAgentActivity"
	case "WebSearch":
		return "webSearch"
	case "ImageView":
		return "imageView"
	case "ImageGeneration":
		return "imageGeneration"
	case "EnteredReviewMode":
		return "enteredReviewMode"
	case "ExitedReviewMode":
		return "exitedReviewMode"
	case "FileChange":
		return "fileChange"
	case "McpToolCall":
		return "mcpToolCall"
	case "ContextCompaction":
		return "contextCompaction"
	case "Extension":
		switch anyString(core, "kind") {
		case "clock.sleep":
			return "sleep"
		case "image_gen.generation":
			return "imageGeneration"
		case "web.search":
			return "webSearch"
		}
	}
	return ""
}

func populateCoreCommand(core map[string]any, item *session.Item, values map[string]any) {
	command := firstAny(values, "command", "cmd")
	args := anyStringSlice(command)
	if len(args) == 0 {
		args = shell.SplitCommandLine(firstNonEmptyString(anyString(values, "command", "cmd"), item.Text))
	}
	copyOptional(core, "plugin_id", omitEmpty(anyString(values, "pluginId", "plugin_id")))
	copyOptional(core, "script_path", omitEmpty(anyString(values, "scriptPath", "script_path")))
	copyOptional(core, "process_id", omitEmpty(anyString(values, "processId", "process_id")))
	core["command"] = args
	core["cwd"] = pathURIString(anyString(values, "cwd"))
	core["parsed_cmd"] = coreParsedCommands(firstAny(values, "parsed_cmd", "parsedCmd", "commandActions", "command_actions"), rolloutShellJoin(args))
	core["source"] = snakeEnum(defaultString(anyString(values, "source"), "agent"))
	copyOptional(core, "interaction_input", firstAny(values, "interactionInput", "interaction_input"))
	core["status"] = snakeEnum(commandStatus(item, values))
	copyOptional(core, "stdout", firstAny(values, "stdout"))
	copyOptional(core, "stderr", firstAny(values, "stderr"))
	copyOptional(core, "aggregated_output", firstNonNil(firstAny(values, "aggregatedOutput", "aggregated_output", "output"), nonEmptyAny(item.Text)))
	copyOptional(core, "exit_code", firstAny(values, "exitCode", "exit_code"))
	if duration, ok := durationFromMS(firstAny(values, "durationMs", "duration_ms")); ok {
		core["duration"] = duration
	}
	copyOptional(core, "formatted_output", firstAny(values, "formattedOutput", "formatted_output"))
}

func populatePublicCommand(out, core map[string]any) {
	out["pluginId"] = nullableAny(core, "plugin_id", "pluginId")
	out["scriptPath"] = nullableAny(core, "script_path", "scriptPath")
	out["command"] = rolloutCommandStringFromAny(firstAny(core, "command"))
	out["cwd"] = legacyPathString(anyString(core, "cwd"))
	out["processId"] = nullableAny(core, "process_id", "processId")
	out["source"] = camelEnum(defaultString(anyString(core, "source"), "agent"))
	out["status"] = camelEnum(anyString(core, "status"))
	out["commandActions"] = rolloutCommandActionsFromAny(firstAny(core, "parsed_cmd", "parsedCmd"), legacyPathString(anyString(core, "cwd")))
	aggregated := rawStringFromMap(core, "aggregated_output")
	if aggregated == "" {
		aggregated = rawStringFromMap(core, "aggregatedOutput")
	}
	if aggregated == "" {
		out["aggregatedOutput"] = nil
	} else {
		out["aggregatedOutput"] = aggregated
	}
	out["exitCode"] = nullableAny(core, "exit_code", "exitCode")
	if ms, ok := rolloutDurationMSFromAny(firstAny(core, "duration")); ok {
		out["durationMs"] = ms
	} else {
		out["durationMs"] = nil
	}
}

func populateCoreDynamicTool(core map[string]any, item *session.Item, values map[string]any) {
	copyOptional(core, "namespace", omitEmpty(firstNonEmptyString(item.Namespace, anyString(values, "namespace"))))
	core["tool"] = firstNonEmptyString(anyString(values, "tool"), toolLeaf(item.Name))
	core["arguments"] = jsonValue(firstAny(values, "arguments", "input", "rawArguments", "raw_arguments"), map[string]any{})
	core["status"] = snakeEnum(dynamicStatus(item, values))
	contentItems := firstAny(values, "contentItems", "content_items")
	if contentItems == nil && item != nil && strings.Contains(item.Type, "output") && strings.TrimSpace(item.Text) != "" {
		contentItems = []any{map[string]any{"type": "inputText", "text": item.Text}}
	}
	copyOptional(core, "content_items", contentItems)
	copyOptional(core, "success", firstAny(values, "success"))
	copyOptional(core, "error", firstAny(values, "error"))
	if duration, ok := durationFromMS(firstAny(values, "durationMs", "duration_ms")); ok {
		core["duration"] = duration
	}
}

func populatePublicDynamic(out, core map[string]any) {
	out["namespace"] = nullableAny(core, "namespace")
	out["tool"] = anyString(core, "tool")
	out["arguments"] = firstNonNil(firstAny(core, "arguments"), map[string]any{})
	out["status"] = camelEnum(anyString(core, "status"))
	out["contentItems"] = nullableAny(core, "content_items", "contentItems")
	out["success"] = nullableAny(core, "success")
	if ms, ok := rolloutDurationMSFromAny(firstAny(core, "duration")); ok {
		out["durationMs"] = ms
	} else {
		out["durationMs"] = nil
	}
}

func populateCoreCollab(core map[string]any, values map[string]any) {
	core["tool"] = snakeEnum(anyString(values, "tool"))
	core["status"] = snakeEnum(defaultString(anyString(values, "status"), "completed"))
	core["sender_thread_id"] = anyString(values, "senderThreadId", "sender_thread_id")
	core["receiver_thread_ids"] = anyStringSliceDefault(firstAny(values, "receiverThreadIds", "receiver_thread_ids"), []string{})
	core["receiver_agents"] = firstNonNil(firstAny(values, "receiverAgents", "receiver_agents"), []any{})
	copyOptional(core, "prompt", firstAny(values, "prompt"))
	copyOptional(core, "model", firstAny(values, "model"))
	copyOptional(core, "reasoning_effort", firstAny(values, "reasoningEffort", "reasoning_effort"))
	core["agents_states"] = firstNonNil(firstAny(values, "agentsStates", "agents_states"), map[string]any{})
}

func populatePublicCollab(out, core map[string]any) {
	out["tool"] = camelEnum(anyString(core, "tool"))
	out["status"] = camelEnum(anyString(core, "status"))
	out["senderThreadId"] = anyString(core, "sender_thread_id", "senderThreadId")
	out["receiverThreadIds"] = anyStringSliceDefault(firstAny(core, "receiver_thread_ids", "receiverThreadIds"), []string{})
	out["prompt"] = nullableAny(core, "prompt")
	out["model"] = nullableAny(core, "model")
	out["reasoningEffort"] = nullableAny(core, "reasoning_effort", "reasoningEffort")
	out["agentsStates"] = publicAgentStates(firstAny(core, "agents_states", "agentsStates"))
}

func populateCoreExtension(core map[string]any, item *session.Item, values map[string]any) {
	switch normalizeRolloutItemType(item.Type) {
	case "sleep":
		core["kind"] = "clock.sleep"
		core["durationMs"] = anyInt64Default(firstAny(values, "durationMs", "duration_ms"), 0)
	case "imageGeneration":
		core["kind"] = "image_gen.generation"
		populateCoreImageGeneration(core, item, values)
	default:
		core["kind"] = anyString(values, "kind")
	}
}

func populatePublicExtension(out, core map[string]any) {
	switch anyString(core, "kind") {
	case "clock.sleep":
		out["durationMs"] = anyInt64Default(firstAny(core, "durationMs", "duration_ms"), 0)
	case "image_gen.generation":
		populatePublicImageGeneration(out, core)
	case "web.search":
		out["query"] = anyString(core, "query")
		out["action"] = publicWebSearchAction(firstAny(core, "action"))
		copyOptional(out, "results", firstAny(core, "results"))
	}
}

func populateCoreImageGeneration(core map[string]any, item *session.Item, values map[string]any) {
	core["status"] = firstNonEmptyString(item.Status, anyString(values, "status"))
	copyOptional(core, "revised_prompt", firstAny(values, "revisedPrompt", "revised_prompt"))
	core["result"] = firstNonEmptyString(anyString(values, "result"), item.Text)
	copyOptional(core, "transparent_background", firstAny(values, "transparentBackground", "transparent_background"))
	copyOptional(core, "saved_path", firstAny(values, "savedPath", "saved_path"))
}

func populatePublicImageGeneration(out, core map[string]any) {
	out["status"] = anyString(core, "status")
	out["revisedPrompt"] = nullableAny(core, "revised_prompt", "revisedPrompt")
	out["result"] = anyString(core, "result")
	out["transparentBackground"] = nullableAny(core, "transparent_background", "transparentBackground")
	copyOptional(out, "savedPath", firstAny(core, "saved_path", "savedPath"))
}

func populateCoreFileChange(core map[string]any, item *session.Item, values map[string]any) {
	changes := publicFileChanges(firstAny(values, "changes", "fileChanges", "file_changes"))
	coreChanges := make(map[string]any, len(changes))
	for _, change := range changes {
		path := anyString(change, "path", "filePath", "file_path", "file")
		if path == "" {
			continue
		}
		kind := patchKind(firstAny(change, "kind", "type"))
		diff := anyString(change, "diff", "unifiedDiff", "unified_diff", "content")
		switch kind {
		case "add":
			coreChanges[path] = map[string]any{"type": "add", "content": diff}
		case "delete":
			coreChanges[path] = map[string]any{"type": "delete", "content": diff}
		default:
			entry := map[string]any{"type": "update", "unified_diff": diff}
			copyOptional(entry, "move_path", firstAny(change, "movePath", "move_path"))
			if mapped := mapFromAny(firstAny(change, "kind")); mapped != nil {
				copyOptional(entry, "move_path", firstAny(mapped, "movePath", "move_path"))
			}
			coreChanges[path] = entry
		}
	}
	core["changes"] = coreChanges
	status := fileChangeStatus(item, values)
	if status != "in_progress" {
		core["status"] = status
	}
	copyOptional(core, "auto_approved", firstAny(values, "autoApproved", "auto_approved"))
	copyOptional(core, "stdout", firstAny(values, "stdout"))
	copyOptional(core, "stderr", firstAny(values, "stderr"))
}

func populatePublicFileChange(out, core map[string]any) {
	changes := mapFromAny(firstAny(core, "changes"))
	paths := make([]string, 0, len(changes))
	for path := range changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	public := make([]any, 0, len(paths))
	for _, path := range paths {
		change := mapFromAny(changes[path])
		kind := patchKind(firstAny(change, "type"))
		diff := anyString(change, "content", "unified_diff")
		entry := map[string]any{"path": path, "kind": map[string]any{"type": kind}, "diff": diff}
		if kind == "update" {
			copyOptional(entry["kind"].(map[string]any), "move_path", firstAny(change, "move_path"))
			if move := anyString(change, "move_path"); move != "" {
				entry["diff"] = diff + "\n\nMoved to: " + move
			}
		}
		public = append(public, entry)
	}
	out["changes"] = public
	out["status"] = camelEnum(defaultString(anyString(core, "status"), "in_progress"))
}

func populateCoreMCP(core map[string]any, item *session.Item, values map[string]any) {
	core["server"] = anyString(values, "server")
	core["tool"] = firstNonEmptyString(anyString(values, "tool"), toolLeaf(item.Name))
	core["arguments"] = jsonValue(firstAny(values, "arguments", "input", "rawArguments", "raw_arguments"), nil)
	appContext := mapFromAny(firstAny(values, "appContext", "app_context"))
	copyOptional(core, "connectorId", firstNonNil(firstAny(values, "connectorId", "connector_id"), firstAny(appContext, "connectorId", "connector_id")))
	copyOptional(core, "mcpAppResourceUri", firstNonNil(firstAny(values, "mcpAppResourceUri", "mcp_app_resource_uri"), firstAny(appContext, "resourceUri", "resource_uri")))
	copyOptional(core, "linkId", firstNonNil(firstAny(values, "linkId", "link_id"), firstAny(appContext, "linkId", "link_id")))
	copyOptional(core, "appName", firstNonNil(firstAny(values, "appName", "app_name"), firstAny(appContext, "appName", "app_name")))
	copyOptional(core, "actionName", firstNonNil(firstAny(values, "actionName", "action_name"), firstAny(appContext, "actionName", "action_name")))
	copyOptional(core, "pluginId", firstAny(values, "pluginId", "plugin_id"))
	copyOptional(core, "readOnlyHint", firstAny(values, "readOnlyHint", "read_only_hint"))
	core["status"] = camelEnum(mcpStatus(item, values))
	copyOptional(core, "result", firstAny(values, "result"))
	if errValue := firstAny(values, "error"); errValue != nil {
		core["error"] = map[string]any{"message": errorMessage(errValue)}
	}
	if duration, ok := durationFromMS(firstAny(values, "durationMs", "duration_ms")); ok {
		core["duration"] = duration
	}
}

func populatePublicMCP(out, core map[string]any) {
	out["server"] = anyString(core, "server")
	out["tool"] = anyString(core, "tool")
	out["status"] = camelEnum(anyString(core, "status"))
	out["arguments"] = firstAny(core, "arguments")
	connector := anyString(core, "connectorId", "connector_id")
	if connector == "" {
		out["appContext"] = nil
	} else {
		out["appContext"] = map[string]any{"connectorId": connector, "linkId": nullableAny(core, "linkId", "link_id"), "resourceUri": nullableAny(core, "mcpAppResourceUri", "mcp_app_resource_uri"), "appName": nullableAny(core, "appName", "app_name"), "actionName": nullableAny(core, "actionName", "action_name")}
	}
	out["mcpAppResourceUri"] = nullableAny(core, "mcpAppResourceUri", "mcp_app_resource_uri")
	out["pluginId"] = nullableAny(core, "pluginId", "plugin_id")
	out["readOnlyHint"] = nullableAny(core, "readOnlyHint", "read_only_hint")
	out["result"] = publicMCPResult(firstAny(core, "result"))
	out["error"] = nullableAny(core, "error")
	if ms, ok := rolloutDurationMSFromAny(firstAny(core, "duration")); ok {
		out["durationMs"] = ms
	} else {
		out["durationMs"] = nil
	}
}

func coreUserContent(item *session.Item, values map[string]any) []any {
	if content := firstAny(values, "content"); content != nil {
		return coreUserContentFromAny(content)
	}
	out := make([]any, 0, len(item.Content))
	for _, part := range item.Content {
		entry := map[string]any{}
		switch part.Type {
		case "input_image", "image":
			entry["type"] = "image"
			entry["image_url"] = part.ImageURL
			copyOptional(entry, "detail", part.Detail)
		case "localImage", "local_image":
			entry["type"] = "local_image"
			entry["path"] = part.ImageURL
			copyOptional(entry, "detail", part.Detail)
		case "input_audio", "audio":
			entry["type"] = "audio"
			entry["audio_url"] = part.AudioURL
		default:
			entry["type"] = "text"
			entry["text"] = firstNonEmptyString(part.Text, item.Text)
			entry["text_elements"] = []any{}
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"type": "text", "text": item.Text, "text_elements": []any{}})
	}
	return out
}

func coreUserContentFromAny(value any) []any {
	out := []any{}
	for _, entry := range anyObjectSlice(value) {
		kind := anyString(entry, "type")
		switch kind {
		case "localImage":
			kind = "local_image"
		case "localAudio":
			kind = "local_audio"
		}
		core := cloneAnyMap(entry)
		core["type"] = kind
		if kind == "image" {
			renameMapKey(core, "url", "image_url")
		}
		if kind == "audio" {
			renameMapKey(core, "url", "audio_url")
		}
		if kind == "text" {
			renameMapKey(core, "textElements", "text_elements")
		}
		if kind == "text" && core["text_elements"] == nil {
			core["text_elements"] = []any{}
		}
		out = append(out, core)
	}
	return out
}
func publicUserContent(value any) []any {
	out := []any{}
	for _, entry := range anyObjectSlice(value) {
		kind := anyString(entry, "type")
		pub := cloneAnyMap(entry)
		switch kind {
		case "local_image":
			pub["type"] = "localImage"
		case "local_audio":
			pub["type"] = "localAudio"
		}
		if kind == "image" {
			renameMapKey(pub, "image_url", "url")
		}
		if kind == "audio" {
			renameMapKey(pub, "audio_url", "url")
		}
		if kind == "text" {
			renameMapKey(pub, "text_elements", "textElements")
			if elements, ok := pub["textElements"].([]any); ok {
				for _, rawElement := range elements {
					if element := mapFromAny(rawElement); element != nil {
						renameMapKey(element, "byte_range", "byteRange")
					}
				}
			}
		}
		out = append(out, pub)
	}
	return out
}
func coreHookFragments(item *session.Item, values map[string]any) []any {
	if value := firstAny(values, "fragments", "hookPromptFragments", "hook_prompt_fragments"); value != nil {
		out := make([]any, 0)
		for _, fragment := range anyObjectSlice(value) {
			out = append(out, map[string]any{
				"text":      anyString(fragment, "text"),
				"hookRunId": anyString(fragment, "hookRunId", "hook_run_id"),
			})
		}
		return out
	}
	if item.Text == "" {
		return []any{}
	}
	return []any{map[string]any{"text": item.Text, "hookRunId": anyString(values, "hookRunId", "hook_run_id", "runId", "run_id")}}
}

func existingCoreTurnItem(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	if publicTypeForCore(v) == "" {
		return nil, false
	}
	return append(json.RawMessage(nil), raw...), true
}
func mergedSessionItemValues(item *session.Item) map[string]any {
	out := cloneAnyMap(item.Data)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range item.Metadata {
		out[k] = v
	}
	if item.CallID != "" {
		out["callId"] = item.CallID
	}
	if item.Name != "" {
		out["name"] = item.Name
	}
	if item.Namespace != "" {
		out["namespace"] = item.Namespace
	}
	return out
}
func sessionItemExactTurnID(item *session.Item) string {
	if item == nil {
		return ""
	}
	return firstNonEmptyString(anyString(item.Metadata, "turnId", "turn_id"), anyString(item.Data, "turnId", "turn_id"))
}
func sessionItemHiddenFromThread(item *session.Item) bool {
	if item == nil {
		return false
	}
	kind := firstNonEmptyString(anyString(item.Metadata, "kind"), anyString(item.Data, "kind"))
	if kind == "skill_instructions" || kind == "image_generation_instructions" {
		return true
	}
	return rolloutBoolFromAny(firstNonNil(firstAny(item.Metadata, "hiddenFromThread"), firstAny(item.Data, "hiddenFromThread")))
}
func coreKindUsesExternalID(kind string) bool {
	switch kind {
	case "CommandExecution", "DynamicToolCall", "FileChange", "McpToolCall":
		return true
	}
	return false
}
func isToolSessionType(kind string) bool {
	switch kind {
	case "function_call", "custom_tool_call", "tool_search_call", "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		return true
	}
	return false
}
func anyString(values map[string]any, keys ...string) string {
	for _, k := range keys {
		if values == nil {
			continue
		}
		switch v := values[k].(type) {
		case string:
			return strings.TrimSpace(v)
		case *string:
			if v != nil {
				return strings.TrimSpace(*v)
			}
		}
	}
	return ""
}
func firstAny(values map[string]any, keys ...string) any {
	for _, k := range keys {
		if values != nil {
			if v, ok := values[k]; ok {
				return v
			}
		}
	}
	return nil
}
func firstNonNil(values ...any) any {
	for _, v := range values {
		if !isNilLike(v) {
			return v
		}
	}
	return nil
}
func isNilLike(value any) bool { return value == nil }
func copyOptional(dst map[string]any, key string, value any) {
	if !isNilLike(value) {
		dst[key] = value
	}
}
func nullableAny(values map[string]any, keys ...string) any { return firstAny(values, keys...) }
func omitEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nonEmptyAny(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func anyStringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := []string{}
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func anyStringSliceDefault(value any, fallback []string) []string {
	if out := anyStringSlice(value); out != nil {
		return out
	}
	return append([]string(nil), fallback...)
}
func splitNonEmptyLines(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	return strings.Split(value, "\n")
}
func anyInt64Default(value any, fallback int64) int64 {
	if v, ok := rolloutInt64FromAny(value); ok {
		return v
	}
	return fallback
}
func snakeEnum(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	var b strings.Builder
	for i, r := range value {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}
func camelEnum(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "_") {
		return strings.ToLower(value[:1]) + value[1:]
	}
	parts := strings.Split(value, "_")
	if len(parts) == 0 {
		return ""
	}
	out := strings.ToLower(parts[0])
	for _, p := range parts[1:] {
		if p != "" {
			out += strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return out
}
func durationFromMS(value any) (map[string]any, bool) {
	ms, ok := rolloutInt64FromAny(value)
	if !ok || ms < 0 {
		return nil, false
	}
	return map[string]any{"secs": ms / 1000, "nanos": uint32(ms%1000) * 1_000_000}, true
}
func pathURIString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "file:///"
	}
	if parsed, err := utils.Parse(value); err == nil {
		return parsed.String()
	}
	if uri, err := utils.FromHostNativePath(value); err == nil {
		return uri.String()
	}
	return value
}
func legacyPathString(value string) string {
	if parsed, err := utils.Parse(value); err == nil {
		if path, err := parsed.HostNativePath(); err == nil {
			return path
		}
	}
	return value
}
func toolLeaf(value string) string {
	if i := strings.LastIndex(value, "."); i >= 0 {
		return value[i+1:]
	}
	return value
}
func jsonValue(value any, fallback any) any {
	if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
		var decoded any
		if json.Unmarshal([]byte(s), &decoded) == nil {
			return decoded
		}
	}
	if value == nil {
		return fallback
	}
	return value
}
func commandStatus(item *session.Item, values map[string]any) string {
	if s := anyString(values, "status"); s != "" {
		return s
	}
	if strings.Contains(item.Type, "call") && !strings.Contains(item.Type, "output") {
		return "inProgress"
	}
	if b, ok := firstAny(values, "success").(bool); ok && !b {
		return "failed"
	}
	if firstAny(values, "error") != nil {
		return "failed"
	}
	return "completed"
}
func dynamicStatus(item *session.Item, values map[string]any) string {
	return commandStatus(item, values)
}
func mcpStatus(item *session.Item, values map[string]any) string { return commandStatus(item, values) }
func fileChangeStatus(item *session.Item, values map[string]any) string {
	return snakeEnum(commandStatus(item, values))
}
func coreParsedCommands(value any, command string) []any {
	entries := anyObjectSlice(value)
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		kind := snakeEnum(anyString(e, "type"))
		if kind == "" {
			kind = "unknown"
		}
		v := cloneAnyMap(e)
		v["type"] = kind
		if anyString(v, "cmd", "command") == "" {
			v["cmd"] = command
		}
		delete(v, "command")
		if path := anyString(v, "path"); path != "" {
			v["path"] = path
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"type": "unknown", "cmd": command})
	}
	return out
}
func publicFileChanges(value any) []map[string]any {
	if m := mapFromAny(value); m != nil {
		out := []map[string]any{}
		for path, raw := range m {
			entry := mapFromAny(raw)
			if entry == nil {
				entry = map[string]any{}
			}
			entry = cloneAnyMap(entry)
			entry["path"] = path
			out = append(out, entry)
		}
		return out
	}
	return anyObjectSlice(value)
}
func patchKind(value any) string {
	if m := mapFromAny(value); m != nil {
		return patchKind(firstNonNil(firstAny(m, "type"), firstPresentVariant(m)))
	}
	s := snakeEnum(fmt.Sprint(value))
	switch s {
	case "add", "delete":
		return s
	}
	return "update"
}
func firstPresentVariant(m map[string]any) any {
	for _, k := range []string{"Add", "add", "Delete", "delete", "Update", "update"} {
		if _, ok := m[k]; ok {
			return k
		}
	}
	return "update"
}
func anyObjectSlice(value any) []map[string]any {
	switch v := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), v...)
	case []any:
		out := []map[string]any{}
		for _, e := range v {
			if m := mapFromAny(e); m != nil {
				out = append(out, m)
			}
		}
		return out
	}
	data, _ := json.Marshal(value)
	var out []map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}
func camelObjectSlice(value any) []any {
	out := []any{}
	for _, m := range anyObjectSlice(value) {
		v := map[string]any{}
		for k, x := range m {
			if strings.Contains(k, "_") {
				v[camelEnum(k)] = x
			} else {
				v[k] = x
			}
		}
		out = append(out, v)
	}
	return out
}
func renameMapKey(m map[string]any, from, to string) {
	if v, ok := m[from]; ok {
		m[to] = v
		delete(m, from)
	}
}
func agentMessageText(value any) string {
	parts := []string{}
	for _, m := range anyObjectSlice(value) {
		if anyString(m, "type") == "Text" {
			parts = append(parts, rawStringFromMap(m, "text"))
		}
	}
	return strings.Join(parts, "")
}
func coreMemoryCitation(value any) any {
	m := mapFromAny(value)
	if m == nil {
		return value
	}
	out := cloneAnyMap(m)
	if v, ok := out["threadIds"]; ok {
		out["rolloutIds"] = v
		delete(out, "threadIds")
	}
	if v, ok := out["thread_ids"]; ok {
		out["rolloutIds"] = v
		delete(out, "thread_ids")
	}
	return out
}
func publicMemoryCitation(value any) any {
	m := mapFromAny(value)
	if m == nil {
		return nil
	}
	out := cloneAnyMap(m)
	if v, ok := out["rolloutIds"]; ok {
		out["threadIds"] = v
		delete(out, "rolloutIds")
	}
	return out
}
func coreWebSearchAction(value any, query string) any {
	m := mapFromAny(value)
	if m == nil {
		if query != "" {
			return map[string]any{"type": "search", "query": query, "queries": nil}
		}
		return map[string]any{"type": "other"}
	}
	out := cloneAnyMap(m)
	out["type"] = snakeEnum(anyString(m, "type"))
	return out
}
func publicWebSearchAction(value any) any {
	m := mapFromAny(value)
	if m == nil {
		return nil
	}
	out := cloneAnyMap(m)
	out["type"] = camelEnum(anyString(m, "type"))
	return out
}
func publicAgentStates(value any) any {
	states := mapFromAny(value)
	if states == nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for id, raw := range states {
		if m := mapFromAny(raw); m != nil {
			out[id] = map[string]any{"status": camelEnum(anyString(m, "status")), "message": nullableAny(m, "message")}
			continue
		}
		out[id] = map[string]any{"status": camelEnum(fmt.Sprint(raw)), "message": nil}
	}
	return out
}
func publicMCPResult(value any) any {
	m := mapFromAny(value)
	if m == nil {
		return nil
	}
	return map[string]any{"content": firstNonNil(firstAny(m, "content"), []any{}), "structuredContent": firstAny(m, "structuredContent", "structured_content"), "_meta": firstAny(m, "_meta", "meta")}
}
func errorMessage(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	if m := mapFromAny(value); m != nil {
		if s := anyString(m, "message", "error", "text"); s != "" {
			return s
		}
	}
	return fmt.Sprint(value)
}
func reviewOutputText(value any) string {
	if value == nil {
		return "No review output was provided."
	}
	if s, ok := value.(string); ok {
		return s
	}
	m := mapFromAny(value)
	if m == nil {
		return fmt.Sprint(value)
	}
	return firstNonEmptyString(anyString(m, "overall_explanation", "overallExplanation"), fmt.Sprint(value))
}
