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
	clockMu                     sync.Mutex
}

type ToolExecutionResult struct {
	Invocation    *tool.Invocation
	Output        *tool.Output
	Response      *ToolResponseItem
	InputItems    []any
	TelemetryTags map[string]string
	StartedAt     time.Time
	FinishedAt    time.Time
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
	return &clone
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
	if err != nil || item == nil || len(item.executedToolCalls) == 0 {
		return encoded, err
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, err
	}
	object["internal_chat_message_metadata_passthrough"] = map[string]any{
		"executed_tool_calls": item.executedToolCalls,
	}
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
	}
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
			continue
		}
		invocation, ok, err := d.router.BuildToolCall(*responseItem)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		d.addInvocationContext(invocation)
		if d.executedToolCalls != nil {
			d.executedToolCalls.RecordToolCall(invocation, d.toolMode)
		}
		invocations = append(invocations, invocation)
	}
	if len(invocations) == 0 {
		return nil, nil
	}
	if len(invocations) == 1 {
		if err := d.router.WaitUntilReady(ctx, invocations[0]); err != nil {
			return nil, err
		}
		result, err := d.executeToolInvocation(ctx, invocations[0])
		if err != nil {
			return nil, err
		}
		return []ToolExecutionResult{*result}, nil
	}
	return d.executeToolInvocations(ctx, invocations)
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
				if !d.emitCodeModeNestedLifecycle || d.onToolCompleted == nil {
					return
				}
				if nestedOutput == nil {
					nestedOutput = &tool.Output{CallID: nested.CallID, ToolName: nested.ToolName, Success: nestedErr == nil, CompletedAt: nestedFinishedAt}
					if nestedErr != nil {
						nestedOutput.Body, nestedOutput.Error = nestedErr.Error(), nestedErr.Error()
					}
				}
				d.onToolCompleted(nestedCtx, &ToolExecutionResult{Invocation: nested, Output: nestedOutput, Response: ToolResponseFromOutput(nested, nestedOutput), TelemetryTags: d.router.TelemetryTags(nested), StartedAt: nestedStartedAt, FinishedAt: nestedFinishedAt})
			})
		}
	}
	startedAt := d.nowUTC()
	var startedAfterPreHooks func(*tool.Invocation)
	if d.onToolStarted != nil {
		// Rust #38568: tool start callbacks run after pre-tool hooks (with the
		// possibly hook-rewritten invocation) and before the executor runs.
		startedAfterPreHooks = func(updated *tool.Invocation) {
			d.onToolStarted(toolCtx, updated, startedAt)
		}
	}
	telemetryTags := d.router.TelemetryTags(invocation)
	output, dispatchErr := d.router.DispatchWithHooksAfterPreHooks(toolCtx, invocation, d.hooks, startedAfterPreHooks)
	if dispatchErr != nil {
		if cause := context.Cause(toolCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			dispatchErr = cause
		}
		callErr := toolCallErrorForModel(dispatchErr)
		if callErr.IsFatal() {
			return nil, dispatchErr
		}
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
	if d.executedToolCalls != nil && invocation.ToolName.Namespace == "" {
		switch invocation.ToolName.Name {
		case tool.CodeModeExecToolName, "wait":
			cellID := ""
			if output.Data != nil {
				cellID, _ = output.Data["cell_id"].(string)
			}
			if strings.TrimSpace(cellID) != "" {
				d.executedToolCalls.RegisterCell(cellID, invocation.CallID)
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
		Invocation:    invocation,
		Output:        output,
		Response:      ToolResponseFromOutput(invocation, output),
		InputItems:    inputItems,
		TelemetryTags: telemetryTags,
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
	}
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
	Type     string  `json:"type"`
	Text     string  `json:"text,omitempty"`
	ImageURL string  `json:"image_url,omitempty"`
	Detail   *string `json:"detail,omitempty"`
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
			Namespace:             item.Namespace,
			Name:                  item.Name,
			CallID:                firstNonEmptyTurnString(item.CallID, item.ID),
			Arguments:             item.Arguments,
			EncryptedFunctionArgs: cloneTurnStringSlicePtr(item.EncryptedFunctionArgs),
		}, true
	case "custom_tool_call":
		return &tool.ResponseItem{
			Type:      item.Type,
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
