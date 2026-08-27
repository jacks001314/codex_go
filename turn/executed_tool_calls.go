package turn

import (
	"encoding/json"
	"strings"
	"sync"

	"codex_go/model"
	"codex_go/tool"
)

const (
	maxPendingExecutedToolCalls                 = 256
	maxExecutedToolCallFullArgumentBytesPerItem = 32 * 1024
)

// ExecutedToolCallRecorder keeps best-effort attempted-tool metadata across
// turns in one thread. Calls are committed only after a sampling request
// succeeds, so transport retries and failed samples do not lose metadata.
type ExecutedToolCallRecorder struct {
	mu      sync.Mutex
	direct  map[string]model.ExecutedToolCall
	groups  map[string]*recordedToolCallGroup
	outputs map[string]string
}

type recordedToolCallGroup struct {
	pending   []recordedToolCall
	fullBytes int
}

type recordedToolCall struct {
	call      model.ExecutedToolCall
	fullBytes int
}

type ExecutedToolCallAttachment struct {
	directCallIDs []string
	groups        []executedToolCallGroupAttachment
}

type executedToolCallGroupAttachment struct {
	groupID string
	count   int
}

func NewExecutedToolCallRecorder() *ExecutedToolCallRecorder {
	return &ExecutedToolCallRecorder{}
}

func (r *ExecutedToolCallRecorder) RecordToolCall(invocation *tool.Invocation, toolMode string) {
	if r == nil || invocation == nil || strings.TrimSpace(invocation.CallID) == "" {
		return
	}
	sourceCodeMode := strings.EqualFold(strings.TrimSpace(invocation.Source), "code_mode")
	if !sourceCodeMode && codeModeToolMetadataSkipped(invocation, toolMode) {
		return
	}
	call, originalBytes := executedToolCallFromInvocation(invocation)
	if strings.TrimSpace(call.Name) == "" {
		return
	}
	if sourceCodeMode {
		groupID := codeModeInvocationGroupID(invocation)
		if groupID == "" {
			return
		}
		r.recordNested(groupID, call, originalBytes)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	if len(r.direct) < maxPendingExecutedToolCalls {
		if _, exists := r.direct[invocation.CallID]; !exists {
			r.direct[invocation.CallID] = call
		}
		return
	}
	if len(r.direct) == maxPendingExecutedToolCalls {
		if _, exists := r.direct[invocation.CallID]; !exists {
			r.direct[invocation.CallID] = model.NewTruncatedExecutedToolCall(call.Name, originalBytes, 0)
		}
	}
}

func (r *ExecutedToolCallRecorder) recordNested(groupID string, call model.ExecutedToolCall, originalBytes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	pendingCount := r.pendingNestedCalls()
	if pendingCount > maxPendingExecutedToolCalls || (len(r.groups) >= maxPendingExecutedToolCalls && r.groups[groupID] == nil) {
		return
	}
	group := r.groups[groupID]
	if group == nil {
		group = &recordedToolCallGroup{}
		r.groups[groupID] = group
	}
	maxBytes := model.MaxExecutedToolCallArgumentBytes
	remaining := maxExecutedToolCallFullArgumentBytesPerItem - group.fullBytes
	if remaining < maxBytes {
		maxBytes = remaining
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	recorded := recordedToolCall{call: call}
	if pendingCount == maxPendingExecutedToolCalls {
		recorded.call = model.NewTruncatedExecutedToolCall(call.Name, originalBytes, 0)
	} else if originalBytes <= maxBytes {
		recorded.fullBytes = originalBytes
		group.fullBytes += originalBytes
	} else {
		recorded.call = model.NewTruncatedExecutedToolCall(call.Name, originalBytes, maxBytes)
	}
	group.pending = append(group.pending, recorded)
}

func (r *ExecutedToolCallRecorder) RegisterCell(cellID string, outputCallID string) {
	r.registerGroup("cell:"+strings.TrimSpace(cellID), outputCallID)
}

func (r *ExecutedToolCallRecorder) RegisterOutputCall(outputCallID string) {
	r.registerGroup("call:"+strings.TrimSpace(outputCallID), outputCallID)
}

func (r *ExecutedToolCallRecorder) registerGroup(groupID string, outputCallID string) {
	if r == nil || strings.TrimSpace(groupID) == "" || strings.TrimSpace(outputCallID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	if (len(r.groups) >= maxPendingExecutedToolCalls && r.groups[groupID] == nil) ||
		(len(r.outputs) >= maxPendingExecutedToolCalls && r.outputs[outputCallID] == "") {
		return
	}
	if r.groups[groupID] == nil {
		r.groups[groupID] = &recordedToolCallGroup{}
	}
	r.outputs[outputCallID] = groupID
}

// AttachPendingToPrompt returns request-local clones with metadata attached.
// CommitAttachment must be called only after the sampling request succeeds.
func (r *ExecutedToolCallRecorder) AttachPendingToPrompt(items []any) ([]any, *ExecutedToolCallAttachment) {
	out := append([]any(nil), items...)
	if r == nil || len(out) == 0 {
		return out, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.direct) == 0 && len(r.outputs) == 0 {
		return out, nil
	}
	attachment := &ExecutedToolCallAttachment{}
	seenDirect := map[string]struct{}{}
	seenGroups := map[string]struct{}{}
	for index := len(out) - 1; index >= 0; index-- {
		_, callID, ok := executedToolCallOutputIdentity(out[index])
		if !ok || callID == "" {
			continue
		}
		calls := make([]model.ExecutedToolCall, 0, 4)
		cellID := ""
		complete := true
		if call, exists := r.direct[callID]; exists {
			if _, seen := seenDirect[callID]; !seen {
				calls = append(calls, call)
				seenDirect[callID] = struct{}{}
				attachment.directCallIDs = append(attachment.directCallIDs, callID)
			}
		}
		if groupID := r.outputs[callID]; groupID != "" {
			if strings.HasPrefix(groupID, "cell:") {
				cellID = strings.TrimPrefix(groupID, "cell:")
			}
			if _, seen := seenGroups[groupID]; !seen {
				if group := r.groups[groupID]; group != nil && len(group.pending) > 0 {
					for _, pending := range group.pending {
						if pending.call.Truncated() {
							complete = false
						}
						calls = append(calls, pending.call)
					}
					seenGroups[groupID] = struct{}{}
					attachment.groups = append(attachment.groups, executedToolCallGroupAttachment{groupID: groupID, count: len(group.pending)})
				}
			}
		}
		if len(calls) > 0 {
			var completePtr *bool
			if strings.TrimSpace(cellID) != "" {
				completePtr = &complete
			}
			out[index] = clonePromptOutputWithExecutedToolCalls(out[index], calls, cellID, completePtr)
		}
	}
	if len(attachment.directCallIDs) == 0 && len(attachment.groups) == 0 {
		return out, nil
	}
	return out, attachment
}

func (r *ExecutedToolCallRecorder) CommitAttachment(attachment *ExecutedToolCallAttachment) {
	if r == nil || attachment == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, callID := range attachment.directCallIDs {
		delete(r.direct, callID)
	}
	for _, attached := range attachment.groups {
		group := r.groups[attached.groupID]
		if group == nil {
			continue
		}
		count := attached.count
		if count > len(group.pending) {
			count = len(group.pending)
		}
		group.pending = append([]recordedToolCall(nil), group.pending[count:]...)
		group.fullBytes = 0
		for _, pending := range group.pending {
			group.fullBytes += pending.fullBytes
		}
		if len(group.pending) == 0 {
			delete(r.groups, attached.groupID)
		}
		for outputCallID, groupID := range r.outputs {
			if groupID == attached.groupID {
				delete(r.outputs, outputCallID)
			}
		}
	}
}

func (r *ExecutedToolCallRecorder) ensureState() {
	if r.direct == nil {
		r.direct = map[string]model.ExecutedToolCall{}
	}
	if r.groups == nil {
		r.groups = map[string]*recordedToolCallGroup{}
	}
	if r.outputs == nil {
		r.outputs = map[string]string{}
	}
}

func (r *ExecutedToolCallRecorder) pendingNestedCalls() int {
	total := 0
	for _, group := range r.groups {
		total += len(group.pending)
	}
	return total
}

func codeModeToolMetadataSkipped(invocation *tool.Invocation, toolMode string) bool {
	mode := strings.ToLower(strings.TrimSpace(toolMode))
	if mode != model.ToolModeCodeMode && mode != model.ToolModeCodeModeOnly {
		return false
	}
	return invocation.ToolName.Namespace == "" &&
		(invocation.ToolName.Name == tool.CodeModeExecToolName || invocation.ToolName.Name == "wait")
}

func codeModeInvocationGroupID(invocation *tool.Invocation) string {
	if invocation == nil {
		return ""
	}
	if cellID := strings.TrimSpace(contextString(invocation.Context, tool.CodeModeCellIDContextKey)); cellID != "" {
		return "cell:" + cellID
	}
	if outputCallID := strings.TrimSpace(contextString(invocation.Context, tool.CodeModeOutputCallIDContextKey)); outputCallID != "" {
		return "call:" + outputCallID
	}
	return ""
}

func contextString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func executedToolCallFromInvocation(invocation *tool.Invocation) (model.ExecutedToolCall, int) {
	name := tool.ResponsesAPIName(invocation.ToolName)
	var arguments any
	originalBytes := 0
	switch invocation.Payload.Kind {
	case tool.PayloadCustom:
		arguments = invocation.Payload.Input
		encoded, err := json.Marshal(invocation.Payload.Input)
		if err != nil {
			originalBytes = int(^uint(0) >> 1)
		} else {
			originalBytes = len(encoded)
		}
	case tool.PayloadToolSearch:
		arguments = invocation.Payload.Search
		encoded, err := json.Marshal(arguments)
		if err != nil {
			originalBytes = int(^uint(0) >> 1)
		} else {
			originalBytes = len(encoded)
		}
	default:
		originalBytes = len(invocation.Payload.Arguments)
		if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &arguments); err != nil {
			arguments = invocation.Payload.Arguments
		}
	}
	if originalBytes > model.MaxExecutedToolCallArgumentBytes {
		return model.NewTruncatedExecutedToolCall(name, originalBytes, model.MaxExecutedToolCallArgumentBytes), originalBytes
	}
	return model.NewExecutedToolCall(name, arguments), originalBytes
}

func executedToolCallOutputIdentity(value any) (string, string, bool) {
	switch item := value.(type) {
	case *ToolResponseItem:
		if item == nil {
			return "", "", false
		}
		return item.Type, strings.TrimSpace(item.CallID), executedToolCallOutputType(item.Type)
	case *trustedExecutedToolCallMapItem:
		if item == nil {
			return "", "", false
		}
		return mapOutputIdentity(item.value)
	case map[string]any:
		return mapOutputIdentity(item)
	case *model.AgentItem:
		if item == nil {
			return "", "", false
		}
		return item.Type, strings.TrimSpace(item.CallID), executedToolCallOutputType(item.Type)
	default:
		return "", "", false
	}
}

func mapOutputIdentity(item map[string]any) (string, string, bool) {
	itemType, _ := item["type"].(string)
	callID, _ := item["call_id"].(string)
	return itemType, strings.TrimSpace(callID), executedToolCallOutputType(itemType)
}

func executedToolCallOutputType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		return true
	default:
		return false
	}
}

func clonePromptOutputWithExecutedToolCalls(value any, calls []model.ExecutedToolCall, cellID string, complete *bool) any {
	if carrier, ok := value.(model.ExecutedToolCallCarrier); ok {
		clone := carrier.CloneForExecutedToolCallPrompt()
		clone.ReplaceExecutedToolCalls(append(clone.ExecutedToolCalls(), calls...))
		if agentItem, okValue := clone.(*model.AgentItem); okValue {
			if strings.TrimSpace(cellID) != "" {
				agentItem.SetExecutedToolCallCell(cellID)
				agentItem.SetExecutedToolCallsComplete(*complete)
			}
			return clone
		}
		if toolItem, okValue := clone.(*ToolResponseItem); okValue {
			if strings.TrimSpace(cellID) != "" {
				toolItem.SetExecutedToolCallCell(cellID)
				toolItem.SetExecutedToolCallsComplete(*complete)
			}
			return clone
		}
		return clone
	}
	if item, ok := value.(map[string]any); ok {
		out := &trustedExecutedToolCallMapItem{value: cloneExecutedToolCallMap(item), calls: append([]model.ExecutedToolCall(nil), calls...)}
		if strings.TrimSpace(cellID) != "" {
			out.cellID = cellID
			out.complete = complete
		}
		return out
	}
	return value
}

type trustedExecutedToolCallMapItem struct {
	value    map[string]any
	calls    []model.ExecutedToolCall
	cellID   string
	complete *bool
}

func (i *trustedExecutedToolCallMapItem) ExecutedToolCalls() []model.ExecutedToolCall {
	if i == nil {
		return nil
	}
	return append([]model.ExecutedToolCall(nil), i.calls...)
}

func (i *trustedExecutedToolCallMapItem) ReplaceExecutedToolCalls(calls []model.ExecutedToolCall) {
	if i != nil {
		i.calls = append([]model.ExecutedToolCall(nil), calls...)
	}
}

func (i *trustedExecutedToolCallMapItem) CloneForExecutedToolCallPrompt() model.ExecutedToolCallCarrier {
	if i == nil {
		return (*trustedExecutedToolCallMapItem)(nil)
	}
	clone := &trustedExecutedToolCallMapItem{value: cloneExecutedToolCallMap(i.value), calls: append([]model.ExecutedToolCall(nil), i.calls...)}
	clone.cellID = i.cellID
	if i.complete != nil {
		value := *i.complete
		clone.complete = &value
	}
	return clone
}

func (i *trustedExecutedToolCallMapItem) MarshalJSON() ([]byte, error) {
	if i == nil {
		return []byte("null"), nil
	}
	return json.Marshal(mapWithExecutedToolCallMetadata(i.value, i.calls, i.cellID, i.complete))
}

func cloneExecutedToolCallMap(value map[string]any) map[string]any {
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	if metadata, ok := clone["internal_chat_message_metadata_passthrough"].(map[string]any); ok {
		metadataClone := make(map[string]any, len(metadata))
		for key, item := range metadata {
			if key != "executed_tool_calls" {
				metadataClone[key] = item
			}
		}
		if len(metadataClone) == 0 {
			delete(clone, "internal_chat_message_metadata_passthrough")
		} else {
			clone["internal_chat_message_metadata_passthrough"] = metadataClone
		}
	}
	return clone
}

func mapWithExecutedToolCalls(value map[string]any, calls []model.ExecutedToolCall) map[string]any {
	clone := cloneExecutedToolCallMap(value)
	if len(calls) == 0 {
		return clone
	}
	metadata, _ := clone["internal_chat_message_metadata_passthrough"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["executed_tool_calls"] = calls
	clone["internal_chat_message_metadata_passthrough"] = metadata
	return clone
}

func mapWithExecutedToolCallMetadata(value map[string]any, calls []model.ExecutedToolCall, cellID string, complete *bool) map[string]any {
	clone := cloneExecutedToolCallMap(value)
	if len(calls) == 0 && strings.TrimSpace(cellID) == "" && complete == nil {
		return clone
	}
	metadata, _ := clone["internal_chat_message_metadata_passthrough"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if len(calls) > 0 {
		metadata["executed_tool_calls"] = calls
	}
	if strings.TrimSpace(cellID) != "" {
		metadata["cell_id"] = cellID
	}
	if complete != nil {
		metadata["tool_calls_complete"] = *complete
	}
	clone["internal_chat_message_metadata_passthrough"] = metadata
	return clone
}
