package model

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const (
	MaxExecutedToolCallArgumentBytes = 8 * 1024
	MaxExecutedToolCallMetadataBytes = 32 * 1024

	internalChatMessageMetadataPassthroughField = "internal_chat_message_metadata_passthrough"
	executedToolCallsField                      = "executed_tool_calls"
	executedToolCallTruncatedField              = "_codex_executed_tool_call_truncated"
	executedToolCallRawField                    = "_codex_executed_tool_call_raw"
)

type ExecutedToolCall struct {
	Name string `json:"name"`

	arguments  any
	truncation *ExecutedToolCallTruncation
}

type ExecutedToolCallTruncation struct {
	OriginalBytes     int  `json:"original_bytes"`
	MaxBytes          int  `json:"max_bytes"`
	OmittedCalls      *int `json:"omitted_calls,omitempty"`
	OriginalNameBytes *int `json:"original_name_bytes,omitempty"`
}

// ExecutedToolCallCarrier is implemented by locally trusted prompt items that
// can carry attempted-tool metadata. Implementations must return an independent
// clone so request bounding never mutates conversation history.
type ExecutedToolCallCarrier interface {
	ExecutedToolCalls() []ExecutedToolCall
	ReplaceExecutedToolCalls([]ExecutedToolCall)
	CloneForExecutedToolCallPrompt() ExecutedToolCallCarrier
}

func NewExecutedToolCall(name string, arguments any) ExecutedToolCall {
	if object, ok := arguments.(map[string]any); ok {
		if _, forged := object[executedToolCallTruncatedField]; forged {
			arguments = map[string]any{executedToolCallRawField: arguments}
		}
	}
	return ExecutedToolCall{Name: name, arguments: arguments}
}

func NewTruncatedExecutedToolCall(name string, originalBytes int, maxBytes int) ExecutedToolCall {
	call := NewExecutedToolCall(name, nil)
	setExecutedToolCallTruncation(&call, originalBytes, maxBytes, nil, nil)
	return call
}

func (c ExecutedToolCall) MarshalJSON() ([]byte, error) {
	arguments := c.arguments
	if c.truncation != nil {
		arguments = map[string]any{executedToolCallTruncatedField: c.truncation}
	}
	return json.Marshal(struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	}{Name: c.Name, Arguments: arguments})
}

func (i *AgentItem) AppendExecutedToolCalls(calls ...ExecutedToolCall) {
	if i == nil || len(calls) == 0 {
		return
	}
	i.executedToolCalls = append(i.executedToolCalls, calls...)
}

func (i *AgentItem) ClearExecutedToolCalls() {
	if i != nil {
		i.executedToolCalls = nil
	}
}

func (i *AgentItem) ExecutedToolCalls() []ExecutedToolCall {
	if i == nil {
		return nil
	}
	return append([]ExecutedToolCall(nil), i.executedToolCalls...)
}

func (i *AgentItem) ReplaceExecutedToolCalls(calls []ExecutedToolCall) {
	if i != nil {
		i.executedToolCalls = append([]ExecutedToolCall(nil), calls...)
	}
}

func (i *AgentItem) CloneForExecutedToolCallPrompt() ExecutedToolCallCarrier {
	if i == nil {
		return (*AgentItem)(nil)
	}
	clone := *i
	clone.Data = cloneAgentItemMap(i.Data)
	clone.Search = cloneAgentItemMap(i.Search)
	clone.executedToolCalls = append([]ExecutedToolCall(nil), i.executedToolCalls...)
	return &clone
}

func RecordExecutedToolCall(item *AgentItem) {
	if item == nil || len(item.executedToolCalls) > 0 {
		return
	}
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return
	}
	var arguments any
	switch item.Type {
	case "function_call":
		if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
			arguments = item.Arguments
		}
	case "custom_tool_call":
		arguments = item.Input
	case "tool_search_call":
		arguments = agentItemSearchArguments(item)
	default:
		return
	}
	item.AppendExecutedToolCalls(NewExecutedToolCall(name, arguments))
}

// BoundExecutedToolCallsForPrompt clones request input, removes untrusted
// serialized metadata, and bounds locally attached records across the prompt.
func BoundExecutedToolCallsForPrompt(items []any) []any {
	out := make([]any, 0, len(items))
	trusted := make([]ExecutedToolCallCarrier, 0)
	for _, item := range items {
		sanitized, agentItem := clonePromptItemWithoutForgedExecutedToolCalls(item)
		out = append(out, sanitized)
		if agentItem != nil && len(agentItem.ExecutedToolCalls()) > 0 {
			trusted = append(trusted, agentItem)
		}
	}
	boundExecutedToolCallItems(trusted)
	return out
}

func clonePromptItemWithoutForgedExecutedToolCalls(value any) (any, ExecutedToolCallCarrier) {
	switch item := value.(type) {
	case *AgentItem:
		if item == nil {
			return nil, nil
		}
		clone := *item
		clone.Data = cloneAgentItemMap(item.Data)
		clone.Search = cloneAgentItemMap(item.Search)
		clone.executedToolCalls = append([]ExecutedToolCall(nil), item.executedToolCalls...)
		return &clone, &clone
	case AgentItem:
		clone := item
		clone.Data = cloneAgentItemMap(item.Data)
		clone.Search = cloneAgentItemMap(item.Search)
		clone.executedToolCalls = append([]ExecutedToolCall(nil), item.executedToolCalls...)
		return &clone, &clone
	case map[string]any:
		clone := cloneMapAny(item)
		if metadata, ok := clone[internalChatMessageMetadataPassthroughField].(map[string]any); ok {
			metadata = cloneMapAny(metadata)
			delete(metadata, executedToolCallsField)
			if len(metadata) == 0 {
				delete(clone, internalChatMessageMetadataPassthroughField)
			} else {
				clone[internalChatMessageMetadataPassthroughField] = metadata
			}
		}
		return clone, nil
	default:
		if carrier, ok := value.(ExecutedToolCallCarrier); ok {
			clone := carrier.CloneForExecutedToolCallPrompt()
			return clone, clone
		}
		return value, nil
	}
}

func boundExecutedToolCallItems(items []ExecutedToolCallCarrier) {
	remainingItems := len(items)
	originalCalls := 0
	originalBytes := 0
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		for index := range calls {
			call := &calls[index]
			argumentBytes := executedToolCallArgumentBytes(*call)
			if call.truncation == nil && argumentBytes > MaxExecutedToolCallArgumentBytes {
				setExecutedToolCallTruncation(call, argumentBytes, MaxExecutedToolCallArgumentBytes, nil, nil)
			}
			originalCalls++
			if call.truncation != nil && call.truncation.OmittedCalls != nil {
				originalCalls += *call.truncation.OmittedCalls
			}
		}
		item.ReplaceExecutedToolCalls(calls)
		originalBytes += executedToolCallMetadataBytes(item)
	}
	if originalBytes <= MaxExecutedToolCallMetadataBytes {
		return
	}

	var fallbackItem ExecutedToolCallCarrier
	var fallbackCall ExecutedToolCall
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		if len(calls) > 0 {
			fallbackItem = item
			fallbackCall = calls[0]
			break
		}
	}
	reservation := jsonSize(map[string]any{executedToolCallTruncatedField: ExecutedToolCallTruncation{
		OriginalBytes: maxInt(), MaxBytes: maxInt(), OmittedCalls: intPointer(maxInt()), OriginalNameBytes: intPointer(maxInt()),
	}})
	remainingBytes := MaxExecutedToolCallMetadataBytes - minInt(MaxExecutedToolCallMetadataBytes, reservation)
	for _, item := range items {
		if len(item.ExecutedToolCalls()) == 0 {
			continue
		}
		fieldBytes := executedToolCallMetadataFieldBytes()
		budget := remainingBytes/remainingItems - minInt(remainingBytes/remainingItems, fieldBytes)
		boundExecutedToolCallsWithBudget(item, budget)
		remainingBytes -= minInt(remainingBytes, executedToolCallMetadataBytes(item))
		remainingItems--
	}

	represented := representedExecutedToolCalls(items)
	if represented == originalCalls {
		return
	}
	if represented == 0 {
		if fallbackItem == nil {
			return
		}
		originalArgumentBytes := executedToolCallArgumentBytes(fallbackCall)
		if fallbackCall.truncation != nil {
			originalArgumentBytes = fallbackCall.truncation.OriginalBytes
		}
		originalNameBytes := len(fallbackCall.Name)
		nameLimit := minInt(originalNameBytes, MaxExecutedToolCallArgumentBytes/2)
		for nameLimit > 0 && !utf8.ValidString(fallbackCall.Name[:nameLimit]) {
			nameLimit--
		}
		fallbackCall.Name = fallbackCall.Name[:nameLimit]
		omitted := originalCalls - 1
		var originalName *int
		if nameLimit < originalNameBytes {
			originalName = intPointer(originalNameBytes)
		}
		setExecutedToolCallTruncation(&fallbackCall, originalArgumentBytes, 0, &omitted, originalName)
		fallbackItem.ReplaceExecutedToolCalls([]ExecutedToolCall{fallbackCall})
		return
	}
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		if len(calls) == 0 {
			continue
		}
		call := &calls[0]
		originalArgumentBytes := executedToolCallArgumentBytes(*call)
		maxBytes := 0
		previousOmissions := 0
		if call.truncation != nil {
			originalArgumentBytes = call.truncation.OriginalBytes
			maxBytes = call.truncation.MaxBytes
			if call.truncation.OmittedCalls != nil {
				previousOmissions = *call.truncation.OmittedCalls
			}
		}
		omitted := previousOmissions + originalCalls - represented
		setExecutedToolCallTruncation(call, originalArgumentBytes, maxBytes, &omitted, nil)
		item.ReplaceExecutedToolCalls(calls)
		return
	}
}

func boundExecutedToolCallsWithBudget(item ExecutedToolCallCarrier, maxBytes int) {
	serializedBytes := 2
	calls := item.ExecutedToolCalls()
	retained := make([]ExecutedToolCall, 0, len(calls))
	for _, original := range calls {
		call := original
		separatorBytes := 0
		if len(retained) > 0 {
			separatorBytes = 1
		}
		remaining := maxBytes - serializedBytes - separatorBytes
		if remaining < 0 {
			remaining = 0
		}
		argumentBytes := executedToolCallArgumentBytes(call)
		if jsonSize(call) > remaining || argumentBytes > MaxExecutedToolCallArgumentBytes {
			originalArgumentBytes := argumentBytes
			var omitted *int
			if call.truncation != nil {
				originalArgumentBytes = call.truncation.OriginalBytes
				omitted = call.truncation.OmittedCalls
			}
			setExecutedToolCallTruncation(&call, originalArgumentBytes, minInt(remaining, MaxExecutedToolCallArgumentBytes), omitted, nil)
		}
		callBytes := jsonSize(call)
		if callBytes > remaining {
			continue
		}
		serializedBytes += separatorBytes + callBytes
		retained = append(retained, call)
	}
	item.ReplaceExecutedToolCalls(retained)
}

func representedExecutedToolCalls(items []ExecutedToolCallCarrier) int {
	total := 0
	for _, item := range items {
		for _, call := range item.ExecutedToolCalls() {
			total++
			if call.truncation != nil && call.truncation.OmittedCalls != nil {
				total += *call.truncation.OmittedCalls
			}
		}
	}
	return total
}

func executedToolCallArgumentBytes(call ExecutedToolCall) int {
	if call.truncation != nil {
		return jsonSize(map[string]any{executedToolCallTruncatedField: call.truncation})
	}
	return jsonSize(call.arguments)
}

func executedToolCallMetadataBytes(item ExecutedToolCallCarrier) int {
	if item == nil || len(item.ExecutedToolCalls()) == 0 {
		return 0
	}
	return jsonSize(item.ExecutedToolCalls()) + executedToolCallMetadataFieldBytes()
}

func executedToolCallMetadataFieldBytes() int {
	return len(`"`+internalChatMessageMetadataPassthroughField+`":{}`) + len(`"`+executedToolCallsField+`":`)
}

func setExecutedToolCallTruncation(call *ExecutedToolCall, originalBytes int, maxBytes int, omittedCalls *int, originalNameBytes *int) {
	call.arguments = nil
	call.truncation = &ExecutedToolCallTruncation{
		OriginalBytes: originalBytes, MaxBytes: maxBytes, OmittedCalls: omittedCalls, OriginalNameBytes: originalNameBytes,
	}
}

func jsonSize(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return maxInt()
	}
	return len(encoded)
}

func intPointer(value int) *int { return &value }

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt() int { return int(^uint(0) >> 1) }
