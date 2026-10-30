package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/sandbox"
	"codex_go/utils"
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
	ExposureModelVisible      Exposure = "model_visible"
	ExposureDirectModelOnly   Exposure = "direct_model_only"
	ExposureDeferredModelOnly Exposure = "deferred_model_only"
	ExposureCodeModeOnly      Exposure = "code_mode_only"
	ExposureHidden            Exposure = "hidden"
	ExposureDiscoverable      Exposure = "discoverable"
)

// IsCodeModeAvailable reports whether a tool with the given exposure may be
// called from nested Code Mode scripts (Rust ToolExposure::is_available_in_code_mode).
func IsCodeModeAvailable(exposure Exposure) bool {
	switch exposure {
	case "", ExposureModelVisible, ExposureDiscoverable, ExposureCodeModeOnly:
		return true
	default:
		return false
	}
}

// IsDeferred reports whether a tool with the given exposure is discoverable
// through tool search (Rust ToolExposure::is_deferred).
func IsDeferred(exposure Exposure) bool {
	switch exposure {
	case ExposureDiscoverable, ExposureDeferredModelOnly:
		return true
	default:
		return false
	}
}

// IsModelVisible reports whether a tool with the given exposure appears in the
// model's initial tool list.
func IsModelVisible(exposure Exposure) bool {
	switch exposure {
	case "", ExposureModelVisible, ExposureDirectModelOnly:
		return true
	default:
		return false
	}
}

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
	Name        ToolName       `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	// InputSchemaMaxBytes is the internal Code Mode type-rendering budget for
	// this tool (Rust #47936). Zero uses the registry default.
	InputSchemaMaxBytes int            `json:"-"`
	OutputSchema        map[string]any `json:"outputSchema,omitempty"`
	// CodeModeOutputSchema declares a code-mode return type without changing
	// the direct Responses API tool payload (Rust #57836 keeps the namespace
	// function's output_schema out of serialization).
	CodeModeOutputSchema map[string]any `json:"-"`
	Freeform             *FreeformSpec  `json:"freeform,omitempty"`
	Search               *SearchInfo    `json:"search,omitempty"`
	Exposure             Exposure       `json:"exposure,omitempty"`
	Parallel             bool           `json:"parallel,omitempty"`
	ReadOnlyHint         *bool          `json:"-"`
	NamespaceDescription string         `json:"-"`
	// FinishesOnCancellation mirrors Rust CoreToolRuntime::finishes_on_cancellation
	// (#51556). When true the handler observes the invocation's cancellation and
	// publishes its own terminal item, so dispatch must let it finish instead of
	// aborting it (and reports an aborted lifecycle outcome).
	FinishesOnCancellation bool `json:"-"`
	// ThirdParty marks MCP/app and client-supplied dynamic tools. Built-ins stay
	// false even when a third-party tool attempts to use the same name.
	ThirdParty bool `json:"-"`
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
	CallID   string
	ToolName ToolName
	Payload  Payload
	Source   string
	// NativeCall is the original function-call item retained through dispatch
	// for tools whose registry policy requires native review (Rust #58040). It
	// is Guardian evidence only: it is never logged or serialized.
	NativeCall json.RawMessage
	StartedAt  time.Time
	Context    map[string]any
	Cancel     context.CancelCauseFunc
	// Truncation is the effective model's output-truncation policy for this
	// call (Rust `ToolCall::truncation_policy`). It bounds a direct call's
	// response-content budget through ResponseByteBudget; Code Mode calls
	// ignore it because they receive typed results.
	Truncation *utils.TruncationPolicy
	// StepAttribution carries the settings captured for the sampling step that
	// issued this call (Rust #45248 `StepContext::settings`), so pre/post tool
	// hooks report the step's model and permission mode rather than the turn's
	// initial ones. It is set per invocation and never serialized.
	StepAttribution *StepAttribution
}

// StepAttribution is the captured execution metadata of one sampling step.
type StepAttribution struct {
	Model           string
	ReasoningEffort string
	PermissionMode  string
}

type CodeModeNotifyFunc func(callID string, text string)
type CodeModeNestedToolStartedFunc func(context.Context, *Invocation, time.Time)
type CodeModeNestedToolCompletedFunc func(context.Context, *Invocation, *Output, error, time.Time, time.Time)

const (
	CodeModeCellIDContextKey       = "code_mode_cell_id"
	CodeModeOutputCallIDContextKey = "code_mode_output_call_id"
	CodeModeEnabledToolsContextKey = "code_mode_enabled_tools"
	// OriginItemIDContextKey carries the Responses item that requested this call
	// (Rust `ToolInvocation::originating_call`'s item id, #40866). MCP requests
	// report it as `_meta.itemId`; a nested Code Mode call inherits its cell's
	// value because the delegate clones the parent invocation's context.
	OriginItemIDContextKey = "origin_item_id"
	// OriginWindowIDContextKey carries the conversation window the call's origin
	// belongs to (Rust `ToolCallOrigin::window_id`, #45409). It is set for a
	// nested Code Mode call so a cell keeps reporting the window it started in
	// even after a wait or a compaction moved the thread to a new window.
	OriginWindowIDContextKey = "origin_window_id"
)

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
	// DeliveredAssistantMessage is the bounded text a successful host-owned
	// User Messaging call confirmed after its input hooks. It is retained as
	// untrusted assistant context for Guardian reviews and never serialized to
	// the model (Rust `CodexHarnessMetadata::delivered_assistant_message`).
	DeliveredAssistantMessage string `json:"-"`
	// SandboxType is the observed process backend for a command execution
	// (Rust #47568). It is analytics-only and never serialized or model-visible.
	SandboxType *sandbox.SandboxType `json:"-"`
	// ToolResultMetadata is the raw tool-result `_meta` a tool exposes for the
	// internal executed-tool-call record (Rust `ToolOutput::tool_result_metadata`,
	// #46010). It is never serialized: the model-visible data carries the
	// destination-filtered `_meta` on its own, and harness metadata is stripped
	// per destination (#44336).
	ToolResultMetadata any `json:"-"`
	// ContainsExternalContext mirrors Rust's
	// `ToolOutput::contains_external_context`: the output carries context the
	// model did not author, so a host that disables memories on external
	// context must mark the thread's memory mode polluted. It is never
	// serialized.
	ContainsExternalContext bool `json:"-"`
}

type Executor interface {
	Spec() Spec
	Execute(ctx context.Context, invocation *Invocation) (*Output, error)
}

// GuardianReviewOptIn opts one invocation into synchronous approval review
// before its handler runs (Rust CoreToolRuntime::requires_guardian_review,
// #57433). A denied review stops dispatch and reaches the model as the tool
// call's error, so the handler never executes.
type GuardianReviewOptIn interface {
	RequiresGuardianReview(invocation *Invocation) bool
}

// NativeReviewPolicyProvider declares at registration time whether an
// executor requires synchronous native-call review (Rust #58040's
// NativeReview::Required captured with the tool plan). The registry remembers
// the answer so dispatch can retain the original call without probing the
// executor with an invocation it does not own yet.
type NativeReviewPolicyProvider interface {
	NativeReviewRequired() bool
}

// GuardianReviewFunc reviews one opted-in invocation after its pre-tool hooks
// produced the final arguments. A non-nil error is the model-facing rejection.
type GuardianReviewFunc func(ctx context.Context, invocation *Invocation) error

type TelemetryTagProvider interface {
	TelemetryTags(invocation *Invocation) map[string]string
}

// RequestErrorCodeTelemetryTag carries a bounded caller-side request-failure
// category from a tool executor to the telemetry layer (Rust #54094:
// `rmcp_client::request_error_code`). It is never model-visible output.
const RequestErrorCodeTelemetryTag = "request_error_code"

// RequestErrorCoder lets an executor error report that bounded failure category
// before the error is flattened to a model-facing string. The dispatcher copies
// it onto the execution's telemetry tags.
type RequestErrorCoder interface {
	RequestErrorCode() string
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
	mu                         sync.RWMutex
	executors                  map[string]Executor
	specs                      map[string]Spec
	nativeReview               map[string]bool
	order                      []string
	defaultInputSchemaMaxBytes int
}

// DefaultCodeModeInputSchemaMaxBytes is Rust's
// DEFAULT_INPUT_SCHEMA_MAX_BYTES for rendered Code Mode input types.
const DefaultCodeModeInputSchemaMaxBytes = 16_000

func NewRegistry() *Registry {
	return &Registry{
		executors:                  map[string]Executor{},
		specs:                      map[string]Spec{},
		nativeReview:               map[string]bool{},
		order:                      []string{},
		defaultInputSchemaMaxBytes: DefaultCodeModeInputSchemaMaxBytes,
	}
}

// SetDefaultInputSchemaMaxBytes sets the Code Mode rendering budget applied to
// tools that do not carry a larger explicit budget.
func (r *Registry) SetDefaultInputSchemaMaxBytes(maxBytes int) {
	if r == nil || maxBytes <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultInputSchemaMaxBytes = maxBytes
}

// EnforceStrictThirdPartyTools keeps every eligible third-party tool deferred
// while preserving Hidden tools that failed eligibility or budget checks
// (Rust #50687).
func (r *Registry) EnforceStrictThirdPartyTools() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range r.order {
		spec := r.specs[key]
		if !spec.ThirdParty || spec.Exposure == ExposureHidden {
			continue
		}
		spec.Exposure = ExposureDiscoverable
		r.specs[key] = spec
	}
}

func (r *Registry) Register(executor Executor) error {
	_, err := r.register(executor, false, false)
	return err
}

// RegisterExternal retains the first runtime registered for a name. External
// tool sources are untrusted input and name collisions must not replace host
// runtimes or abort construction of the turn.
func (r *Registry) RegisterExternal(executor Executor) (bool, error) {
	if executor != nil {
		spec := executor.Spec()
		if spec.Name.Namespace == "" && spec.Name.Name == DefaultShellCommandToolName {
			return false, nil
		}
	}
	return r.register(executor, true, false)
}

// Prepend registers a trusted synthetic runtime ahead of existing runtimes.
func (r *Registry) Prepend(executor Executor) error {
	_, err := r.register(executor, false, true)
	return err
}

// SetCodeModeMessages refreshes the registered exec/wait specs after a model
// catalog change. Registry stores spec snapshots, so updating only the
// executors would leave the next request with stale descriptions.
func (r *Registry) SetCodeModeMessages(messages *CodeModeToolMessages) {
	if r == nil {
		return
	}
	execName := PlainName(CodeModeExecToolName)
	waitName := PlainName(CodeModeWaitToolName)
	r.mu.Lock()
	executor := r.executors[execName.Key()]
	waitExecutor := r.executors[waitName.Key()]
	r.mu.Unlock()
	if setter, ok := executor.(interface{ SetCodeModeMessages(*CodeModeToolMessages) }); ok {
		setter.SetCodeModeMessages(messages)
	}
	var execSpec Spec
	if executor != nil {
		execSpec = executor.Spec()
	}
	var waitSpec Spec
	if waitExecutor != nil {
		waitSpec = waitExecutor.Spec()
	}
	r.mu.Lock()
	if executor != nil && execSpec.Name.Key() != "" {
		r.specs[execSpec.Name.Key()] = execSpec
	}
	if waitExecutor != nil && waitSpec.Name.Key() != "" {
		r.specs[waitSpec.Name.Key()] = waitSpec
	}
	r.mu.Unlock()
}

// RestrictToTools removes every tool outside the startup allowlist. An empty
// allowlist disables all tools; namespace-qualified keys and plain base names
// are both accepted (Rust #45711).
func (r *Registry) RestrictToTools(allowed []string) {
	if r == nil {
		return
	}
	names := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		if name != "" {
			names[name] = struct{}{}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	order := r.order[:0]
	for _, key := range r.order {
		spec, ok := r.specs[key]
		if !ok {
			continue
		}
		if !allowedToolName(names, spec.Name) {
			delete(r.executors, key)
			delete(r.specs, key)
			continue
		}
		order = append(order, key)
	}
	r.order = order
}

func allowedToolName(allowed map[string]struct{}, name ToolName) bool {
	if _, ok := allowed[name.Key()]; ok {
		return true
	}
	if name.Namespace == "" || name.Namespace == "functions" {
		if _, ok := allowed[name.Name]; ok {
			return true
		}
		_, ok := allowed["functions."+name.Name]
		return ok
	}
	return false
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
	if spec.InputSchemaMaxBytes < r.defaultInputSchemaMaxBytes {
		spec.InputSchemaMaxBytes = r.defaultInputSchemaMaxBytes
	}
	if _, exists := r.executors[key]; exists {
		if skipDuplicate {
			return false, nil
		}
		return false, fmt.Errorf("%w: %s", ErrDuplicateToolName, key)
	}
	r.executors[key] = executor
	r.specs[key] = spec
	r.nativeReview[key] = registryRequiresNativeReview(executor)
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
	delete(r.nativeReview, key)
	for index, orderedKey := range r.order {
		if orderedKey == key {
			r.order = append(r.order[:index], r.order[index+1:]...)
			break
		}
	}
	return executor, true
}

// RemoveNamespace removes every registered tool whose namespace equals the
// given value and returns the removed qualified tool names. Special model
// tools own the namespace matching their wire identity, so regular namespace
// tools cannot advertise that same model-visible surface (Rust 98da2c4499,
// #37188).
func (r *Registry) RemoveNamespace(namespace string) []ToolName {
	if r == nil {
		return nil
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []ToolName
	for _, key := range r.order {
		spec := r.specs[key]
		if spec.Name.Namespace != namespace {
			continue
		}
		delete(r.executors, key)
		delete(r.specs, key)
		removed = append(removed, spec.Name)
	}
	if len(removed) > 0 {
		kept := r.order[:0]
		for _, key := range r.order {
			if _, exists := r.executors[key]; exists {
				kept = append(kept, key)
			}
		}
		r.order = kept
	}
	return removed
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
	if !ok {
		return spec, false
	}
	return liveDescriptionSpec(r.executors[name.Key()], spec), true
}

// LiveDescriptionProvider lets a turn-scoped executor publish a model-facing
// description that follows the sampling step's model. The registry keeps the
// registered schema and overlays the live text on every read, so a mid-turn
// model change reaches the next step without re-registering the tool
// (Rust step settings: model-owned tool messages).
type LiveDescriptionProvider interface {
	LiveDescription() (string, bool)
}

func liveDescriptionSpec(executor Executor, spec Spec) Spec {
	if executor == nil {
		return spec
	}
	provider, ok := executor.(LiveDescriptionProvider)
	if !ok {
		return spec
	}
	description, ok := provider.LiveDescription()
	if !ok {
		return spec
	}
	spec.Description = description
	return spec
}

// NativeReviewRequired reports the native-review policy captured for the
// registered tool (Rust #58040 RegisteredTool::native_review).
func (r *Registry) NativeReviewRequired(name ToolName) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nativeReview[name.Key()]
}

func registryRequiresNativeReview(executor Executor) bool {
	provider, ok := executor.(NativeReviewPolicyProvider)
	return ok && provider.NativeReviewRequired()
}

// SetExposure replaces one registered tool's exposure. It backs Rust's
// direct-only namespace post-pass (spec_plan.rs
// apply_direct_model_only_namespace_overrides).
func (r *Registry) SetExposure(name ToolName, exposure Exposure) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	spec, ok := r.specs[name.Key()]
	if !ok {
		return false
	}
	spec.Exposure = exposure
	r.specs[name.Key()] = spec
	return true
}

// ReplaceSpec updates a registered tool's model-visible spec while keeping its
// executor and registration order. Dynamic-tool replacement uses it so a
// pending call for the previous catalog still dispatches (Rust #57553).
func (r *Registry) ReplaceSpec(spec Spec) bool {
	if r == nil {
		return false
	}
	key := spec.Name.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.executors[key]; !ok {
		return false
	}
	if _, ok := r.specs[key]; !ok {
		return false
	}
	if spec.InputSchemaMaxBytes < r.defaultInputSchemaMaxBytes {
		spec.InputSchemaMaxBytes = r.defaultInputSchemaMaxBytes
	}
	r.specs[key] = spec
	return true
}

func (r *Registry) ModelVisibleSpecs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.specs))
	for _, key := range r.order {
		spec := r.specs[key]
		if IsModelVisible(spec.Exposure) {
			out = append(out, liveDescriptionSpec(r.executors[key], spec))
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
		if IsDeferred(spec.Exposure) {
			out = append(out, liveDescriptionSpec(r.executors[key], spec))
		}
	}
	return out
}

// MCPServerNamespaces maps each configured MCP server name to the callable
// namespaces it exposes. Indirect description prefixes use this mapping so a
// server selector applies to every tool namespace of that server (Rust #47642).
func (r *Registry) MCPServerNamespaces() map[string][]string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string][]string{}
	seen := map[string]struct{}{}
	for _, key := range r.order {
		spec := r.specs[key]
		namespace := strings.TrimSpace(spec.Name.Namespace)
		if namespace == "" {
			continue
		}
		executor := r.executors[key]
		namer, ok := executor.(interface{ MCPServerName() string })
		if !ok {
			continue
		}
		server := strings.TrimSpace(namer.MCPServerName())
		if server == "" {
			continue
		}
		seenKey := server + "\x00" + namespace
		if _, exists := seen[seenKey]; exists {
			continue
		}
		seen[seenKey] = struct{}{}
		out[server] = append(out[server], namespace)
	}
	return out
}

// ApplyFunctionsNamespaceFunctionDescriptionPrefixes prepends model-owned
// guidance to available default-namespace tools. Values are trimmed; empty
// values and unavailable names add nothing. The combined prefix text for
// reachable tools, including two-byte separators, is capped at 256 UTF-8 bytes
// (Rust #51930).
func (r *Registry) ApplyFunctionsNamespaceFunctionDescriptionPrefixes(prefixes map[string]string) error {
	if r == nil || len(prefixes) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	remaining := 256
	for _, key := range r.order {
		spec := r.specs[key]
		if strings.TrimSpace(spec.Name.Namespace) != "" {
			continue
		}
		prefix, ok := prefixes[spec.Name.Name]
		if !ok {
			continue
		}
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if len(prefix)+2 > remaining {
			return errors.New("available functions' description prefixes exceed the 256-byte combined limit")
		}
		remaining -= len(prefix) + 2
		spec.Description = prependToolDescriptionPrefix(prefix, spec.Description)
		r.specs[key] = spec
	}
	return nil
}

func prependToolDescriptionPrefix(prefix string, description string) string {
	if strings.TrimSpace(description) == "" {
		return prefix
	}
	return prefix + "\n\n" + description
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
	registry             *Registry
	now                  func() time.Time
	toolMode             string
	plannedCodeModeNames map[string]CodeModeToolNameMetadata
	toolNamespacesInfo   ToolNamespacesInfo
	canManageChildren    bool
	guardianReview       GuardianReviewFunc
}

func NewRouter(registry *Registry) *Router {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Router{registry: registry, now: time.Now}
}

// nestedGuardianReviewSetter receives the host review for the nested router a
// Code Mode cell builds (Rust's nested calls share the registry's dispatch).
type nestedGuardianReviewSetter interface {
	SetNestedGuardianReview(review GuardianReviewFunc)
}

// SetGuardianReview installs the host review an opted-in tool invocation runs
// before its handler (Rust #57433). Without one, an opted-in invocation is not
// reviewed here; the tool layer never silently approves on the host's behalf.
// Code Mode cells get the same review, because Rust dispatches nested calls
// through the same registry and must not let the wrapper bypass it.
func (r *Router) SetGuardianReview(review GuardianReviewFunc) {
	if r == nil {
		return
	}
	r.guardianReview = review
	if r.registry == nil {
		return
	}
	if executor, ok := r.registry.Lookup(PlainName(CodeModeExecToolName)); ok {
		if setter, ok := executor.(nestedGuardianReviewSetter); ok {
			setter.SetNestedGuardianReview(review)
		}
	}
}

// RequiresGuardianReview reports whether the invocation's executor opts into a
// synchronous review before dispatch.
func (r *Router) RequiresGuardianReview(invocation *Invocation) bool {
	if r == nil || r.registry == nil || invocation == nil {
		return false
	}
	executor, ok := r.registry.Lookup(invocation.ToolName)
	if !ok {
		return false
	}
	optIn, ok := executor.(GuardianReviewOptIn)
	// The plan captured at registration already settles native-review tools
	// (Rust #58040); the executor opt-in remains for runtimes whose eligibility
	// depends on the invocation itself.
	if r.registry.NativeReviewRequired(invocation.ToolName) {
		return true
	}
	return ok && optIn.RequiresGuardianReview(invocation)
}

// reviewBeforeDispatch runs the host review for an opted-in invocation. A nil
// review function leaves the call unreviewed, matching the default-off trait
// method in Rust.
func (r *Router) reviewBeforeDispatch(ctx context.Context, invocation *Invocation) error {
	if r == nil || r.guardianReview == nil || !r.RequiresGuardianReview(invocation) {
		return nil
	}
	_, err := runCancellableStep(ctx, func() (struct{}, error) {
		return struct{}{}, r.guardianReview(ctx, invocation)
	})
	return err
}

func (r *Router) SetClock(clock func() time.Time) {
	if clock == nil {
		r.now = time.Now
		return
	}
	r.now = clock
}

// SetToolPlan freezes the finalized model-specific tool plan on the router.
func (r *Router) SetToolPlan(toolMode string, codeModeNames map[string]CodeModeToolNameMetadata, info ToolNamespacesInfo) {
	if r == nil {
		return
	}
	r.toolMode = strings.TrimSpace(toolMode)
	r.plannedCodeModeNames = cloneCodeModeToolNames(codeModeNames)
	r.toolNamespacesInfo = cloneToolNamespacesInfo(info)
	r.canManageChildren = r.registryExposesChildManagementTools()
}

// SetToolMode updates the effective mode after code-mode availability is
// resolved, without rebuilding the finalized tool inventory.
func (r *Router) SetToolMode(toolMode string) {
	if r != nil {
		r.toolMode = strings.TrimSpace(toolMode)
	}
}

func (r *Router) ToolMode() string {
	if r == nil {
		return ""
	}
	return r.toolMode
}

func (r *Router) RequiresCodeModeWorker() bool {
	if r == nil {
		return false
	}
	return r.toolMode == "code_mode" || r.toolMode == "code_mode_only"
}

func (r *Router) ToolNamespacesInfo() ToolNamespacesInfo {
	if r == nil {
		return nil
	}
	return cloneToolNamespacesInfo(r.toolNamespacesInfo)
}

func (r *Router) HasTerminalControls() bool {
	if r == nil || r.registry == nil {
		return false
	}
	return r.registryHasBaseName("exec_command") && r.registryHasBaseName("write_stdin")
}

func (r *Router) CanManageChildren() bool {
	return r != nil && r.canManageChildren
}

func (r *Router) registryHasBaseName(name string) bool {
	if r == nil || r.registry == nil {
		return false
	}
	for _, candidate := range r.registry.Names() {
		if strings.TrimSpace(candidate.Name) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}

func (r *Router) registryExposesChildManagementTools() bool {
	v1 := []string{"spawn_agent", "send_input", "wait_agent"}
	v2 := []string{"spawn_agent", "send_message", "followup_task", "wait_agent"}
	hasAll := func(names []string) bool {
		for _, name := range names {
			if !r.registryHasBaseName(name) {
				return false
			}
		}
		return true
	}
	return hasAll(v1) || hasAll(v2)
}

func cloneCodeModeToolNames(values map[string]CodeModeToolNameMetadata) map[string]CodeModeToolNameMetadata {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]CodeModeToolNameMetadata, len(values))
	for key, value := range values {
		if value.Namespace != nil {
			namespace := *value.Namespace
			value.Namespace = &namespace
		}
		out[key] = value
	}
	return out
}

func cloneToolNamespacesInfo(values ToolNamespacesInfo) ToolNamespacesInfo {
	if values == nil {
		return nil
	}
	out := make(ToolNamespacesInfo, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
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

// Registry returns the router's tool registry (the inventory collector walks it
// to report every model-visible function).
func (r *Router) Registry() *Registry {
	if r == nil {
		return nil
	}
	return r.registry
}

func (r *Router) ModelVisibleSpecs() []Spec {
	if r == nil || r.registry == nil {
		return nil
	}
	return r.registry.ModelVisibleSpecs()
}

// RegistrationCounts returns the router's finalized inventory grouped by
// exposure (Rust #51868 record_registration_metrics).
func (r *Router) RegistrationCounts() RegistrationCounts {
	if r == nil || r.registry == nil {
		return RegistrationCounts{}
	}
	return r.registry.RegistrationCounts()
}

func (r *Router) Executor(name ToolName) (Executor, bool) {
	if r == nil || r.registry == nil {
		return nil, false
	}
	return r.registry.Lookup(name)
}

func (r *Router) TelemetryTags(invocation *Invocation) map[string]string {
	if r == nil || r.registry == nil || invocation == nil {
		return nil
	}
	executor, ok := r.registry.Lookup(invocation.ToolName)
	if !ok {
		return nil
	}
	provider, ok := executor.(TelemetryTagProvider)
	if !ok {
		return nil
	}
	tags := provider.TelemetryTags(invocation)
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func (r *Router) DeclaresOutputSchema(name ToolName) bool {
	if r == nil || r.registry == nil {
		return false
	}
	spec, ok := r.registry.Spec(name)
	return ok && spec.OutputSchema != nil
}

// MCPConnectorInfoProvider reports the connector an MCP tool call targets
// (Rust's approval metadata connector id/name, which mcp_tool_call.rs records on
// the call's span and in its metric tags).
type MCPConnectorInfoProvider interface {
	MCPConnectorInfo(invocation *Invocation) (connectorID string, connectorName string)
}

// MCPConnectorInfo reports the connector of the MCP tool a call targets, or
// empty strings for a tool without connector metadata.
func (r *Router) MCPConnectorInfo(invocation *Invocation) (string, string) {
	if r == nil || r.registry == nil || invocation == nil {
		return "", ""
	}
	executor, ok := r.registry.Lookup(invocation.ToolName)
	if !ok {
		return "", ""
	}
	provider, ok := executor.(MCPConnectorInfoProvider)
	if !ok {
		return "", ""
	}
	return provider.MCPConnectorInfo(invocation)
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

// SetCodeModeMessages updates the active model's Code Mode catalog overrides
// on the registered exec/wait executors (Rust #47118 mid-turn model changes).
func (r *Router) SetCodeModeMessages(messages *CodeModeToolMessages) {
	if r == nil || r.registry == nil {
		return
	}
	r.registry.SetCodeModeMessages(messages)
}

func (r *Router) CodeModeAvailability() error {
	if r == nil || r.registry == nil {
		return nil
	}
	executor, ok := r.registry.Lookup(PlainName(CodeModeExecToolName))
	if !ok {
		return nil
	}
	provider, ok := executor.(interface{ CodeModeAvailability() error })
	if !ok {
		return nil
	}
	return provider.CodeModeAvailability()
}

func (r *Router) TakeCodeModeUnavailableWarning(effectiveToolMode string) string {
	if r == nil || r.registry == nil {
		return ""
	}
	executor, ok := r.registry.Lookup(PlainName(CodeModeExecToolName))
	if !ok {
		return ""
	}
	provider, ok := executor.(interface {
		TakeCodeModeUnavailableWarning(string) string
	})
	if !ok {
		return ""
	}
	return provider.TakeCodeModeUnavailableWarning(effectiveToolMode)
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
	if item.Type == "function_call" && r != nil && r.registry != nil && r.registry.NativeReviewRequired(name) {
		// Rust #58040 retains the original call for native-review tools so the
		// evidence survives even when live history no longer holds it.
		invocation.NativeCall = cloneResponseItemRaw(item.Raw)
		if len(invocation.NativeCall) == 0 {
			invocation.NativeCall = synthesizeNativeCallJSON(item)
		}
	}
	if origin := strings.TrimSpace(item.ID); origin != "" {
		// Rust ToolInvocation::originating_call: a non-code-mode call reports the
		// item that requested it, which is the dispatched call item itself.
		invocation.Context = map[string]any{OriginItemIDContextKey: origin}
	}
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

func cloneResponseItemRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// synthesizeNativeCallJSON rebuilds the function-call item from the dispatch
// view when the dispatcher could not supply the original wire item.
func synthesizeNativeCallJSON(item ResponseItem) json.RawMessage {
	call := map[string]any{
		"type":      "function_call",
		"name":      item.Name,
		"arguments": item.Arguments,
		"call_id":   item.CallID,
	}
	if strings.TrimSpace(item.ID) != "" {
		call["id"] = item.ID
	}
	if namespace := strings.TrimSpace(item.Namespace); namespace != "" {
		call["namespace"] = namespace
	}
	if item.EncryptedFunctionArgs != nil {
		call["encrypted_function_args"] = append([]string{}, (*item.EncryptedFunctionArgs)...)
	}
	raw, err := json.Marshal(call)
	if err != nil {
		return nil
	}
	return raw
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
	if err := r.reviewBeforeDispatch(ctx, invocation); err != nil {
		return nil, err
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
	if payload, ok := postToolUsePayload(executor, invocation, output); ok {
		captureDeliveredAssistantMessage(output, payload)
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

// FinishesOnCancellation reports whether the tool's handler observes its
// cancellation token and publishes a terminal item itself (Rust
// CoreToolRuntime::finishes_on_cancellation, #51556).
func (r *Router) FinishesOnCancellation(name ToolName) bool {
	if r == nil || r.registry == nil {
		return false
	}
	spec, ok := r.registry.Spec(name)
	return ok && spec.FinishesOnCancellation
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
	_, err := runCancellableStep(ctx, func() (struct{}, error) {
		return struct{}{}, waiter.WaitUntilReady(ctx, invocation)
	})
	return err
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
	Type string
	// ID is the Responses item id of the call item (Rust `ResponseItem::id`),
	// the originating item an MCP request reports as `_meta.itemId` (#40866).
	ID                    string
	Namespace             string
	Name                  string
	CallID                string
	Arguments             string
	EncryptedFunctionArgs *[]string
	Input                 string
	Execution             string
	Search                map[string]any
	// Raw is the original wire item when the dispatcher has it, so a tool that
	// requires native review can retain the exact evidence (Rust #58040).
	Raw json.RawMessage
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
