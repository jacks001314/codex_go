package codemode

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/tool"
)

func TestNameForToolNameAndCollectDefinitions(t *testing.T) {
	if got := NameForToolName(tool.NamespacedName("mcp_server", "search")); got != "mcp_server__search" {
		t.Fatalf("NameForToolName() = %q", got)
	}
	definitions := CollectDefinitions([]tool.Spec{
		{Name: tool.PlainName(PublicToolName)},
		{Name: tool.PlainName("b"), Description: "B tool"},
		{Name: tool.PlainName("a"), Description: "A tool"},
		{Name: tool.PlainName("a"), Description: "duplicate"},
		{
			Name:        tool.PlainName("patch"),
			Description: "Patch files",
			Freeform:    &tool.FreeformSpec{Syntax: "lark", Definition: "start: PATCH"},
		},
	})
	names := []string{definitions[0].Name, definitions[1].Name, definitions[2].Name}
	if !reflect.DeepEqual(names, []string{"a", "b", "patch"}) {
		t.Fatalf("definitions = %#v", definitions)
	}
	if !strings.Contains(definitions[0].Description, "declare const tools: { a(args: unknown): Promise<unknown>; };") {
		t.Fatalf("description was not augmented: %q", definitions[0].Description)
	}
	if definitions[2].Kind != ToolKindFreeform || definitions[2].InputSchema["syntax"] != "lark" || !strings.Contains(definitions[2].Description, "patch(input: string): Promise<unknown>;") {
		t.Fatalf("freeform definition = %#v", definitions[2])
	}
}

func TestAugmentToolDefinitionRendersRustTypedDeclaration(t *testing.T) {
	definition := AugmentToolDefinition(ToolDefinition{
		Name:        "weather_tool",
		Description: "Weather tool",
		Kind:        ToolKindFunction,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"weather": map[string]any{
					"type":        "array",
					"description": "look up weather for a given list of locations",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{"type": "string"},
						},
						"required": []string{"location"},
					},
				},
			},
			"required": []string{"weather"},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"forecast": map[string]any{
					"type":        "string",
					"description": "human readable weather forecast",
				},
			},
			"required": []string{"forecast"},
		},
	})
	want := `weather_tool(args: {
  // look up weather for a given list of locations
  weather: Array<{ location: string; }>;
}): Promise<{
  // human readable weather forecast
  forecast: string;
}>;`
	if !strings.Contains(definition.Description, "exec tool declaration:\n```ts\n") || !strings.Contains(definition.Description, want) {
		t.Fatalf("description = %q", definition.Description)
	}
}

func TestAugmentToolSpecUsesNestedNamespaceName(t *testing.T) {
	spec := AugmentToolSpec(tool.Spec{
		Name:        tool.NamespacedName("web", "run"),
		Description: "Search the web",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"weather": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":       "object",
						"properties": map[string]any{"location": map[string]any{"type": "string"}},
						"required":   []string{"location"},
					},
				},
			},
		},
	})
	if !strings.Contains(spec.Description, "web__run(args: { weather?: Array<{ location: string; }>; }): Promise<unknown>;") {
		t.Fatalf("description = %q", spec.Description)
	}
}

func TestBuildToolSpecsAndParseExec(t *testing.T) {
	spec := BuildExecTool([]ToolDefinition{{Name: "update_plan", Description: "Update plan"}}, nil, true, true)
	if spec.Name != PublicToolName || spec.Syntax != "lark" || !strings.Contains(spec.Description, "update_plan") {
		t.Fatalf("spec = %#v", spec)
	}
	wait := BuildWaitTool()
	if wait.Name != WaitToolName || wait.Parameters["required"] == nil {
		t.Fatalf("wait = %#v", wait)
	}
	request := ParseExecRequest("// @exec: timeout=1\nawait update_plan({});")
	if request.Pragma != "timeout=1" || !strings.Contains(request.Source, "update_plan") {
		t.Fatalf("request = %#v", request)
	}
}

func TestBuildToolSurfaceFromRegistry(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("visible"), Description: "Visible tool"}, nil)); err != nil {
		t.Fatalf("register visible: %v", err)
	}
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("hidden"), Exposure: tool.ExposureHidden}, nil)); err != nil {
		t.Fatalf("register hidden: %v", err)
	}
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("deferred"), Exposure: tool.ExposureDiscoverable, Search: &tool.SearchInfo{Text: "deferred"}}, nil)); err != nil {
		t.Fatalf("register deferred: %v", err)
	}

	surface := BuildToolSurface(registry, nil, true)
	if !surface.DeferredToolsAvailable || !strings.Contains(surface.Exec.Description, "Deferred tools may be discovered") {
		t.Fatalf("surface = %#v", surface)
	}
	if len(surface.Definitions) != 1 || surface.Definitions[0].Name != "visible" {
		t.Fatalf("definitions = %#v", surface.Definitions)
	}
}

func TestWaitParams(t *testing.T) {
	params, err := ParseWaitParams(`{"cell_id":"cell-a","yield_time_ms":10,"max_tokens":20}`)
	if err != nil {
		t.Fatalf("ParseWaitParams() error = %v", err)
	}
	if params.CellID != "cell-a" || params.YieldTimeMS != 10 || params.MaxTokens != 20 {
		t.Fatalf("params = %#v", params)
	}
	if _, err := ParseWaitParams(`{"yield_time_ms":-1}`); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestCellStoreLifecycle(t *testing.T) {
	store := NewCellStore()
	now := time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	if _, err := store.Start("cell-a", "source"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := store.AppendOutput("cell-a", "hello"); err != nil {
		t.Fatalf("AppendOutput() error = %v", err)
	}
	cell, err := store.Complete("cell-a", " world", nil)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if cell.Status != CellCompleted || cell.Output != "hello world" {
		t.Fatalf("cell = %#v", cell)
	}
	if _, err := store.AppendOutput("cell-a", "!"); !errors.Is(err, ErrCellComplete) {
		t.Fatalf("expected ErrCellComplete, got %v", err)
	}
	if len(store.List()) != 1 {
		t.Fatalf("List() = %#v", store.List())
	}
}
