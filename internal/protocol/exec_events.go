package protocol

type ThreadEvent struct {
	Type string `json:"type"`

	ThreadID string       `json:"thread_id,omitempty"`
	Usage    *Usage       `json:"usage,omitempty"`
	Error    *ThreadError `json:"error,omitempty"`
	Item     *ThreadItem  `json:"item,omitempty"`
	Delta    *Delta       `json:"delta,omitempty"`
}

type Usage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

type ThreadError struct {
	Message string `json:"message"`
}

type ThreadItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	Text     string         `json:"text,omitempty"`
	ToolName string         `json:"tool_name,omitempty"`
	Input    string         `json:"input,omitempty"`
	Output   string         `json:"output,omitempty"`
	Success  *bool          `json:"success,omitempty"`
	Items    []TodoItem     `json:"items,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type TodoItem struct {
	Text      string `json:"text"`
	Completed bool   `json:"completed"`
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

func TurnStarted() ThreadEvent {
	return ThreadEvent{Type: "turn.started"}
}

func AgentMessageItem(id, text string) ThreadItem {
	return ThreadItem{
		ID:   id,
		Type: "agent_message",
		Text: text,
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

func TodoListItem(id string, items []TodoItem) ThreadItem {
	copied := append([]TodoItem(nil), items...)
	return ThreadItem{
		ID:    id,
		Type:  "todo_list",
		Items: copied,
	}
}

func ItemStarted(item ThreadItem) ThreadEvent {
	return ThreadEvent{
		Type: "item.started",
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
