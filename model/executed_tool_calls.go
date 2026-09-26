package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxExecutedToolCallArgumentBytes = 8 * 1024
	// MaxExecutedToolCallMetadataBytes is Rust's protocol
	// `MAX_EXECUTED_TOOL_CALL_METADATA_BYTES`: the budget for one serialized
	// prompt. The 32 KiB per-output gate is the *retention* threshold and lives in
	// the turn recorder (`maxExecutedToolCallFullArgumentBytesPerItem`), not here.
	MaxExecutedToolCallMetadataBytes = 2 * 1024 * 1024
	MaxToolResultSources             = 32
	MaxToolResultSourceFieldBytes    = 128

	internalChatMessageMetadataPassthroughField = "internal_chat_message_metadata_passthrough"
	executedToolCallsField                      = "executed_tool_calls"
	executedToolCallTruncatedField              = "_codex_executed_tool_call_truncated"
	executedToolCallRawField                    = "_codex_executed_tool_call_raw"
	toolResultSourcesField                      = "tool_result_sources"
	toolResultMetadataField                     = "tool_result_metadata"
	toolResultMetadataOmittedMarker             = "omitted_due_to_size_limit"
	// resourceAccessMetadataKey is Rust's RESOURCE_ACCESS_METADATA_KEY: the one
	// field kept when a snapshot must shrink but its evidence is still useful.
	resourceAccessMetadataKey = "openai/resource_access"
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

// retainResourceAccess mirrors Rust's `retain_resource_access`: an object that
// carries the resource-access field keeps only that field, so the caller can
// reuse the smaller snapshot instead of omitting it. It reports whether the
// snapshot changed.
func (m *ToolResultMetadata) retainResourceAccess() bool {
	if m == nil {
		return false
	}
	object, ok := m.value.(map[string]any)
	if !ok {
		return false
	}
	value, present := object[resourceAccessMetadataKey]
	if !present {
		return false
	}
	*m = ToolResultMetadata{value: map[string]any{resourceAccessMetadataKey: value}}
	return true
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
	return boundExecutedToolCallsForPrompt(items, false)
}

// BoundExecutedToolCallsForPromptPrioritizingRecent mirrors Rust's
// `bound_executed_tool_calls_for_prompt_prioritizing_recent`: retained history is
// bounded without letting older calls displace the newest calls (Rust reverses
// the items, bounds, and reverses back). It is applied behind the 32 KiB
// attach-stage gate.
func BoundExecutedToolCallsForPromptPrioritizingRecent(items []any) []any {
	return boundExecutedToolCallsForPrompt(items, true)
}

func boundExecutedToolCallsForPrompt(items []any, prioritizeRecent bool) []any {
	out := make([]any, 0, len(items))
	trusted := make([]ExecutedToolCallCarrier, 0)
	for _, item := range items {
		sanitized, agentItem := clonePromptItemWithoutForgedExecutedToolCalls(item)
		out = append(out, sanitized)
		if agentItem != nil && len(agentItem.ExecutedToolCalls()) > 0 {
			trusted = append(trusted, agentItem)
		}
	}
	if prioritizeRecent {
		// Rust's `items.reverse()` around the bound: the newest outputs get the
		// largest eviction order, so size ties evict older outputs first.
		for left, right := 0, len(trusted)-1; left < right; left, right = left+1, right-1 {
			trusted[left], trusted[right] = trusted[right], trusted[left]
		}
	}
	boundExecutedToolCallItemsWithBudget(trusted, MaxExecutedToolCallMetadataBytes, false, prioritizeRecent)
	return out
}

// executedToolCallCarriers collects the carriers of items that already hold a
// trusted executed-tool-call inventory, without cloning them.
func executedToolCallCarriers(items []any) []ExecutedToolCallCarrier {
	var carriers []ExecutedToolCallCarrier
	for _, item := range items {
		carrier, ok := item.(ExecutedToolCallCarrier)
		if !ok || carrier == nil || len(carrier.ExecutedToolCalls()) == 0 {
			continue
		}
		carriers = append(carriers, carrier)
	}
	return carriers
}

// ExecutedToolCallMetadataTotalBytes mirrors Rust's
// `metadata_metrics::metadata_bytes`: the serialized bytes the
// executed-tool-call metadata occupies across items.
func ExecutedToolCallMetadataTotalBytes(items []any) int {
	return executedToolCallMetadataBytesOf(executedToolCallCarriers(items))
}

// BoundExecutedToolCallsForMessage mirrors Rust's
// `bound_executed_tool_calls_for_message`: bound optional observations to the
// space left in the actual outgoing message. Only the wire copy changes, and the
// whole-message scope sheds sources and recorded arguments before resource
// evidence. The items are bounded in place and returned.
func BoundExecutedToolCallsForMessage(items []any, maxMetadataBytes int) []any {
	carriers := executedToolCallCarriers(items)
	if len(carriers) == 0 {
		return items
	}
	boundExecutedToolCallItemsWithBudget(carriers, maxMetadataBytes, true, false)
	return items
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

// shedGenericResultMetadata mirrors Rust's `shed_generic_result_metadata` for
// the prompt budget: snapshots are shed largest-first (ties by output order,
// then the call's own order within the output), a snapshot that carries the
// resource-access field keeps only that field, and every other snapshot is
// replaced by the omission marker only when the marker is smaller. It reports
// the remaining metadata bytes.
func shedGenericResultMetadata(items []ExecutedToolCallCarrier, maxBytes int, wholeMessage bool, prioritizeRecent bool, totalBytes int) int {
	type metadataHandle struct {
		itemIndex int
		callIndex int
		bytes     int
	}
	var handles []metadataHandle
	for itemIndex := range items {
		calls := items[itemIndex].ExecutedToolCalls()
		for callIndex := range calls {
			if !calls[callIndex].HasToolResultMetadata() {
				continue
			}
			handles = append(handles, metadataHandle{
				itemIndex: itemIndex,
				callIndex: callIndex,
				bytes:     jsonSize(calls[callIndex].toolResultMetadata),
			})
		}
	}
	sort.SliceStable(handles, func(i int, j int) bool {
		if handles[i].bytes != handles[j].bytes {
			return handles[i].bytes > handles[j].bytes
		}
		leftOrder := metadataEvictionOrder(handles[i].itemIndex, prioritizeRecent)
		rightOrder := metadataEvictionOrder(handles[j].itemIndex, prioritizeRecent)
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return handles[i].callIndex < handles[j].callIndex
	})
	total := totalBytes
	for _, handle := range handles {
		if total <= maxBytes {
			break
		}
		calls := items[handle.itemIndex].ExecutedToolCalls()
		metadata := &calls[handle.callIndex].toolResultMetadata
		original := jsonSize(*metadata)
		retained := original
		if metadata.retainResourceAccess() {
			retained = jsonSize(*metadata)
		} else {
			retained = metadata.omitIfSmaller(original, total-maxBytes)
		}
		items[handle.itemIndex].ReplaceExecutedToolCalls(calls)
		total -= original - retained
	}
	// Tiny generic values and markers can be cheaper than replacing them with a
	// new marker. Remove their complete fields before sacrificing resource
	// evidence (Rust's whole-message scope).
	if wholeMessage {
		for _, handle := range handles {
			if total <= maxBytes {
				break
			}
			calls := items[handle.itemIndex].ExecutedToolCalls()
			metadata := &calls[handle.callIndex].toolResultMetadata
			if metadata.retainResourceAccess() {
				continue
			}
			before := jsonSize(*metadata)
			*metadata = ToolResultMetadata{}
			items[handle.itemIndex].ReplaceExecutedToolCalls(calls)
			total -= before + len(`,"tool_result_metadata":`)
			if total < 0 {
				total = 0
			}
		}
	}
	return total
}

// shedRemainingResultMetadata mirrors Rust's `shed_remaining_result_metadata`
// for the prompt budget: re-sort by the updated sizes (a plain snapshot before
// an existing marker of the same size, largest first) and omit what is still
// over budget. It reports the remaining metadata bytes.
func shedRemainingResultMetadata(items []ExecutedToolCallCarrier, maxBytes int, wholeMessage bool, prioritizeRecent bool, totalBytes int) int {
	type metadataHandle struct {
		itemIndex int
		callIndex int
		bytes     int
		omitted   bool
	}
	var handles []metadataHandle
	for itemIndex := range items {
		calls := items[itemIndex].ExecutedToolCalls()
		for callIndex := range calls {
			metadata := calls[callIndex].toolResultMetadata
			if !metadata.IsSome() {
				continue
			}
			handles = append(handles, metadataHandle{
				itemIndex: itemIndex,
				callIndex: callIndex,
				bytes:     jsonSize(metadata),
				omitted:   metadata.isOmittedDueToSizeLimit(),
			})
		}
	}
	sort.SliceStable(handles, func(i int, j int) bool {
		if wholeMessage && handles[i].omitted != handles[j].omitted {
			return !handles[i].omitted
		}
		if handles[i].bytes != handles[j].bytes {
			return handles[i].bytes > handles[j].bytes
		}
		if handles[i].omitted != handles[j].omitted {
			return !handles[i].omitted
		}
		leftOrder := metadataEvictionOrder(handles[i].itemIndex, prioritizeRecent)
		rightOrder := metadataEvictionOrder(handles[j].itemIndex, prioritizeRecent)
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return handles[i].callIndex < handles[j].callIndex
	})
	total := totalBytes
	for _, handle := range handles {
		if total <= maxBytes {
			break
		}
		calls := items[handle.itemIndex].ExecutedToolCalls()
		metadata := &calls[handle.callIndex].toolResultMetadata
		original := jsonSize(*metadata)
		retained := metadata.omitIfSmaller(original, total-maxBytes)
		items[handle.itemIndex].ReplaceExecutedToolCalls(calls)
		total -= original - retained
	}
	// Use the updated sizes: markers should be removed before smaller provider
	// metadata, and the largest remaining snapshots go first.
	for index := range handles {
		calls := items[handles[index].itemIndex].ExecutedToolCalls()
		metadata := calls[handles[index].callIndex].toolResultMetadata
		handles[index].bytes = jsonSize(metadata)
		handles[index].omitted = metadata.isOmittedDueToSizeLimit()
	}
	sort.SliceStable(handles, func(i int, j int) bool {
		left := wholeMessage && !handles[i].omitted
		right := wholeMessage && !handles[j].omitted
		if left != right {
			return left
		}
		if handles[i].bytes != handles[j].bytes {
			return handles[i].bytes > handles[j].bytes
		}
		if handles[i].omitted != handles[j].omitted {
			return !handles[i].omitted
		}
		leftOrder := metadataEvictionOrder(handles[i].itemIndex, prioritizeRecent)
		rightOrder := metadataEvictionOrder(handles[j].itemIndex, prioritizeRecent)
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return handles[i].callIndex < handles[j].callIndex
	})
	for _, handle := range handles {
		if total <= maxBytes {
			break
		}
		calls := items[handle.itemIndex].ExecutedToolCalls()
		metadata := &calls[handle.callIndex].toolResultMetadata
		if metadata.IsNone() {
			continue
		}
		before := jsonSize(*metadata)
		*metadata = ToolResultMetadata{}
		items[handle.itemIndex].ReplaceExecutedToolCalls(calls)
		total -= before + len(`,"tool_result_metadata":`)
		if total < 0 {
			total = 0
		}
	}
	return total
}

// executedToolCallCellID reads a carrier's Code Mode cell id when it exposes one.
func executedToolCallCellID(item ExecutedToolCallCarrier) string {
	carrier, ok := item.(interface{ ExecutedToolCallCellID() string })
	if !ok {
		return ""
	}
	return strings.TrimSpace(carrier.ExecutedToolCallCellID())
}

// metadataEvictionOrder mirrors Rust's per-output tie-break order: older
// outputs are evicted first, and the retained-history entry point
// (prioritizeRecent, which reverses the items) keeps the newest calls.
func metadataEvictionOrder(itemIndex int, prioritizeRecent bool) int {
	if prioritizeRecent {
		return maxInt() - itemIndex
	}
	return itemIndex
}

// executedToolCallMetadataBytesOf sums the metadata bytes of trusted carriers.
func executedToolCallMetadataBytesOf(items []ExecutedToolCallCarrier) int {
	total := 0
	for _, item := range items {
		total += executedToolCallMetadataBytes(item)
	}
	return total
}

// clearDamagedCellCompleteness discards the completion claim on every carrier
// whose Code Mode cell is damaged (Rust's `clear_damaged_cell_completeness`).
func clearDamagedCellCompleteness(items []ExecutedToolCallCarrier, damagedCells map[string]bool) {
	if len(damagedCells) == 0 {
		return
	}
	for _, item := range items {
		if damagedCells[executedToolCallCellID(item)] {
			clearExecutedToolCallsComplete(item)
		}
	}
}

// shedResultSources mirrors Rust's `shed_result_sources`: source evidence is
// optional and must not discard calls or their completion proof. The
// whole-message scope stops once the budget is met; the prompt scope drops it
// all. It reports the remaining overage.
func shedResultSources(items []ExecutedToolCallCarrier, maxMetadataBytes int, wholeMessage bool) int {
	overage := executedToolCallMetadataBytesOf(items) - maxMetadataBytes
	if overage < 0 {
		overage = 0
	}
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		changed := false
		for index := range calls {
			if wholeMessage && overage == 0 {
				break
			}
			if !calls[index].HasToolResultSources() {
				continue
			}
			bytes := jsonSize(calls[index].toolResultSources)
			calls[index].SetToolResultSources(ToolResultSources{})
			changed = true
			overage -= bytes + len(`,"tool_result_sources":`)
			if overage < 0 {
				overage = 0
			}
		}
		if changed {
			item.ReplaceExecutedToolCalls(calls)
		}
	}
	return overage
}

// truncateCallArgumentsToFit mirrors Rust's `truncate_call_arguments_to_fit`: a
// large outgoing message should not lose the other tool names in a cell when
// replacing one call's arguments is enough to make it fit.
func truncateCallArgumentsToFit(items []ExecutedToolCallCarrier, maxMetadataBytes int, overageBytes int, damagedCells map[string]bool) {
	for itemIndex := range items {
		for overageBytes > 0 {
			calls := items[itemIndex].ExecutedToolCalls()
			changed := false
			for index := range calls {
				if calls[index].truncation != nil {
					continue
				}
				original := executedToolCallArgumentBytes(calls[index])
				if original <= 0 {
					continue
				}
				retained := original - overageBytes
				if retained < 0 {
					retained = 0
				}
				candidate := calls[index]
				setExecutedToolCallTruncation(&candidate, original, minInt(retained, MaxExecutedToolCallArgumentBytes), nil, nil)
				truncatedBytes := jsonSize(map[string]any{executedToolCallTruncatedField: *candidate.truncation})
				if truncatedBytes >= original {
					continue
				}
				calls[index] = candidate
				changed = true
				clearExecutedToolCallsComplete(items[itemIndex])
				if cell := executedToolCallCellID(items[itemIndex]); cell != "" {
					damagedCells[cell] = true
				}
				break
			}
			if !changed {
				break
			}
			items[itemIndex].ReplaceExecutedToolCalls(calls)
			// Losing one call's arguments invalidates every output in that cell.
			clearDamagedCellCompleteness(items, damagedCells)
			overageBytes = executedToolCallMetadataBytesOf(items) - maxMetadataBytes
			if overageBytes < 0 {
				overageBytes = 0
			}
		}
	}
}

// clearExecutedToolCallsComplete discards a carrier's completion claim when the
// host cannot retain the cell's full evidence (Rust's
// `ResponseItem::clear_tool_calls_complete`).
func clearExecutedToolCallsComplete(item ExecutedToolCallCarrier) {
	if clearer, ok := item.(interface{ ClearExecutedToolCallsComplete() }); ok {
		clearer.ClearExecutedToolCallsComplete()
	}
}

func boundExecutedToolCallItems(items []ExecutedToolCallCarrier) {
	boundExecutedToolCallItemsWithBudget(items, MaxExecutedToolCallMetadataBytes, false, false)
}

// boundExecutedToolCallItemsWithBudget mirrors Rust's
// `bound_executed_tool_calls_with_metadata_budget` for one budget and scope.
// The whole-message scope sheds optional sources and recorded arguments before
// resource-access evidence; the prompt budget keeps its existing order.
func boundExecutedToolCallItemsWithBudget(items []ExecutedToolCallCarrier, maxMetadataBytes int, wholeMessage bool, prioritizeRecent bool) {
	originalBytes := 0
	damagedCells := map[string]bool{}
	for _, item := range items {
		calls := item.ExecutedToolCalls()
		truncated := false
		for index := range calls {
			call := &calls[index]
			argumentBytes := executedToolCallArgumentBytes(*call)
			if call.truncation == nil && argumentBytes > MaxExecutedToolCallArgumentBytes {
				setExecutedToolCallTruncation(call, argumentBytes, MaxExecutedToolCallArgumentBytes, nil, nil)
			}
			truncated = truncated || call.truncation != nil
		}
		item.ReplaceExecutedToolCalls(calls)
		if truncated {
			// An inventory whose calls or arguments were lost cannot claim
			// completeness (Rust #41058 `mark_tool_calls_complete`).
			clearExecutedToolCallsComplete(item)
			if cell := executedToolCallCellID(item); cell != "" {
				damagedCells[cell] = true
			}
		}
		originalBytes += executedToolCallMetadataBytes(item)
	}
	// Every item in a damaged cell loses its completion claim too, so a later
	// output cannot present the cell's surviving calls as complete.
	for _, item := range items {
		if damagedCells[executedToolCallCellID(item)] {
			clearExecutedToolCallsComplete(item)
		}
	}
	if originalBytes <= maxMetadataBytes {
		return
	}
	// Raw result metadata must not displace existing source evidence, calls, or
	// completion proof. Rust's `shed_generic_result_metadata` sheds the largest
	// snapshots first, so one large result cannot discard unrelated small
	// results; a resource-access field is kept on its own before a snapshot is
	// replaced by the smaller omission marker (`omit_if_smaller`).
	originalBytes = shedGenericResultMetadata(items, maxMetadataBytes, wholeMessage, prioritizeRecent, originalBytes)
	if originalBytes <= maxMetadataBytes {
		return
	}
	if wholeMessage {
		// At the message limit, shed optional sources and recorded arguments
		// before resource-access evidence (Rust's whole-message ordering).
		overage := shedResultSources(items, maxMetadataBytes, true)
		if overage > 0 {
			truncateCallArgumentsToFit(items, maxMetadataBytes, overage, damagedCells)
		}
		originalBytes = executedToolCallMetadataBytesOf(items)
	}
	// Rust's `shed_remaining_result_metadata`: with the updated sizes, a plain
	// snapshot is omitted before an existing marker of the same size, and the
	// largest remaining snapshots are shed first.
	originalBytes = shedRemainingResultMetadata(items, maxMetadataBytes, wholeMessage, prioritizeRecent, originalBytes)
	if originalBytes <= maxMetadataBytes {
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
	if originalBytes <= maxMetadataBytes {
		return
	}
	// Source evidence is optional: dropping it must not discard calls or their
	// completion proof (Rust #42164). The whole-message path already handled
	// sources and arguments before resource evidence.
	if !wholeMessage {
		shedResultSources(items, maxMetadataBytes, false)
	}
	originalBytes = executedToolCallMetadataBytesOf(items)
	if originalBytes <= maxMetadataBytes {
		return
	}

	distributeRemainingBudget(items, maxMetadataBytes, prioritizeRecent, damagedCells)
}

// distributeRemainingBudget mirrors Rust's `distribute_remaining_budget`: give
// each output that still holds evidence a share of what is left (the whole
// remainder, newest first, when the retained-history entry point prioritizes
// recent calls), truncate the outputs that exceed their share, and revoke their
// completion claims.
func distributeRemainingBudget(items []ExecutedToolCallCarrier, maxMetadataBytes int, prioritizeRecent bool, damagedCells map[string]bool) {
	remainingItems := 0
	for _, item := range items {
		if executedToolCallMetadataBytes(item) > 0 {
			remainingItems++
		}
	}
	remainingBytes := maxMetadataBytes
	for _, item := range items {
		if remainingItems == 0 {
			break
		}
		itemBytes := executedToolCallMetadataBytes(item)
		if itemBytes == 0 {
			continue
		}
		itemBudget := remainingBytes / remainingItems
		if prioritizeRecent {
			itemBudget = remainingBytes
		}
		if itemBytes > itemBudget {
			if cell := executedToolCallCellID(item); cell != "" {
				damagedCells[cell] = true
			}
			clearExecutedToolCallsComplete(item)
			fieldBytes := executedToolCallMetadataFieldBytes()
			boundExecutedToolCallsWithBudget(item, itemBudget-minInt(itemBudget, fieldBytes))
		}
		remainingBytes -= minInt(remainingBytes, executedToolCallMetadataBytes(item))
		remainingItems--
	}
}

// boundExecutedToolCallsWithBudget mirrors Rust's
// `ExecutedToolCalls::bound_executed_tool_calls_with_budget`: keep the
// inventory's first call, record how many calls it represents, trim its name to
// fit, and drop the whole inventory when even that cannot fit.
func boundExecutedToolCallsWithBudget(item ExecutedToolCallCarrier, maxCallBytes int) {
	if item == nil {
		return
	}
	calls := item.ExecutedToolCalls()
	if len(calls) == 0 {
		clearCarrierExecutedToolCalls(item)
		return
	}
	representedCalls := 0
	for _, call := range calls {
		representedCalls++
		if call.truncation != nil && call.truncation.OmittedCalls != nil {
			representedCalls += *call.truncation.OmittedCalls
		}
	}
	call := calls[0]
	originalBytes := executedToolCallArgumentBytes(call)
	var originalNameBytes *int
	if call.truncation != nil {
		originalBytes = call.truncation.OriginalBytes
		originalNameBytes = call.truncation.OriginalNameBytes
	}
	var omittedCalls *int
	if representedCalls > 1 {
		value := representedCalls - 1
		omittedCalls = &value
	}
	maxBytes := minInt(maxCallBytes, MaxExecutedToolCallArgumentBytes)
	setExecutedToolCallTruncation(&call, originalBytes, maxBytes, omittedCalls, originalNameBytes)
	if jsonSize([]ExecutedToolCall{call}) > maxCallBytes {
		recordedName := originalNameBytes
		if recordedName == nil {
			value := len(call.Name)
			recordedName = &value
		}
		setExecutedToolCallTruncation(&call, originalBytes, maxBytes, omittedCalls, recordedName)
		// Removing UTF-8 name bytes saves at least that many serialized JSON bytes.
		nameLimit := len(call.Name) - (jsonSize([]ExecutedToolCall{call}) - maxCallBytes)
		if nameLimit < 0 {
			nameLimit = 0
		}
		for nameLimit > 0 && !utf8.ValidString(call.Name[:nameLimit]) {
			nameLimit--
		}
		call.Name = call.Name[:nameLimit]
	}
	if jsonSize([]ExecutedToolCall{call}) > maxCallBytes {
		clearCarrierExecutedToolCalls(item)
		return
	}
	item.ReplaceExecutedToolCalls([]ExecutedToolCall{call})
}

// clearCarrierExecutedToolCalls drops a carrier's whole inventory when even the
// truncated call cannot fit (Rust's `clear_executed_tool_calls`).
func clearCarrierExecutedToolCalls(item ExecutedToolCallCarrier) {
	if clearer, ok := item.(interface{ ClearExecutedToolCalls() }); ok {
		clearer.ClearExecutedToolCalls()
		return
	}
	item.ReplaceExecutedToolCalls(nil)
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
