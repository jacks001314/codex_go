package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"codex_go/appserver"
	"codex_go/turn"
)

func validateDynamicPrompt(prompt string, maxBytes int) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("prompt must not be empty")
	}
	if len(prompt) > maxBytes {
		return errors.New("prompt exceeded the maximum context budget")
	}
	return nil
}

// dynamicDelegatedPrompt wraps a delegated prompt so the child task can see
// which task sent it (Rust delegated_prompt).
func dynamicDelegatedPrompt(sourceThreadID string, prompt string) string {
	escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return "<codex_delegation>\n  <source_thread_id>" + escape.Replace(sourceThreadID) +
		"</source_thread_id>\n  <input>" + escape.Replace(prompt) + "</input>\n</codex_delegation>"
}

// dynamicParseDelegatedPrompt extracts the source thread and input from a
// delegation wrapper (Rust parse_delegated_prompt).
func dynamicParseDelegatedPrompt(prompt string) (string, string, bool) {
	const prefix = "<codex_delegation>\n  <source_thread_id>"
	if !strings.HasPrefix(prompt, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(prompt, prefix)
	index := strings.Index(rest, "</source_thread_id>\n  <input>")
	if index < 0 {
		return "", "", false
	}
	source := rest[:index]
	delegated := rest[index+len("</source_thread_id>\n  <input>"):]
	if !strings.HasSuffix(delegated, "</input>\n</codex_delegation>") {
		return "", "", false
	}
	delegated = strings.TrimSuffix(delegated, "</input>\n</codex_delegation>")
	unescape := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")
	return unescape.Replace(source), unescape.Replace(delegated), true
}

// dynamicParseDelegatedToolOutput recognizes a delegation tool output so the
// read tool can surface who delegated the prompt (Rust
// parse_delegated_tool_output).
func dynamicParseDelegatedToolOutput(name string, namespace string, output string) (string, string, bool) {
	if namespace != DynamicToolNamespace && namespace != "codex_app" {
		return "", "", false
	}
	if name != "create_thread" && name != "send_message_to_thread" {
		return "", "", false
	}
	return dynamicParseDelegatedPrompt(output)
}

func dynamicSameThreadID(first string, second string) bool {
	return dynamicCanonicalThreadID(first) != "" &&
		dynamicCanonicalThreadID(first) == dynamicCanonicalThreadID(second)
}

// dynamicCanonicalThreadID is Rust ThreadId::from_string's canonical form when
// the id parses, else the trimmed input.
func dynamicCanonicalThreadID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(trimmed)
}

// truncateDynamicText mirrors Rust dynamic_tools::truncate (char bounded, with
// an ellipsis when shortened).
func truncateDynamicText(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "\u2026"
}

// dynamicOutputSummary mirrors Rust output_summary.
func dynamicOutputSummary(text string, limit int) map[string]any {
	total := utf8.RuneCountInString(text)
	if total <= limit {
		return map[string]any{"text": text, "truncated": false}
	}
	runes := []rune(text)
	return map[string]any{
		"text":          string(runes[:limit]),
		"truncated":     true,
		"originalChars": total,
	}
}

func dynamicStringPointerValue(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return strings.TrimSpace(*value)
}

func dynamicJSONLength(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
}

func dynamicThreadStatusLabel(status appserver.ThreadStatus) string {
	switch strings.ToLower(strings.TrimSpace(status.Type)) {
	case "idle":
		return "idle"
	case "active":
		return "active"
	case "systemerror":
		return "systemError"
	default:
		return "notLoaded"
	}
}

func dynamicThreadIsActive(status appserver.ThreadStatus) bool {
	return strings.EqualFold(strings.TrimSpace(status.Type), "active")
}

// dynamicThreadSummary mirrors Rust thread_summary.
func dynamicThreadSummary(thread *appserver.Thread) map[string]any {
	if thread == nil {
		return map[string]any{}
	}
	title := dynamicStringPointerValue(thread.Name)
	if title != nil {
		title = truncateDynamicText(title.(string), dynamicDefaultOutputChars)
	}
	return map[string]any{
		"id":        thread.ID,
		"kind":      "codex",
		"projectId": dynamicStringPointerValue(thread.ProjectID),
		"title":     title,
		"summary":   truncateDynamicText(thread.Preview, 300),
		"status":    dynamicThreadStatusLabel(thread.Status),
		"cwd":       thread.CWD,
		"updatedAt": thread.UpdatedAt,
	}
}

// dynamicTurnSummary mirrors Rust turn_summary.
func dynamicTurnSummary(turnRecord *appserver.Turn, includeOutputs bool, outputChars int) map[string]any {
	if turnRecord == nil {
		return map[string]any{}
	}
	items := make([]any, 0, len(turnRecord.Items))
	for index := len(turnRecord.Items) - 1; index >= 0; index-- {
		items = append(items, dynamicItemSummary(turnRecord.Items[index], includeOutputs, outputChars))
	}
	var errorValue any
	if turnRecord.Error != nil {
		errorValue = map[string]any{
			"message":           turnRecord.Error.Message,
			"additionalDetails": dynamicStringPointerValue(turnRecord.Error.AdditionalDetails),
		}
	}
	return map[string]any{
		"id":          turnRecord.ID,
		"status":      string(turnRecord.Status),
		"error":       errorValue,
		"startedAt":   turnRecord.StartedAt,
		"completedAt": turnRecord.CompletedAt,
		"durationMs":  turnRecord.DurationMS,
		"items":       items,
	}
}

func dynamicItemSummary(item appserver.ThreadItem, includeOutputs bool, outputChars int) map[string]any {
	itemType := remoteTUINormalizedThreadItemType(item.Type)
	switch itemType {
	case "usermessage":
		return map[string]any{
			"type":    "userMessage",
			"id":      item.ID,
			"content": dynamicUserContentSummaries(item),
		}
	case "functioncalloutput":
		summary := map[string]any{
			"type":      "functionCallOutput",
			"id":        item.ID,
			"name":      item.Name,
			"namespace": dynamicStringPointerValue(stringPointerOrNil(item.Namespace)),
		}
		if source, delegated, ok := dynamicParseDelegatedToolOutput(item.Name, item.Namespace, dynamicItemOutputText(item)); ok {
			summary["codexDelegation"] = map[string]any{
				"sourceThreadId": source,
				"input":          truncateDynamicText(delegated, dynamicDefaultOutputChars),
			}
		}
		if includeOutputs {
			summary["output"] = dynamicOutputSummary(dynamicItemOutputText(item), outputChars)
		}
		return summary
	case "agentmessage":
		return map[string]any{
			"type": "agentMessage",
			"id":   item.ID,
			"text": truncateDynamicText(item.Text, dynamicDefaultOutputChars),
		}
	case "plan":
		return map[string]any{
			"type": "plan",
			"id":   item.ID,
			"text": truncateDynamicText(item.Text, dynamicDefaultOutputChars),
		}
	case "reasoning":
		summary := map[string]any{
			"type":    "reasoning",
			"id":      item.ID,
			"summary": dynamicStringListTruncated(remoteTUIAnyStrings(item.Data["summary"]), dynamicDefaultOutputChars),
		}
		if includeOutputs {
			content := remoteTUIAnyStrings(item.Data["content"])
			rendered := make([]any, 0, len(content))
			for _, text := range content {
				rendered = append(rendered, dynamicOutputSummary(text, outputChars))
			}
			summary["content"] = rendered
		}
		return summary
	case "commandexecution":
		summary := map[string]any{
			"type":       "commandExecution",
			"id":         item.ID,
			"command":    truncateDynamicText(dynamicItemDataString(item, "command"), dynamicDefaultOutputChars),
			"cwd":        dynamicItemDataString(item, "cwd"),
			"exitCode":   dynamicItemDataAny(item, "exitCode"),
			"status":     item.Status,
			"durationMs": dynamicItemDataAny(item, "durationMs"),
		}
		if includeOutputs {
			if output := dynamicItemDataString(item, "aggregatedOutput"); output != "" {
				summary["output"] = dynamicOutputSummary(output, outputChars)
			}
		}
		return summary
	case "filechange":
		return map[string]any{
			"type":    "fileChange",
			"id":      item.ID,
			"status":  item.Status,
			"changes": dynamicItemDataAny(item, "changes"),
		}
	default:
		return map[string]any{
			"type":   item.Type,
			"id":     item.ID,
			"name":   firstNonEmptyLocal(item.Name, item.Type),
			"status": item.Status,
		}
	}
}

func dynamicUserContentSummaries(item appserver.ThreadItem) []any {
	contents := make([]any, 0, len(item.Content))
	for _, content := range item.Content {
		switch strings.ToLower(strings.TrimSpace(content.Type)) {
		case "input_text", "text":
			summary := map[string]any{"type": "text", "text": truncateDynamicText(content.Text, dynamicDefaultOutputChars)}
			if source, delegated, ok := dynamicParseDelegatedPrompt(content.Text); ok {
				summary["codexDelegation"] = map[string]any{
					"sourceThreadId": source,
					"input":          truncateDynamicText(delegated, dynamicDefaultOutputChars),
				}
			}
			contents = append(contents, summary)
		case "input_image", "image":
			contents = append(contents, map[string]any{"type": "image", "url": content.ImageURL})
		case "local_image":
			contents = append(contents, map[string]any{"type": "localImage", "path": content.ImageURL})
		case "input_audio", "audio":
			contents = append(contents, map[string]any{"type": "audio", "url": content.AudioURL})
		}
	}
	if len(contents) == 0 && strings.TrimSpace(item.Text) != "" {
		contents = append(contents, map[string]any{"type": "text", "text": truncateDynamicText(item.Text, dynamicDefaultOutputChars)})
	}
	return contents
}

func dynamicStringListTruncated(values []string, limit int) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, truncateDynamicText(value, limit))
	}
	return out
}

func dynamicItemOutputText(item appserver.ThreadItem) string {
	if text := strings.TrimSpace(item.Text); text != "" {
		return text
	}
	return remoteTUIThreadItemToolText(item)
}

func dynamicItemDataString(item appserver.ThreadItem, key string) string {
	return remoteTUIAnyString(item.Data[key])
}

func dynamicItemDataAny(item appserver.ThreadItem, key string) any {
	return item.Data[key]
}

func stringPointerOrNil(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// ---- response shaping ----

func dynamicToolFailureResponse(message string) appserver.DynamicToolCallResponse {
	return appserver.DynamicToolCallResponse{
		Success: false,
		ContentItems: []appserver.DynamicToolCallOutputContent{{
			Type: "inputText",
			Text: truncateDynamicText(message, dynamicMaxResponseBytes/4-1),
		}},
	}
}

// dynamicToolSuccessResponse serializes the value, shrinking it to fit the
// response budget (Rust success_response).
func dynamicToolSuccessResponse(value map[string]any) appserver.DynamicToolCallResponse {
	var serialized any = value
	maxChars := dynamicMaxResponseBytes / 2
	for {
		text, err := json.Marshal(serialized)
		if err != nil {
			return dynamicToolFailureResponse(err.Error())
		}
		if len(text) <= dynamicMaxResponseBytes {
			return appserver.DynamicToolCallResponse{
				Success:      true,
				ContentItems: []appserver.DynamicToolCallOutputContent{{Type: "inputText", Text: string(text)}},
			}
		}
		if maxChars == 0 {
			if dynamicDropOneSummaryItem(serialized) {
				continue
			}
			return dynamicToolFailureResponse("Dynamic tool response exceeded the maximum context budget")
		}
		maxChars /= 2
		dynamicTruncateResponse(serialized, maxChars)
		if fields, ok := serialized.(map[string]any); ok {
			fields["truncated"] = true
		}
	}
}

// dynamicDropOneSummaryItem removes the least important entry so the response
// can still fit (Rust success_response's fallback).
func dynamicDropOneSummaryItem(value any) bool {
	fields, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if turns, ok := fields["turns"].([]any); ok {
		for index := len(turns) - 1; index >= 0; index-- {
			turnFields, ok := turns[index].(map[string]any)
			if !ok {
				continue
			}
			items, ok := turnFields["items"].([]any)
			if !ok || len(items) == 0 {
				continue
			}
			turnFields["items"] = items[1:]
			return true
		}
	}
	if threads, ok := fields["threads"].([]any); ok && len(threads) > 1 {
		fields["threads"] = threads[:len(threads)-1]
		return true
	}
	if polls, ok := fields["polls"].([]any); ok {
		for index := len(polls) - 1; index >= 0; index-- {
			pollFields, ok := polls[index].(map[string]any)
			if !ok {
				continue
			}
			for _, name := range []string{"latestAssistantMessage", "latestToolMarker", "latestTurn", "latestAssistantMessageId", "latestToolMarkerId", "revision", "schemaVersion", "changed", "cursor"} {
				if _, present := pollFields[name]; present {
					delete(pollFields, name)
					return true
				}
			}
		}
	}
	return false
}

func dynamicTruncateResponse(value any, limit int) {
	switch typed := value.(type) {
	case string:
		// Strings are only replaced in place through their parent object.
	case []any:
		for _, item := range typed {
			dynamicTruncateResponse(item, limit)
		}
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			if _, hasFlag := typed["truncated"].(bool); hasFlag && utf8.RuneCountInString(text) > limit {
				typed["truncated"] = true
				if _, exists := typed["originalChars"]; !exists {
					typed["originalChars"] = utf8.RuneCountInString(text)
				}
			}
		}
		for name, item := range typed {
			if dynamicTruncateResponseSkipKey(name) {
				continue
			}
			if text, ok := item.(string); ok {
				typed[name] = truncateDynamicText(text, limit)
				continue
			}
			dynamicTruncateResponse(item, limit)
		}
	}
}

func dynamicTruncateResponseSkipKey(name string) bool {
	switch name {
	case "id", "cursor", "type", "status", "kind", "reason", "namespace", "tool", "server":
		return true
	}
	return strings.HasSuffix(name, "Id") || strings.HasSuffix(name, "Ids") ||
		strings.HasSuffix(name, "Cursor") || strings.HasSuffix(name, "Status")
}

// ---- app-server access ----

func (e *dynamicToolExecutor) readThread(threadID string) (*appserver.Thread, error) {
	var response appserver.ThreadReadResponse
	if err := e.request(appserver.MethodThreadRead, appserver.ThreadReadParams{
		ThreadID:     strings.TrimSpace(threadID),
		IncludeTurns: false,
	}, &response); err != nil {
		return nil, err
	}
	if response.Thread == nil || strings.TrimSpace(response.Thread.ID) == "" {
		return nil, errors.New("thread/read returned no thread")
	}
	return response.Thread, nil
}

// resumeThread loads a thread under the calling thread's config, with the
// history-pagination fallback (Rust request_with_history_fallback).
func (e *dynamicToolExecutor) resumeThread(threadID string, excludeTurns bool, config map[string]any) (*appserver.ThreadResumeResponse, error) {
	build := func(exclude bool) appserver.ThreadResumeParams {
		return appserver.ThreadResumeParams{
			ThreadID:     strings.TrimSpace(threadID),
			ExcludeTurns: exclude,
			Config:       config,
		}
	}
	var response appserver.ThreadResumeResponse
	if err := e.request(appserver.MethodThreadResume, build(excludeTurns), &response); err != nil {
		if !excludeTurns || !isHistoryPaginationUnsupportedError(err) {
			return nil, err
		}
		response = appserver.ThreadResumeResponse{}
		if err := e.request(appserver.MethodThreadResume, build(false), &response); err != nil {
			return nil, err
		}
	}
	return &response, nil
}

// startTurn submits a tool-output turn that delegates a prompt (Rust start_turn).
func (e *dynamicToolExecutor) startTurn(threadID string, tool string, prompt string, model *string, sandboxPolicy any) error {
	params := turn.TurnStartParams{
		ThreadID: strings.TrimSpace(threadID),
		ToolOutput: &turn.TurnToolOutput{
			Name:      tool,
			Namespace: DynamicToolNamespace,
			Output:    prompt,
		},
		SandboxPolicy: sandboxPolicy,
	}
	if model != nil {
		params.Model = strings.TrimSpace(*model)
	}
	var response turn.TurnStartResponse
	return e.request(appserver.MethodTurnStart, params, &response)
}

func (e *dynamicToolExecutor) registerBackgroundThread(threadID string, taskToolsAvailable bool) error {
	if e.options.RegisterBackgroundThread == nil {
		return nil
	}
	if err := e.options.RegisterBackgroundThread(strings.TrimSpace(threadID), taskToolsAvailable); err != nil {
		return fmt.Errorf("Failed to register background task: %v", err)
	}
	return nil
}

// threadTurnsPage returns up to limit turns newest-first, with the legacy
// thread/read fallback (Rust thread/turns/list handling).
func (e *dynamicToolExecutor) threadTurnsPage(threadID string, cursor *string, limit int) ([]appserver.Turn, *string, error) {
	pageLimit := limit
	params := appserver.ThreadTurnsListParams{
		ThreadID:      strings.TrimSpace(threadID),
		Cursor:        cursor,
		Limit:         &pageLimit,
		SortDirection: appserver.SortDesc,
		ItemsView:     appserver.TurnItemsFull,
	}
	var page appserver.TurnsPage
	if err := e.request(appserver.MethodThreadTurnsList, params, &page); err != nil {
		if !isHistoryPaginationUnsupportedError(err) {
			return nil, nil, err
		}
		return e.threadTurnsPageFromRead(threadID, cursor, limit)
	}
	return page.Data, page.NextCursor, nil
}

func (e *dynamicToolExecutor) threadTurnsPageFromRead(threadID string, cursor *string, limit int) ([]appserver.Turn, *string, error) {
	var response appserver.ThreadReadResponse
	if err := e.request(appserver.MethodThreadRead, appserver.ThreadReadParams{
		ThreadID:     strings.TrimSpace(threadID),
		IncludeTurns: true,
	}, &response); err != nil {
		return nil, nil, err
	}
	if response.Thread == nil {
		return nil, nil, errors.New("thread/read returned no thread")
	}
	turns := response.Thread.Turns
	end := len(turns)
	if cursor != nil && strings.TrimSpace(*cursor) != "" {
		found := -1
		for index := range turns {
			if turns[index].ID == strings.TrimSpace(*cursor) {
				found = index
				break
			}
		}
		if found < 0 {
			return nil, nil, fmt.Errorf("Unknown cursor: %s", strings.TrimSpace(*cursor))
		}
		end = found
	}
	selected := make([]appserver.Turn, 0, limit)
	for index := end - 1; index >= 0 && len(selected) < limit; index-- {
		selected = append(selected, turns[index])
	}
	var nextCursor *string
	if end > len(selected) && len(selected) > 0 {
		value := selected[len(selected)-1].ID
		nextCursor = &value
	}
	return selected, nextCursor, nil
}

// latestTurnAndItems returns the newest turn and its newest items for the wait
// snapshot (Rust wait_threads' per-target read).
func (e *dynamicToolExecutor) latestTurnAndItems(threadID string) (*appserver.Turn, []appserver.ThreadItem, error) {
	turns, _, err := e.threadTurnsPage(threadID, nil, 1)
	if err != nil {
		return nil, nil, err
	}
	if len(turns) == 0 {
		return nil, nil, nil
	}
	latest := turns[0]
	limit := 20
	params := appserver.ThreadItemsListParams{
		ThreadID:      strings.TrimSpace(threadID),
		TurnID:        &latest.ID,
		Limit:         &limit,
		SortDirection: appserver.SortDesc,
	}
	var page appserver.ThreadItemsListResponse
	if err := e.request(appserver.MethodThreadItemsList, params, &page); err == nil {
		items := make([]appserver.ThreadItem, 0, len(page.Data))
		for _, entry := range page.Data {
			items = append(items, entry.Item)
		}
		return &latest, items, nil
	}
	items := make([]appserver.ThreadItem, 0, len(latest.Items))
	for index := len(latest.Items) - 1; index >= 0 && len(items) < limit; index-- {
		items = append(items, latest.Items[index])
	}
	return &latest, items, nil
}

func isHistoryPaginationUnsupportedError(err error) bool {
	var rpcErr *remoteRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	if rpcErr.Code == -32601 {
		return true
	}
	if rpcErr.Code != -32600 && rpcErr.Code != -32602 {
		return false
	}
	message := strings.ToLower(rpcErr.Message)
	for _, needle := range []string{
		"historymode", "history mode", "excludeturns", "exclude turns",
		"thread/turns/list", "thread/items/list",
	} {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

// dynamicSandboxFromResume maps a resumed sandbox policy to the start mode and
// the turn policy (Rust create_thread's sandbox inheritance).
func dynamicSandboxFromResume(resumed *appserver.ThreadResumeResponse) (any, any, error) {
	if resumed == nil {
		return nil, nil, nil
	}
	policyType := ""
	if fields, ok := resumed.Sandbox.(map[string]any); ok {
		policyType = remoteTUIAnyString(fields["type"])
	} else if text, ok := resumed.Sandbox.(string); ok {
		policyType = text
	}
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(policyType), "_", "")) {
	case "dangerfullaccess":
		return "danger-full-access", resumed.Sandbox, nil
	case "readonly":
		return "read-only", resumed.Sandbox, nil
	case "workspacewrite":
		return "workspace-write", resumed.Sandbox, nil
	case "externalsandbox":
		return nil, nil, errors.New("Cannot inherit an external sandbox without a permission profile")
	default:
		return nil, resumed.Sandbox, nil
	}
}

// ---- wait_threads snapshot shaping ----

func dynamicWaitCursor(thread *appserver.Thread, latestTurn *appserver.Turn, latestItems []appserver.ThreadItem) string {
	payload := map[string]any{
		"updatedAt": thread.UpdatedAt,
		"status":    dynamicThreadStatusLabel(thread.Status),
	}
	if latestTurn != nil {
		payload["turnId"] = latestTurn.ID
		payload["turnStatus"] = string(latestTurn.Status)
	} else {
		payload["turnId"] = nil
		payload["turnStatus"] = nil
	}
	if len(latestItems) > 0 {
		payload["latestItemId"] = latestItems[0].ID
	} else {
		payload["latestItemId"] = nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func dynamicWaitWake(thread *appserver.Thread, latestTurn *appserver.Turn, changed bool) map[string]any {
	status := dynamicThreadStatusLabel(thread.Status)
	switch status {
	case "idle":
		if latestTurn != nil {
			if changed && latestTurn.Status != appserver.TurnStatusInProgress {
				return map[string]any{"threadId": thread.ID, "reason": "turnCompleted", "turnId": latestTurn.ID}
			}
			return nil
		}
		return map[string]any{"threadId": thread.ID, "reason": "inactiveStatus"}
	case "notLoaded", "systemError":
		return map[string]any{"threadId": thread.ID, "reason": "inactiveStatus"}
	case "active":
		if len(thread.Status.ActiveFlags) > 0 {
			return map[string]any{"threadId": thread.ID, "reason": "actionableStatus"}
		}
	}
	return nil
}

func dynamicWaitPoll(thread *appserver.Thread, latestTurn *appserver.Turn, latestItems []appserver.ThreadItem, cursor string, changed bool) map[string]any {
	poll := map[string]any{
		"schemaVersion": 1,
		"thread":        map[string]any{"id": thread.ID, "status": dynamicThreadStatusLabel(thread.Status)},
		"cursor":        cursor,
		"revision":      thread.UpdatedAt,
		"changed":       changed,
		"latestTurn":    nil,
	}
	if latestTurn != nil {
		var errorValue any
		if latestTurn.Error != nil {
			errorValue = map[string]any{"message": latestTurn.Error.Message}
		}
		poll["latestTurn"] = map[string]any{
			"id":          latestTurn.ID,
			"status":      string(latestTurn.Status),
			"error":       errorValue,
			"startedAt":   latestTurn.StartedAt,
			"completedAt": latestTurn.CompletedAt,
			"durationMs":  latestTurn.DurationMS,
		}
	}
	assistantID, assistant := dynamicLatestAssistantMessage(latestTurn)
	poll["latestAssistantMessageId"] = assistantID
	poll["latestAssistantMessage"] = nil
	if changed {
		poll["latestAssistantMessage"] = assistant
	}
	markerID, marker := dynamicLatestToolMarker(latestTurn, latestItems)
	poll["latestToolMarkerId"] = markerID
	poll["latestToolMarker"] = nil
	if changed {
		poll["latestToolMarker"] = marker
	}
	return poll
}

func dynamicLatestAssistantMessage(turnRecord *appserver.Turn) (any, any) {
	if turnRecord == nil {
		return nil, nil
	}
	for index := len(turnRecord.Items) - 1; index >= 0; index-- {
		item := turnRecord.Items[index]
		if remoteTUINormalizedThreadItemType(item.Type) != "agentmessage" {
			continue
		}
		return item.ID, map[string]any{
			"id":     item.ID,
			"turnId": turnRecord.ID,
			"phase":  dynamicStringPointerValue(stringPointerOrNil(remoteTUIAnyString(item.Data["phase"]))),
			"text":   truncateDynamicText(item.Text, dynamicDefaultOutputChars),
		}
	}
	return nil, nil
}

func dynamicLatestToolMarker(turnRecord *appserver.Turn, items []appserver.ThreadItem) (any, any) {
	if turnRecord == nil {
		return nil, nil
	}
	for _, item := range items {
		itemType := remoteTUINormalizedThreadItemType(item.Type)
		name := item.Name
		status := any(item.Status)
		switch itemType {
		case "commandexecution":
			return item.ID, map[string]any{"id": item.ID, "turnId": turnRecord.ID, "type": "commandExecution", "name": "commandExecution", "status": status}
		case "filechange":
			return item.ID, map[string]any{"id": item.ID, "turnId": turnRecord.ID, "type": "fileChange", "name": "fileChange", "status": status}
		case "mcptoolcall":
			return item.ID, map[string]any{"id": item.ID, "turnId": turnRecord.ID, "type": "mcpToolCall", "name": name, "status": status}
		case "dynamictoolcall":
			return item.ID, map[string]any{"id": item.ID, "turnId": turnRecord.ID, "type": "dynamicToolCall", "name": name, "status": status}
		case "collabagenttoolcall":
			return item.ID, map[string]any{"id": item.ID, "turnId": turnRecord.ID, "type": "collabAgentToolCall", "name": name, "status": status}
		case "websearch":
			return item.ID, map[string]any{"id": item.ID, "turnId": turnRecord.ID, "type": "webSearch", "name": "webSearch", "status": nil}
		case "sleep":
			return item.ID, map[string]any{"id": item.ID, "turnId": turnRecord.ID, "type": "sleep", "name": "sleep", "status": nil}
		}
	}
	return nil, nil
}
