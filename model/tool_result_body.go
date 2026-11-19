package model

import (
	"encoding/json"
	"strings"
)

const (
	// MaxToolResultBodyBytes caps one projected Apps result body.
	MaxToolResultBodyBytes = 1024 * 1024
	// maxToolResultDepth is the depth limit for structuredContent: the body
	// counts at depth zero and structuredContent at depth one (Rust
	// MAX_TOOL_RESULT_DEPTH).
	maxToolResultDepth = 64
	// toolResultFieldPrefix precedes the injected body inside an executed-tool
	// call row.
	toolResultFieldPrefix = `,"tool_result":`
	// toolResultCallIDFieldPrefix precedes the Code Mode child call ID that a
	// nested body travels with (#62423).
	toolResultCallIDFieldPrefix = `,"tool_result_call_id":`
)

// ToolResultBodySource is the host-side view of a completed MCP call used to
// project an optional Apps result body. Content blocks are JSON-shaped values
// (`map[string]any` or typed structs with the MCP wire tags).
type ToolResultBodySource struct {
	Content           []any
	StructuredContent any
	IsError           bool
}

// ToolResultBody is a bounded, receiver-shaped projection of one successful
// Apps tool result: plaintext content plus optional object-valued structured
// content. It is kept separately from ordinary tool output and history, and it
// never reaches logs (Rust ToolResultBody, #62388).
type ToolResultBody struct {
	json string
	// nestedCallIDJSON is the encoded Code Mode child call ID a nested result
	// travelled with. It is set only for a call made inside a cell, so the
	// receiver can pair the body with that child invocation (#62423).
	nestedCallIDJSON string
}

// WithNestedCallID returns a copy that carries the Code Mode child call ID. The
// ID travels with the private body so both fit the request budget or neither is
// sent (Rust ToolResultBody::set_nested_call_id).
func (b *ToolResultBody) WithNestedCallID(callID string) *ToolResultBody {
	if b == nil {
		return nil
	}
	trimmed := strings.TrimSpace(callID)
	if trimmed == "" {
		return b
	}
	encoded, err := json.Marshal(trimmed)
	if err != nil {
		return b
	}
	clone := *b
	clone.nestedCallIDJSON = string(encoded)
	return &clone
}

// NestedCallIDJSON reports the encoded child call ID, if the body came from a
// nested Code Mode call.
func (b *ToolResultBody) NestedCallIDJSON() string {
	if b == nil {
		return ""
	}
	return b.nestedCallIDJSON
}

// ProjectToolResultBody projects only plaintext content and an object-valued
// structuredContent that fit the receiver's limits. Anything else yields nil, so
// the caller simply attaches no observation.
func ProjectToolResultBody(source ToolResultBodySource) *ToolResultBody {
	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type body struct {
		Content           []textBlock `json:"content"`
		StructuredContent any         `json:"structuredContent,omitempty"`
	}
	// Even empty text blocks cost this many bytes, so bound the borrowed views
	// before collecting them.
	if source.IsError || len(source.Content) > MaxToolResultBodyBytes/len(`{"type":"text","text":""}`) {
		return nil
	}
	blocks := make([]textBlock, 0, len(source.Content))
	for _, value := range source.Content {
		text, ok := plainTextBlock(value)
		if !ok {
			return nil
		}
		blocks = append(blocks, textBlock{Type: "text", Text: text})
	}
	projected := body{Content: blocks}
	if source.StructuredContent != nil {
		object, ok := source.StructuredContent.(map[string]any)
		if !ok || !withinToolResultDepth(object, maxToolResultDepth) {
			return nil
		}
		projected.StructuredContent = object
	}
	encoded, err := json.Marshal(projected)
	if err != nil || len(encoded) > MaxToolResultBodyBytes {
		return nil
	}
	return &ToolResultBody{json: string(encoded)}
}

// CompactBytes reports the body's encoded length, including the child call ID it
// travels with.
func (b *ToolResultBody) CompactBytes() int {
	if b == nil {
		return 0
	}
	return len(b.json) + len(b.nestedCallIDJSON)
}

// JSON returns the encoded body.
func (b *ToolResultBody) JSON() string {
	if b == nil {
		return ""
	}
	return b.json
}

// String redacts the body so it never reaches logs.
func (b *ToolResultBody) String() string {
	if b == nil {
		return "ToolResultBody(nil)"
	}
	return "ToolResultBody([redacted])"
}

// plainTextBlock accepts one content block only when it is a plain `text` block
// that the receiver can replay verbatim (Rust ToolResultBody::from_mcp_result).
func plainTextBlock(value any) (string, bool) {
	block, ok := jsonObject(value)
	if !ok {
		return "", false
	}
	if block["type"] != "text" {
		return "", false
	}
	if meta, present := block["_meta"]; present {
		metaObject, ok := jsonObject(meta)
		if !ok {
			return "", false
		}
		if encrypted, ok := metaObject["codex/encryptedContent"].(bool); ok && encrypted {
			return "", false
		}
	}
	text, ok := block["text"].(string)
	if !ok || len(text) > MaxToolResultBodyBytes {
		return "", false
	}
	return text, true
}

// jsonObject normalizes a JSON-shaped value into an object map.
func jsonObject(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

// withinToolResultDepth reports whether every nested array and object fits the
// remaining depth. The receiver counts the body at depth zero and
// structuredContent at depth one.
func withinToolResultDepth(value any, remaining int) bool {
	remaining--
	if remaining < 0 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if !withinToolResultDepth(child, remaining) {
				return false
			}
		}
	case []any:
		for _, child := range typed {
			if !withinToolResultDepth(child, remaining) {
				return false
			}
		}
	}
	return true
}

// ToolResultAttachment selects one executed-tool call row inside the encoded
// request body and the body to attach to it.
type ToolResultAttachment struct {
	ItemIndex int
	CallIndex int
	Body      *ToolResultBody
}

// WithToolResults adds authorized connector results to the selected rows without
// re-encoding the ordinary request (Rust with_tool_results, #62388). Borrowed raw
// JSON keeps routing field order, tool schemas and unrelated inputs intact, and
// the enrichment gives up rather than exceed MaxResponseMessageBytes.
func WithToolResults(body string, results []ToolResultAttachment) (string, bool) {
	if len(results) == 0 {
		return "", false
	}
	remaining := MaxResponseMessageBytes - len(body)
	if remaining <= 0 {
		return "", false
	}
	input, inputStart, ok := jsonFieldSpan(body, "input")
	if !ok {
		return "", false
	}
	var enriched strings.Builder
	cursor := 0
	for _, attachment := range results {
		if attachment.Body == nil || attachment.Body.CompactBytes() == 0 {
			return "", false
		}
		item, itemStart, ok := jsonArrayElementSpan(input, attachment.ItemIndex)
		if !ok {
			return "", false
		}
		metadata, metadataStart, ok := jsonFieldSpan(item, internalChatMessageMetadataPassthroughField)
		if !ok {
			return "", false
		}
		calls, callsStart, ok := jsonFieldSpan(metadata, executedToolCallsField)
		if !ok {
			return "", false
		}
		row, rowStart, ok := jsonArrayElementSpan(calls, attachment.CallIndex)
		if !ok {
			return "", false
		}
		// A native executed-tool row always carries name and arguments.
		if len(row) < 3 || row[0] != '{' || row[len(row)-1] != '}' {
			return "", false
		}
		close := inputStart + itemStart + metadataStart + callsStart + rowStart + len(row) - 1
		if close <= cursor || close >= len(body) {
			return "", false
		}
		callIDPrefix := ""
		if attachment.Body.NestedCallIDJSON() != "" {
			callIDPrefix = toolResultCallIDFieldPrefix
		}
		// The body and the child call ID fit the request budget together or are
		// omitted together (Rust with_tool_results, #62423).
		cost := len(toolResultFieldPrefix) + len(callIDPrefix) + attachment.Body.CompactBytes()
		if cost > remaining {
			continue
		}
		enriched.WriteString(body[cursor:close])
		enriched.WriteString(toolResultFieldPrefix)
		enriched.WriteString(attachment.Body.JSON())
		if callIDPrefix != "" {
			enriched.WriteString(toolResultCallIDFieldPrefix)
			enriched.WriteString(attachment.Body.NestedCallIDJSON())
		}
		cursor = close
		remaining -= cost
	}
	if enriched.Len() == 0 {
		return "", false
	}
	enriched.WriteString(body[cursor:])
	return enriched.String(), true
}

// RequestInputExecutedToolCalls reports the executed-tool rows an in-memory
// request item carries, including their local result-body bindings (Rust
// ResponseItem::executed_tool_call_metadata). Serialized items cannot carry a
// binding, so a decoded map yields none.
func RequestInputExecutedToolCalls(item any) []ExecutedToolCall {
	carrier, ok := item.(ExecutedToolCallCarrier)
	if !ok || carrier == nil {
		return nil
	}
	return carrier.ExecutedToolCalls()
}

// ScopeExecutedToolCallsForRequest returns the request input with executed-tool
// metadata narrowed to the rows that carry a local result body (Rust
// scope_request_metadata, #62392). Items keep their identity and position, so
// result-body attachments still address the same indexes. A connector-only
// subset cannot claim a cell's complete inventory, so the projection drops the
// completeness marker and the cell association whenever it removed anything.
func ScopeExecutedToolCallsForRequest(items []any) []any {
	scoped := make([]any, len(items))
	copy(scoped, items)
	for index, item := range items {
		carrier, ok := item.(ExecutedToolCallCarrier)
		if !ok || carrier == nil {
			continue
		}
		calls := carrier.ExecutedToolCalls()
		if len(calls) == 0 {
			continue
		}
		kept := make([]ExecutedToolCall, 0, len(calls))
		for _, call := range calls {
			if call.ToolResultID() == "" {
				continue
			}
			kept = append(kept, call)
		}
		if len(kept) == len(calls) && carrierHasCompleteInventory(carrier) {
			continue
		}
		clone := carrier.CloneForExecutedToolCallPrompt()
		if clone == nil {
			continue
		}
		clone.ReplaceExecutedToolCalls(kept)
		if narrowed, ok := clone.(interface {
			SetExecutedToolCallCell(string)
			ClearExecutedToolCallsComplete()
		}); ok {
			narrowed.ClearExecutedToolCallsComplete()
			if len(kept) == 0 {
				narrowed.SetExecutedToolCallCell("")
			}
		}
		scoped[index] = clone
	}
	return scoped
}

// carrierHasCompleteInventory reports whether a carrier claims the cell's full
// ordered call inventory.
func carrierHasCompleteInventory(carrier ExecutedToolCallCarrier) bool {
	agentItem, ok := carrier.(*AgentItem)
	if !ok || agentItem == nil {
		return false
	}
	return agentItem.toolCallsComplete != nil && *agentItem.toolCallsComplete
}

// jsonFieldSpan returns the raw value stored under key in the JSON object body
// and the value's byte offset within body.
func jsonFieldSpan(body string, key string) (string, int, bool) {
	decoder := json.NewDecoder(strings.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return "", 0, false
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return "", 0, false
	}
	for {
		name, err := decoder.Token()
		if err != nil {
			return "", 0, false
		}
		if _, done := name.(json.Delim); done {
			return "", 0, false
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return "", 0, false
		}
		if name == key {
			start := int(decoder.InputOffset()) - len(raw)
			if start < 0 {
				return "", 0, false
			}
			return string(raw), start, true
		}
	}
}

// jsonArrayElementSpan returns the raw element at index in the JSON array body
// and the element's byte offset within body.
func jsonArrayElementSpan(body string, index int) (string, int, bool) {
	if index < 0 {
		return "", 0, false
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return "", 0, false
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return "", 0, false
	}
	for position := 0; ; position++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return "", 0, false
		}
		if position != index {
			continue
		}
		start := int(decoder.InputOffset()) - len(raw)
		if start < 0 {
			return "", 0, false
		}
		return string(raw), start, true
	}
}
