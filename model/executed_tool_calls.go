package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxExecutedToolCallArgumentBytes = 8 * 1024
	MaxExecutedToolCallMetadataBytes = 32 * 1024
	MaxToolResultSources             = 32
	MaxToolResultSourceFieldBytes    = 128

	internalChatMessageMetadataPassthroughField = "internal_chat_message_metadata_passthrough"
	executedToolCallsField                      = "executed_tool_calls"
	executedToolCallTruncatedField              = "_codex_executed_tool_call_truncated"
	executedToolCallRawField                    = "_codex_executed_tool_call_raw"
	toolResultSourcesField                      = "tool_result_sources"
	toolResultMetadataField                     = "tool_result_metadata"
	toolResultMetadataOmittedMarker             = "omitted_due_to_size_limit"
)

type ExecutedToolCall struct {
	Name string `json:"name"`

	arguments  any
	truncation *ExecutedToolCallTruncation

	// toolResultSources carries host-generated analytics evidence. It is kept
	// unexported so untrusted serialized input cannot inject it, mirroring
	// Rust's skip_deserializing/skip_serializing_if behavior.
	toolResultSources any
	// toolResultMetadata carries a host-recorded MCP `_meta` snapshot (Rust
	// #44336). It is unexported so untrusted serialized input cannot inject it,
	// and its raw values never reach logs.
	toolResultMetadata ToolResultMetadata
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

// ToolResultSource is a trusted source identity observed by the host in an
// accepted tool result (Rust #42164).
type ToolResultSource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ToolResultSources is a bounded capture update. The zero value means no
// capture attempt; NewToolResultSourcesParseFailed records that parsing was
// attempted but failed rather than a budget exceeded.
type ToolResultSources struct {
	value any
}

// NewToolResultSources deduplicates a complete source list and rejects the
// whole capture when it exceeds the source count or per-field byte limits.
func NewToolResultSources(sources []ToolResultSource) ToolResultSources {
	unique := make([]ToolResultSource, 0, len(sources))
	seen := make(map[ToolResultSource]struct{}, len(sources))
	for _, source := range sources {
		if _, ok := seen[source]; ok {
			continue
		}
		if len(unique) == MaxToolResultSources || len(source.Type) > MaxToolResultSourceFieldBytes || len(source.ID) > MaxToolResultSourceFieldBytes {
			return ToolResultSources{}
		}
		seen[source] = struct{}{}
		unique = append(unique, source)
	}
	return ToolResultSources{value: unique}
}

// NewToolResultSourcesParseFailed records a failed parse using the receiver's
// existing array-of-sources shape. `parse_failed` is a status marker, not a
// resource type; its ID is empty (Rust #44336).
func NewToolResultSourcesParseFailed() ToolResultSources {
	return ToolResultSources{value: []ToolResultSource{{Type: "parse_failed"}}}
}

// ToolResultMetadata is an entire MCP result's `_meta`, or a string marker when
// omitted due to the size limit (Rust #44336). It is host-recorded only and is
// never trusted from serialized input; its raw values never reach logs.
type ToolResultMetadata struct {
	value any
}

// NewToolResultMetadata captures the complete MCP `_meta` snapshot. No keys are
// filtered: retention and outgoing-request budgets apply later (Rust
// `ToolResultMetadata::new`). The prompt bound sheds an oversized snapshot.
func NewToolResultMetadata(metadata any) ToolResultMetadata {
	if metadata == nil {
		return ToolResultMetadata{}
	}
	return ToolResultMetadata{value: metadata}
}

// OmittedToolResultMetadata is the harness status marker used when raw metadata
// exceeds the byte limit. It is the parse-compatible bare form; the shedding
// path writes the overage form (Rust `omitted_due_to_size_limit`).
func OmittedToolResultMetadata() ToolResultMetadata {
	return ToolResultMetadata{value: toolResultMetadataOmittedMarker}
}

// omittedToolResultMetadataForOverage is Rust's `omitted_due_to_size_limit
// (overage_bytes=N)` marker.
func omittedToolResultMetadataForOverage(overageBytes int) ToolResultMetadata {
	return ToolResultMetadata{value: fmt.Sprintf("%s (overage_bytes=%d)", toolResultMetadataOmittedMarker, overageBytes)}
}

// isOmittedDueToSizeLimit mirrors Rust's `is_omitted_due_to_size_limit`: both
// the bare marker and the overage form count.
func (m ToolResultMetadata) isOmittedDueToSizeLimit() bool {
	value, ok := m.value.(string)
	if !ok {
		return false
	}
	if value == toolResultMetadataOmittedMarker {
		return true
	}
	overage, ok := strings.CutPrefix(value, toolResultMetadataOmittedMarker+" (overage_bytes=")
	if !ok {
		return false
	}
	overage, ok = strings.CutSuffix(overage, ")")
	if !ok {
		return false
	}
	_, err := strconv.Atoi(overage)
	return err == nil
}

// omitIfSmaller mirrors Rust's `omit_if_smaller`: replace the snapshot with the
// overage marker only when the marker is smaller, and report the retained size.
// An absent or already-omitted snapshot keeps its original size.
func (m *ToolResultMetadata) omitIfSmaller(originalBytes int, overageBytes int) int {
	if m == nil || m.IsNone() || m.isOmittedDueToSizeLimit() {
		return originalBytes
	}
	omitted := omittedToolResultMetadataForOverage(overageBytes)
	omittedBytes := jsonSize(omitted)
	if omittedBytes < originalBytes {
		*m = omitted
		return omittedBytes
	}
	return originalBytes
}

// IsNone reports whether no snapshot (or omission marker) is recorded.
func (m ToolResultMetadata) IsNone() bool { return m.value == nil }

// IsSome reports whether the bounded snapshot holds metadata or an omission marker.
func (m ToolResultMetadata) IsSome() bool { return m.value != nil }

// String redacts raw metadata so it never reaches logs.
func (m ToolResultMetadata) String() string { return "ToolResultMetadata([redacted])" }

func (m ToolResultMetadata) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.value)
}

// SetToolResultMetadata replaces the entire `_meta` snapshot, including with an
// omission marker. It reports whether metadata (or a marker) is present.
func (c *ExecutedToolCall) SetToolResultMetadata(metadata ToolResultMetadata) bool {
	if c == nil {
		return false
	}
	c.toolResultMetadata = metadata
	return c.toolResultMetadata.IsSome()
}

// HasToolResultMetadata reports whether a bounded `_meta` snapshot is attached.
func (c ExecutedToolCall) HasToolResultMetadata() bool {
	return c.toolResultMetadata.IsSome()
}

// SetToolResultSources replaces the invocation's capture outcome, including
// clearing omitted evidence. It reports whether evidence was present after the
// replacement.
func (c *ExecutedToolCall) SetToolResultSources(sources ToolResultSources) bool {
	if c == nil {
		return false
	}
	c.toolResultSources = sources.value
	return c.toolResultSources != nil
}

// HasToolResultSources reports whether host-generated source evidence is
// currently attached to this call.
func (c ExecutedToolCall) HasToolResultSources() bool {
	return c.toolResultSources != nil
}

func (c ExecutedToolCall) MarshalJSON() ([]byte, error) {
	arguments := c.arguments
	if c.truncation != nil {
		arguments = map[string]any{executedToolCallTruncatedField: c.truncation}
	}
	payload := struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	}{
		Name:      c.Name,
		Arguments: arguments,
	}
	if c.toolResultSources != nil {
		if c.toolResultMetadata.IsSome() {
			return json.Marshal(struct {
				Name               string `json:"name"`
				Arguments          any    `json:"arguments"`
				ToolResultSources  any    `json:"tool_result_sources"`
				ToolResultMetadata any    `json:"tool_result_metadata"`
			}{Name: c.Name, Arguments: arguments, ToolResultSources: c.toolResultSources, ToolResultMetadata: c.toolResultMetadata.value})
		}
		return json.Marshal(struct {
			Name              string `json:"name"`
			Arguments         any    `json:"arguments"`
			ToolResultSources any    `json:"tool_result_sources"`
		}{Name: c.Name, Arguments: arguments, ToolResultSources: c.toolResultSources})
	}
	if c.toolResultMetadata.IsSome() {
		return json.Marshal(struct {
			Name               string `json:"name"`
			Arguments          any    `json:"arguments"`
			ToolResultMetadata any    `json:"tool_result_metadata"`
		}{Name: c.Name, Arguments: arguments, ToolResultMetadata: c.toolResultMetadata.value})
	}
	return json.Marshal(payload)
}

// Truncated reports whether the recorded tool call had its arguments (or the
// call itself) truncated by the recording limit (Rust #41058).
func (c ExecutedToolCall) Truncated() bool {
	return c.truncation != nil
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
		// The completeness marker belongs to the call inventory, so stripping
		// the calls strips the marker with it.
		i.cellID = ""
		i.toolCallsComplete = nil
	}
}

// ExecutedToolCallCellID returns the originating Code Mode cell associated with
// the item's recorded calls. History seeding observes it so a resumed cell
// cannot claim a historical ID as fresh (Rust #44472).
func (i *AgentItem) ExecutedToolCallCellID() string {
	if i == nil {
		return ""
	}
	return strings.TrimSpace(i.cellID)
}

// ClearToolResultMetadata omits raw tool-result metadata without changing
// existing calls, sources, or completion markers (Rust #44336).
// ClearToolResultMetadata drops this recorded call's host result metadata while
// keeping its identity, arguments, sources, and truncation marker (Rust #45185
// clears result metadata before dropping the record under the retention budget).
func (c *ExecutedToolCall) ClearToolResultMetadata() {
	if c != nil {
		c.toolResultMetadata = ToolResultMetadata{}
	}
}

func (i *AgentItem) ClearToolResultMetadata() {
	if i == nil {
		return
	}
	for index := range i.executedToolCalls {
		i.executedToolCalls[index].ClearToolResultMetadata()
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
	// Raw result metadata must not displace existing source evidence, calls, or
	// completion proof. Oversized snapshots degrade to the omission marker first,
	// then are dropped entirely (Rust #44336).
	overageBytes := originalBytes - MaxExecutedToolCallMetadataBytes
	originalBytes = 0
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		changed := false
		for index := range calls {
			if !calls[index].HasToolResultMetadata() {
				continue
			}
			// Rust's `omit_if_smaller`: the marker carries the shed overage and
			// replaces the snapshot only when it is actually smaller.
			currentBytes := jsonSize(calls[index].toolResultMetadata)
			if retained := calls[index].toolResultMetadata.omitIfSmaller(currentBytes, overageBytes); retained != currentBytes {
				changed = true
			}
		}
		if changed {
			item.ReplaceExecutedToolCalls(calls)
		}
		originalBytes += executedToolCallMetadataBytes(item)
	}
	if originalBytes <= MaxExecutedToolCallMetadataBytes {
		return
	}
	// Omission markers are optional too; keep the original call budget if they
	// cannot fit.
	originalBytes = 0
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		changed := false
		for index := range calls {
			if calls[index].HasToolResultMetadata() {
				calls[index].toolResultMetadata = ToolResultMetadata{}
				changed = true
			}
		}
		if changed {
			item.ReplaceExecutedToolCalls(calls)
		}
		originalBytes += executedToolCallMetadataBytes(item)
	}
	if originalBytes <= MaxExecutedToolCallMetadataBytes {
		return
	}
	// Source evidence is optional: dropping it must not discard calls or their
	// completion proof (Rust #42164).
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		changed := false
		for index := range calls {
			if calls[index].HasToolResultSources() {
				calls[index].SetToolResultSources(ToolResultSources{})
				changed = true
			}
		}
		if changed {
			item.ReplaceExecutedToolCalls(calls)
		}
	}
	originalBytes = 0
	for _, item := range items {
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

// ExecutedToolCallMetadataBytes reports the serialized size of an item's
// host-owned executed-tool-call metadata (Rust executed_tool_call_metadata_bytes).
// The request budget and the recorder's retention budget share it.
func ExecutedToolCallMetadataBytes(item ExecutedToolCallCarrier) int {
	return executedToolCallMetadataBytes(item)
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
