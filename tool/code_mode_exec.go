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
	bindingMu         sync.RWMutex
	registry          *Registry
	nestedCommandTool ToolName
	nextID            atomic.Uint64
	storeMu           sync.RWMutex
	store             map[string]json.RawMessage
	cellsMu           sync.Mutex
	cells             map[string]*codeModeCell
	remote            CodeModeRemoteSession
	provider          CodeModeRemoteProvider
	disableFallback   bool
	warningEmitted    atomic.Bool
	remoteCellsMu     sync.RWMutex
	remoteCells       map[string]*Registry
}

type CodeModeRemoteProvider interface {
	NewSession(delegate CodeModeRemoteDelegate) CodeModeRemoteSession
}

type CodeModeRemoteAvailabilityProvider interface {
	Availability() error
}

type CodeModeRemoteSession interface {
	Execute(context.Context, CodeModeRemoteExecuteRequest) (CodeModeRemoteResponse, error)
	Wait(context.Context, string, uint64) (CodeModeRemoteResponse, error)
	Terminate(context.Context, string) (CodeModeRemoteResponse, error)
	Close() error
}

type CodeModeRemoteDelegate interface {
	Invoke(context.Context, CodeModeRemoteNestedCall) (json.RawMessage, error)
	Notify(context.Context, string, string, string) error
}

type CodeModeRemoteNestedCall struct {
	CellID            string
	RuntimeToolCallID string
	ToolName          ToolName
	Kind              PayloadKind
	Input             json.RawMessage
}

type CodeModeRemoteToolDefinition struct {
	Name         string
	ToolName     ToolName
	Description  string
	Kind         PayloadKind
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
}

type CodeModeToolNameMetadata struct {
	Name      string  `json:"name"`
	Namespace *string `json:"namespace"`
}

type codeModeNestedTool struct {
	globalName string
	name       ToolName
	spec       Spec
}

type CodeModeRemoteExecuteRequest struct {
	ToolCallID      string
	Source          string
	EnabledTools    []CodeModeRemoteToolDefinition
	YieldTimeMS     *uint64
	MaxOutputTokens *int
}

type CodeModeRemoteResponse struct {
	CellID       string
	State        string
	ContentItems []map[string]any
	ErrorText    string
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
	return NewCodeModeExecutorsWithProvider(registry, nil, false, nestedCommandTool...)
}

func NewCodeModeExecutorsWithProvider(registry *Registry, provider CodeModeRemoteProvider, disableFallback bool, nestedCommandTool ...ToolName) (Executor, Executor) {
	runtime := NewCodeModeRuntime(provider, disableFallback)
	return runtime.Executors(registry, nestedCommandTool...)
}

// CodeModeRuntime owns the session state shared by every turn in one thread.
// Each turn rebinds its current registry while the remote host session, cells,
// and store remain alive until the thread is unloaded.
type CodeModeRuntime struct {
	exec      *codeModeExecExecutor
	wait      *codeModeWaitExecutor
	closeOnce sync.Once
	closeErr  error
}

func NewCodeModeRuntime(provider CodeModeRemoteProvider, disableFallback bool) *CodeModeRuntime {
	exec := &codeModeExecExecutor{
		store: map[string]json.RawMessage{}, cells: map[string]*codeModeCell{}, remoteCells: map[string]*Registry{},
		provider: provider, disableFallback: disableFallback,
	}
	if provider != nil {
		exec.remote = provider.NewSession(&codeModeRemoteDelegate{exec: exec})
	}
	return &CodeModeRuntime{exec: exec, wait: &codeModeWaitExecutor{exec: exec}}
}

func (r *CodeModeRuntime) Executors(registry *Registry, nestedCommandTool ...ToolName) (Executor, Executor) {
	var commandTool ToolName
	if len(nestedCommandTool) > 0 {
		commandTool = nestedCommandTool[0]
	}
	if r == nil || r.exec == nil {
		return NewCodeModeExecutorsWithProvider(registry, nil, false, nestedCommandTool...)
	}
	r.exec.bind(registry, commandTool)
	return r.exec, r.wait
}

func (r *CodeModeRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.exec == nil {
			return
		}
		r.exec.cellsMu.Lock()
		for _, cell := range r.exec.cells {
			if cell != nil && cell.cancel != nil {
				cell.cancel()
			}
		}
		r.exec.cellsMu.Unlock()
		if r.exec.remote != nil {
			r.closeErr = r.exec.remote.Close()
		}
		remoteInvocationStates.Delete(r.exec)
	})
	return r.closeErr
}

func (e *codeModeExecExecutor) bind(registry *Registry, nestedCommandTool ToolName) {
	e.bindingMu.Lock()
	e.registry = registry
	e.nestedCommandTool = nestedCommandTool
	e.bindingMu.Unlock()
}

func (e *codeModeExecExecutor) binding() (*Registry, ToolName) {
	if e == nil {
		return nil, ToolName{}
	}
	e.bindingMu.RLock()
	defer e.bindingMu.RUnlock()
	return e.registry, e.nestedCommandTool
}

func (e *codeModeExecExecutor) Spec() Spec {
	registry, nestedCommandTool := e.binding()
	return Spec{
		Name:        PlainName(CodeModeExecToolName),
		Description: codeModeExecDescription(registry, nestedCommandTool),
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
	registry, _ := e.binding()
	if registry == nil {
		return nil, Fatal("code mode tool registry is unavailable")
	}
	source, options, err := parseCodeModeSource(invocation.Payload.Input)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("invalid exec pragma: %v", err))
	}
	if len(invocation.Payload.Input) > codeModeMaxFrameBytes {
		return nil, RespondToModel(fmt.Sprintf("code-mode IPC frame length %d exceeds %d bytes", len(invocation.Payload.Input), codeModeMaxFrameBytes))
	}
	if e.remote == nil && e.disableFallback {
		return nil, Fatal("code-mode host is disabled and in-process fallback is disabled")
	}
	if e.remote != nil {
		output, remoteErr := e.executeRemote(ctx, invocation, source, options)
		if remoteErr == nil {
			return output, nil
		}
		var callErr *FunctionCallError
		if AsFunctionCallError(remoteErr, &callErr) {
			return nil, callErr
		}
		return nil, Fatal(fmt.Sprintf("code-mode remote host unavailable: %v", remoteErr))
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
	cellID := ""
	if invocation.Context != nil {
		cellID, _ = invocation.Context[CodeModeCellIDContextKey].(string)
		cellID = strings.TrimSpace(cellID)
	}
	if cellID == "" {
		cellID = fmt.Sprintf("cell-%d", e.nextID.Add(1))
	}
	cellCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cell := &codeModeCell{done: make(chan struct{}), cancel: cancel}
	e.cellsMu.Lock()
	e.cells[cellID] = cell
	e.cellsMu.Unlock()
	invocationCopy := *invocation
	invocationCopy.Payload.Input = source
	invocationCopy.Context = cloneInvocationContext(invocation.Context)
	if invocationCopy.Context == nil {
		invocationCopy.Context = map[string]any{}
	}
	invocationCopy.Context[CodeModeCellIDContextKey] = cellID
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

func (e *codeModeExecExecutor) executeRemote(ctx context.Context, invocation *Invocation, source string, options codeModeExecOptions) (*Output, error) {
	delegate, _ := e.remoteDelegate()
	done := delegate.begin(invocation)
	defer done()
	definitions := make([]CodeModeRemoteToolDefinition, 0)
	for _, nested := range e.nestedTools() {
		name := nested.name
		spec := nested.spec
		kind := PayloadFunction
		var inputSchema json.RawMessage
		if spec.Freeform != nil {
			kind = PayloadCustom
			inputSchema, _ = json.Marshal(map[string]any{"type": "grammar", "syntax": spec.Freeform.Syntax, "definition": spec.Freeform.Definition})
		} else {
			inputSchema, _ = json.Marshal(spec.InputSchema)
		}
		outputSchema, _ := json.Marshal(spec.OutputSchema)
		definitions = append(definitions, CodeModeRemoteToolDefinition{Name: nested.globalName, ToolName: name, Description: spec.Description, Kind: kind, InputSchema: inputSchema, OutputSchema: outputSchema})
	}
	var yield *uint64
	if options.YieldTimeMS != nil {
		value := uint64(*options.YieldTimeMS)
		yield = &value
	}
	response, err := e.remote.Execute(ctx, CodeModeRemoteExecuteRequest{ToolCallID: invocation.CallID, Source: source, EnabledTools: definitions, YieldTimeMS: yield, MaxOutputTokens: options.MaxOutputTokens})
	if err != nil {
		return nil, err
	}
	if response.State == "yielded" && strings.TrimSpace(response.CellID) != "" {
		registry, _ := e.binding()
		e.remoteCellsMu.Lock()
		e.remoteCells[response.CellID] = registry
		e.remoteCellsMu.Unlock()
	}
	return remoteResponseOutput(invocation.CallID, response, codeModeTokenLimit(options.MaxOutputTokens))
}

func (e *codeModeExecExecutor) CodeModeToolNames() map[string]CodeModeToolNameMetadata {
	nestedTools := e.nestedTools()
	if len(nestedTools) == 0 {
		return nil
	}
	out := make(map[string]CodeModeToolNameMetadata, len(nestedTools))
	for _, nested := range nestedTools {
		var namespace *string
		if nested.name.Namespace != "" {
			value := nested.name.Namespace
			namespace = &value
		}
		out[nested.globalName] = CodeModeToolNameMetadata{Name: nested.name.Name, Namespace: namespace}
	}
	return out
}

func (e *codeModeExecExecutor) CodeModeToolSpecs() []Spec {
	nestedTools := e.nestedTools()
	if len(nestedTools) == 0 {
		return nil
	}
	out := make([]Spec, 0, len(nestedTools))
	for _, nested := range nestedTools {
		out = append(out, nested.spec)
	}
	return out
}

func (e *codeModeExecExecutor) CodeModeAvailability() error {
	if e == nil || e.provider == nil {
		return nil
	}
	provider, ok := e.provider.(CodeModeRemoteAvailabilityProvider)
	if !ok {
		return nil
	}
	return provider.Availability()
}

func (e *codeModeExecExecutor) TakeCodeModeUnavailableWarning(effectiveToolMode string) string {
	if e == nil || e.provider == nil || e.warningEmitted.Load() {
		return ""
	}
	provider, ok := e.provider.(CodeModeRemoteAvailabilityProvider)
	if !ok {
		return ""
	}
	err := provider.Availability()
	if err == nil || e.warningEmitted.Swap(true) {
		return ""
	}
	behavior := "Code mode will fail closed"
	if strings.EqualFold(strings.TrimSpace(effectiveToolMode), "direct") {
		behavior = "Falling back to direct tools"
	}
	return fmt.Sprintf("Code Mode is unavailable because %v. %s; enable `features.code_mode_host` and install `codex-code-mode-host`.", err, behavior)
}

func (e *codeModeExecExecutor) nestedTools() []codeModeNestedTool {
	registry, nestedCommandTool := e.binding()
	if registry == nil {
		return nil
	}
	names := registry.Names()
	out := make([]codeModeNestedTool, 0, len(names))
	seenIdentifiers := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name.Key() == CodeModeExecToolName || name.Key() == "wait" {
			continue
		}
		spec, ok := registry.Spec(name)
		if !ok || spec.Exposure == ExposureDirectModelOnly || (spec.Exposure == ExposureHidden && name.Key() != nestedCommandTool.Key()) {
			continue
		}
		globalName := codeModeIdentifier(ResponsesAPIName(name))
		if _, exists := seenIdentifiers[globalName]; exists {
			continue
		}
		seenIdentifiers[globalName] = struct{}{}
		out = append(out, codeModeNestedTool{globalName: globalName, name: name, spec: spec})
	}
	return out
}

func (e *codeModeExecExecutor) nestedToolsForInvocation(invocation *Invocation) []codeModeNestedTool {
	nested := e.nestedTools()
	if invocation == nil || invocation.Context == nil {
		return nested
	}
	allowed, ok := invocation.Context[CodeModeEnabledToolsContextKey].(map[string]struct{})
	if !ok {
		return nested
	}
	out := make([]codeModeNestedTool, 0, len(nested))
	for _, candidate := range nested {
		if _, exists := allowed[candidate.name.Key()]; exists {
			out = append(out, candidate)
		}
	}
	return out
}

func (e *codeModeExecExecutor) registryForRemoteCell(cellID string) *Registry {
	if e == nil {
		return nil
	}
	if cellID = strings.TrimSpace(cellID); cellID != "" {
		e.remoteCellsMu.RLock()
		registry := e.remoteCells[cellID]
		e.remoteCellsMu.RUnlock()
		if registry != nil {
			return registry
		}
	}
	registry, _ := e.binding()
	return registry
}

func (e *codeModeExecExecutor) forgetRemoteCell(cellID string) {
	if e == nil || strings.TrimSpace(cellID) == "" {
		return
	}
	e.remoteCellsMu.Lock()
	delete(e.remoteCells, cellID)
	e.remoteCellsMu.Unlock()
}

func (e *codeModeExecExecutor) remoteDelegate() (*codeModeRemoteDelegate, bool) {
	if e == nil {
		return nil, false
	}
	return &codeModeRemoteDelegate{exec: e}, true
}

type codeModeRemoteDelegate struct{ exec *codeModeExecExecutor }

type codeModeRemoteInvocationState struct {
	mu     sync.Mutex
	active map[string]*Invocation
}

var remoteInvocationStates sync.Map

func remoteInvocationState(exec *codeModeExecExecutor) *codeModeRemoteInvocationState {
	value, _ := remoteInvocationStates.LoadOrStore(exec, &codeModeRemoteInvocationState{active: map[string]*Invocation{}})
	return value.(*codeModeRemoteInvocationState)
}

func (d *codeModeRemoteDelegate) begin(invocation *Invocation) func() {
	state := remoteInvocationState(d.exec)
	state.mu.Lock()
	state.active[invocation.CallID] = invocation
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		delete(state.active, invocation.CallID)
		state.mu.Unlock()
	}
}

func (d *codeModeRemoteDelegate) invocation(callID string) *Invocation {
	state := remoteInvocationState(d.exec)
	state.mu.Lock()
	defer state.mu.Unlock()
	if invocation := state.active[callID]; invocation != nil {
		return invocation
	}
	for parentID, invocation := range state.active {
		if strings.HasPrefix(callID, parentID) {
			return invocation
		}
	}
	if len(state.active) == 1 {
		for _, invocation := range state.active {
			return invocation
		}
	}
	return nil
}

func (d *codeModeRemoteDelegate) Invoke(ctx context.Context, call CodeModeRemoteNestedCall) (json.RawMessage, error) {
	if d == nil || d.exec == nil {
		return nil, fmt.Errorf("code mode tool registry is unavailable")
	}
	registry := d.exec.registryForRemoteCell(call.CellID)
	if registry == nil {
		return nil, fmt.Errorf("code mode tool registry is unavailable")
	}
	executor, ok := registry.Lookup(call.ToolName)
	if !ok {
		return nil, fmt.Errorf("tool %s not found", call.ToolName.Key())
	}
	payload := Payload{Kind: call.Kind}
	if call.Kind == PayloadCustom {
		var input string
		if err := json.Unmarshal(call.Input, &input); err != nil {
			input = string(call.Input)
		}
		payload.Input = input
	} else {
		payload.Arguments = string(call.Input)
	}
	parent := d.invocation(call.RuntimeToolCallID)
	invocationContext := map[string]any(nil)
	if parent != nil {
		invocationContext = cloneInvocationContext(parent.Context)
	}
	invocation := &Invocation{CallID: call.RuntimeToolCallID, ToolName: call.ToolName, Payload: payload, Source: "code_mode", Context: invocationContext}
	if invocation.Context == nil {
		invocation.Context = map[string]any{}
	}
	if strings.TrimSpace(call.CellID) != "" {
		invocation.Context[CodeModeCellIDContextKey] = call.CellID
	}
	applySpecInvocationContext(invocation, executor.Spec())
	startedAt := time.Now().UTC()
	if parent != nil {
		if started, ok := parent.Context["code_mode_nested_tool_started"].(CodeModeNestedToolStartedFunc); ok {
			started(ctx, invocation, startedAt)
		}
	}
	out, err := executor.Execute(ctx, invocation)
	finishedAt := time.Now().UTC()
	if parent != nil {
		if completed, ok := parent.Context["code_mode_nested_tool_completed"].(CodeModeNestedToolCompletedFunc); ok {
			completed(ctx, invocation, out, err, startedAt, finishedAt)
		}
	}
	if err != nil {
		return nil, err
	}
	if failure, failed := codeModeToolFailure(call.ToolName, out); failed {
		return nil, fmt.Errorf("%s", failure)
	}
	return json.Marshal(codeModeToolResult(out))
}

func (d *codeModeRemoteDelegate) Notify(_ context.Context, callID string, _ string, text string) error {
	if invocation := d.invocation(callID); invocation != nil {
		if notify, ok := invocation.Context["code_mode_notify"].(CodeModeNotifyFunc); ok {
			notify(invocation.CallID, text)
		}
	}
	return nil
}

func remoteResponseOutput(callID string, response CodeModeRemoteResponse, maxTokens int) (*Output, error) {
	texts := make([]string, 0)
	for _, item := range response.ContentItems {
		if item["type"] == "input_text" {
			texts = append(texts, fmt.Sprint(item["text"]))
		}
	}
	if strings.TrimSpace(response.ErrorText) != "" {
		return nil, RespondToModel("JavaScript execution failed: " + response.ErrorText)
	}
	body := strings.Join(texts, "\n")
	output := &Output{CallID: callID, ToolName: PlainName(CodeModeExecToolName), Success: true, Body: body, Data: map[string]any{"content_items": response.ContentItems}}
	if strings.TrimSpace(response.CellID) != "" {
		output.Data["cell_id"] = response.CellID
	}
	if response.State == "yielded" {
		output.Body = "Script running with cell ID " + response.CellID + "\n" + body
		output.Data["cell_id"] = response.CellID
		output.Data["running"] = true
	}
	return truncateCodeModeOutput(output, maxTokens), nil
}

func (e *codeModeExecExecutor) executeScript(ctx context.Context, invocation *Invocation, yield chan<- struct{}, cell *codeModeCell) (*Output, error) {
	runtime := sobek.New()
	registry, _ := e.binding()
	nestedRouter := NewRouter(registry)
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
		encoded, err := json.Marshal(value)
		if err != nil {
			panic(runtime.ToValue("store value must be serializable"))
		}
		e.storeMu.Lock()
		e.store[key] = append(json.RawMessage(nil), encoded...)
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
		jsonObject := runtime.Get("JSON").ToObject(runtime)
		parse, ok := sobek.AssertFunction(jsonObject.Get("parse"))
		if !ok {
			panic(runtime.ToValue("JSON.parse is unavailable"))
		}
		parsed, err := parse(jsonObject, runtime.ToValue(string(value)))
		if err != nil {
			panic(runtime.ToValue(err.Error()))
		}
		return parsed
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
	nestedTools := e.nestedToolsForInvocation(invocation)
	toolsObject := runtime.NewObject()
	for _, nested := range nestedTools {
		name := nested.name
		spec := nested.spec
		globalName := nested.globalName
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
				applySpecInvocationContext(nestedInvocation, toolSpec)
				startedAt := time.Now().UTC()
				if started, ok := invocation.Context["code_mode_nested_tool_started"].(CodeModeNestedToolStartedFunc); ok {
					started(ctx, nestedInvocation, startedAt)
				}
				out, callErr := nestedRouter.Dispatch(ctx, nestedInvocation)
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
	for _, nested := range nestedTools {
		metadata = append(metadata, map[string]string{"name": nested.globalName, "description": nested.spec.Description})
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
	if e.exec.remote != nil {
		var response CodeModeRemoteResponse
		var remoteErr error
		if params.Terminate {
			response, remoteErr = e.exec.remote.Terminate(ctx, params.CellID)
		} else {
			waitMS := params.YieldTimeMS
			if waitMS <= 0 {
				waitMS = 10000
			}
			response, remoteErr = e.exec.remote.Wait(ctx, params.CellID, uint64(waitMS))
		}
		if remoteErr == nil {
			if response.State != "yielded" {
				e.exec.forgetRemoteCell(params.CellID)
			}
			return remoteResponseOutput(invocation.CallID, response, codeModeWaitTokenLimit(params.MaxTokens))
		}
		var callErr *FunctionCallError
		if AsFunctionCallError(remoteErr, &callErr) {
			return nil, callErr
		}
		return nil, Fatal(fmt.Sprintf("code-mode remote host unavailable: %v", remoteErr))
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
