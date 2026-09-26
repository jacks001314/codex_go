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
	// seenNestedIDs tracks nested Code Mode invocation IDs separately from the
	// shared ID filter, so the conservative duplicate filter cannot change
	// existing completeness decisions (Rust #48222).
	seenNestedIDs *seenIDs
	// retained binds the calls attached to each output call ID so later requests
	// re-emit them and a late result can still update the original output
	// (Rust RetainedToolCalls, #46044 and #48222).
	retained map[string]*retainedToolCallBinding
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
	// finished mirrors Rust's CellCompletion::Complete (#46081): the cell's
	// dispatch gate closed, so its inventory is final even when it is empty.
	finished bool
	// originCallID is the exec/wait call ID that first registered the cell
	// (Rust's RecordedCell::originating_call_id). It is the value written as the
	// item's metadata cell id, which names the originating call rather than the
	// runtime handle (Rust seen_ids.rs).
	originCallID string
	// runtimeCellID is the Code Mode runtime handle (Rust's CellId).
	runtimeCellID string
	// truncatedMetadataBindingValid records that this cell's observed identity
	// can bind a truncated call to an output for late result backfill
	// (Rust #48222).
	truncatedMetadataBindingValid bool
	// observedTruncatedCall records that the cell recorded at least one
	// truncated call, so a closed cell may still need its binding retained.
	observedTruncatedCall bool
	// incomplete mirrors Rust's CellCompletion::Incomplete: the inventory can no
	// longer be reported as complete (a truncated argument or an overflow), but
	// the cell's identity is still trusted for late truncated backfill.
	incomplete bool
	// dispatchClosed records that the cell's dispatch gate closed; a later
	// nested call for it cannot be trusted (Rust #48222).
	dispatchClosed bool
}

type recordedToolCall struct {
	call      model.ExecutedToolCall
	callID    string
	fullBytes int
}

// retainedToolCallBinding mirrors Rust's RetainedToolCalls: the calls attached to
// one output (keyed by the output's call ID) plus the indices that let a late
// accepted result update them and the next request re-emit them.
type retainedToolCallBinding struct {
	groupID string
	// originCallID is the originating exec/wait call ID (Rust's
	// RetainedToolCalls::cell_id), written as the item's metadata cell id.
	originCallID string
	// runtimeCellID is the Code Mode runtime handle (Rust's runtime_cell_id).
	runtimeCellID string
	calls         []recordedToolCall
	complete      bool
	// callIndexByID indexes calls whose arguments were recorded in full; a late
	// result may still update one of them.
	callIndexByID map[string]int
	// truncatedCallIndexByID indexes truncated calls that have no result yet, so
	// a late result can be bound to them (Rust #48222).
	truncatedCallIndexByID map[string]int
	// lateTruncatedIndices records which calls were updated by a late result, so
	// ambiguous attribution can clear exactly that evidence again.
	lateTruncatedIndices map[int]struct{}
}

// clearLateTruncatedMetadata removes the late-backfilled result evidence from a
// binding whose attribution became ambiguous. Once cleared, the binding stops
// accepting further late results (Rust RetainedToolCalls::clear_late_truncated_metadata).
func (b *retainedToolCallBinding) clearLateTruncatedMetadata() {
	if b == nil {
		return
	}
	for index := range b.lateTruncatedIndices {
		if index >= 0 && index < len(b.calls) {
			b.calls[index].call.SetToolResultMetadata(model.ToolResultMetadata{})
		}
	}
	b.lateTruncatedIndices = map[int]struct{}{}
	b.truncatedCallIndexByID = map[string]int{}
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
	groupID       string
	outputCallID  string
	originCallID  string
	runtimeCellID string
	count         int
	// calls is the attached inventory, kept so the commit can bind it to the
	// output for later re-emission and late-result backfill (Rust #48222).
	calls []recordedToolCall
	// complete is the completeness proved at attachment time; it is only
	// meaningful when the output carries a cell marker.
	complete bool
	hasCell  bool
}

func NewExecutedToolCallRecorder() *ExecutedToolCallRecorder {
	return &ExecutedToolCallRecorder{
		seenIDs:                newSeenIDs(),
		seenNestedIDs:          newSeenIDs(),
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

// AttachToCompactionPrompt mirrors Rust's attach_to_compaction_prompt (#46044):
// compaction prompts carry the recorded Code Mode tool inventory. With capture
// disabled, direct records already present in history are removed so compaction
// cannot replay them; otherwise the pending Code Mode observations are attached
// without consuming them, because a compaction request is not the normal
// sampling window that commits an attachment.
//
// Go's recorder state exists per thread and is created on the first recorded
// call, so the caller passes the session's metadata enablement explicitly
// (Rust reads the recorder's own state existence).
func (r *ExecutedToolCallRecorder) AttachToCompactionPrompt(items []any, enabled bool) []any {
	if !enabled {
		StripDirectCallMetadata(items)
		return items
	}
	if r == nil {
		return items
	}
	attached, _ := r.AttachPendingToPrompt(items)
	return attached
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
	trimmedCallID := strings.TrimSpace(callID)
	// Nested invocation IDs use their own filter so tracking them cannot change
	// the completeness of existing observations (Rust #48222).
	freshCallID := r.seenNestedIDs.observeCallID(trimmedCallID)
	if !freshCallID {
		// A repeated nested ID makes the late-result binding ambiguous, so drop
		// the backfilled evidence before losing the binding that identified it.
		for _, binding := range r.retained {
			if _, ok := binding.truncatedCallIndexByID[trimmedCallID]; ok {
				binding.clearLateTruncatedMetadata()
			}
		}
	}
	// A closed dispatch gate cannot accept a new nested call, and neither can a
	// cell whose identity was already revoked (Rust #48222).
	if group := r.groups[groupID]; group != nil && group.dispatchClosed {
		r.invalidateGroup(groupID)
	}
	pendingCount := r.pendingNestedCalls()
	if pendingCount > maxPendingExecutedToolCalls || (len(r.groups) >= maxPendingExecutedToolCalls && r.groups[groupID] == nil) {
		if group := r.groups[groupID]; group != nil {
			// Rust revokes completeness (CellCompletion::Incomplete) but keeps
			// the cell's identity trusted.
			group.incomplete = true
		}
		return
	}
	atPendingCallLimit := pendingCount == maxPendingExecutedToolCalls
	group := r.groups[groupID]
	if group == nil {
		group = &recordedToolCallGroup{}
		r.groups[groupID] = group
	}
	duplicate := false
	duplicateIndex := -1
	for index := range group.pending {
		if group.pending[index].callID == trimmedCallID {
			duplicate = true
			duplicateIndex = index
			break
		}
	}
	if !freshCallID || duplicate {
		// A duplicate or repeated ID cannot be proven to belong to this cell, so
		// its truncated calls cannot be bound to an output for late backfill.
		group.truncatedMetadataBindingValid = false
	}
	maxBytes := model.MaxExecutedToolCallArgumentBytes
	remaining := maxExecutedToolCallFullArgumentBytesPerItem - group.fullBytes
	if remaining < maxBytes {
		maxBytes = remaining
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	recorded := recordedToolCall{call: call, callID: trimmedCallID}
	if atPendingCallLimit {
		recorded.call = model.NewTruncatedExecutedToolCall(call.Name, originalBytes, 0)
		group.incomplete = true
	} else if originalBytes <= maxBytes {
		recorded.fullBytes = originalBytes
		group.fullBytes += originalBytes
	} else {
		recorded.call = model.NewTruncatedExecutedToolCall(call.Name, originalBytes, maxBytes)
		group.incomplete = true
	}
	// A duplicate call ID cannot be proven to belong to this cell, so revoke
	// completeness while retaining the recorded attempt (Rust #44472).
	if duplicate || !freshCallID {
		r.invalidateGroup(groupID)
	}
	group.observedTruncatedCall = group.observedTruncatedCall || recorded.call.Truncated()
	// A repeated ID replaces its earlier attempt instead of counting against the
	// pending budget twice (Rust #48222).
	if duplicate {
		group.pending[duplicateIndex] = recorded
		return
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
	runtimeCellID := strings.TrimPrefix(groupID, "cell:")
	if runtimeCellID == groupID {
		runtimeCellID = ""
	}
	if group := r.groups[groupID]; group != nil {
		for index := range group.pending {
			if group.pending[index].callID == callID {
				group.pending[index].call.SetToolResultMetadata(bounded)
				return hasMetadata
			}
		}
	}
	// The call may already be bound to an output the current request attached
	// (Rust #48222). A late result updates that binding so the original output
	// keeps the evidence instead of dropping it with the pending inventory.
	if runtimeCellID == "" {
		return false
	}
	for _, binding := range r.retained {
		if binding.runtimeCellID != runtimeCellID {
			continue
		}
		index, ok := binding.callIndexByID[callID]
		if !ok {
			continue
		}
		binding.calls[index].call.SetToolResultMetadata(bounded)
		return hasMetadata
	}
	var candidate *retainedToolCallBinding
	candidateIndex := -1
	candidates := 0
	for _, binding := range r.retained {
		if binding.runtimeCellID != runtimeCellID {
			continue
		}
		index, ok := binding.truncatedCallIndexByID[callID]
		if !ok {
			continue
		}
		candidates++
		candidate = binding
		candidateIndex = index
	}
	// Only an unambiguous truncated binding may be backfilled; a conflicting
	// output leaves the result unattributed (Rust #48222).
	if candidates != 1 || candidate == nil {
		return false
	}
	candidate.lateTruncatedIndices[candidateIndex] = struct{}{}
	candidate.calls[candidateIndex].call.SetToolResultMetadata(bounded)
	return hasMetadata
}

func (r *ExecutedToolCallRecorder) RegisterCell(cellID string, outputCallID string) {
	r.registerGroup("cell:"+strings.TrimSpace(cellID), outputCallID, false)
}

// StartCell mirrors Rust `start_cell` (#48222): a new Code Mode execution takes
// the runtime handle, so a reused runtime ID releases the previous execution's
// pending calls, output mappings and late-result bindings instead of inheriting
// them. The wait path registers into an already started cell with RegisterCell.
func (r *ExecutedToolCallRecorder) StartCell(cellID string, outputCallID string) {
	r.registerGroup("cell:"+strings.TrimSpace(cellID), outputCallID, true)
}

// FinishCell mirrors Rust `finish_cell_recording` (#46081): once the cell's
// dispatch gate closes, a losslessly recorded inventory is final - even when it
// contains no tool calls - so the next request can mark it complete with an
// explicit (possibly empty) executed_tool_calls list.
func (r *ExecutedToolCallRecorder) FinishCell(cellID string) {
	if r == nil {
		return
	}
	cellID = strings.TrimSpace(cellID)
	if cellID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	groupID := "cell:" + cellID
	group := r.groups[groupID]
	if group == nil {
		return
	}
	// An incomplete inventory never becomes complete, even when the dispatch gate
	// closes (Rust CellCompletion::Incomplete); it still attaches its partial
	// records without the marker.
	group.finished = !group.incomplete
	group.dispatchClosed = true
	// A closed cell keeps its original binding while late truncated results
	// remain eligible for backfill; an unverified empty cell is dropped
	// (Rust #48222).
	if len(group.pending) == 0 && !r.hasRetainedTruncatedForCell(cellID) && (group.incomplete || r.groupInvalid(groupID)) {
		delete(r.groups, groupID)
		for outputCallID, mapped := range r.outputs {
			if mapped == groupID {
				delete(r.outputs, outputCallID)
			}
		}
	}
}

func (r *ExecutedToolCallRecorder) RegisterOutputCall(outputCallID string) {
	r.registerGroup("call:"+strings.TrimSpace(outputCallID), outputCallID, false)
}

func (r *ExecutedToolCallRecorder) registerGroup(groupID string, outputCallID string, forcedStart bool) {
	if r == nil || strings.TrimSpace(groupID) == "" || strings.TrimSpace(outputCallID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureState()
	// Rust #46712: an ended empty cell can leave an output mapping behind with
	// nothing left to attach. Reclaim those mappings under mapping pressure so
	// orphaned evidence cannot exhaust the budget fresh calls need.
	if len(r.outputs) >= maxPendingExecutedToolCalls {
		r.reclaimOrphanedOutputs()
	}
	// Rust #46712: under group or pending-call pressure, evict finished groups
	// that no output mapping can reach any more, revoking their completeness and
	// releasing their pending-call budget. The group whose output is about to
	// make the late records attachable again is never discarded, and late
	// records are kept until pressure requires the cleanup.
	for r.capacityPressure(groupID) {
		if !r.evictFinishedGroup(groupID) {
			break
		}
	}
	if (len(r.groups) >= maxPendingExecutedToolCalls && r.groups[groupID] == nil) ||
		(len(r.outputs) >= maxPendingExecutedToolCalls && r.outputs[outputCallID] == "") {
		r.invalidateGroup(groupID)
		return
	}
	freshCell := true
	runtimeCellID := strings.TrimPrefix(groupID, "cell:")
	if runtimeCellID == groupID {
		runtimeCellID = ""
	}
	started := false
	if runtimeCellID != "" {
		if _, alreadyStarted := r.startedCells[runtimeCellID]; forcedStart || !alreadyStarted {
			started = true
			if len(r.startedCells) < maxPendingExecutedToolCalls {
				r.startedCells[runtimeCellID] = struct{}{}
			}
			if !r.seenIDs.observeRuntimeCellID(runtimeCellID) {
				freshCell = false
			}
		}
	}
	freshOrigin := r.observeOrigin(outputCallID)
	if !freshCell {
		// A reused runtime ID cannot distinguish the old pending calls or output
		// mappings from this new execution, so release them instead of attaching
		// them to it (Rust #48222).
		r.dropReusedCell(runtimeCellID)
	}
	if !freshOrigin || !freshCell {
		r.invalidateGroup(groupID)
	}
	if r.groups[groupID] == nil {
		r.groups[groupID] = &recordedToolCallGroup{}
	}
	group := r.groups[groupID]
	if started {
		// Rust's start_cell owns these flags; a later registration (register_cell)
		// must not reset a closed dispatch gate.
		group.dispatchClosed = false
		group.truncatedMetadataBindingValid = freshOrigin && freshCell && r.historyIndexed()
	}
	if strings.TrimSpace(group.originCallID) == "" && runtimeCellID != "" {
		group.originCallID = strings.TrimSpace(outputCallID)
	}
	group.runtimeCellID = runtimeCellID
	r.outputs[outputCallID] = groupID
}

// reclaimOrphanedOutputs drops output mappings whose group no longer exists, so
// an ended cell that already attached its records stops consuming the mapping
// budget (Rust #46712).
func (r *ExecutedToolCallRecorder) reclaimOrphanedOutputs() {
	for outputCallID, groupID := range r.outputs {
		if r.groups[groupID] == nil {
			delete(r.outputs, outputCallID)
		}
	}
}

// capacityPressure mirrors Rust #46712's registration loop condition: the
// recorder must make room when the group budget is exhausted for a group that
// is not registered yet, or when the pending nested calls reach the budget.
func (r *ExecutedToolCallRecorder) capacityPressure(groupID string) bool {
	return (len(r.groups) >= maxPendingExecutedToolCalls && r.groups[groupID] == nil) ||
		r.pendingNestedCalls() >= maxPendingExecutedToolCalls
}

// evictFinishedGroup removes one finished group that no output mapping reaches
// any more, revoking its completeness (lost evidence cannot become complete
// again) and releasing the pending calls it held. The group being registered is
// protected because its output is what makes its own late records attachable
// (Rust #46712). The lowest group ID is evicted when several qualify, so the
// choice does not depend on Go's map iteration order.
func (r *ExecutedToolCallRecorder) evictFinishedGroup(protectedGroupID string) bool {
	mapped := make(map[string]struct{}, len(r.outputs))
	for _, groupID := range r.outputs {
		mapped[groupID] = struct{}{}
	}
	candidate := ""
	for groupID, group := range r.groups {
		if groupID == protectedGroupID || group == nil {
			continue
		}
		// Rust evicts cells that are Complete or Incomplete: a finished
		// inventory, or one whose completeness was already revoked.
		if !group.finished && !group.incomplete && !r.groupInvalid(groupID) {
			continue
		}
		if _, reachable := mapped[groupID]; reachable {
			continue
		}
		if candidate == "" || groupID < candidate {
			candidate = groupID
		}
	}
	if candidate == "" {
		return false
	}
	r.invalidateGroup(candidate)
	delete(r.groups, candidate)
	return true
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
	if len(r.outputs) == 0 && len(r.retained) == 0 {
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
	r.invalidateUntrustedTruncatedBindings(out, outputCounts, inputIndices)
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
		// A retained binding re-emits the output's inventory on every request, so
		// evidence attached before the cell finished (or before a late result
		// arrived) is not dropped from later requests (Rust #46044/#48222).
		if binding := r.retained[callID]; binding != nil {
			attachRetainedBinding(r, out, index, callID, binding, outputCounts, inputIndices)
			continue
		}
		calls := make([]model.ExecutedToolCall, 0, 4)
		recorded := make([]recordedToolCall, 0, 4)
		cellID := ""
		groupFinished := false
		// Completeness requires evidence that the supplied history was indexed
		// and that no reused or ambiguous ID revoked it (Rust #44472).
		complete := r.historyIndexed()
		groupID := r.outputs[callID]
		if r.callInvalid(callID) {
			complete = false
		}
		var group *recordedToolCallGroup
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
			if group = r.groups[groupID]; group != nil {
				groupFinished = group.finished
			}
			// Rust #46081: only a finished cell is complete; a still-running
			// cell attaches its partial inventory without the marker.
			complete = complete && groupFinished
			if _, seen := seenGroups[groupID]; !seen && group != nil {
				if len(group.pending) > 0 || groupFinished {
					for _, pending := range group.pending {
						if pending.call.Truncated() {
							complete = false
						}
						calls = append(calls, pending.call)
						recorded = append(recorded, pending)
					}
					seenGroups[groupID] = struct{}{}
					attachment.groups = append(attachment.groups, executedToolCallGroupAttachment{
						groupID:       groupID,
						outputCallID:  callID,
						originCallID:  group.originCallID,
						runtimeCellID: group.runtimeCellID,
						count:         len(group.pending),
						calls:         recorded,
						complete:      complete,
						hasCell:       strings.TrimSpace(cellID) != "",
					})
				}
			}
		}
		// Rust #46081: a complete inventory is attached even when it is empty, so
		// an explicit `[]` reaches the model instead of omitting the marker.
		if len(calls) > 0 || groupFinished {
			var completePtr *bool
			metadataCellID := ""
			if group != nil {
				metadataCellID = group.originCallID
			}
			if strings.TrimSpace(metadataCellID) != "" {
				completePtr = &complete
			}
			out[index] = clonePromptOutputWithExecutedToolCalls(out[index], calls, metadataCellID, completePtr)
		}
	}
	if len(attachment.groups) == 0 {
		return out, nil
	}
	// Rust's attach stage ("retained") bounds the request items once they carry
	// more than the per-output gate, preserving the newest calls so an older
	// call cannot displace them (the retained-inventory bound).
	if model.ExecutedToolCallMetadataTotalBytes(out) > maxExecutedToolCallFullArgumentBytesPerItem {
		out = model.BoundExecutedToolCallsForPromptPrioritizingRecent(out)
	}
	return out, attachment
}

// attachRetainedBinding re-emits the calls already bound to an output and
// re-validates the completeness proof they carry. A binding whose output or
// input became ambiguous loses its completeness on the spot (Rust #48222).
func attachRetainedBinding(r *ExecutedToolCallRecorder, out []any, index int, callID string, binding *retainedToolCallBinding, outputCounts map[string]int, inputIndices map[string]*int) {
	metadataCellID := strings.TrimSpace(binding.originCallID)
	complete := binding.complete
	if outputCounts[callID] > 1 {
		complete = false
		binding.complete = false
	}
	if binding.runtimeCellID != "" {
		if !r.canProveWaitCompletion && !executedToolCallOutputIsCustom(out[index]) {
			complete = false
		}
		if inputIndex, present := inputIndices[callID]; present {
			// A verified output may outlive its input after compaction, but any
			// input still present must keep identifying the same execution.
			if inputIndex == nil || *inputIndex >= index ||
				!codeModeInputMatchesOutput(out[*inputIndex], out[index], callID, binding.runtimeCellID) {
				complete = false
				binding.complete = false
			}
		}
	}
	if metadataCellID == "" && len(binding.calls) == 0 {
		return
	}
	calls := make([]model.ExecutedToolCall, 0, len(binding.calls))
	for _, call := range binding.calls {
		calls = append(calls, call.call)
	}
	var completePtr *bool
	if metadataCellID != "" {
		completePtr = &complete
	}
	out[index] = clonePromptOutputWithExecutedToolCalls(out[index], calls, metadataCellID, completePtr)
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
		// Bind the attached inventory to its output before dropping it from the
		// pending state, so later requests re-emit it and a late result can still
		// update the original output (Rust #48222).
		if attached.outputCallID != "" {
			r.retainBinding(attached, group)
		}
		group.pending = append([]recordedToolCall(nil), group.pending[count:]...)
		group.fullBytes = 0
		for _, pending := range group.pending {
			group.fullBytes += pending.fullBytes
		}
		if len(group.pending) == 0 && !r.keepTruncatedBinding(group) {
			delete(r.groups, attached.groupID)
			delete(r.invalidGroups, attached.groupID)
			if cellID := strings.TrimPrefix(attached.groupID, "cell:"); cellID != attached.groupID {
				delete(r.invalidCells, cellID)
			}
		}
		delete(r.outputs, attached.outputCallID)
	}
}

// retainBinding records the inventory attached to one output so the next request
// can re-emit it without the pending state. Only a validated cell binding admits
// truncated calls to late-result backfill (Rust #48222).
func (r *ExecutedToolCallRecorder) retainBinding(attached executedToolCallGroupAttachment, group *recordedToolCallGroup) {
	if _, exists := r.retained[attached.outputCallID]; !exists && len(r.retained) >= maxPendingExecutedToolCalls {
		r.evictRetainedBinding()
	}
	binding := &retainedToolCallBinding{
		groupID:                attached.groupID,
		originCallID:           attached.originCallID,
		runtimeCellID:          attached.runtimeCellID,
		calls:                  append([]recordedToolCall(nil), attached.calls...),
		complete:               attached.complete,
		callIndexByID:          map[string]int{},
		truncatedCallIndexByID: map[string]int{},
		lateTruncatedIndices:   map[int]struct{}{},
	}
	for index, call := range binding.calls {
		if call.callID == "" {
			continue
		}
		if !call.call.Truncated() {
			binding.callIndexByID[call.callID] = index
			continue
		}
		if group != nil && group.truncatedMetadataBindingValid && !call.call.HasToolResultMetadata() {
			binding.truncatedCallIndexByID[call.callID] = index
		}
	}
	r.retained[attached.outputCallID] = binding
}

// keepTruncatedBinding reports whether a drained cell must stay registered so its
// truncated calls remain bound to their outputs: a live cell can still dispatch,
// and a closed one still has results eligible for late backfill (Rust #48222).
func (r *ExecutedToolCallRecorder) keepTruncatedBinding(group *recordedToolCallGroup) bool {
	if group == nil || !group.truncatedMetadataBindingValid || !group.observedTruncatedCall {
		return false
	}
	if !group.dispatchClosed {
		return true
	}
	return r.hasRetainedTruncatedForCell(group.runtimeCellID)
}

// invalidateUntrustedTruncatedBindings checks every output that names a cell
// before any retained calls are cloned into the request. A duplicated output, or
// an input that no longer identifies the same execution, makes the whole cell's
// attribution ambiguous, so its late-backfilled evidence is cleared and no
// further backfill is admitted (Rust #48222).
func (r *ExecutedToolCallRecorder) invalidateUntrustedTruncatedBindings(out []any, outputCounts map[string]int, inputIndices map[string]*int) {
	if !r.hasTruncatedBindings() {
		return
	}
	untrusted := map[string]struct{}{}
	for index, item := range out {
		if hasDirectCallMetadata(item) {
			continue
		}
		_, callID, ok := executedToolCallOutputIdentity(item)
		if !ok || callID == "" {
			continue
		}
		runtimeCellID := ""
		previouslyRetained := false
		if binding := r.retained[callID]; binding != nil {
			runtimeCellID = binding.runtimeCellID
			previouslyRetained = true
		} else if group := r.groups[r.outputs[callID]]; group != nil {
			runtimeCellID = group.runtimeCellID
		}
		if runtimeCellID == "" {
			continue
		}
		matchesInput := previouslyRetained
		if inputIndex, present := inputIndices[callID]; present {
			matchesInput = inputIndex != nil && *inputIndex < index &&
				codeModeInputMatchesOutput(out[*inputIndex], item, callID, runtimeCellID)
		}
		if outputCounts[callID] != 1 || !matchesInput {
			untrusted[runtimeCellID] = struct{}{}
		}
	}
	for cellID := range untrusted {
		if group := r.groups["cell:"+cellID]; group != nil {
			group.truncatedMetadataBindingValid = false
		}
		for _, binding := range r.retained {
			if binding.runtimeCellID != cellID {
				continue
			}
			binding.complete = false
			binding.clearLateTruncatedMetadata()
		}
	}
}

// hasTruncatedBindings reports whether late truncated backfill is in play: a
// retained truncated call, or a validated cell holding a truncated call that has
// no result yet (Rust #48222).
func (r *ExecutedToolCallRecorder) hasTruncatedBindings() bool {
	for _, binding := range r.retained {
		if len(binding.truncatedCallIndexByID) > 0 {
			return true
		}
	}
	for _, group := range r.groups {
		if !group.truncatedMetadataBindingValid {
			continue
		}
		for _, pending := range group.pending {
			if pending.call.Truncated() && !pending.call.HasToolResultMetadata() {
				return true
			}
		}
	}
	return false
}

// evictRetainedBinding drops the lowest-key retained binding when the retained
// budget is exhausted. The choice does not depend on Go's map iteration order.
func (r *ExecutedToolCallRecorder) evictRetainedBinding() {
	candidate := ""
	for outputCallID := range r.retained {
		if candidate == "" || outputCallID < candidate {
			candidate = outputCallID
		}
	}
	if candidate != "" {
		delete(r.retained, candidate)
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
	if r.seenNestedIDs == nil {
		r.seenNestedIDs = newSeenIDs()
	}
	if r.retained == nil {
		r.retained = map[string]*retainedToolCallBinding{}
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
	if group := r.groups[groupID]; group != nil {
		group.truncatedMetadataBindingValid = false
	}
	// Retained bindings created from this grouping lose their completeness and
	// any late-backfilled evidence; ambiguous attribution cannot be repaired by
	// a later result (Rust #48222).
	for _, binding := range r.retained {
		if binding.groupID != groupID {
			continue
		}
		binding.complete = false
		binding.clearLateTruncatedMetadata()
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

// dropReusedCell releases a reused runtime cell's stale pending calls and output
// mappings, and revokes the retained bindings that named it, so the new
// execution cannot inherit them (Rust #48222).
func (r *ExecutedToolCallRecorder) dropReusedCell(cellID string) {
	if cellID == "" {
		return
	}
	groupID := "cell:" + cellID
	delete(r.groups, groupID)
	// The revocation applied to the previous execution does not carry over to a
	// fresh execution of the same runtime handle (Rust start_cell recreates it).
	delete(r.invalidCells, cellID)
	delete(r.invalidGroups, groupID)
	for outputCallID, mapped := range r.outputs {
		if mapped == groupID {
			delete(r.outputs, outputCallID)
		}
	}
	for _, binding := range r.retained {
		if binding.runtimeCellID != cellID {
			continue
		}
		binding.complete = false
		binding.clearLateTruncatedMetadata()
	}
}

// hasRetainedTruncatedForCell reports whether any output still holds a truncated
// call bound to this runtime cell, so its binding must be kept for late
// backfill (Rust #48222).
func (r *ExecutedToolCallRecorder) hasRetainedTruncatedForCell(cellID string) bool {
	if cellID == "" {
		return false
	}
	for _, binding := range r.retained {
		if binding.runtimeCellID == cellID && len(binding.truncatedCallIndexByID) > 0 {
			return true
		}
	}
	return false
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
		i.cellID = ""
		i.complete = nil
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
