package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ThreadEvent struct {
	Type string `json:"type"`

	ThreadID   string             `json:"thread_id,omitempty"`
	Usage      *Usage             `json:"usage,omitempty"`
	Error      *ThreadError       `json:"error,omitempty"`
	Item       *ThreadItem        `json:"item,omitempty"`
	Delta      *Delta             `json:"delta,omitempty"`
	RateLimit  *RateLimitSnapshot `json:"rateLimit,omitempty"`
	TokenUsage *ThreadTokenUsage  `json:"tokenUsage,omitempty"`
}

type ThreadTokenUsage struct {
	Total              Usage  `json:"total"`
	Last               Usage  `json:"last"`
	ModelContextWindow *int64 `json:"modelContextWindow,omitempty"`
}

type Usage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens,omitempty"`
}

func TokenUsageUpdated(usage ThreadTokenUsage) ThreadEvent {
	return ThreadEvent{Type: "thread.token_usage.updated", TokenUsage: &usage}
}

func Reconnecting(attempt, max uint64, message string) ThreadEvent {
	title := fmt.Sprintf("Reconnecting... %d/%d", attempt, max)
	return ThreadEvent{Type: "turn.reconnecting", Item: &ThreadItem{Type: "reconnecting", Message: title, Output: message}}
}

func Reconnected() ThreadEvent {
	return ThreadEvent{Type: "turn.reconnected", Item: &ThreadItem{Type: "reconnecting"}}
}

func Compacting() ThreadEvent {
	return ThreadEvent{Type: "turn.compacting", Item: &ThreadItem{Type: "compaction", Message: "Compacting context..."}}
}

func Compacted() ThreadEvent {
	return ThreadEvent{Type: "turn.compacted", Item: &ThreadItem{Type: "compaction"}}
}

type RateLimitSnapshot struct {
	LimitID              string           `json:"limitId,omitempty"`
	LimitName            string           `json:"limitName,omitempty"`
	Primary              *RateLimitWindow `json:"primary,omitempty"`
	Secondary            *RateLimitWindow `json:"secondary,omitempty"`
	Credits              *CreditsSnapshot `json:"credits,omitempty"`
	PlanType             string           `json:"planType,omitempty"`
	RateLimitReachedType string           `json:"rateLimitReachedType,omitempty"`
}

type RateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins,omitempty"`
	ResetsAt           *int64  `json:"resetsAt,omitempty"`
}

type CreditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance,omitempty"`
}

type ThreadError struct {
	Message string `json:"message"`
}

type ThreadItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	Message               string                       `json:"message,omitempty"`
	Text                  string                       `json:"text,omitempty"`
	Phase                 string                       `json:"phase,omitempty"`
	ToolName              string                       `json:"tool_name,omitempty"`
	CallID                string                       `json:"call_id,omitempty"`
	Input                 string                       `json:"input,omitempty"`
	Output                string                       `json:"output,omitempty"`
	Query                 string                       `json:"query,omitempty"`
	Action                map[string]any               `json:"action,omitempty"`
	Changes               []FileChange                 `json:"changes,omitempty"`
	Server                string                       `json:"server,omitempty"`
	Tool                  string                       `json:"tool,omitempty"`
	SenderThreadID        string                       `json:"sender_thread_id,omitempty"`
	ReceiverThreadIDs     *[]string                    `json:"receiver_thread_ids,omitempty"`
	Prompt                *string                      `json:"prompt,omitempty"`
	AgentsStates          *map[string]CollabAgentState `json:"agents_states,omitempty"`
	ActivityKind          string                       `json:"kind,omitempty"`
	AgentThreadID         string                       `json:"agent_thread_id,omitempty"`
	AgentPath             string                       `json:"agent_path,omitempty"`
	Arguments             *any                         `json:"arguments,omitempty"`
	Result                *MCPToolResult               `json:"result,omitempty"`
	CallError             *MCPToolError                `json:"error,omitempty"`
	Command               string                       `json:"command,omitempty"`
	PluginID              string                       `json:"plugin_id,omitempty"`
	ScriptPath            string                       `json:"script_path,omitempty"`
	AggregatedOutput      *string                      `json:"aggregated_output,omitempty"`
	ExitCode              *int                         `json:"exit_code,omitempty"`
	Status                string                       `json:"status,omitempty"`
	Stdout                string                       `json:"stdout,omitempty"`
	Stderr                string                       `json:"stderr,omitempty"`
	AutoApproved          *bool                        `json:"auto_approved,omitempty"`
	RevisedPrompt         string                       `json:"revised_prompt,omitempty"`
	SavedPath             string                       `json:"saved_path,omitempty"`
	TransparentBackground *bool                        `json:"transparent_background,omitempty"`
	Success               *bool                        `json:"success,omitempty"`
	Items                 []TodoItem                   `json:"items,omitempty"`
	Metadata              map[string]any               `json:"metadata,omitempty"`
}

type TodoItem struct {
	Text      string `json:"text"`
	Completed bool   `json:"completed"`
}

type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	// Diff and MovePath are retained for in-process consumers such as the TUI,
	// but the public exec/SDK event shape intentionally matches Rust and only
	// serializes path and kind.
	Diff     string `json:"-"`
	MovePath string `json:"-"`
}

type MCPToolResult struct {
	Content           []any `json:"content"`
	Meta              any   `json:"_meta,omitempty"`
	StructuredContent any   `json:"structured_content"`
}

type MCPToolError struct {
	Message string `json:"message"`
}

type CollabAgentState struct {
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

type Delta struct {
	ItemID string `json:"item_id"`
	Text   string `json:"text,omitempty"`
	Input  string `json:"input,omitempty"`
	CallID string `json:"call_id,omitempty"`
}

func ThreadStarted(threadID string) ThreadEvent {
	return ThreadEvent{
		Type:     "thread.started",
		ThreadID: threadID,
	}
}

func CommandExecutionItemWithAttribution(id string, command string, aggregatedOutput string, exitCode *int, status string, pluginID string, scriptPath string) ThreadItem {
	item := CommandExecutionItem(id, command, aggregatedOutput, exitCode, status)
	item.PluginID = pluginID
	item.ScriptPath = scriptPath
	return item
}

func TurnStarted() ThreadEvent {
	return ThreadEvent{Type: "turn.started"}
}

func AgentMessageItem(id, text string) ThreadItem {
	return AgentMessageItemWithPhase(id, text, "")
}

func AgentMessageItemWithPhase(id, text, phase string) ThreadItem {
	return ThreadItem{
		ID:    id,
		Type:  "agent_message",
		Text:  text,
		Phase: phase,
	}
}

func PlanItem(id, text string) ThreadItem {
	return ThreadItem{ID: id, Type: "plan", Text: text}
}

func PlanDelta(itemID, text string) ThreadEvent {
	return ThreadEvent{Type: "item.plan.delta", Delta: &Delta{ItemID: itemID, Text: text}}
}

// ImageGenerationFailure mirrors the Rust ImageGenerationFailure enum
// (Rust #38024): the wire shape is a tagged object with type "usageLimitExceeded".
type ImageGenerationFailure struct {
	Type     string `json:"type"`
	LimitID  string `json:"limitId"`
	ResetsAt *int64 `json:"resetsAt"`
}

func UsageLimitExceededFailure(limitID string, resetsAt *int64) ImageGenerationFailure {
	return ImageGenerationFailure{Type: "usageLimitExceeded", LimitID: limitID, ResetsAt: resetsAt}
}

func ImageGenerationItem(id string, status string, revisedPrompt string, savedPath string, transparentBackground ...*bool) ThreadItem {
	var transparent *bool
	if len(transparentBackground) > 0 {
		transparent = transparentBackground[0]
	}
	return ThreadItem{
		ID:                    id,
		Type:                  "imageGeneration",
		Status:                status,
		RevisedPrompt:         revisedPrompt,
		SavedPath:             savedPath,
		TransparentBackground: transparent,
		Metadata: map[string]any{
			"status":         status,
			"revisedPrompt":  revisedPrompt,
			"revised_prompt": revisedPrompt,
			"savedPath":      savedPath,
			"saved_path":     savedPath,
		},
	}
}

func ToolCallItem(id string, toolName string, input string) ThreadItem {
	return ThreadItem{
		ID:       id,
		Type:     "tool_call",
		ToolName: toolName,
		Input:    input,
	}
}

func ToolCallItemWithCallID(id string, callID string, toolName string, input string) ThreadItem {
	item := ToolCallItem(id, toolName, input)
	item.CallID = callID
	return item
}

func ToolOutputItem(id string, toolName string, output string, success bool) ThreadItem {
	return ToolOutputItemWithMetadata(id, toolName, output, success, nil)
}

func ToolOutputItemWithMetadata(id string, toolName string, output string, success bool, metadata map[string]any) ThreadItem {
	return ThreadItem{
		ID:       id,
		Type:     "tool_output",
		ToolName: toolName,
		Output:   output,
		Success:  &success,
		Metadata: metadata,
	}
}

func ToolOutputItemWithCallID(id string, callID string, toolName string, output string, success bool, metadata map[string]any) ThreadItem {
	item := ToolOutputItemWithMetadata(id, toolName, output, success, metadata)
	item.CallID = callID
	return item
}

func TodoListItem(id string, items []TodoItem) ThreadItem {
	copied := append([]TodoItem(nil), items...)
	return ThreadItem{
		ID:    id,
		Type:  "todo_list",
		Items: copied,
	}
}

func MCPToolCallItem(id string, server string, tool string, arguments any, result *MCPToolResult, callErr *MCPToolError, status string) ThreadItem {
	return ThreadItem{
		ID:        id,
		Type:      "mcp_tool_call",
		Server:    server,
		Tool:      tool,
		Arguments: anyValue(arguments),
		Result:    result,
		CallError: callErr,
		Status:    status,
	}
}

func CollabToolCallItem(id string, collabTool string, senderThreadID string, receiverThreadIDs []string, prompt *string, agentsStates map[string]CollabAgentState, status string) ThreadItem {
	receivers := append([]string(nil), receiverThreadIDs...)
	if receivers == nil {
		receivers = []string{}
	}
	states := cloneCollabAgentStates(agentsStates)
	if states == nil {
		states = map[string]CollabAgentState{}
	}
	return ThreadItem{
		ID:                id,
		Type:              "collab_tool_call",
		Tool:              collabTool,
		SenderThreadID:    senderThreadID,
		ReceiverThreadIDs: &receivers,
		Prompt:            cloneStringPointer(prompt),
		AgentsStates:      &states,
		Status:            status,
	}
}

func SubAgentActivityItem(id string, kind string, agentThreadID string, agentPath string) ThreadItem {
	return ThreadItem{
		ID:            id,
		Type:          "sub_agent_activity",
		ActivityKind:  kind,
		AgentThreadID: agentThreadID,
		AgentPath:     agentPath,
	}
}

func CommandExecutionItem(id string, command string, aggregatedOutput string, exitCode *int, status string) ThreadItem {
	return ThreadItem{
		ID:               id,
		Type:             "command_execution",
		Command:          command,
		AggregatedOutput: stringValue(aggregatedOutput),
		ExitCode:         cloneIntPointer(exitCode),
		Status:           status,
	}
}

func ErrorItem(id string, message string) ThreadItem {
	return ThreadItem{
		ID:      id,
		Type:    "error",
		Message: message,
	}
}

func FileChangeItem(id string, changes []FileChange, status string) ThreadItem {
	copied := append([]FileChange(nil), changes...)
	return ThreadItem{
		ID:      id,
		Type:    "file_change",
		Changes: copied,
		Status:  status,
	}
}

func FileChangeItemWithOutput(id string, changes []FileChange, status string, stdout string, stderr string) ThreadItem {
	item := FileChangeItem(id, changes, status)
	item.Stdout = stdout
	item.Stderr = stderr
	return item
}

func WebSearchItem(id string, query string, action map[string]any) ThreadItem {
	if action == nil {
		action = map[string]any{"type": "other"}
	}
	return ThreadItem{
		ID:     id,
		Type:   "web_search",
		Query:  query,
		Action: cloneAnyMap(action),
	}
}

func (i ThreadItem) MarshalJSON() ([]byte, error) {
	switch i.Type {
	case "mcp_tool_call":
		var arguments any
		if i.Arguments != nil {
			arguments = *i.Arguments
		}
		return marshalThreadItemJSON(struct {
			ID        string         `json:"id"`
			Type      string         `json:"type"`
			Server    string         `json:"server"`
			Tool      string         `json:"tool"`
			Arguments any            `json:"arguments"`
			Result    *MCPToolResult `json:"result"`
			Error     *MCPToolError  `json:"error"`
			Status    string         `json:"status"`
		}{
			ID:        i.ID,
			Type:      i.Type,
			Server:    i.Server,
			Tool:      i.Tool,
			Arguments: arguments,
			Result:    i.Result,
			Error:     i.CallError,
			Status:    i.Status,
		})
	case "collab_tool_call":
		receivers := []string{}
		if i.ReceiverThreadIDs != nil {
			receivers = append([]string(nil), (*i.ReceiverThreadIDs)...)
		}
		if receivers == nil {
			receivers = []string{}
		}
		states := map[string]CollabAgentState{}
		if i.AgentsStates != nil {
			states = cloneCollabAgentStates(*i.AgentsStates)
		}
		return marshalThreadItemJSON(struct {
			ID                string                      `json:"id"`
			Type              string                      `json:"type"`
			Tool              string                      `json:"tool"`
			SenderThreadID    string                      `json:"sender_thread_id"`
			ReceiverThreadIDs []string                    `json:"receiver_thread_ids"`
			Prompt            *string                     `json:"prompt"`
			AgentsStates      map[string]CollabAgentState `json:"agents_states"`
			Status            string                      `json:"status"`
		}{
			ID:                i.ID,
			Type:              i.Type,
			Tool:              i.Tool,
			SenderThreadID:    i.SenderThreadID,
			ReceiverThreadIDs: receivers,
			Prompt:            cloneStringPointer(i.Prompt),
			AgentsStates:      states,
			Status:            i.Status,
		})
	default:
		type threadItemAlias ThreadItem
		return marshalThreadItemJSON(threadItemAlias(i))
	}
}

func marshalThreadItemJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func stringValue(value string) *string {
	return &value
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func anyValue(value any) *any {
	return &value
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneCollabAgentStates(values map[string]CollabAgentState) map[string]CollabAgentState {
	if values == nil {
		return nil
	}
	out := make(map[string]CollabAgentState, len(values))
	for key, value := range values {
		out[key] = CollabAgentState{
			Status:  value.Status,
			Message: cloneStringPointer(value.Message),
		}
	}
	return out
}

func ItemStarted(item ThreadItem) ThreadEvent {
	return ThreadEvent{
		Type: "item.started",
		Item: &item,
	}
}

func ItemUpdated(item ThreadItem) ThreadEvent {
	return ThreadEvent{
		Type: "item.updated",
		Item: &item,
	}
}

func ItemCompleted(item ThreadItem) ThreadEvent {
	return ThreadEvent{
		Type: "item.completed",
		Item: &item,
	}
}

func AgentMessageDelta(itemID string, text string) ThreadEvent {
	return ThreadEvent{
		Type:  "item.delta",
		Delta: &Delta{ItemID: itemID, Text: text},
	}
}

func ToolCallInputDelta(itemID string, callID string, input string) ThreadEvent {
	return ThreadEvent{
		Type:  "item.delta",
		Delta: &Delta{ItemID: itemID, CallID: callID, Input: input},
	}
}

func TurnCompleted(usage Usage) ThreadEvent {
	return ThreadEvent{
		Type:  "turn.completed",
		Usage: &usage,
	}
}

func TurnFailed(message string) ThreadEvent {
	return ThreadEvent{
		Type:  "turn.failed",
		Error: &ThreadError{Message: message},
	}
}

func ErrorEvent(message string) ThreadEvent {
	return ThreadEvent{
		Type:  "error",
		Error: &ThreadError{Message: message},
	}
}

func RateLimitSnapshotEvent(snapshot RateLimitSnapshot) ThreadEvent {
	return ThreadEvent{
		Type:      "response.rate_limits",
		RateLimit: &snapshot,
	}
}
