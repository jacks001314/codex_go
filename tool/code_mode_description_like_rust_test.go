package tool

import (
	"context"
	"strings"
	"testing"
)

// Rust composes the model-visible Code Mode text in
// codex-rs/code-mode-protocol/src/description.rs, so these tests pin the
// bundled rendering to that source rather than to a paraphrased Go copy.

func TestCodeModeExecDescriptionMatchesRustTemplate(t *testing.T) {
	description := codeModeExecDescription(nil, 12345, nil)
	want := strings.ReplaceAll(CodeModeExecDescriptionTemplate, "{{ default_exec_yield_time_ms }}", "12345")
	want = strings.ReplaceAll(want, "{{ image_helper }}", CodeModeLegacyImageHelperDescription)
	want += "\n\n" + codeModeDeferredNestedToolsGuidance
	if description != want {
		t.Fatalf("exec description diverged from the Rust template:\n%s", description)
	}
	for _, fragment := range []string{
		"- Evaluates the provided JavaScript code in a fresh V8 isolate as an async module.",
		"\x60await tools.exec_command(...)\x60",
		"- Runs raw JavaScript -- no Node, no file system, no network access, no console.",
		"- \x60ALL_TOOLS\x60: metadata for the enabled nested tools as \x60{ name, description }\x60 entries.",
		"- \x60image(imageUrlOrItem: string | { image_url: string; detail?: \"auto\" | \"low\" | \"high\" | \"original\" | null } | ImageContent",
		"\x60notify(value: string | number | boolean | undefined | null)\x60",
	} {
		if !strings.Contains(description, fragment) {
			t.Errorf("exec description is missing the Rust fragment %q", fragment)
		}
	}
	for _, fragment := range []string{"Enabled nested command tool:", "There is no nested tools.exec method"} {
		if strings.Contains(description, fragment) {
			t.Errorf("exec description still carries the Go-only fragment %q", fragment)
		}
	}
}

func TestCodeModeExecDescriptionAddsRankedToolSearchGuidanceLikeRust(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(NewExecutorFunc(Spec{
		Name:        PlainName(ToolSearchName),
		Description: CodeModeToolSearchGuidance,
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *Invocation) (*Output, error) { return nil, nil }))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	description := codeModeExecDescription(registry, 10000, nil)
	if !strings.HasSuffix(description, "\n\n"+CodeModeToolSearchGuidance) {
		t.Fatalf("exec description is missing the ranked search guidance:\n%s", description)
	}
	if count := strings.Count(description, "await tools.tool_search("); count != 1 {
		t.Fatalf("tool_search guidance appears %d times:\n%s", count, description)
	}

	withoutSearch := codeModeExecDescription(NewRegistry(), 10000, nil)
	if strings.Contains(withoutSearch, CodeModeToolSearchGuidance) {
		t.Fatalf("exec description added search guidance without a search tool:\n%s", withoutSearch)
	}

	// An empty catalog guidance override suppresses the bundled discovery text,
	// and Rust only nests the search guidance inside it.
	empty := ""
	if got := codeModeExecDescription(registry, 10000, &CodeModeToolMessages{DeferredNestedToolsGuidance: &empty}); strings.Contains(got, CodeModeToolSearchGuidance) {
		t.Fatalf("empty guidance override still appended the search guidance:\n%s", got)
	}
}

func TestCodeModeWaitSpecMatchesRustWaitSpec(t *testing.T) {
	spec := (&codeModeWaitExecutor{exec: &codeModeExecExecutor{}}).Spec()
	wantDescription := "Waits on a yielded \x60exec\x60 cell and returns new output or completion.\n" + CodeModeWaitDescriptionTemplate
	if spec.Description != wantDescription {
		t.Fatalf("wait description = %q", spec.Description)
	}

	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("wait properties = %#v", spec.InputSchema["properties"])
	}
	documented := map[string]struct{ kind, description string }{
		"cell_id":       {"string", "Identifier of the running exec cell."},
		"yield_time_ms": {"number", "Wait before yielding more output. Defaults to 10000 ms."},
		"max_tokens":    {"number", "Output token budget for this wait call. Defaults to 10000 tokens."},
		"terminate":     {"boolean", "True stops the running exec cell; false or omitted waits for output."},
	}
	for name, want := range documented {
		property, ok := properties[name].(map[string]any)
		if !ok {
			t.Errorf("wait property %q = %#v", name, properties[name])
			continue
		}
		if property["type"] != want.kind {
			t.Errorf("wait property %q type = %#v, want %q", name, property["type"], want.kind)
		}
		if property["description"] != want.description {
			t.Errorf("wait property %q description = %#v, want %q", name, property["description"], want.description)
		}
	}
	required, ok := spec.InputSchema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "cell_id" {
		t.Fatalf("wait required = %#v", spec.InputSchema["required"])
	}
}
