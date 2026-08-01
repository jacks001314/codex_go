package tool

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRegistryRegisterAndModelVisibleSpecs(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("b"), Exposure: ExposureHidden}, nil)); err != nil {
		t.Fatalf("register hidden: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("a"), Exposure: ExposureModelVisible}, noopExecutor)); err != nil {
		t.Fatalf("register visible: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName("ns", "c")}, noopExecutor)); err != nil {
		t.Fatalf("register default visible: %v", err)
	}
	specs := registry.ModelVisibleSpecs()
	if len(specs) != 2 || specs[0].Name.Key() != "a" || specs[1].Name.Key() != "ns.c" {
		t.Fatalf("specs = %+v", specs)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("d"), Exposure: ExposureDiscoverable}, noopExecutor)); err != nil {
		t.Fatalf("register discoverable: %v", err)
	}
	discoverable := registry.DiscoverableSpecs()
	if len(discoverable) != 1 || discoverable[0].Name.Key() != "d" {
		t.Fatalf("discoverable = %+v", discoverable)
	}
	names := registry.Names()
	if !reflect.DeepEqual([]string{names[0].Key(), names[1].Key(), names[2].Key(), names[3].Key()}, []string{"b", "a", "ns.c", "d"}) {
		t.Fatalf("names = %+v", names)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("a")}, noopExecutor)); !errors.Is(err, ErrDuplicateToolName) {
		t.Fatalf("expected duplicate, got %v", err)
	}
}

func TestRegistryExternalRegistrationPreservesFirstRuntimeAndTrustedOrder(t *testing.T) {
	registry := NewRegistry()
	first := NewExecutorFunc(Spec{Name: PlainName("first"), Description: "winner"}, noopExecutor)
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	registered, err := registry.RegisterExternal(NewExecutorFunc(Spec{Name: PlainName("first"), Description: "shadow"}, noopExecutor))
	if err != nil || registered {
		t.Fatalf("RegisterExternal(duplicate) registered=%t err=%v", registered, err)
	}
	registered, err = registry.RegisterExternal(NewExecutorFunc(Spec{Name: PlainName("second")}, noopExecutor))
	if err != nil || !registered {
		t.Fatalf("RegisterExternal(second) registered=%t err=%v", registered, err)
	}
	if err := registry.Prepend(NewExecutorFunc(Spec{Name: PlainName("synthetic")}, noopExecutor)); err != nil {
		t.Fatal(err)
	}
	if got := registry.Names(); len(got) != 3 || got[0].Key() != "synthetic" || got[1].Key() != "first" || got[2].Key() != "second" {
		t.Fatalf("ordered names = %#v", got)
	}
	if executor, ok := registry.Lookup(PlainName("first")); !ok || executor != first {
		t.Fatalf("first runtime was replaced: %#v", executor)
	}
	removed, ok := registry.Remove(PlainName("first"))
	if !ok || removed != first {
		t.Fatalf("Remove(first) = %#v, %t", removed, ok)
	}
	if got := registry.Names(); len(got) != 2 || got[0].Key() != "synthetic" || got[1].Key() != "second" {
		t.Fatalf("names after removal = %#v", got)
	}
}

func TestRegistryDirectModelOnlySpecsAreVisibleButNotDiscoverable(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("direct_only"), Exposure: ExposureDirectModelOnly}, noopExecutor)); err != nil {
		t.Fatal(err)
	}
	visible := registry.ModelVisibleSpecs()
	if len(visible) != 1 || visible[0].Name.Key() != "direct_only" {
		t.Fatalf("visible = %#v", visible)
	}
	if discoverable := registry.DiscoverableSpecs(); len(discoverable) != 0 {
		t.Fatalf("discoverable = %#v", discoverable)
	}
}

func TestRegistryRegisterExternalReservesPlainShellCommandLikeRust(t *testing.T) {
	registry := NewRegistry()
	plain, err := registry.RegisterExternal(NewExecutorFunc(Spec{Name: PlainName(DefaultShellCommandToolName)}, noopExecutor))
	if err != nil || plain {
		t.Fatalf("plain shell_command registered=%t err=%v", plain, err)
	}
	namespaced, err := registry.RegisterExternal(NewExecutorFunc(Spec{Name: NamespacedName("client", DefaultShellCommandToolName)}, noopExecutor))
	if err != nil || !namespaced {
		t.Fatalf("namespaced shell_command registered=%t err=%v", namespaced, err)
	}
}

func TestRouterRegisterIfAbsentUpdatesSharedCodeModeRegistry(t *testing.T) {
	registry := NewRegistry()
	router := NewRouter(registry)
	executor := NewExecutorFunc(Spec{Name: PlainName("view_image")}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true}, nil
	})
	if err := router.RegisterIfAbsent(executor); err != nil {
		t.Fatalf("RegisterIfAbsent() error = %v", err)
	}
	if err := router.RegisterIfAbsent(executor); err != nil {
		t.Fatalf("duplicate RegisterIfAbsent() error = %v", err)
	}
	if _, ok := registry.Lookup(PlainName("view_image")); !ok {
		t.Fatal("shared registry is missing view_image")
	}
}

func TestRegistryDeferredToolNamespacesPrefersFirstNonEmptyDescription(t *testing.T) {
	registry := NewRegistry()
	for _, spec := range []Spec{
		{Name: NamespacedName("mcp__drive", "a"), Exposure: ExposureDiscoverable},
		{Name: NamespacedName("mcp__drive", "b"), Exposure: ExposureDiscoverable, NamespaceDescription: "Drive tools"},
		{Name: NamespacedName("mcp__drive", "c"), Exposure: ExposureDiscoverable, NamespaceDescription: "later description"},
		{Name: PlainName("plain"), Exposure: ExposureDiscoverable, NamespaceDescription: "ignored"},
		{Name: NamespacedName("visible", "tool"), Exposure: ExposureModelVisible, NamespaceDescription: "ignored"},
	} {
		if err := registry.Register(NewExecutorFunc(spec, noopExecutor)); err != nil {
			t.Fatalf("register %s: %v", spec.Name.Key(), err)
		}
	}
	got := NewRouter(registry).DeferredToolNamespaces()
	if !reflect.DeepEqual(got, map[string]string{"mcp__drive": "Drive tools"}) {
		t.Fatalf("namespaces = %#v", got)
	}
}

func TestRouterBuildToolCall(t *testing.T) {
	router := NewRouter(NewRegistry())
	invocation, ok, err := router.BuildToolCall(ResponseItem{Type: "function_call", Name: "shell", CallID: "call-a", Arguments: `{"cmd":"date"}`})
	if err != nil || !ok {
		t.Fatalf("build function: ok=%v err=%v", ok, err)
	}
	if invocation.ToolName.Key() != "shell" || invocation.Payload.Kind != PayloadFunction {
		t.Fatalf("invocation = %+v", invocation)
	}
	invocation, ok, err = router.BuildToolCall(ResponseItem{Type: "tool_search_call", CallID: "call-b", Execution: "client", Search: map[string]any{"q": "x"}})
	if err != nil || !ok {
		t.Fatalf("build search: ok=%v err=%v", ok, err)
	}
	if invocation.ToolName.Key() != "tool_search" || invocation.Payload.Kind != PayloadToolSearch {
		t.Fatalf("search invocation = %+v", invocation)
	}
	_, ok, err = router.BuildToolCall(ResponseItem{Type: "tool_search_call", CallID: "call-c", Execution: "server"})
	if err != nil || ok {
		t.Fatalf("server search should be ignored: ok=%v err=%v", ok, err)
	}
}

func TestRouterBuildToolCallResolvesResponsesAPIName(t *testing.T) {
	registry := NewRegistry()
	readOnly := true
	err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName("mcp__memory", "create_entities"), ReadOnlyHint: &readOnly}, noopExecutor))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	router := NewRouter(registry)

	invocation, ok, err := router.BuildToolCall(ResponseItem{
		Type:      "function_call",
		Name:      "mcp__memory__create_entities",
		CallID:    "call-a",
		Arguments: `{}`,
	})
	if err != nil || !ok {
		t.Fatalf("BuildToolCall() ok=%v err=%v", ok, err)
	}
	if invocation.ToolName.Key() != "mcp__memory.create_entities" {
		t.Fatalf("tool name = %s", invocation.ToolName.Key())
	}
	if hint, ok := invocation.Context["read_only_hint"].(bool); !ok || !hint {
		t.Fatalf("invocation read-only context = %#v", invocation.Context)
	}
}

func TestRouterBuildToolCallMarksOnlyPlaintextCollaborationMessages(t *testing.T) {
	empty := []string{}
	encrypted := []string{"message"}
	router := NewRouter(NewRegistry())
	tests := []struct {
		name      ToolName
		metadata  *[]string
		plaintext bool
	}{
		{name: NamespacedName("collaboration", "spawn_agent"), metadata: &empty, plaintext: true},
		{name: NamespacedName("collaboration", "send_message"), metadata: &empty, plaintext: true},
		{name: NamespacedName("collaboration", "followup_task"), metadata: &empty, plaintext: true},
		{name: NamespacedName("collaboration", "wait_agent"), metadata: &empty},
		{name: NamespacedName("mcp__server", "spawn_agent"), metadata: &empty},
		{name: NamespacedName("collaboration", "spawn_agent"), metadata: &encrypted},
		{name: NamespacedName("collaboration", "spawn_agent")},
	}
	for index, test := range tests {
		invocation, ok, err := router.BuildToolCall(ResponseItem{
			Type: "function_call", Namespace: test.name.Namespace, Name: test.name.Name,
			CallID: fmt.Sprintf("call-%d", index), EncryptedFunctionArgs: test.metadata,
		})
		if err != nil || !ok {
			t.Fatalf("BuildToolCall(%d) ok=%t err=%v", index, ok, err)
		}
		if got := invocation.Source == "direct_plaintext_message"; got != test.plaintext {
			t.Fatalf("BuildToolCall(%d) source=%q, plaintext=%t", index, invocation.Source, test.plaintext)
		}
	}
}

func TestRouterBuildToolCallResolvesUniqueBareNamespacedTool(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName("mcp__geogebra", "geogebra_create_circle")}, noopExecutor)); err != nil {
		t.Fatalf("register: %v", err)
	}
	router := NewRouter(registry)
	for _, item := range []ResponseItem{
		{Type: "function_call", Name: "geogebra_create_circle", CallID: "bare"},
		{Type: "function_call", Namespace: "geogebra", Name: "geogebra_create_circle", CallID: "legacy-namespace"},
	} {
		invocation, ok, err := router.BuildToolCall(item)
		if err != nil || !ok {
			t.Fatalf("BuildToolCall(%#v) ok=%v err=%v", item, ok, err)
		}
		if invocation.ToolName.Key() != "mcp__geogebra.geogebra_create_circle" {
			t.Fatalf("tool name = %s", invocation.ToolName.Key())
		}
	}
}

func TestRouterBuildToolCallDoesNotGuessAmbiguousBareTool(t *testing.T) {
	registry := NewRegistry()
	for _, namespace := range []string{"mcp__one", "mcp__two"} {
		if err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName(namespace, "lookup")}, noopExecutor)); err != nil {
			t.Fatalf("register %s: %v", namespace, err)
		}
	}
	invocation, ok, err := NewRouter(registry).BuildToolCall(ResponseItem{Type: "function_call", Name: "lookup", CallID: "ambiguous"})
	if err != nil || !ok {
		t.Fatalf("BuildToolCall() ok=%v err=%v", ok, err)
	}
	if invocation.ToolName.Key() != "lookup" {
		t.Fatalf("ambiguous tool resolved to %s", invocation.ToolName.Key())
	}
}

func TestRouterDispatch(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		return &Output{Success: true, Body: args.Text}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	router := NewRouter(registry)
	router.SetClock(func() time.Time { return fixedTime() })
	output, err := router.Dispatch(context.Background(), &Invocation{
		CallID:   "call-a",
		ToolName: PlainName("echo"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"text":"hello"}`},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if output.Body != "hello" || output.CompletedAt != fixedTime() {
		t.Fatalf("output = %+v", output)
	}
	if _, err := router.Dispatch(context.Background(), &Invocation{CallID: "call-b", ToolName: PlainName("missing")}); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("expected missing tool, got %v", err)
	}
}

func TestRouterDispatchConvertsToolPanicToFatalError(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("panic_tool")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		panic("boom")
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	router := NewRouter(registry)

	_, err := router.Dispatch(context.Background(), &Invocation{CallID: "call-a", ToolName: PlainName("panic_tool")})
	var callErr *FunctionCallError
	if !AsFunctionCallError(err, &callErr) || !callErr.IsFatal() {
		t.Fatalf("error = %#v, want fatal function call error", err)
	}
	if !strings.Contains(callErr.ModelMessage(), "panic_tool") || !strings.Contains(callErr.ModelMessage(), "boom") {
		t.Fatalf("panic error message = %q", callErr.ModelMessage())
	}
}

func TestDispatchParallel(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("parallel"), Parallel: true}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: invocation.CallID}, nil
	}))
	if err != nil {
		t.Fatalf("register parallel: %v", err)
	}
	err = registry.Register(NewExecutorFunc(Spec{Name: PlainName("serial"), Parallel: false}, noopExecutor))
	if err != nil {
		t.Fatalf("register serial: %v", err)
	}
	router := NewRouter(registry)
	results, err := router.DispatchParallel(context.Background(), []Invocation{
		{CallID: "a", ToolName: PlainName("parallel")},
		{CallID: "b", ToolName: PlainName("parallel")},
	})
	if err != nil {
		t.Fatalf("parallel: %v", err)
	}
	if results[0].Body != "a" || results[1].Body != "b" {
		t.Fatalf("results = %+v", results)
	}
	_, err = router.DispatchParallel(context.Background(), []Invocation{{CallID: "c", ToolName: PlainName("serial")}})
	if !errors.Is(err, ErrToolNotParallel) {
		t.Fatalf("expected not parallel, got %v", err)
	}
}

func TestLifecycleEvents(t *testing.T) {
	invocation := &Invocation{CallID: "call-a", ToolName: PlainName("echo")}
	start := StartEvent(invocation, fixedTime())
	if start.Type != "started" || start.CallID != "call-a" {
		t.Fatalf("start = %+v", start)
	}
	finish := FinishEvent(&Output{CallID: "call-a", ToolName: PlainName("echo"), Success: false, Error: "boom"}, fixedTime())
	if finish.Type != "failed" || finish.Message != "boom" {
		t.Fatalf("finish = %+v", finish)
	}
}

func noopExecutor(context.Context, *Invocation) (*Output, error) {
	return &Output{Success: true}, nil
}

func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
}
