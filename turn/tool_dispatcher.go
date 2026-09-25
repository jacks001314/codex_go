package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/model"
	"codex_go/tool"
	"codex_go/utils"
)

type ToolDispatcherOptions struct {
	Router                      *tool.Router
	Hooks                       tool.HookRunner
	Now                         func() time.Time
	PostToolInputItems          ToolPostExecutionInputItems
	OnToolStarted               ToolStartedCallback
	OnToolCompleted             ToolCompletedCallback
	EmitCodeModeNestedLifecycle bool
	OnCodeModeNotify            CodeModeNotifyCallback
	ThreadID                    string
	TurnID                      string
	ExecutedToolCalls           *ExecutedToolCallRecorder
	ToolMode                    string
	// InstantInterrupt enables Rust #48135's opt-in code-mode yielding: each
	// sampling request watches the turn's queued user input and hands the
	// code-mode calls a shared preemption signal, so a message queued during a
	// long-running cell is delivered while the cell keeps running.
	InstantInterrupt bool
	// Truncation is the effective model's output-truncation policy for this
	// turn (Rust `ToolCall::truncation_policy`). It bounds a direct call's
	// response-content budget; Code Mode calls ignore it because they receive
	// typed results.
	Truncation *utils.TruncationPolicy
	// OnToolOutputExternalContext runs after a tool returns an output that
	// declares external context (Rust `handle_any_tool` marking the thread's
	// memory mode polluted when `memories.disable_on_external_context`).
	OnToolOutputExternalContext func(ctx context.Context, invocation *tool.Invocation, output *tool.Output)
}

// observeNonDispatchedItem records a call ID that bypassed local dispatch so a
// reused ID cannot be presented as fresh Code Mode evidence (Rust #45185).
func (d *ToolDispatcher) observeNonDispatchedItem(item *model.AgentItem) {
	if d == nil || d.executedToolCalls == nil || item == nil {
		return
	}
	d.executedToolCalls.ObserveNonDispatchedCall(item)
}

// completeDirectCall attaches the prepared direct record to the invocation's own
// output and releases the pending slot. Fatal errors release the slot without an
// output, matching Rust's permit drop.
func (d *ToolDispatcher) completeDirectCall(invocation *tool.Invocation, response *ToolResponseItem, resultMetadata any) {
	if d == nil || d.executedToolCalls == nil || invocation == nil {
		return
	}
	d.directCallsMu.Lock()
	call := d.preparedDirectCalls[invocation]
	permit := d.permittedDirectCalls[invocation]
	delete(d.preparedDirectCalls, invocation)
	delete(d.permittedDirectCalls, invocation)
	d.directCallsMu.Unlock()
	if call != nil {
		// Rust #46010 (parallel.rs): a late result's raw `_meta` is attached to
		// the recorded call before the record reaches the invocation's output.
		if resultMetadata != nil {
			call.SetToolResultMetadata(model.NewToolResultMetadata(resultMetadata))
		}
		if response != nil {
			d.executedToolCalls.AttachDirectCallToOutput(response, call, permit)
		}
	}
	if permit != nil {
		permit.Release()
	}
}

// releasePreparedDirectCalls frees every pending direct-call slot still reserved
// for this batch.
func (d *ToolDispatcher) releasePreparedDirectCalls() {
	if d == nil {
		return
	}
	d.directCallsMu.Lock()
	defer d.directCallsMu.Unlock()
	for invocation, permit := range d.permittedDirectCalls {
		if permit != nil {
			permit.Release()
		}
		delete(d.permittedDirectCalls, invocation)
		delete(d.preparedDirectCalls, invocation)
	}
}

type ToolDispatcher struct {
	router                      *tool.Router
	hooks                       tool.HookRunner
	now                         func() time.Time
	postToolInputItems          ToolPostExecutionInputItems
	onToolStarted               ToolStartedCallback
	onToolCompleted             ToolCompletedCallback
	emitCodeModeNestedLifecycle bool
	onCodeModeNotify            CodeModeNotifyCallback
	threadID                    string
	turnID                      string
	executedToolCalls           *ExecutedToolCallRecorder
	toolMode                    string
	instantInterrupt            bool
	truncation                  *utils.TruncationPolicy
	onToolOutputExternalContext func(ctx context.Context, invocation *tool.Invocation, output *tool.Output)
	clockMu                     sync.Mutex
	// preparedDirectCalls/permittedDirectCalls carry the direct-call records
	// reserved before dispatch so executeToolInvocation can attach each one to
	// its own output (Rust #45185). They are written before execution starts and
	// only read while the invocations run.
	preparedDirectCalls  map[*tool.Invocation]*model.ExecutedToolCall
	permittedDirectCalls map[*tool.Invocation]*ExecutedToolCallPermit
	// directCallsMu guards the prepared-call maps, which the parallel execution
	// path completes from multiple goroutines.
	directCallsMu sync.Mutex
}

type ToolExecutionResult struct {
	Invocation    *tool.Invocation
	Output        *tool.Output
	Response      *ToolResponseItem
	InputItems    []any
	TelemetryTags map[string]string
	StartedAt     time.Time
	FinishedAt    time.Time
	// HandlerExecuted is true when the tool's handler actually ran. It is false
	// when the call was blocked/rejected before the executor was reached (for
	// example a pre-tool hook block). The goal extension uses this to
	// distinguish a handler-executed failure (Rust ToolCallOutcome::Failed {
	// handler_executed: true }) from a blocked call when deciding whether an
	// exec attempt counts toward goal-blocking (#41454).
	HandlerExecuted bool
}

type ToolPostExecutionInputItems func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any

type ToolStartedCallback func(ctx context.Context, invocation *tool.Invocation, startedAt time.Time)
type ToolCompletedCallback func(ctx context.Context, result *ToolExecutionResult)
type CodeModeNotifyCallback func(ctx context.Context, callID string, text string)

type ToolResponseItem struct {
	Type      string                     `json:"type"`
	CallID    string                     `json:"call_id,omitempty"`
	Name      string                     `json:"name,omitempty"`
	Status    string                     `json:"status,omitempty"`
	Execution string                     `json:"execution,omitempty"`
	Output    *FunctionCallOutputPayload `json:"output,omitempty"`
	Tools     []any                      `json:"tools,omitempty"`

	executedToolCalls []model.ExecutedToolCall
	cellID            string
	toolCallsComplete *bool
}

func (i *ToolResponseItem) ExecutedToolCalls() []model.ExecutedToolCall {
	if i == nil {
		return nil
	}
	return append([]model.ExecutedToolCall(nil), i.executedToolCalls...)
}

func (i *ToolResponseItem) ReplaceExecutedToolCalls(calls []model.ExecutedToolCall) {
	if i != nil {
		i.executedToolCalls = append([]model.ExecutedToolCall(nil), calls...)
	}
}

func (i *ToolResponseItem) CloneForExecutedToolCallPrompt() model.ExecutedToolCallCarrier {
	if i == nil {
		return (*ToolResponseItem)(nil)
	}
	clone := *i
	clone.Tools = append([]any(nil), i.Tools...)
	clone.executedToolCalls = append([]model.ExecutedToolCall(nil), i.executedToolCalls...)
	clone.cellID = i.cellID
	if i.toolCallsComplete != nil {
		value := *i.toolCallsComplete
		clone.toolCallsComplete = &value
	}
	return &clone
}

func (i *ToolResponseItem) SetExecutedToolCallCell(cellID string) {
	if i != nil {
		i.cellID = strings.TrimSpace(cellID)
	}
}

// ExecutedToolCallCellID reports the Code Mode cell owning the item's recorded
// calls; empty means the record is a direct invocation's (Rust #45185).
func (i *ToolResponseItem) ExecutedToolCallCellID() string {
	if i == nil {
		return ""
	}
	return strings.TrimSpace(i.cellID)
}

func (i *ToolResponseItem) ClearExecutedToolCalls() {
	if i != nil {
		i.executedToolCalls = nil
		// The completeness marker belongs to the call inventory, so stripping
		// the calls strips the marker with it.
		i.cellID = ""
		i.toolCallsComplete = nil
	}
}

func (i *ToolResponseItem) ClearToolResultMetadata() {
	if i == nil {
		return
	}
	for index := range i.executedToolCalls {
		i.executedToolCalls[index].ClearToolResultMetadata()
	}
}

func (i *ToolResponseItem) SetExecutedToolCallsComplete(complete bool) {
	if i != nil {
		value := complete
		i.toolCallsComplete = &value
	}
}

func (i *ToolResponseItem) MarshalJSON() ([]byte, error) {
	if i == nil {
		return []byte("null"), nil
	}
	switch i.Type {
	case "tool_search_output":
		tools := append([]any(nil), i.Tools...)
		if tools == nil {
			tools = []any{}
		}
		return marshalToolResponseItem(i, struct {
			Type      string  `json:"type"`
			CallID    *string `json:"call_id"`
			Status    string  `json:"status"`
			Execution string  `json:"execution"`
			Tools     []any   `json:"tools"`
		}{
			Type:      "tool_search_output",
			CallID:    optionalTurnString(i.CallID),
			Status:    firstNonEmptyTurnString(i.Status, "completed"),
			Execution: firstNonEmptyTurnString(i.Execution, "client"),
			Tools:     tools,
		})
	case "custom_tool_call_output":
		return marshalToolResponseItem(i, struct {
			Type   string                     `json:"type"`
			CallID string                     `json:"call_id"`
			Name   string                     `json:"name,omitempty"`
			Output *FunctionCallOutputPayload `json:"output"`
		}{
			Type:   "custom_tool_call_output",
			CallID: i.CallID,
			Name:   i.Name,
			Output: functionCallOutputPayloadForJSON(i.Output),
		})
	default:
		return marshalToolResponseItem(i, struct {
			Type   string                     `json:"type"`
			CallID string                     `json:"call_id"`
			Output *FunctionCallOutputPayload `json:"output"`
		}{
			Type:   firstNonEmptyTurnString(i.Type, "function_call_output"),
			CallID: i.CallID,
			Output: functionCallOutputPayloadForJSON(i.Output),
		})
	}
}

func marshalToolResponseItem(item *ToolResponseItem, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || item == nil {
		return encoded, err
	}
	// Rust #46081: a completeness marker always carries an explicit call list,
	// using [] for an empty inventory, so a verified empty inventory is not
	// mistaken for an unverified one.
	if len(item.executedToolCalls) == 0 && item.cellID == "" && item.toolCallsComplete == nil {
		return encoded, err
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, err
	}
	calls := item.executedToolCalls
	if calls == nil {
		calls = []model.ExecutedToolCall{}
	}
	metadata := map[string]any{"executed_tool_calls": calls}
	if item.cellID != "" {
		metadata["cell_id"] = item.cellID
	}
	if item.toolCallsComplete != nil {
		metadata["tool_calls_complete"] = *item.toolCallsComplete
	}
	object["internal_chat_message_metadata_passthrough"] = metadata
	return json.Marshal(object)
}

type FunctionCallOutputPayload struct {
	Body    any   `json:"-"`
	Success *bool `json:"-"`
}

func NewToolDispatcher(options *ToolDispatcherOptions) *ToolDispatcher {
	if options == nil {
		options = &ToolDispatcherOptions{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &ToolDispatcher{
		router:                      options.Router,
		hooks:                       options.Hooks,
		now:                         now,
		postToolInputItems:          options.PostToolInputItems,
		onToolStarted:               options.OnToolStarted,
		onToolCompleted:             options.OnToolCompleted,
		emitCodeModeNestedLifecycle: options.EmitCodeModeNestedLifecycle,
		onCodeModeNotify:            options.OnCodeModeNotify,
		threadID:                    strings.TrimSpace(options.ThreadID),
		turnID:                      strings.TrimSpace(options.TurnID),
		executedToolCalls:           options.ExecutedToolCalls,
		toolMode:                    strings.TrimSpace(options.ToolMode),
		instantInterrupt:            options.InstantInterrupt,
		truncation:                  options.Truncation,
		onToolOutputExternalContext: options.OnToolOutputExternalContext,
	}
}

// BeginStepPreempt mirrors Rust #48135's `run_sampling_request`: when the
// instant-interrupt feature is enabled, the sampling request creates a
// preemption signal and watches the turn's queued user input, and the caller
// attaches it to the step context so every code-mode call of that request
// yields its observation once a user message arrives. It returns a stop
// function that releases the watcher; both results are empty when the feature
// is off or the dispatcher has no mailbox.
func (d *ToolDispatcher) BeginStepPreempt(mailbox *SteerMailbox) (*tool.YieldSignal, func()) {
	if d == nil || !d.instantInterrupt || mailbox == nil {
		return nil, nil
	}
	signal := tool.NewYieldSignal()
	return signal, mailbox.WatchUserInput(d.threadID, d.turnID, signal)
}

func (d *ToolDispatcher) ExecuteToolItems(ctx context.Context, items []model.AgentItem) ([]ToolExecutionResult, error) {
	if d == nil || d.router == nil {
		return nil, fmt.Errorf("%w: tool router is nil", tool.ErrToolInvalidCall)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	invocations := make([]*tool.Invocation, 0, len(items))
	for i := range items {
		responseItem, ok := responseItemFromAgentItem(&items[i])
		if !ok {
			d.observeNonDispatchedItem(&items[i])
			continue
		}
		invocation, ok, err := d.router.BuildToolCall(*responseItem)
		if err != nil {
			return nil, err
		}
		if !ok {
			// The call shape did not resolve to a dispatchable tool, so its ID
			// cannot establish Code Mode completeness (Rust #45185).
			d.observeNonDispatchedItem(&items[i])
			continue
		}
		d.addInvocationContext(invocation)
		if d.executedToolCalls != nil {
			d.executedToolCalls.RecordToolCall(invocation, d.toolMode)
			if call, permit := d.executedToolCalls.PrepareDirectCall(invocation, d.toolMode); call != nil {
				if d.preparedDirectCalls == nil {
					d.preparedDirectCalls = map[*tool.Invocation]*model.ExecutedToolCall{}
					d.permittedDirectCalls = map[*tool.Invocation]*ExecutedToolCallPermit{}
				}
				d.preparedDirectCalls[invocation] = call
				d.permittedDirectCalls[invocation] = permit
			}
		}
		invocations = append(invocations, invocation)
	}
	if len(invocations) == 0 {
		return nil, nil
	}
	if len(invocations) == 1 {
		if err := d.router.WaitUntilReady(ctx, invocations[0]); err != nil {
			d.releasePreparedDirectCalls()
			return nil, err
		}
		result, err := d.executeToolInvocation(ctx, invocations[0])
		if err != nil {
			d.releasePreparedDirectCalls()
			return nil, err
		}
		return []ToolExecutionResult{*result}, nil
	}
	results, err := d.executeToolInvocations(ctx, invocations)
	if err != nil {
		// A failed batch leaves the prepared records for invocations that never
		// ran; release their pending slots (Rust drops each permit on cancel).
		d.releasePreparedDirectCalls()
		return nil, err
	}
	return results, nil
}

func (d *ToolDispatcher) addInvocationContext(invocation *tool.Invocation) {
	if d == nil || invocation == nil {
		return
	}
	if invocation.Context == nil {
		invocation.Context = map[string]any{}
	}
	if d.threadID != "" {
		invocation.Context["thread_id"] = d.threadID
		invocation.Context["threadId"] = d.threadID
	}
	if d.turnID != "" {
		invocation.Context["turn_id"] = d.turnID
		invocation.Context["turnId"] = d.turnID
	}
	invocation.Truncation = d.truncation
}

func (d *ToolDispatcher) executeToolInvocations(ctx context.Context, invocations []*tool.Invocation) ([]ToolExecutionResult, error) {
	results := make([]ToolExecutionResult, len(invocations))
	var executionGate sync.RWMutex
	var readinessWG sync.WaitGroup
	var readinessErrMu sync.Mutex
	var readinessErr error
	executeReady := func(index int, invocation *tool.Invocation) {
		defer readinessWG.Done()
		if err := d.router.WaitUntilReady(ctx, invocation); err != nil {
			readinessErrMu.Lock()
			if readinessErr == nil {
				readinessErr = err
			}
			readinessErrMu.Unlock()
			return
		}
		if d.router.SupportsParallel(invocation.ToolName) {
			executionGate.RLock()
			defer executionGate.RUnlock()
		} else {
			executionGate.Lock()
			defer executionGate.Unlock()
		}
		result, err := d.executeToolInvocation(ctx, invocation)
		readinessErrMu.Lock()
		defer readinessErrMu.Unlock()
		if err != nil {
			if readinessErr == nil {
				readinessErr = err
			}
			return
		}
		results[index] = *result
	}
	index := 0
	for index < len(invocations) {
		if d.router.HasReadinessWait(invocations[index].ToolName) {
			readinessWG.Add(1)
			go executeReady(index, invocations[index])
			index++
			continue
		}
		if !d.router.SupportsParallel(invocations[index].ToolName) {
			executionGate.Lock()
			result, err := d.executeToolInvocation(ctx, invocations[index])
			executionGate.Unlock()
			if err != nil {
				readinessWG.Wait()
				return nil, err
			}
			results[index] = *result
			index++
			continue
		}
		start := index
		for index < len(invocations) && d.router.SupportsParallel(invocations[index].ToolName) && !d.router.HasReadinessWait(invocations[index].ToolName) {
			index++
		}
		executionGate.RLock()
		groupResults, err := d.executeParallelToolInvocations(ctx, invocations[start:index])
		executionGate.RUnlock()
		if err != nil {
			readinessWG.Wait()
			return nil, err
		}
		copy(results[start:index], groupResults)
	}
	readinessWG.Wait()
	if readinessErr != nil {
		return nil, readinessErr
	}
	return results, nil
}

func (d *ToolDispatcher) executeParallelToolInvocations(ctx context.Context, invocations []*tool.Invocation) ([]ToolExecutionResult, error) {
	if len(invocations) == 1 {
		result, err := d.executeToolInvocation(ctx, invocations[0])
		if err != nil {
			return nil, err
		}
		return []ToolExecutionResult{*result}, nil
	}
	results := make([]ToolExecutionResult, len(invocations))
	var errorLock sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	setError := func(err error) {
		if err == nil {
			return
		}
		errorLock.Lock()
		defer errorLock.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}
	for i := range invocations {
		index := i
		invocation := invocations[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := d.executeToolInvocation(ctx, invocation)
			if err != nil {
				setError(err)
				return
			}
			results[index] = *result
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func (d *ToolDispatcher) executeToolInvocation(ctx context.Context, invocation *tool.Invocation) (*ToolExecutionResult, error) {
	toolCtx, cancel := context.WithCancelCause(ctx)
	invocation.Cancel = cancel
	defer func() {
		invocation.Cancel = nil
		cancel(nil)
	}()
	// #48135: a sampling request's preemption signal reaches the code-mode exec
	// and wait executors through the invocation context, so queued user input
	// yields their observation while the cell keeps running.
	if tool.IsCodeModeToolName(invocation.ToolName) {
		if signal := tool.CodeModePreemptFromContext(ctx); signal != nil {
			if invocation.Context == nil {
				invocation.Context = map[string]any{}
			}
			invocation.Context[tool.CodeModePreemptContextKey] = signal
		}
	}
	var notifyMu sync.Mutex
	notifyItems := []any{}
	if invocation.ToolName.Namespace == "" && invocation.ToolName.Name == tool.CodeModeExecToolName {
		if invocation.Context == nil {
			invocation.Context = map[string]any{}
		}
		invocation.Context[tool.CodeModeOutputCallIDContextKey] = invocation.CallID
		invocation.Context["code_mode_notify"] = tool.CodeModeNotifyFunc(func(callID string, text string) {
			if strings.TrimSpace(text) == "" {
				return
			}
			item := &ToolResponseItem{Type: "custom_tool_call_output", CallID: callID, Name: tool.CodeModeExecToolName, Output: NewFunctionCallOutputPayload(text, boolPtr(true))}
			notifyMu.Lock()
			notifyItems = append(notifyItems, item)
			notifyMu.Unlock()
			if d.onCodeModeNotify != nil {
				d.onCodeModeNotify(toolCtx, callID, text)
			}
		})
		if d.emitCodeModeNestedLifecycle || d.executedToolCalls != nil {
			invocation.Context["code_mode_nested_tool_started"] = tool.CodeModeNestedToolStartedFunc(func(nestedCtx context.Context, nested *tool.Invocation, nestedStartedAt time.Time) {
				if d.executedToolCalls != nil {
					d.executedToolCalls.RecordToolCall(nested, d.toolMode)
				}
				if d.emitCodeModeNestedLifecycle && d.onToolStarted != nil {
					d.onToolStarted(nestedCtx, nested, nestedStartedAt)
				}
			})
			invocation.Context["code_mode_nested_tool_completed"] = tool.CodeModeNestedToolCompletedFunc(func(nestedCtx context.Context, nested *tool.Invocation, nestedOutput *tool.Output, nestedErr error, nestedStartedAt, nestedFinishedAt time.Time) {
				// Rust #46010 (record_accepted_result): a nested code-mode result
				// records the raw `_meta` the handler exposed, before any lifecycle
				// reporting or the early return below.
				if d.executedToolCalls != nil && nested != nil && nestedOutput != nil && nestedErr == nil {
					if metadata := nestedOutput.ToolResultMetadata; metadata != nil {
						d.executedToolCalls.RecordToolResultMetadata(nested, metadata)
					}
				}
				if !d.emitCodeModeNestedLifecycle || d.onToolCompleted == nil {
					return
				}
				if nestedOutput == nil {
					nestedOutput = &tool.Output{CallID: nested.CallID, ToolName: nested.ToolName, Success: nestedErr == nil, CompletedAt: nestedFinishedAt}
					if nestedErr != nil {
						nestedOutput.Body, nestedOutput.Error = nestedErr.Error(), nestedErr.Error()
					}
				}
				d.onToolCompleted(nestedCtx, &ToolExecutionResult{Invocation: nested, Output: nestedOutput, Response: ToolResponseFromOutput(nested, nestedOutput), TelemetryTags: d.router.TelemetryTags(nested), StartedAt: nestedStartedAt, FinishedAt: nestedFinishedAt, HandlerExecuted: nestedErr != nil})
			})
		}
	}
	startedAt := d.nowUTC()
	// Rust #38568: tool start callbacks run after pre-tool hooks (with the
	// possibly hook-rewritten invocation) and before the executor runs. We
	// always install the pre-executor callback (even when no onToolStarted
	// consumer is registered) so the dispatcher can report whether the handler
	// was actually reached, which the goal extension uses to distinguish a
	// handler-executed failure from a pre-tool-hook block (#41454).
	handlerReached := false
	startedAfterPreHooks := func(updated *tool.Invocation) {
		handlerReached = true
		if d.onToolStarted != nil {
			d.onToolStarted(toolCtx, updated, startedAt)
		}
	}
	telemetryTags := d.router.TelemetryTags(invocation)
	output, dispatchErr := d.router.DispatchWithHooksAfterPreHooks(toolCtx, invocation, d.hooks, startedAfterPreHooks)
	// Rust ToolCallOutcome::Failed { handler_executed: true } is produced only
	// when the executor returned an error (dispatch error), not when the handler
	// completed with a success=false output. A blocked pre-tool hook leaves
	// handlerReached false (Rust ToolCallOutcome::Blocked).
	handlerExecuted := false
	if dispatchErr != nil {
		if cause := context.Cause(toolCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			dispatchErr = cause
		}
		callErr := toolCallErrorForModel(dispatchErr)
		if callErr.IsFatal() {
			d.completeDirectCall(invocation, nil, nil)
			return nil, dispatchErr
		}
		handlerExecuted = handlerReached
		message := callErr.ModelMessage()
		body := message
		if d.router.DeclaresOutputSchema(invocation.ToolName) {
			encoded, err := json.Marshal(message)
			if err != nil {
				return nil, err
			}
			body = string(encoded)
		}
		output = &tool.Output{
			CallID:      invocation.CallID,
			ToolName:    invocation.ToolName,
			Success:     false,
			Body:        body,
			Error:       message,
			CompletedAt: d.nowUTC(),
		}
	}
	if output == nil {
		output = &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true, CompletedAt: d.nowUTC()}
	}
	// Rust checks the tool output's external-context marker right after a
	// successful handler return, before any post-tool bookkeeping.
	if dispatchErr == nil && output.ContainsExternalContext && d.onToolOutputExternalContext != nil {
		d.onToolOutputExternalContext(toolCtx, invocation, output)
	}
	if d.executedToolCalls != nil && invocation.ToolName.Namespace == "" {
		switch invocation.ToolName.Name {
		case tool.CodeModeExecToolName, "wait":
			cellID := ""
			if output.Data != nil {
				cellID, _ = output.Data["cell_id"].(string)
			}
			if strings.TrimSpace(cellID) != "" {
				// Rust's exec handler starts the cell, while a wait registers into
				// the already started cell (#48222 distinguishes the two, so a
				// reused runtime ID can release the previous execution).
				if invocation.ToolName.Name == tool.CodeModeExecToolName {
					d.executedToolCalls.StartCell(cellID, invocation.CallID)
				} else {
					d.executedToolCalls.RegisterCell(cellID, invocation.CallID)
				}
				// Rust #46081: a non-yielded exec/wait output closes the cell's
				// dispatch gate, so its recorded inventory is final (possibly
				// empty). A yielded output keeps the cell running.
				if !codeModeOutputIsRunning(output) {
					d.executedToolCalls.FinishCell(cellID)
				}
			} else if invocation.ToolName.Name == tool.CodeModeExecToolName {
				d.executedToolCalls.RegisterOutputCall(invocation.CallID)
			}
		}
	}
	finishedAt := output.CompletedAt
	if finishedAt.IsZero() {
		finishedAt = d.nowUTC()
	}
	inputItems := d.postExecutionInputItems(toolCtx, invocation, output)
	notifyMu.Lock()
	inputItems = append(notifyItems, inputItems...)
	notifyMu.Unlock()
	result := &ToolExecutionResult{
		Invocation:      invocation,
		Output:          output,
		Response:        ToolResponseFromOutput(invocation, output),
		InputItems:      inputItems,
		TelemetryTags:   telemetryTags,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		HandlerExecuted: handlerExecuted,
	}
	d.completeDirectCall(invocation, result.Response, output.ToolResultMetadata)
	if d.onToolCompleted != nil {
		d.onToolCompleted(toolCtx, result)
	}
	return result, nil
}

func toolCallErrorForModel(err error) *tool.FunctionCallError {
	if err == nil {
		return nil
	}
	var callErr *tool.FunctionCallError
	if errors.As(err, &callErr) {
		return callErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, tool.ErrToolCancelled) {
		return tool.Fatal(err.Error())
	}
	if errors.Is(err, tool.ErrToolNotFound) || errors.Is(err, tool.ErrToolInvalidCall) {
		return tool.RespondToModel(err.Error())
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return tool.RespondToModel(err.Error())
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return tool.RespondToModel(err.Error())
	}
	return tool.Fatal(err.Error())
}

func (d *ToolDispatcher) nowUTC() time.Time {
	if d == nil || d.now == nil {
		return time.Now().UTC()
	}
	d.clockMu.Lock()
	defer d.clockMu.Unlock()
	return d.now().UTC()
}

func (d *ToolDispatcher) postExecutionInputItems(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any {
	if d == nil || d.postToolInputItems == nil {
		return nil
	}
	items := d.postToolInputItems(ctx, invocation, output)
	if len(items) == 0 {
		return nil
	}
	return append([]any(nil), items...)
}

func ToolResponseFromOutput(invocation *tool.Invocation, output *tool.Output) *ToolResponseItem {
	if invocation == nil {
		return nil
	}
	if output == nil {
		output = &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true}
	}
	switch invocation.Payload.Kind {
	case tool.PayloadToolSearch:
		return &ToolResponseItem{
			Type:      "tool_search_output",
			CallID:    firstNonEmptyTurnString(output.CallID, invocation.CallID),
			Status:    "completed",
			Execution: "client",
			Tools:     outputTools(output),
		}
	case tool.PayloadCustom:
		return &ToolResponseItem{
			Type:   "custom_tool_call_output",
			CallID: firstNonEmptyTurnString(output.CallID, invocation.CallID),
			Output: NewFunctionCallOutputPayload(outputBody(output), boolPtr(output.Success)),
		}
	default:
		body := outputBody(output)
		if invocation.ToolName.Namespace == "" && invocation.ToolName.Name == tool.DefaultExecCommandToolName {
			body = []FunctionCallOutputContentItem{{Type: "input_text", Text: functionCallOutputBodyText(body)}}
		}
		return &ToolResponseItem{
			Type:   "function_call_output",
			CallID: firstNonEmptyTurnString(output.CallID, invocation.CallID),
			Output: NewFunctionCallOutputPayload(body, boolPtr(output.Success)),
		}
	}
}

func functionCallOutputBodyText(body any) string {
	switch typed := body.(type) {
	case string:
		return typed
	case []FunctionCallOutputContentItem:
		return FunctionCallOutputContentItemsText(typed)
	default:
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Sprint(body)
		}
		return string(data)
	}
}

func NewFunctionCallOutputPayload(body any, success *bool) *FunctionCallOutputPayload {
	if body == nil {
		body = ""
	}
	return &FunctionCallOutputPayload{Body: body, Success: success}
}

func functionCallOutputPayloadForJSON(payload *FunctionCallOutputPayload) *FunctionCallOutputPayload {
	if payload == nil {
		return NewFunctionCallOutputPayload("", nil)
	}
	return payload
}

func (p *FunctionCallOutputPayload) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(p.Body)
}

func (p *FunctionCallOutputPayload) Text() string {
	if p == nil || p.Body == nil {
		return ""
	}
	switch body := p.Body.(type) {
	case string:
		return body
	case []FunctionCallOutputContentItem:
		return FunctionCallOutputContentItemsText(body)
	case []any:
		return functionCallOutputAnyItemsText(body)
	default:
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Sprint(body)
		}
		return string(data)
	}
}

type FunctionCallOutputContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	// FileID is the uploaded-file form of an image content item (Rust #45794):
	// file references are preserved through tool output and never inlined.
	FileID string  `json:"file_id,omitempty"`
	Detail *string `json:"detail,omitempty"`
}

func FunctionCallOutputContentItemsText(items []FunctionCallOutputContentItem) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		if items[i].Type != "input_text" && items[i].Type != "" {
			continue
		}
		if strings.TrimSpace(items[i].Text) != "" {
			parts = append(parts, items[i].Text)
		}
	}
	return strings.Join(parts, "\n")
}

func responseItemFromAgentItem(item *model.AgentItem) (*tool.ResponseItem, bool) {
	if item == nil {
		return nil, false
	}
	switch item.Type {
	case "function_call":
		return &tool.ResponseItem{
			Type:                  item.Type,
			ID:                    item.ID,
			Namespace:             item.Namespace,
			Name:                  item.Name,
			CallID:                firstNonEmptyTurnString(item.CallID, item.ID),
			Arguments:             item.Arguments,
			EncryptedFunctionArgs: cloneTurnStringSlicePtr(item.EncryptedFunctionArgs),
		}, true
	case "custom_tool_call":
		return &tool.ResponseItem{
			Type:      item.Type,
			ID:        item.ID,
			Namespace: item.Namespace,
			Name:      item.Name,
			CallID:    firstNonEmptyTurnString(item.CallID, item.ID),
			Input:     item.Input,
		}, true
	case "tool_search_call":
		return &tool.ResponseItem{
			Type:      item.Type,
			CallID:    firstNonEmptyTurnString(item.CallID, item.ID),
			Execution: item.Execution,
			Search:    toolSearchMapFromAgentItem(item),
		}, true
	default:
		return nil, false
	}
}

func cloneTurnStringSlicePtr(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	cloned := append([]string{}, (*value)...)
	return &cloned
}

func outputBody(output *tool.Output) any {
	if output == nil {
		return ""
	}
	if output.Data != nil {
		if value, ok := output.Data["content_items"]; ok {
			return value
		}
	}
	if strings.TrimSpace(output.Body) != "" {
		return output.Body
	}
	if output.Data != nil {
		if value, ok := output.Data["output"]; ok {
			return value
		}
	}
	if strings.TrimSpace(output.Error) != "" {
		return output.Error
	}
	data, err := json.Marshal(map[string]any{"success": output.Success})
	if err != nil {
		return fmt.Sprintf("success=%v", output.Success)
	}
	return string(data)
}

func outputTools(output *tool.Output) []any {
	if output == nil || output.Data == nil {
		return nil
	}
	if tools, ok := model.ResponsesLoadableToolsFromValue(output.Data["tools"]); ok {
		return tools
	}
	return nil
}

func toolSearchMapFromAgentItem(item *model.AgentItem) map[string]any {
	if item == nil {
		return nil
	}
	if item.Search != nil {
		out := make(map[string]any, len(item.Search))
		for key, value := range item.Search {
			out[key] = value
		}
		return out
	}
	if strings.TrimSpace(item.Arguments) == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(item.Arguments), &out); err != nil {
		return nil
	}
	return out
}

func functionCallOutputAnyItemsText(items []any) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := entry["type"].(string)
		if itemType != "input_text" && itemType != "" {
			continue
		}
		text, _ := entry["text"].(string)
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func boolPtr(value bool) *bool {
	return &value
}

// codeModeOutputIsRunning reports whether a Code Mode exec/wait output left the
// cell running (Rust's `RuntimeResponse::Yielded`). A finished output closes the
// cell's dispatch gate, which finalizes its recorded tool-call inventory
// (#46081).
func codeModeOutputIsRunning(output *tool.Output) bool {
	if output == nil || output.Data == nil {
		return false
	}
	running, _ := output.Data["running"].(bool)
	return running
}

func firstNonEmptyTurnString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func optionalTurnString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func IsRespondToModelError(err error) bool {
	var callErr *tool.FunctionCallError
	return errors.As(err, &callErr) && callErr.RespondsToModel()
}
