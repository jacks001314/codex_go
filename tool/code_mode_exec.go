package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/utils"

	"github.com/grafana/sobek"
)

const CodeModeExecToolName = "exec"

const (
	codeModeMaxFrameBytes       = 64 * 1024 * 1024
	codeModeMaxPendingCallbacks = 1024
	codeModeMaxSafeInteger      = 1<<53 - 1
)

const codeModeExecGrammar = `start: pragma_source | plain_source
pragma_source: PRAGMA_LINE NEWLINE SOURCE
plain_source: SOURCE

PRAGMA_LINE: /[ \t]*\/\/ @exec:[^\r\n]*/
NEWLINE: /\r?\n/
SOURCE: /[\s\S]+/
`

type codeModeExecExecutor struct {
	registry          *Registry
	nestedCommandTool ToolName
	nextID            atomic.Uint64
	storeMu           sync.RWMutex
	store             map[string]any
	cellsMu           sync.Mutex
	cells             map[string]*codeModeCell
}

type codeModeCell struct {
	done       chan struct{}
	cancel     context.CancelFunc
	output     *Output
	err        error
	completed  bool
	mu         sync.Mutex
	items      []map[string]any
	texts      []string
	cursor     int
	terminated bool
}

type codeModeExecOptions struct {
	YieldTimeMS     *int `json:"yield_time_ms"`
	MaxOutputTokens *int `json:"max_output_tokens"`
}

type codeModeWaitParams struct {
	CellID      string `json:"cell_id"`
	YieldTimeMS int    `json:"yield_time_ms,omitempty"`
	MaxTokens   int    `json:"max_tokens,omitempty"`
	Terminate   bool   `json:"terminate,omitempty"`
}

type codeModeToolCompletion struct {
	resolve  func(interface{}) error
	reject   func(interface{}) error
	output   *Output
	err      error
	payload  Payload
	name     ToolName
	callback sobek.Callable
	timerID  uint64
}

func NewCodeModeExecExecutor(registry *Registry) Executor {
	exec, _ := NewCodeModeExecutors(registry)
	return exec
}

func NewCodeModeExecutors(registry *Registry, nestedCommandTool ...ToolName) (Executor, Executor) {
	var commandTool ToolName
	if len(nestedCommandTool) > 0 {
		commandTool = nestedCommandTool[0]
	}
	exec := &codeModeExecExecutor{registry: registry, nestedCommandTool: commandTool, store: map[string]any{}, cells: map[string]*codeModeCell{}}
	return exec, &codeModeWaitExecutor{exec: exec}
}

func (e *codeModeExecExecutor) Spec() Spec {
	return Spec{
		Name:        PlainName(CodeModeExecToolName),
		Description: codeModeExecDescription(e.registry, e.nestedCommandTool),
		Freeform:    &FreeformSpec{Syntax: "lark", Definition: codeModeExecGrammar},
	}
}

func codeModeExecDescription(registry *Registry, preferredCommandTool ToolName) string {
	description := `Run JavaScript code to orchestrate/compose tool calls
- Evaluates the provided JavaScript code in a fresh isolate as an async module.
- All nested tools are available on the global tools object. Tool names are exposed as normalized JavaScript identifiers.
- Nested tool methods take either a string or an object as their input argument.
- Nested tools return either an object or a string, based on the description.
- Runs raw JavaScript -- no Node, no file system, no network access, no console.
- Accepts raw JavaScript source text, not JSON, quoted strings, or markdown code fences.
- You may optionally start the tool input with a first-line pragma like // @exec: {"yield_time_ms": 10000, "max_output_tokens": 1000}.
- yield_time_ms asks exec to yield early if the script is still running. Defaults to 10000 ms.
- max_output_tokens sets the token budget for direct exec results. Defaults to 10000 tokens.
- When the JavaScript code is fully evaluated, the isolate's lifetime ends and unawaited promises are discarded.
- Do not use fetch, XMLHttpRequest, require, process, or console; they are unavailable.
- There is no nested tools.exec method. To run commands, call the enabled nested command tool and await its result.

Global helpers:
- exit(): Immediately ends the current script successfully.
- text(value): Appends text to the exec result. Use text(result.output) to forward command output.
- image(value), audio(value), generatedImage(value): Append supported media results.
- store(key, value) and load(key): Persist serializable values within the code-mode session.
- notify(value): Immediately injects an extra custom_tool_call_output.
- setTimeout(callback, delayMs) and clearTimeout(id): Schedule or cancel timers.
- ALL_TOOLS: Metadata for enabled nested tools as { name, description } entries.
- yield_control(): Yields accumulated output while the script keeps running.`

	if registry == nil {
		return description
	}
	commandToolName := preferredCommandTool.Key()
	var execSpec Spec
	var ok bool
	if commandToolName != "" {
		execSpec, ok = registry.Spec(preferredCommandTool)
	} else {
		commandToolName = DefaultExecCommandToolName
		execSpec, ok = registry.Spec(PlainName(commandToolName))
		if !ok || execSpec.Exposure == ExposureHidden {
			commandToolName = DefaultShellCommandToolName
			execSpec, ok = registry.Spec(PlainName(commandToolName))
		}
	}
	if !ok {
		return description
	}
	schema, err := json.Marshal(execSpec.InputSchema)
	if err != nil {
		return description
	}
	argumentName := "cmd"
	if commandToolName == DefaultShellCommandToolName {
		argumentName = "command"
	}
	return description + "\n\nEnabled nested command tool:\n## tools." + commandToolName + "\n" + strings.TrimSpace(execSpec.Description) +
		"\nInput schema: " + string(schema) +
		"\nExample: const r = await tools." + commandToolName + "({\"" + argumentName + "\":\"Write-Output hello\",\"timeout_ms\":10000}); text(r.output);"
}

func (e *codeModeExecExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if invocation == nil || invocation.Payload.Kind != PayloadCustom {
		return nil, RespondToModel("exec expects raw JavaScript source text")
	}
	if e == nil || e.registry == nil {
		return nil, Fatal("code mode tool registry is unavailable")
	}
	source, options, err := parseCodeModeSource(invocation.Payload.Input)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("invalid exec pragma: %v", err))
	}
	if len(invocation.Payload.Input) > codeModeMaxFrameBytes {
		return nil, RespondToModel(fmt.Sprintf("code-mode IPC frame length %d exceeds %d bytes", len(invocation.Payload.Input), codeModeMaxFrameBytes))
	}
	yieldTimeMS := 10000
	if options.YieldTimeMS != nil {
		yieldTimeMS = *options.YieldTimeMS
	}
	if yieldTimeMS < 0 {
		return nil, RespondToModel("yield_time_ms must be non-negative")
	}
	if options.YieldTimeMS == nil && !strings.Contains(source, "yield_control") {
		invocationCopy := *invocation
		invocationCopy.Payload.Input = source
		output, runErr := e.executeScript(ctx, &invocationCopy, nil, nil)
		return truncateCodeModeOutput(output, codeModeTokenLimit(options.MaxOutputTokens)), runErr
	}
	cellID := fmt.Sprintf("cell-%d", e.nextID.Add(1))
	cellCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cell := &codeModeCell{done: make(chan struct{}), cancel: cancel}
	e.cellsMu.Lock()
	e.cells[cellID] = cell
	e.cellsMu.Unlock()
	invocationCopy := *invocation
	invocationCopy.Payload.Input = source
	yield := make(chan struct{}, 1)
	go func() {
		output, runErr := e.executeScript(cellCtx, &invocationCopy, yield, cell)
		e.cellsMu.Lock()
		if cell.terminated && (errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)) {
			output = &Output{CallID: invocation.CallID, ToolName: PlainName(CodeModeExecToolName), Success: true, Data: map[string]any{
				"content_items": []map[string]any{}, "nested_commands": []string{}, "nested_outputs": []string{}, "nested_exit_codes": []int{}, "terminated": true,
			}}
			runErr = nil
		}
		cell.output, cell.err, cell.completed = output, runErr, true
		close(cell.done)
		e.cellsMu.Unlock()
	}()
	timer := time.NewTimer(time.Duration(yieldTimeMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-cell.done:
		output, runErr := e.consumeCell(cellID, cell)
		return truncateCodeModeOutput(output, codeModeTokenLimit(options.MaxOutputTokens)), runErr
	case <-yield:
	case <-timer.C:
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
	return &Output{CallID: invocation.CallID, ToolName: PlainName(CodeModeExecToolName), Success: true, Body: "Script running with cell ID " + cellID + "\n" + e.cellDelta(cell), Data: map[string]any{"cell_id": cellID, "running": true}}, nil
}

func (e *codeModeExecExecutor) executeScript(ctx context.Context, invocation *Invocation, yield chan<- struct{}, cell *codeModeCell) (*Output, error) {
	runtime := sobek.New()
	completion := make(chan codeModeToolCompletion, 32)
	pending := 0
	timers := map[uint64]*time.Timer{}
	var nextTimerID uint64
	defer func() {
		for _, timer := range timers {
			timer.Stop()
		}
	}()
	texts := make([]string, 0)
	contentItems := make([]map[string]any, 0)
	commands := make([]string, 0)
	nestedOutputs := make([]string, 0)
	exitCodes := make([]int, 0)
	if err := runtime.Set("text", func(call sobek.FunctionCall) sobek.Value {
		text := renderCodeModeValue(call.Argument(0))
		texts = append(texts, text)
		contentItems = append(contentItems, map[string]any{"type": "input_text", "text": text})
		if cell != nil {
			cell.mu.Lock()
			cell.texts = append(cell.texts, text)
			cell.items = append(cell.items, map[string]any{"type": "input_text", "text": text})
			cell.mu.Unlock()
		}
		return sobek.Undefined()
	}); err != nil {
		return nil, err
	}
	if err := e.installContentHelpers(runtime, &contentItems); err != nil {
		return nil, err
	}
	if err := runtime.Set("notify", func(call sobek.FunctionCall) sobek.Value {
		text := renderCodeModeValue(call.Argument(0))
		if notify, ok := invocation.Context["code_mode_notify"].(CodeModeNotifyFunc); ok {
			notify(invocation.CallID, text)
		}
		return sobek.Undefined()
	}); err != nil {
		return nil, err
	}
	if err := runtime.Set("store", func(call sobek.FunctionCall) sobek.Value {
		key := call.Argument(0).String()
		var value any
		if err := runtime.ExportTo(call.Argument(1), &value); err != nil {
			panic(runtime.ToValue(err.Error()))
		}
		if _, err := json.Marshal(value); err != nil {
			panic(runtime.ToValue("store value must be serializable"))
		}
		e.storeMu.Lock()
		e.store[key] = value
		e.storeMu.Unlock()
		return sobek.Undefined()
	}); err != nil {
		return nil, err
	}
	if err := runtime.Set("load", func(call sobek.FunctionCall) sobek.Value {
		e.storeMu.RLock()
		value, ok := e.store[call.Argument(0).String()]
		e.storeMu.RUnlock()
		if !ok {
			return sobek.Undefined()
		}
		return runtime.ToValue(value)
	}); err != nil {
		return nil, err
	}
	if err := runtime.Set("exit", func() { runtime.Interrupt(codeModeExit{}) }); err != nil {
		return nil, err
	}
	if err := runtime.Set("setTimeout", func(call sobek.FunctionCall) sobek.Value {
		callback, ok := sobek.AssertFunction(call.Argument(0))
		if !ok {
			panic(runtime.ToValue("setTimeout callback must be a function"))
		}
		delay := int64(0)
		if len(call.Arguments) > 1 {
			delay = call.Argument(1).ToInteger()
		}
		if delay < 0 {
			delay = 0
		}
		nextTimerID++
		timerID := nextTimerID
		if pending >= codeModeMaxPendingCallbacks {
			panic(runtime.ToValue(fmt.Sprintf("code-mode host exceeded the limit of %d pending delegate calls", codeModeMaxPendingCallbacks)))
		}
		pending++
		timers[timerID] = time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
			select {
			case completion <- codeModeToolCompletion{callback: callback, timerID: timerID}:
			case <-ctx.Done():
			}
		})
		return runtime.ToValue(timerID)
	}); err != nil {
		return nil, err
	}
	if err := runtime.Set("clearTimeout", func(call sobek.FunctionCall) sobek.Value {
		id := uint64(call.Argument(0).ToInteger())
		if timer := timers[id]; timer != nil {
			if timer.Stop() {
				pending--
			}
			delete(timers, id)
		}
		return sobek.Undefined()
	}); err != nil {
		return nil, err
	}
	if err := runtime.Set("yield_control", func() sobek.Value {
		if yield != nil {
			select {
			case yield <- struct{}{}:
			default:
			}
		}
		return sobek.Undefined()
	}); err != nil {
		return nil, err
	}
	toolsObject := runtime.NewObject()
	for _, name := range e.registry.Names() {
		if name.Key() == CodeModeExecToolName || name.Key() == "wait" {
			continue
		}
		executor, ok := e.registry.Lookup(name)
		if !ok {
			continue
		}
		spec := executor.Spec()
		if spec.Exposure == ExposureHidden && name.Key() != e.nestedCommandTool.Key() {
			continue
		}
		globalName := codeModeIdentifier(ResponsesAPIName(name))
		toolName := name
		toolSpec := spec
		if err := toolsObject.Set(globalName, func(call sobek.FunctionCall) sobek.Value {
			payload, err := nestedPayload(runtime, toolSpec, call)
			if err != nil {
				panic(runtime.ToValue(err.Error()))
			}
			callID := fmt.Sprintf("%s-nested-%d", invocation.CallID, e.nextID.Add(1))
			if pending >= codeModeMaxPendingCallbacks {
				panic(runtime.ToValue(fmt.Sprintf("code-mode host exceeded the limit of %d pending delegate calls", codeModeMaxPendingCallbacks)))
			}
			promise, resolve, reject := runtime.NewPromise()
			pending++
			go func() {
				nestedInvocation := &Invocation{CallID: callID, ToolName: toolName, Payload: payload, Context: cloneInvocationContext(invocation.Context), Source: "code_mode"}
				startedAt := time.Now().UTC()
				if started, ok := invocation.Context["code_mode_nested_tool_started"].(CodeModeNestedToolStartedFunc); ok {
					started(ctx, nestedInvocation, startedAt)
				}
				out, callErr := NewRouter(e.registry).Dispatch(ctx, nestedInvocation)
				finishedAt := time.Now().UTC()
				if completed, ok := invocation.Context["code_mode_nested_tool_completed"].(CodeModeNestedToolCompletedFunc); ok {
					completed(ctx, nestedInvocation, out, callErr, startedAt, finishedAt)
				}
				select {
				case completion <- codeModeToolCompletion{resolve: resolve, reject: reject, output: out, err: callErr, payload: payload, name: toolName}:
				case <-ctx.Done():
				}
			}()
			return runtime.ToValue(promise)
		}); err != nil {
			return nil, err
		}
	}
	if err := runtime.Set("tools", toolsObject); err != nil {
		return nil, err
	}
	metadata := make([]map[string]string, 0)
	for _, name := range e.registry.Names() {
		if name.Key() == CodeModeExecToolName || name.Key() == "wait" {
			continue
		}
		if spec, ok := e.registry.Spec(name); ok {
			if spec.Exposure == ExposureHidden && name.Key() != e.nestedCommandTool.Key() {
				continue
			}
			metadata = append(metadata, map[string]string{"name": codeModeIdentifier(ResponsesAPIName(name)), "description": spec.Description})
		}
	}
	if err := runtime.Set("ALL_TOOLS", metadata); err != nil {
		return nil, err
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			runtime.Interrupt(ctx.Err())
		case <-done:
		}
	}()
	value, err := runtime.RunString("(async () => {\n" + invocation.Payload.Input + "\n})()")
	if err != nil {
		if _, ok := err.(*sobek.InterruptedError); ok {
			if strings.Contains(err.Error(), "code mode exit") {
				err = nil
			} else if ctx.Err() != nil {
				return nil, ctx.Err()
			} else {
				return nil, RespondToModel(fmt.Sprintf("JavaScript execution failed: %v", err))
			}
		} else {
			return nil, RespondToModel(fmt.Sprintf("JavaScript execution failed: %v", err))
		}
	}
	if err == nil && value == nil {
		value = sobek.Undefined()
	}
	promise, hasPromise := value.Export().(*sobek.Promise)
	for hasPromise && promise.State() == sobek.PromiseStatePending {
		if pending == 0 {
			return nil, RespondToModel("JavaScript execution did not settle")
		}
		select {
		case <-ctx.Done():
			runtime.Interrupt(ctx.Err())
			return nil, ctx.Err()
		case completed := <-completion:
			pending--
			if completed.callback != nil {
				delete(timers, completed.timerID)
				if _, err := completed.callback(sobek.Undefined()); err != nil {
					return nil, RespondToModel(fmt.Sprintf("JavaScript timer failed: %v", err))
				}
				continue
			}
			if completed.err != nil {
				if err := completed.reject(completed.err.Error()); err != nil {
					return nil, err
				}
				continue
			}
			result := codeModeToolResult(completed.output)
			if IsShellCommandToolName(completed.name) {
				if cmd, ok := payloadCommand(completed.payload); ok {
					commands = append(commands, cmd)
				}
				nestedOutputs = append(nestedOutputs, result["output"].(string))
				if exitCode, ok := result["exit_code"].(int); ok {
					exitCodes = append(exitCodes, exitCode)
				}
			}
			if failure, failed := codeModeToolFailure(completed.name, completed.output); failed {
				if err := completed.reject(failure); err != nil {
					return nil, err
				}
				continue
			}
			if err := completed.resolve(result); err != nil {
				return nil, err
			}
		}
	}
	if hasPromise && promise.State() == sobek.PromiseStateRejected {
		return nil, RespondToModel(fmt.Sprintf("JavaScript execution failed: %s", promise.Result().String()))
	}
	body := strings.Join(texts, "\n")
	return &Output{CallID: invocation.CallID, ToolName: PlainName(CodeModeExecToolName), Success: true, Body: body, Data: map[string]any{"content_items": contentItems, "nested_commands": commands, "nested_outputs": nestedOutputs, "nested_exit_codes": exitCodes}}, nil
}

func cloneInvocationContext(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func parseCodeModeSource(input string) (string, codeModeExecOptions, error) {
	input = strings.TrimPrefix(input, "\ufeff")
	if strings.TrimSpace(input) == "" {
		return "", codeModeExecOptions{}, fmt.Errorf("exec expects raw JavaScript source text (non-empty)")
	}
	lines := strings.SplitN(input, "\n", 2)
	if len(lines) != 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "// @exec:") {
		return input, codeModeExecOptions{}, nil
	}
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "// @exec:"))
	options := codeModeExecOptions{}
	if raw != "" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return "", options, err
		}
		for key := range fields {
			if key != "yield_time_ms" && key != "max_output_tokens" {
				return "", options, fmt.Errorf("exec pragma only supports yield_time_ms and max_output_tokens; got %q", key)
			}
		}
		if err := json.Unmarshal([]byte(raw), &options); err != nil {
			return "", options, err
		}
	}
	if options.YieldTimeMS != nil && *options.YieldTimeMS < 0 {
		return "", options, fmt.Errorf("yield_time_ms must be non-negative")
	}
	if options.MaxOutputTokens != nil && *options.MaxOutputTokens < 0 {
		return "", options, fmt.Errorf("max_output_tokens must be non-negative")
	}
	if options.YieldTimeMS != nil && *options.YieldTimeMS > codeModeMaxSafeInteger {
		return "", options, fmt.Errorf("yield_time_ms must be a non-negative safe integer")
	}
	if options.MaxOutputTokens != nil && *options.MaxOutputTokens > codeModeMaxSafeInteger {
		return "", options, fmt.Errorf("max_output_tokens must be a non-negative safe integer")
	}
	return lines[1], options, nil
}

func codeModeTokenLimit(value *int) int {
	if value == nil {
		return 10000
	}
	return *value
}

func truncateCodeModeOutput(output *Output, maxTokens int) *Output {
	if output == nil {
		return nil
	}
	truncated := utils.FormattedTruncateText(output.Body, utils.TokensPolicy(maxTokens))
	if truncated == output.Body {
		return output
	}
	output.Body = truncated
	if output.Data != nil {
		output.Data["content_items"] = []map[string]any{{"type": "input_text", "text": output.Body}}
	}
	return output
}

func (e *codeModeExecExecutor) consumeCell(cellID string, cell *codeModeCell) (*Output, error) {
	e.cellsMu.Lock()
	delete(e.cells, cellID)
	output, err := cell.output, cell.err
	e.cellsMu.Unlock()
	if output != nil {
		output.Data["cell_id"] = cellID
		output.Body = e.cellDelta(cell)
		output.Data["content_items"] = e.cellContentDelta(cell)
	}
	return output, err
}

func (e *codeModeExecExecutor) cellDelta(cell *codeModeCell) string {
	if cell == nil {
		return ""
	}
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if cell.cursor >= len(cell.texts) {
		return ""
	}
	delta := strings.Join(cell.texts[cell.cursor:], "\n")
	cell.cursor = len(cell.texts)
	return delta
}

func (e *codeModeExecExecutor) cellContentDelta(cell *codeModeCell) []map[string]any {
	if cell == nil {
		return nil
	}
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if len(cell.items) == 0 {
		return []map[string]any{}
	}
	items := append([]map[string]any(nil), cell.items...)
	cell.items = nil
	return items
}

type codeModeWaitExecutor struct{ exec *codeModeExecExecutor }

func (e *codeModeWaitExecutor) Spec() Spec {
	return Spec{Name: PlainName("wait"), Description: "Waits on a yielded exec cell and returns new output or completion.", InputSchema: map[string]any{"type": "object", "required": []string{"cell_id"}, "properties": map[string]any{"cell_id": map[string]any{"type": "string"}, "yield_time_ms": map[string]any{"type": "integer"}, "max_tokens": map[string]any{"type": "integer"}, "terminate": map[string]any{"type": "boolean"}}}}
}

func (e *codeModeWaitExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	var params codeModeWaitParams
	if err := invocation.DecodeArguments(&params); err != nil {
		return nil, RespondToModel(err.Error())
	}
	if strings.TrimSpace(params.CellID) == "" {
		return nil, RespondToModel("cell_id is required")
	}
	e.exec.cellsMu.Lock()
	cell := e.exec.cells[params.CellID]
	e.exec.cellsMu.Unlock()
	if cell == nil {
		return nil, RespondToModel(fmt.Sprintf("exec cell %s not found", params.CellID))
	}
	if params.Terminate {
		e.exec.cellsMu.Lock()
		if cell.terminated {
			e.exec.cellsMu.Unlock()
			return nil, RespondToModel(fmt.Sprintf("exec cell %s is already terminating", params.CellID))
		}
		cell.terminated = true
		e.exec.cellsMu.Unlock()
		cell.cancel()
	}
	waitMS := params.YieldTimeMS
	if waitMS <= 0 {
		waitMS = 10000
	}
	timer := time.NewTimer(time.Duration(waitMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-cell.done:
		output, runErr := e.exec.consumeCell(params.CellID, cell)
		if params.Terminate && output != nil {
			output.Success = true
			if output.Data == nil {
				output.Data = map[string]any{}
			}
			output.Data["terminated"] = true
		}
		return truncateCodeModeOutput(output, codeModeWaitTokenLimit(params.MaxTokens)), runErr
	case <-timer.C:
		output := &Output{CallID: invocation.CallID, ToolName: PlainName("wait"), Success: true, Body: "Script running with cell ID " + params.CellID + "\n" + e.exec.cellDelta(cell), Data: map[string]any{"cell_id": params.CellID, "running": true}}
		return truncateCodeModeOutput(output, codeModeWaitTokenLimit(params.MaxTokens)), nil
	}
}

func codeModeWaitTokenLimit(value int) int {
	if value == 0 {
		return 10000
	}
	return value
}

type codeModeExit struct{}

func (codeModeExit) Error() string { return "code mode exit" }

func (e *codeModeExecExecutor) installContentHelpers(runtime *sobek.Runtime, items *[]map[string]any) error {
	if err := runtime.Set("image", func(call sobek.FunctionCall) sobek.Value {
		value := call.Argument(0)
		url := value.String()
		detail := "high"
		if object, ok := value.(*sobek.Object); ok {
			if candidate := object.Get("image_url"); candidate != nil && !sobek.IsUndefined(candidate) {
				url = candidate.String()
			} else if data := object.Get("data"); data != nil && !sobek.IsUndefined(data) {
				mimeType := "image/png"
				if candidate := object.Get("mimeType"); candidate != nil && !sobek.IsUndefined(candidate) && strings.TrimSpace(candidate.String()) != "" {
					mimeType = candidate.String()
				}
				url = "data:" + mimeType + ";base64," + data.String()
			}
			if candidate := object.Get("detail"); candidate != nil && !sobek.IsUndefined(candidate) && !sobek.IsNull(candidate) {
				detail = candidate.String()
			}
			if meta := object.Get("_meta"); meta != nil && !sobek.IsUndefined(meta) && !sobek.IsNull(meta) {
				if metaObject, ok := meta.(*sobek.Object); ok {
					if candidate := metaObject.Get("codex/imageDetail"); candidate != nil && !sobek.IsUndefined(candidate) && !sobek.IsNull(candidate) {
						detail = candidate.String()
					}
				}
			}
		}
		if len(call.Arguments) > 1 && !sobek.IsUndefined(call.Argument(1)) && !sobek.IsNull(call.Argument(1)) {
			detail = call.Argument(1).String()
		}
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			panic(runtime.ToValue("remote image URLs are not supported"))
		}
		*items = append(*items, map[string]any{"type": "input_image", "image_url": url, "detail": detail})
		return sobek.Undefined()
	}); err != nil {
		return err
	}
	if err := runtime.Set("audio", func(call sobek.FunctionCall) sobek.Value {
		value := call.Argument(0)
		url := value.String()
		if object, ok := value.(*sobek.Object); ok {
			if candidate := object.Get("audio_url"); candidate != nil && !sobek.IsUndefined(candidate) {
				url = candidate.String()
			} else if data := object.Get("data"); data != nil && !sobek.IsUndefined(data) {
				mimeType := "audio/wav"
				if candidate := object.Get("mimeType"); candidate != nil && !sobek.IsUndefined(candidate) && strings.TrimSpace(candidate.String()) != "" {
					mimeType = candidate.String()
				}
				url = "data:" + mimeType + ";base64," + data.String()
			}
		}
		*items = append(*items, map[string]any{"type": "input_audio", "audio_url": url})
		return sobek.Undefined()
	}); err != nil {
		return err
	}
	return runtime.Set("generatedImage", func(call sobek.FunctionCall) sobek.Value {
		object := call.Argument(0).ToObject(runtime)
		url := object.Get("image_url").String()
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			panic(runtime.ToValue("remote image URLs are not supported"))
		}
		*items = append(*items, map[string]any{"type": "input_image", "image_url": url, "detail": "high"})
		if hint := object.Get("output_hint"); hint != nil && !sobek.IsUndefined(hint) {
			*items = append(*items, map[string]any{"type": "input_text", "text": hint.String()})
		}
		return sobek.Undefined()
	})
}

func nestedPayload(runtime *sobek.Runtime, spec Spec, call sobek.FunctionCall) (Payload, error) {
	if spec.Freeform != nil {
		if len(call.Arguments) == 0 {
			return Payload{}, fmt.Errorf("%s expects a string argument", spec.Name.Key())
		}
		return Payload{Kind: PayloadCustom, Input: call.Argument(0).String()}, nil
	}
	argument := map[string]any{}
	if len(call.Arguments) > 0 && !sobek.IsUndefined(call.Argument(0)) {
		if err := runtime.ExportTo(call.Argument(0), &argument); err != nil {
			return Payload{}, fmt.Errorf("invalid %s arguments: %w", spec.Name.Key(), err)
		}
	}
	encoded, err := json.Marshal(argument)
	return Payload{Kind: PayloadFunction, Arguments: string(encoded)}, err
}

func codeModeToolResult(output *Output) map[string]any {
	result := map[string]any{"success": output != nil && output.Success, "output": ""}
	if output == nil {
		return result
	}
	result["body"] = output.Body
	result["error"] = output.Error
	for key, value := range output.Data {
		result[key] = value
	}
	result["output"] = normalizeNestedExecOutput(output.Body)
	return result
}

func codeModeToolFailure(name ToolName, output *Output) (string, bool) {
	if output == nil || !IsShellCommandToolName(name) {
		return "", false
	}
	failed := !output.Success
	if timedOut, _ := output.Data["timed_out"].(bool); timedOut {
		failed = true
	}
	if _, running := output.Data["process_id"]; !running {
		if exitCode, ok := codeModeInt(output.Data["exit_code"]); ok && exitCode != 0 {
			failed = true
		}
	}
	if !failed {
		return "", false
	}
	if body := strings.TrimSpace(output.Body); body != "" {
		return body, true
	}
	if message := strings.TrimSpace(output.Error); message != "" {
		return message, true
	}
	return fmt.Sprintf("tool %s failed", name.Key()), true
}

func codeModeInt(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float64:
		return int(value), value == float64(int(value))
	case json.Number:
		parsed, err := value.Int64()
		return int(parsed), err == nil
	default:
		return 0, false
	}
}

func payloadCommand(payload Payload) (string, bool) {
	var args struct {
		Cmd     string `json:"cmd"`
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(payload.Arguments), &args) != nil {
		return "", false
	}
	command := firstNonEmptyString(args.Cmd, args.Command)
	return command, command != ""
}

func renderCodeModeValue(value sobek.Value) string {
	if sobek.IsUndefined(value) {
		return "undefined"
	}
	if sobek.IsNull(value) {
		return "null"
	}
	if text, ok := value.Export().(string); ok {
		return text
	}
	encoded, err := json.Marshal(value.Export())
	if err == nil {
		return string(encoded)
	}
	return value.String()
}

func codeModeIdentifier(value string) string {
	var b strings.Builder
	for index, char := range value {
		valid := char == '_' || char == '$' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || index > 0 && char >= '0' && char <= '9'
		if valid {
			b.WriteRune(char)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func normalizeNestedExecOutput(value string) string {
	if marker := strings.Index(value, "Output:\n"); marker >= 0 {
		value = value[marker+len("Output:\n"):]
	}
	return strings.TrimRight(value, "\n")
}
