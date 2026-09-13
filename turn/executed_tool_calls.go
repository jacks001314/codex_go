package turn

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	"codex_go/model"
	"codex_go/tool"
)

const (
	maxPendingExecutedToolCalls                 = 256
	maxExecutedToolCallFullArgumentBytesPerItem = 32 * 1024
	// maxRetainedDirectMetadataBytes limits the Direct metadata retained in
	// history across one recorder lifetime. A new process cannot recover the
	// prior budget because raw result metadata is not deserialized (Rust #45185).
	maxRetainedDirectMetadataBytes = 1024 * 1024
)

// ExecutedToolCallRecorder keeps best-effort attempted-tool metadata across
// turns in one thread. Calls are committed only after a sampling request
// succeeds, so transport retries and failed samples do not lose metadata.
type ExecutedToolCallRecorder struct {
	mu      sync.Mutex
	groups  map[string]*recordedToolCallGroup
	outputs map[string]string
	// lifetime identifies the current enabled recorder state; a prepared direct
	// call is only attached while its lifetime is still current, so disabling
	// capture invalidates records prepared before the change (Rust #45185).
	lifetime *executedToolCallLifetime
	// pendingDirectCalls counts prepared direct calls that have not been
	// attached or released yet.
	pendingDirectCalls int
	// retainedDirectMetadataBytes counts the direct metadata kept in history for
	// the retention budget above.
	retainedDirectMetadataBytes int
	// seenIDs tracks observed call and runtime cell IDs so reused or historical
	// IDs cannot be presented as fresh evidence (Rust #44472).
	seenIDs *seenIDs
	// historySeeded records that the first prompt's supplied history was
	// indexed. Rust indexes the session's initial history at construction; Go
	// records are created before the thread's first sampling request, so the
	// first request input is the equivalent supply.
	historySeeded bool
	// canProveWaitCompletion is false when the thread carried prior history, so
	// inherited wait handles cannot be distinguished from newly allocated ones.
	canProveWaitCompletion bool
	// pendingWrapperOrigins holds Code Mode exec/wait wrapper call IDs observed
	// before their cell is registered.
	pendingWrapperOrigins map[string]struct{}
	// invalidCells and invalidGroups retain revoked completeness until the
	// affected output commits; lost evidence cannot become complete again.
	invalidCells  map[string]struct{}
	invalidGroups map[string]struct{}
	invalidCalls  map[string]struct{}
	// startedCells records cells whose runtime handle was already observed, so a
	// later exec/wait registration for the same cell is not treated as reuse.
	startedCells map[string]struct{}
}

// executedToolCallLifetime is the identity token of one enabled recorder state.
// The type must not be zero-sized: Go may give distinct zero-sized allocations
// the same address, which would make separate generations compare equal.
type executedToolCallLifetime struct {
	generation uint64
}

var executedToolCallLifetimeGeneration atomic.Uint64

func newExecutedToolCallLifetime() *executedToolCallLifetime {
	return &executedToolCallLifetime{generation: executedToolCallLifetimeGeneration.Add(1)}
}

// ExecutedToolCallPermit reserves one pending direct-call recording slot for the
// invocation that owns it. Release frees the slot when the invocation finishes or
// is cancelled, mirroring Rust's DirectCallPermit drop.
type ExecutedToolCallPermit struct {
	lifetime *executedToolCallLifetime
	recorder *ExecutedToolCallRecorder
	released bool
}

// Release frees the reserved pending slot. It is safe to call once per permit.
func (p *ExecutedToolCallPermit) Release() {
	if p == nil || p.released || p.recorder == nil {
		return
	}
	p.released = true
	p.recorder.mu.Lock()
	defer p.recorder.mu.Unlock()
	if p.recorder.pendingDirectCalls > 0 {
		p.recorder.pendingDirectCalls--
	}
}

type recordedToolCallGroup struct {
	pending   []recordedToolCall
	fullBytes int
}

type recordedToolCall struct {
	call      model.ExecutedToolCall
	callID    string
	fullBytes int
}

type ExecutedToolCallAttachment struct {
	groups []executedToolCallGroupAttachment
}

// executedToolCallCellCarrier reports the Code Mode cell associated with an
// item's recorded calls; an item without one carries direct-call metadata.
type executedToolCallCellCarrier interface {
	ExecutedToolCallCellID() string
}

// executedToolCallCompletionSetter marks an item's recorded call inventory as
// complete (Rust ResponseItem::mark_tool_calls_complete).
type executedToolCallCompletionSetter interface {
	SetExecutedToolCallsComplete(bool)
}

// hasDirectCallMetadata reports whether an item already carries a direct
// (non-cell) executed-tool-call record (Rust has_direct_call_metadata).
func hasDirectCallMetadata(item any) bool {
	carrier, ok := item.(model.ExecutedToolCallCarrier)
	if !ok || len(carrier.ExecutedToolCalls()) == 0 {
		return false
	}
	if cell, ok := item.(executedToolCallCellCarrier); ok && strings.TrimSpace(cell.ExecutedToolCallCellID()) != "" {
		return false
	}
	return true
}

func attachDirectCallToItem(item any, call model.ExecutedToolCall, complete bool) bool {
	carrier, ok := item.(model.ExecutedToolCallCarrier)
	if !ok {
		return false
	}
	carrier.ReplaceExecutedToolCalls(append(carrier.ExecutedToolCalls(), call))
	if complete {
		if setter, ok := item.(executedToolCallCompletionSetter); ok {
			setter.SetExecutedToolCallsComplete(true)
		}
	}
	return true
}

func clearToolResultMetadataForItem(item any) {
	if clearer, ok := item.(interface{ ClearToolResultMetadata() }); ok {
		clearer.ClearToolResultMetadata()
	}
}

func clearExecutedToolCallsForItem(item any) {
	if clearer, ok := item.(interface{ ClearExecutedToolCalls() }); ok {
		clearer.ClearExecutedToolCalls()
		return
	}
	if carrier, ok := item.(model.ExecutedToolCallCarrier); ok {
		carrier.ReplaceExecutedToolCalls(nil)
	}
}

func executedToolCallMetadataBytesForItem(item any) int {
	if carrier, ok := item.(model.ExecutedToolCallCarrier); ok {
		return model.ExecutedToolCallMetadataBytes(carrier)
	}
	return 0
}

type executedToolCallGroupAttachment struct {
	groupID string
	count   int
}

func NewExecutedToolCallRecorder() *ExecutedToolCallRecorder {
	return &ExecutedToolCallRecorder{
		seenIDs:                newSeenIDs(),
		canProveWaitCompletion: true,
	}
}

func (r *ExecutedToolCallRecorder) RecordToolCall(invocation *tool.Invocation, toolMode string) {
	if r == nil || invocation == nil || strings.TrimSpace(invocation.CallID) == "" {
		return
	}
	sourceCodeMode := strings.EqualFold(strings.TrimSpace(invocation.Source), "code_mode")
	if !sourceCodeMode && codeModeToolMetadataSkipped(invocation, toolMode) {
		// A Code Mode exec/wait wrapper is not recorded as a call, but its
		// identity still proves that the eventual cell origin is fresh.
		r.observeWrapperOrigin(invocation.CallID)
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
		r.recordNested(groupID, invocation.CallID, call, originalBytes)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	if !r.seenIDs.observeCallID(invocation.CallID) {
		r.invalidateCall(invocation.CallID)
	}
	// Direct records are attached to their invocation's own output, so the
	// dispatcher records them through PrepareDirectCall instead. Only the ID
	// observation above (which revokes completeness on reuse) remains here.
}

// PrepareDirectCall mirrors Rust's ExecutedToolCalls::prepare_direct_call: it
// reserves a pending slot and snapshots the invocation so the dispatcher can
// attach the record to that invocation's output before it enters history. A nil
// result means capture is disabled, the pending budget is exhausted, or the call
// is a Code Mode exec/wait wrapper (whose cell carries its own metadata).
func (r *ExecutedToolCallRecorder) PrepareDirectCall(invocation *tool.Invocation, toolMode string) (*model.ExecutedToolCall, *ExecutedToolCallPermit) {
	if r == nil || invocation == nil || strings.TrimSpace(invocation.CallID) == "" {
		return nil, nil
	}
	if codeModeToolMetadataSkipped(invocation, toolMode) {
		return nil, nil
	}
	call, _ := executedToolCallFromInvocation(invocation)
	if strings.TrimSpace(call.Name) == "" {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	if r.lifetime == nil || r.pendingDirectCalls >= maxPendingExecutedToolCalls {
		return nil, nil
	}
	r.pendingDirectCalls++
	return &call, &ExecutedToolCallPermit{lifetime: r.lifetime, recorder: r}
}

// AttachDirectCallToOutput mirrors Rust's attach_direct_call_to_output: the
// prepared record joins the invocation's output item, the item is marked
// complete when the arguments were fully recorded, and the retained-metadata
// budget drops tool-result metadata first and the record second.
func (r *ExecutedToolCallRecorder) AttachDirectCallToOutput(item any, call *model.ExecutedToolCall, permit *ExecutedToolCallPermit) {
	if r == nil || item == nil || call == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lifetime == nil || permit == nil || permit.lifetime != r.lifetime {
		return
	}
	complete := !call.Truncated()
	if !attachDirectCallToItem(item, *call, complete) {
		return
	}
	available := maxRetainedDirectMetadataBytes - r.retainedDirectMetadataBytes
	bytes := executedToolCallMetadataBytesForItem(item)
	if bytes > available {
		clearToolResultMetadataForItem(item)
		bytes = executedToolCallMetadataBytesForItem(item)
	}
	if bytes > available {
		clearExecutedToolCallsForItem(item)
		return
	}
	r.retainedDirectMetadataBytes += bytes
}

// ObserveNonDispatchedCall mirrors Rust's observe_non_dispatched_call: a call ID
// from a response item that bypassed local tool dispatch is recorded so its
// later reuse cannot establish Code Mode completeness.
func (r *ExecutedToolCallRecorder) ObserveNonDispatchedCall(item any) {
	if r == nil || item == nil {
		return
	}
	_, callID, _, _, ok := executedToolCallInputInfo(item)
	if !ok || strings.TrimSpace(callID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	if !r.seenIDs.observeCallID(callID) {
		r.invalidateCall(callID)
	}
}

// StripDirectMetadataWhenDisabled mirrors Rust's strip_disabled_direct_metadata:
// with capture disabled, direct records already present in history are removed
// from the supplied items so inference (and compaction) cannot replay them.
func (r *ExecutedToolCallRecorder) StripDirectMetadataWhenDisabled(items []any) {
	if r == nil {
		StripDirectCallMetadata(items)
		return
	}
	r.mu.Lock()
	enabled := r.lifetime != nil
	r.mu.Unlock()
	if !enabled {
		StripDirectCallMetadata(items)
	}
}

// StripDirectCallMetadata removes direct-call metadata (records without a Code
// Mode cell) from the supplied prompt items (Rust #45185
// clear_direct_call_metadata).
func StripDirectCallMetadata(items []any) {
	for _, item := range items {
		if !hasDirectCallMetadata(item) {
			continue
		}
		clearExecutedToolCallsForItem(item)
	}
}

func (r *ExecutedToolCallRecorder) recordNested(groupID string, callID string, call model.ExecutedToolCall, originalBytes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	freshCallID := r.seenIDs.observeCallID(callID)
	pendingCount := r.pendingNestedCalls()
	if pendingCount > maxPendingExecutedToolCalls || (len(r.groups) >= maxPendingExecutedToolCalls && r.groups[groupID] == nil) {
		r.invalidateGroup(groupID)
		return
	}
	group := r.groups[groupID]
	if group == nil {
		group = &recordedToolCallGroup{}
		r.groups[groupID] = group
	}
	duplicate := false
	for index := range group.pending {
		if group.pending[index].callID == strings.TrimSpace(callID) {
			duplicate = true
			break
		}
	}
	maxBytes := model.MaxExecutedToolCallArgumentBytes
	remaining := maxExecutedToolCallFullArgumentBytesPerItem - group.fullBytes
	if remaining < maxBytes {
		maxBytes = remaining
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	recorded := recordedToolCall{call: call, callID: strings.TrimSpace(callID)}
	if pendingCount == maxPendingExecutedToolCalls {
		recorded.call = model.NewTruncatedExecutedToolCall(call.Name, originalBytes, 0)
		r.invalidateGroup(groupID)
	} else if originalBytes <= maxBytes {
		recorded.fullBytes = originalBytes
		group.fullBytes += originalBytes
	} else {
		recorded.call = model.NewTruncatedExecutedToolCall(call.Name, originalBytes, maxBytes)
		r.invalidateGroup(groupID)
	}
	// A duplicate call ID cannot be proven to belong to this cell, so revoke
	// completeness while retaining the recorded attempt (Rust #44472).
	if duplicate || !freshCallID {
		r.invalidateGroup(groupID)
	}
	group.pending = append(group.pending, recorded)
}

// RecordToolResultSources attaches host-generated analytics evidence to the
// matching Code Mode executed-tool call (Rust #45185 restricts result metadata
// to nested calls; direct records carry it from their own invocation instead).
// Source data only replaces an existing call and is ignored when the call was
// compacted away or the result arrived for a different retry copy.
func (r *ExecutedToolCallRecorder) RecordToolResultSources(invocation *tool.Invocation, sources model.ToolResultSources) bool {
	if r == nil || invocation == nil || strings.TrimSpace(invocation.CallID) == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(invocation.Source), "code_mode") {
		return false
	}
	callID := strings.TrimSpace(invocation.CallID)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	groupID := codeModeInvocationGroupID(invocation)
	if groupID == "" {
		return false
	}
	group := r.groups[groupID]
	if group == nil {
		return false
	}
	for index := range group.pending {
		if group.pending[index].callID == callID {
			return group.pending[index].call.SetToolResultSources(sources)
		}
	}
	return false
}

// RecordToolResultMetadata attaches a host-recorded MCP `_meta` snapshot to the
// matching Code Mode executed-tool call (Rust #44336/#45185). MCP capture is
// disabled today, so this mirrors the Rust recorder for when it is enabled; the
// snapshot is bounded and never trusted from serialized input.
func (r *ExecutedToolCallRecorder) RecordToolResultMetadata(invocation *tool.Invocation, metadata any) bool {
	if r == nil || invocation == nil || strings.TrimSpace(invocation.CallID) == "" || metadata == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(invocation.Source), "code_mode") {
		return false
	}
	bounded := model.NewToolResultMetadata(metadata)
	hasMetadata := bounded.IsSome()
	callID := strings.TrimSpace(invocation.CallID)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	groupID := codeModeInvocationGroupID(invocation)
	if groupID == "" {
		return false
	}
	group := r.groups[groupID]
	if group == nil {
		return false
	}
	for index := range group.pending {
		if group.pending[index].callID == callID {
			group.pending[index].call.SetToolResultMetadata(bounded)
			return hasMetadata
		}
	}
	return false
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
		r.invalidateGroup(groupID)
		return
	}
	freshOrigin := r.observeOrigin(outputCallID)
	freshCell := true
	if cellID := strings.TrimPrefix(groupID, "cell:"); cellID != groupID {
		if _, started := r.startedCells[cellID]; !started {
			if len(r.startedCells) < maxPendingExecutedToolCalls {
				r.startedCells[cellID] = struct{}{}
			}
			if !r.seenIDs.observeRuntimeCellID(cellID) {
				freshCell = false
			}
		}
	}
	if !freshOrigin || !freshCell {
		r.invalidateGroup(groupID)
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
	r.ensureState()
	r.seedHistoryOnce(out)
	if len(r.outputs) == 0 {
		return out, nil
	}
	attachment := &ExecutedToolCallAttachment{}
	seenGroups := map[string]struct{}{}
	outputCounts := map[string]int{}
	for _, item := range out {
		if _, callID, ok := executedToolCallOutputIdentity(item); ok && callID != "" {
			outputCounts[callID]++
		}
	}
	// The input index is None when the same call ID appears twice, matching
	// Rust's input_indices map (Rust #44472).
	inputIndices := map[string]*int{}
	for index, item := range out {
		_, inputCallID, _, _, ok := executedToolCallInputInfo(item)
		if !ok || inputCallID == "" {
			continue
		}
		if _, exists := inputIndices[inputCallID]; exists {
			inputIndices[inputCallID] = nil
			continue
		}
		position := index
		inputIndices[inputCallID] = &position
	}
	for index := len(out) - 1; index >= 0; index-- {
		// Direct records are attached to their own output before history, so
		// items that already carry one are not re-attached (Rust #45185).
		if hasDirectCallMetadata(out[index]) {
			continue
		}
		_, callID, ok := executedToolCallOutputIdentity(out[index])
		if !ok || callID == "" {
			continue
		}
		calls := make([]model.ExecutedToolCall, 0, 4)
		cellID := ""
		// Completeness requires evidence that the supplied history was indexed
		// and that no reused or ambiguous ID revoked it (Rust #44472).
		complete := r.historyIndexed()
		groupID := r.outputs[callID]
		if r.callInvalid(callID) {
			complete = false
		}
		if groupID != "" {
			if strings.HasPrefix(groupID, "cell:") {
				cellID = strings.TrimPrefix(groupID, "cell:")
			}
			if r.groupInvalid(groupID) || outputCounts[callID] > 1 {
				// Lost, reused, or duplicated evidence cannot become complete again.
				complete = false
			}
			if !r.canProveWaitCompletion && !executedToolCallOutputIsCustom(out[index]) {
				// Inherited wait handles cannot be proven after resume or fork.
				complete = false
			}
			if cellID != "" {
				// An exec/wait output must still be identified by its matching
				// input; a missing or ambiguous association cannot be repaired by
				// a later wait (Rust #44472).
				if inputIndex, present := inputIndices[callID]; !present || inputIndex == nil || *inputIndex >= index ||
					!codeModeInputMatchesOutput(out[*inputIndex], out[index], callID, cellID) {
					complete = false
				}
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
	if len(attachment.groups) == 0 {
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
			delete(r.invalidGroups, attached.groupID)
			if cellID := strings.TrimPrefix(attached.groupID, "cell:"); cellID != attached.groupID {
				delete(r.invalidCells, cellID)
			}
		}
		for outputCallID, groupID := range r.outputs {
			if groupID == attached.groupID {
				delete(r.outputs, outputCallID)
			}
		}
	}
}

func (r *ExecutedToolCallRecorder) ensureState() {
	if r.groups == nil {
		r.groups = map[string]*recordedToolCallGroup{}
	}
	if r.lifetime == nil {
		r.lifetime = newExecutedToolCallLifetime()
	}
	if r.outputs == nil {
		r.outputs = map[string]string{}
	}
	if r.seenIDs == nil {
		r.seenIDs = newSeenIDs()
	}
	if r.pendingWrapperOrigins == nil {
		r.pendingWrapperOrigins = map[string]struct{}{}
	}
	if r.invalidCells == nil {
		r.invalidCells = map[string]struct{}{}
	}
	if r.invalidGroups == nil {
		r.invalidGroups = map[string]struct{}{}
	}
	if r.invalidCalls == nil {
		r.invalidCalls = map[string]struct{}{}
	}
	if r.startedCells == nil {
		r.startedCells = map[string]struct{}{}
	}
}

// observeWrapperOrigin records a Code Mode exec/wait wrapper call whose cell is
// not known yet. A reused wrapper ID is not remembered, so the eventual cell
// sees a non-fresh origin and withholds completeness (Rust #44472).
func (r *ExecutedToolCallRecorder) observeWrapperOrigin(callID string) {
	callID = strings.TrimSpace(callID)
	if r == nil || callID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	if !r.seenIDs.observeCallID(callID) {
		return
	}
	if len(r.pendingWrapperOrigins) < maxPendingExecutedToolCalls {
		r.pendingWrapperOrigins[callID] = struct{}{}
	}
}

// observeOrigin consumes a wrapper origin recorded at submission, otherwise
// treats the output call ID as a fresh observation.
func (r *ExecutedToolCallRecorder) observeOrigin(callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false
	}
	if _, ok := r.pendingWrapperOrigins[callID]; ok {
		delete(r.pendingWrapperOrigins, callID)
		return true
	}
	return r.seenIDs.observeCallID(callID)
}

func (r *ExecutedToolCallRecorder) invalidateCall(callID string) {
	callID = strings.TrimSpace(callID)
	if callID == "" || len(r.invalidCalls) >= maxPendingExecutedToolCalls {
		return
	}
	r.invalidCalls[callID] = struct{}{}
}

// invalidateGroup revokes completeness for one cell or output grouping. The
// revocation is sticky: lost or ambiguous evidence cannot become complete again.
func (r *ExecutedToolCallRecorder) invalidateGroup(groupID string) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return
	}
	if cellID := strings.TrimPrefix(groupID, "cell:"); cellID != groupID {
		if len(r.invalidCells) < maxPendingExecutedToolCalls {
			r.invalidCells[cellID] = struct{}{}
		}
		return
	}
	if len(r.invalidGroups) < maxPendingExecutedToolCalls {
		r.invalidGroups[groupID] = struct{}{}
	}
}

func (r *ExecutedToolCallRecorder) groupInvalid(groupID string) bool {
	if groupID == "" {
		return false
	}
	if _, ok := r.invalidGroups[groupID]; ok {
		return true
	}
	if cellID := strings.TrimPrefix(groupID, "cell:"); cellID != groupID {
		_, ok := r.invalidCells[cellID]
		return ok
	}
	return false
}

func (r *ExecutedToolCallRecorder) callInvalid(callID string) bool {
	_, ok := r.invalidCalls[strings.TrimSpace(callID)]
	return ok
}

func (r *ExecutedToolCallRecorder) historyIndexed() bool {
	return r.seenIDs != nil && r.seenIDs.historyIndexed()
}

// seedHistoryOnce indexes the thread's supplied history on the recorder's first
// prompt. The first sampling request happens before this turn records any call,
// so its input is the equivalent of Rust's InitialHistory.
func (r *ExecutedToolCallRecorder) seedHistoryOnce(items []any) {
	if r.historySeeded {
		return
	}
	r.historySeeded = true
	for _, item := range items {
		if len(historyObservedIDs(item)) > 0 {
			// Inherited runtime cell handles cannot be distinguished from newly
			// allocated ones after resume or fork (Rust #44472).
			r.canProveWaitCompletion = false
			break
		}
	}
	r.seenIDs.observeHistory(items)
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

// executedToolCallOutputIsCustom reports whether the output came from a custom
// (Code Mode) tool call. Only these outputs can prove wait completion when the
// thread carried prior history (Rust #44472).
func executedToolCallOutputIsCustom(value any) bool {
	itemType, _, ok := executedToolCallOutputIdentity(value)
	return ok && strings.TrimSpace(itemType) == "custom_tool_call_output"
}

// executedToolCallInputInfo extracts the identity of an input call item
// (function, custom, tool-search, or local-shell call). Unsupported shapes
// report ok=false, mirroring Rust's optional input accessors.
func executedToolCallInputInfo(value any) (itemType string, callID string, name string, arguments string, ok bool) {
	switch item := value.(type) {
	case *model.AgentItem:
		if item == nil || !executedToolCallInputType(item.Type) {
			return "", "", "", "", false
		}
		return item.Type, strings.TrimSpace(item.CallID), strings.TrimSpace(item.Name), item.Arguments, true
	case *ToolResponseItem:
		if item == nil || !executedToolCallInputType(item.Type) {
			return "", "", "", "", false
		}
		return item.Type, strings.TrimSpace(item.CallID), strings.TrimSpace(item.Name), "", true
	case *trustedExecutedToolCallMapItem:
		if item == nil {
			return "", "", "", "", false
		}
		return executedToolCallInputInfo(item.value)
	case map[string]any:
		itemType = strings.TrimSpace(mapString(item, "type"))
		if !executedToolCallInputType(itemType) {
			return "", "", "", "", false
		}
		return itemType, strings.TrimSpace(mapString(item, "call_id")), strings.TrimSpace(mapString(item, "name")), mapString(item, "arguments"), true
	default:
		return "", "", "", "", false
	}
}

func executedToolCallInputType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call":
		return true
	default:
		return false
	}
}

// codeModeInputMatchesOutput mirrors Rust's code_mode_input_matches_output: an
// exec custom call must own the cell output, while a wait function call must
// reference the same runtime cell. The Rust origin guard (the wait call ID
// differs from the exec call ID) is implied here because the matching input
// shares the output's call ID while the cell origin is the exec call ID.
func codeModeInputMatchesOutput(input any, output any, outputCallID string, runtimeCell string) bool {
	inputType, inputCallID, inputName, inputArguments, ok := executedToolCallInputInfo(input)
	if !ok || inputCallID != strings.TrimSpace(outputCallID) {
		return false
	}
	outputType, _, isOutput := executedToolCallOutputIdentity(output)
	if !isOutput {
		return false
	}
	switch strings.TrimSpace(outputType) {
	case "custom_tool_call_output":
		return inputType == "custom_tool_call" && isCodeModeExecName(inputName)
	case "function_call_output":
		if inputType != "function_call" || inputName != "wait" {
			return false
		}
		return decodedCellIDArgument(inputArguments) == runtimeCell
	default:
		return false
	}
}

func isCodeModeExecName(name string) bool {
	return strings.TrimSpace(name) == tool.CodeModeExecToolName
}

func decodedCellIDArgument(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
		return ""
	}
	cellID, _ := decoded["cell_id"].(string)
	return strings.TrimSpace(cellID)
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

// ExecutedToolCallCellID reports the Code Mode cell owning the item's recorded
// calls; empty means the record is a direct invocation's (Rust #45185).
func (i *trustedExecutedToolCallMapItem) ExecutedToolCallCellID() string {
	if i == nil {
		return ""
	}
	return strings.TrimSpace(i.cellID)
}

func (i *trustedExecutedToolCallMapItem) ClearExecutedToolCalls() {
	if i != nil {
		i.calls = nil
	}
}

func (i *trustedExecutedToolCallMapItem) ClearToolResultMetadata() {
	if i == nil {
		return
	}
	for index := range i.calls {
		i.calls[index].ClearToolResultMetadata()
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
