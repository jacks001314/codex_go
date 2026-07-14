package tool

import (
	"context"
	"errors"
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
	if !reflect.DeepEqual([]string{names[0].Key(), names[1].Key(), names[2].Key(), names[3].Key()}, []string{"a", "b", "d", "ns.c"}) {
		t.Fatalf("names = %+v", names)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("a")}, noopExecutor)); !errors.Is(err, ErrDuplicateToolName) {
		t.Fatalf("expected duplicate, got %v", err)
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
	err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName("mcp__memory", "create_entities")}, noopExecutor))
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
