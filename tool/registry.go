package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrToolNotFound      = errors.New("tool not found")
	ErrToolInvalidCall   = errors.New("invalid tool call")
	ErrToolCancelled     = errors.New("tool call cancelled")
	ErrToolNotParallel   = errors.New("tool does not support parallel calls")
	ErrDuplicateToolName = errors.New("duplicate tool name")
)

type Exposure string

const (
	ExposureModelVisible    Exposure = "model_visible"
	ExposureDirectModelOnly Exposure = "direct_model_only"
	ExposureHidden          Exposure = "hidden"
	ExposureDiscoverable    Exposure = "discoverable"
)

type ToolName struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

func PlainName(name string) ToolName {
	return ToolName{Name: name}
}

func NamespacedName(namespace string, name string) ToolName {
	return ToolName{Namespace: namespace, Name: name}
}

func (n *ToolName) Key() string {
	if n == nil {
		return ""
	}
	if n.Namespace == "" {
		return n.Name
	}
	return n.Namespace + "." + n.Name
}

type Spec struct {
	Name                 ToolName       `json:"name"`
	Description          string         `json:"description,omitempty"`
	InputSchema          map[string]any `json:"inputSchema,omitempty"`
	OutputSchema         map[string]any `json:"outputSchema,omitempty"`
	Freeform             *FreeformSpec  `json:"freeform,omitempty"`
	Search               *SearchInfo    `json:"search,omitempty"`
	Exposure             Exposure       `json:"exposure,omitempty"`
	Parallel             bool           `json:"parallel,omitempty"`
	ReadOnlyHint         *bool          `json:"-"`
	NamespaceDescription string         `json:"-"`
}

type FreeformSpec struct {
	Syntax     string `json:"syntax"`
	Definition string `json:"definition"`
}

type SearchInfo struct {
	Text   string            `json:"text,omitempty"`
	Source *SearchSourceInfo `json:"source,omitempty"`
}

type SearchSourceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PayloadKind string

const (
	PayloadFunction   PayloadKind = "function"
	PayloadToolSearch PayloadKind = "tool_search"
	PayloadCustom     PayloadKind = "custom"
)

type Payload struct {
	Kind      PayloadKind    `json:"kind"`
	Arguments string         `json:"arguments,omitempty"`
	Input     string         `json:"input,omitempty"`
	Search    map[string]any `json:"search,omitempty"`
}

type Invocation struct {
	CallID    string
	ToolName  ToolName
	Payload   Payload
	Source    string
	StartedAt time.Time
	Context   map[string]any
	Cancel    context.CancelCauseFunc
}

type CodeModeNotifyFunc func(callID string, text string)
type CodeModeNestedToolStartedFunc func(context.Context, *Invocation, time.Time)
type CodeModeNestedToolCompletedFunc func(context.Context, *Invocation, *Output, error, time.Time, time.Time)

func (i *Invocation) DecodeArguments(target any) error {
	if i == nil {
		return fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if target == nil {
		return fmt.Errorf("%w: target is nil", ErrToolInvalidCall)
	}
	if i.Payload.Arguments == "" {
		i.Payload.Arguments = "{}"
	}
	return json.Unmarshal([]byte(i.Payload.Arguments), target)
}

type Output struct {
	CallID      string         `json:"callId"`
	ToolName    ToolName       `json:"toolName"`
	Success     bool           `json:"success"`
	Body        string         `json:"body,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
	Error       string         `json:"error,omitempty"`
	LogPreview  string         `json:"logPreview,omitempty"`
	CompletedAt time.Time      `json:"completedAt"`
}

type Executor interface {
	Spec() Spec
	Execute(ctx context.Context, invocation *Invocation) (*Output, error)
}

// ReadinessWaiter lets an exact tool runtime wait for external readiness before
// it enters the response's parallel/serial execution gate.
type ReadinessWaiter interface {
	WaitUntilReady(ctx context.Context, invocation *Invocation) error
}

type ExecutorFunc struct {
	spec Spec
	run  func(context.Context, *Invocation) (*Output, error)
}

func NewExecutorFunc(spec Spec, run func(context.Context, *Invocation) (*Output, error)) *ExecutorFunc {
	return &ExecutorFunc{spec: spec, run: run}
}

func (e *ExecutorFunc) Spec() Spec {
	return e.spec
}

func (e *ExecutorFunc) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if e == nil || e.run == nil {
		return nil, fmt.Errorf("%w: executor is nil", ErrToolInvalidCall)
	}
	return e.run(ctx, invocation)
}

type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
	specs     map[string]Spec
	order     []string
}

func NewRegistry() *Registry {
	return &Registry{executors: map[string]Executor{}, specs: map[string]Spec{}, order: []string{}}
}

func (r *Registry) Register(executor Executor) error {
	_, err := r.register(executor, false, false)
	return err
}

// RegisterExternal retains the first runtime registered for a name. External
// tool sources are untrusted input and name collisions must not replace host
// runtimes or abort construction of the turn.
func (r *Registry) RegisterExternal(executor Executor) (bool, error) {
	return r.register(executor, true, false)
}

// Prepend registers a trusted synthetic runtime ahead of existing runtimes.
func (r *Registry) Prepend(executor Executor) error {
	_, err := r.register(executor, false, true)
	return err
}

func (r *Registry) register(executor Executor, skipDuplicate bool, prepend bool) (bool, error) {
	if executor == nil {
		return false, fmt.Errorf("%w: executor is nil", ErrToolInvalidCall)
	}
	spec := executor.Spec()
	key := spec.Name.Key()
	if key == "" {
		return false, fmt.Errorf("%w: name is required", ErrToolInvalidCall)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.executors[key]; exists {
		if skipDuplicate {
			return false, nil
		}
		return false, fmt.Errorf("%w: %s", ErrDuplicateToolName, key)
	}
	r.executors[key] = executor
	r.specs[key] = spec
	if prepend {
		r.order = append([]string{key}, r.order...)
	} else {
		r.order = append(r.order, key)
	}
	return true, nil
}

func (r *Registry) Remove(name ToolName) (Executor, bool) {
	if r == nil {
		return nil, false
	}
	key := name.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	executor, ok := r.executors[key]
	if !ok {
		return nil, false
	}
	delete(r.executors, key)
	delete(r.specs, key)
	for index, orderedKey := range r.order {
		if orderedKey == key {
			r.order = append(r.order[:index], r.order[index+1:]...)
			break
		}
	}
	return executor, true
}

func (r *Registry) Lookup(name ToolName) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	executor, ok := r.executors[name.Key()]
	return executor, ok
}

func (r *Registry) Spec(name ToolName) (Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[name.Key()]
	return spec, ok
}

func (r *Registry) ModelVisibleSpecs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.specs))
	for _, key := range r.order {
		spec := r.specs[key]
		if spec.Exposure == "" || spec.Exposure == ExposureModelVisible || spec.Exposure == ExposureDirectModelOnly {
			out = append(out, spec)
		}
	}
	return out
}

func (r *Registry) DiscoverableSpecs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.specs))
	for _, key := range r.order {
		spec := r.specs[key]
		if spec.Exposure == ExposureDiscoverable {
			out = append(out, spec)
		}
	}
	return out
}

func (r *Registry) DeferredToolNamespaces() map[string]string {
	namespaces := map[string]string{}
	for _, spec := range r.DiscoverableSpecs() {
		namespace := strings.TrimSpace(spec.Name.Namespace)
		if namespace == "" {
			continue
		}
		description := strings.TrimSpace(spec.NamespaceDescription)
		if existing, ok := namespaces[namespace]; !ok || (strings.TrimSpace(existing) == "" && description != "") {
			namespaces[namespace] = description
		}
	}
	return namespaces
}

func (r *Registry) Names() []ToolName {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolName, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.specs[key].Name)
	}
	return out
}

type Router struct {
	registry *Registry
	now      func() time.Time
}

func NewRouter(registry *Registry) *Router {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Router{registry: registry, now: time.Now}
}

func (r *Router) SetClock(clock func() time.Time) {
	if clock == nil {
		r.now = time.Now
		return
	}
	r.now = clock
}

func (r *Router) RegisterIfAbsent(executor Executor) error {
	if r == nil || r.registry == nil {
		return fmt.Errorf("%w: tool router is nil", ErrToolInvalidCall)
	}
	if executor == nil {
		return fmt.Errorf("%w: executor is nil", ErrToolInvalidCall)
	}
	if _, ok := r.registry.Lookup(executor.Spec().Name); ok {
		return nil
	}
	if err := r.registry.Register(executor); err != nil && !errors.Is(err, ErrDuplicateToolName) {
		return err
	}
	return nil
}

func (r *Router) ModelVisibleSpecs() []Spec {
	if r == nil || r.registry == nil {
		return nil
	}
	return r.registry.ModelVisibleSpecs()
}

func (r *Router) Executor(name ToolName) (Executor, bool) {
	if r == nil || r.registry == nil {
		return nil, false
	}
	return r.registry.Lookup(name)
}

func (r *Router) DeclaresOutputSchema(name ToolName) bool {
	if r == nil || r.registry == nil {
		return false
	}
	spec, ok := r.registry.Spec(name)
	return ok && spec.OutputSchema != nil
}

func (r *Router) DeferredToolNamespaces() map[string]string {
	if r == nil || r.registry == nil {
		return nil
	}
	return r.registry.DeferredToolNamespaces()
}

func (r *Router) CodeModeToolNames() map[string]CodeModeToolNameMetadata {
	if r == nil || r.registry == nil {
		return nil
	}
	executor, ok := r.registry.Lookup(PlainName(CodeModeExecToolName))
	if !ok {
		return nil
	}
	provider, ok := executor.(interface {
		CodeModeToolNames() map[string]CodeModeToolNameMetadata
	})
	if !ok {
		return nil
	}
	return provider.CodeModeToolNames()
}

func (r *Router) CodeModeToolSpecs() []Spec {
	if r == nil || r.registry == nil {
		return nil
	}
	executor, ok := r.registry.Lookup(PlainName(CodeModeExecToolName))
	if !ok {
		return nil
	}
	provider, ok := executor.(interface {
		CodeModeToolSpecs() []Spec
	})
	if !ok {
		return nil
	}
	return provider.CodeModeToolSpecs()
}

func (r *Router) BuildToolCall(item ResponseItem) (*Invocation, bool, error) {
	if item.Type != "function_call" && item.Type != "tool_search_call" && item.Type != "custom_tool_call" {
		return nil, false, nil
	}
	if item.CallID == "" {
		return nil, true, fmt.Errorf("%w: call id is required", ErrToolInvalidCall)
	}
	name := r.resolveResponseToolName(ToolName{Namespace: item.Namespace, Name: item.Name})
	payload := Payload{Kind: PayloadFunction, Arguments: item.Arguments}
	if item.Type == "tool_search_call" {
		if item.Execution != "" && item.Execution != "client" {
			return nil, false, nil
		}
		name = PlainName("tool_search")
		payload = Payload{Kind: PayloadToolSearch, Search: item.Search}
	}
	if item.Type == "custom_tool_call" {
		payload = Payload{Kind: PayloadCustom, Input: item.Input}
	}
	source := ""
	if directPlaintextCollaborationCall(name, item.EncryptedFunctionArgs) {
		source = "direct_plaintext_message"
	}
	invocation := &Invocation{CallID: item.CallID, ToolName: name, Payload: payload, Source: source, StartedAt: r.now().UTC()}
	if r != nil && r.registry != nil {
		if spec, ok := r.registry.Spec(name); ok {
			applySpecInvocationContext(invocation, spec)
		}
	}
	return invocation, true, nil
}

func directPlaintextCollaborationCall(name ToolName, encryptedFunctionArgs *[]string) bool {
	if encryptedFunctionArgs == nil || len(*encryptedFunctionArgs) != 0 || name.Namespace != "collaboration" {
		return false
	}
	switch name.Name {
	case "spawn_agent", "send_message", "followup_task":
		return true
	default:
		return false
	}
}

func applySpecInvocationContext(invocation *Invocation, spec Spec) {
	if invocation == nil || spec.ReadOnlyHint == nil {
		return
	}
	if invocation.Context == nil {
		invocation.Context = map[string]any{}
	}
	invocation.Context["read_only_hint"] = *spec.ReadOnlyHint
}

func (r *Router) resolveResponseToolName(name ToolName) ToolName {
	if r == nil || r.registry == nil {
		return name
	}
	if _, ok := r.registry.Lookup(name); ok {
		return name
	}
	if resolved, ok := r.registry.resolveCompatibleToolName(name); ok {
		return resolved
	}
	key := strings.TrimSpace(name.Name)
	if key == "" {
		return name
	}
	for _, spec := range r.registry.NamesAsSpecs() {
		if ResponsesAPIName(spec.Name) == key {
			return spec.Name
		}
	}
	return name
}

func (r *Registry) resolveCompatibleToolName(name ToolName) (ToolName, bool) {
	if r == nil {
		return ToolName{}, false
	}
	namespace := strings.TrimSpace(name.Namespace)
	toolName := strings.TrimSpace(name.Name)
	if toolName == "" {
		return ToolName{}, false
	}
	var match ToolName
	matches := 0
	for _, spec := range r.NamesAsSpecs() {
		candidate := spec.Name
		candidateNamespace := strings.TrimSpace(candidate.Namespace)
		candidateName := strings.TrimSpace(candidate.Name)
		compatible := false
		switch {
		case namespace != "":
			compatible = candidateName == toolName &&
				(candidateNamespace == namespace || strings.TrimPrefix(candidateNamespace, "mcp__") == namespace)
		case candidate.Key() == toolName:
			compatible = true
		case candidateName == toolName:
			compatible = true
		}
		if !compatible {
			continue
		}
		match = candidate
		matches++
		if matches > 1 {
			return ToolName{}, false
		}
	}
	return match, matches == 1
}

func (r *Router) Dispatch(ctx context.Context, invocation *Invocation) (*Output, error) {
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	executor, ok := r.registry.Lookup(invocation.ToolName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, invocation.ToolName.Key())
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, ErrToolCancelled
	default:
	}
	output, err := executeToolSafely(ctx, executor, invocation)
	if err != nil {
		return nil, err
	}
	if output == nil {
		output = &Output{Success: true}
	}
	output.CallID = invocation.CallID
	output.ToolName = invocation.ToolName
	if output.CompletedAt.IsZero() {
		output.CompletedAt = r.now().UTC()
	}
	return output, nil
}

func executeToolSafely(ctx context.Context, executor Executor, invocation *Invocation) (output *Output, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			toolName := "<unknown>"
			if invocation != nil {
				toolName = invocation.ToolName.Key()
			}
			err = Fatal(fmt.Sprintf("tool %s panicked: %v", toolName, recovered))
		}
	}()
	return executor.Execute(ctx, invocation)
}

func (r *Router) SupportsParallel(name ToolName) bool {
	spec, ok := r.registry.Spec(name)
	return ok && spec.Parallel
}

func (r *Router) HasReadinessWait(name ToolName) bool {
	if r == nil || r.registry == nil {
		return false
	}
	executor, ok := r.registry.Lookup(name)
	if !ok {
		return false
	}
	_, ok = executor.(ReadinessWaiter)
	return ok
}

func (r *Router) WaitUntilReady(ctx context.Context, invocation *Invocation) error {
	if r == nil || r.registry == nil || invocation == nil {
		return nil
	}
	executor, ok := r.registry.Lookup(invocation.ToolName)
	if !ok {
		return nil
	}
	waiter, ok := executor.(ReadinessWaiter)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return waiter.WaitUntilReady(ctx, invocation)
}

func (r *Router) DispatchParallel(ctx context.Context, invocations []Invocation) ([]Output, error) {
	results := make([]Output, len(invocations))
	for i := range invocations {
		if !r.SupportsParallel(invocations[i].ToolName) {
			return nil, fmt.Errorf("%w: %s", ErrToolNotParallel, invocations[i].ToolName.Key())
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := range invocations {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, err := r.Dispatch(ctx, &invocations[i])
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
				return
			}
			if output != nil {
				results[i] = *output
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

type ResponseItem struct {
	Type                  string
	Namespace             string
	Name                  string
	CallID                string
	Arguments             string
	EncryptedFunctionArgs *[]string
	Input                 string
	Execution             string
	Search                map[string]any
}

type LifecycleEvent struct {
	Type     string
	CallID   string
	ToolName ToolName
	Message  string
	At       time.Time
}

func StartEvent(invocation *Invocation, at time.Time) LifecycleEvent {
	return LifecycleEvent{Type: "started", CallID: invocation.CallID, ToolName: invocation.ToolName, At: at}
}

func FinishEvent(output *Output, at time.Time) LifecycleEvent {
	event := LifecycleEvent{Type: "completed", CallID: output.CallID, ToolName: output.ToolName, At: at}
	if !output.Success {
		event.Type = "failed"
		event.Message = output.Error
	}
	return event
}

func sortSpecs(specs []Spec) {
	for i := 1; i < len(specs); i++ {
		current := specs[i]
		j := i - 1
		for j >= 0 && specs[j].Name.Key() > current.Name.Key() {
			specs[j+1] = specs[j]
			j--
		}
		specs[j+1] = current
	}
}

func sortNames(names []ToolName) {
	for i := 1; i < len(names); i++ {
		current := names[i]
		j := i - 1
		for j >= 0 && names[j].Key() > current.Key() {
			names[j+1] = names[j]
			j--
		}
		names[j+1] = current
	}
}
