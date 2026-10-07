package turn

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// TestModelVisibleToolDefinitionsLikeRust covers the Go half of Rust #50540
// `create_tools_json_for_responses_lite(&model_visible_specs())`: the catalog the
// incremental tool diff compares against is exactly the serialized
// model-visible declarations - namespaces of named members plus built-ins
// identified by type.
func TestModelVisibleToolDefinitionsLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	router := tool.NewRouter(registry)
	runtime := NewRuntime(&RuntimeOptions{Router: router})

	specs := router.ModelVisibleSpecs()
	if len(specs) == 0 {
		t.Fatal("no model-visible tool specs")
	}
	definitions := runtime.ModelVisibleToolDefinitions("", false)
	if len(definitions) == 0 {
		t.Fatal("no model-visible tool definitions")
	}
	// Direct tool mode advertises the code-mode exec namespace, so the catalog is
	// the direct-mode transformation of the model-visible specs, matching what the
	// sampling request would send.
	if want := model.ResponsesToolsFromSpecs(runtime.modelVisibleSpecsForRequest(model.ToolModeDirect)); !reflect.DeepEqual(definitions, want) {
		t.Fatalf("definitions = %#v, want the direct-mode specs %#v", definitions, want)
	}
	declaredMembers := 0
	declaredBuiltins := 0
	for _, definition := range definitions {
		object, ok := definition.(map[string]any)
		if !ok {
			t.Fatalf("definition = %#v, want an object", definition)
		}
		// Rust `definition_name` reads the declaration name and falls back to the
		// type, which is how built-ins such as `tool_search` are identified.
		name := strings.TrimSpace(stringValue(object["name"]))
		if name == "" {
			name = strings.TrimSpace(stringValue(object["type"]))
		}
		if name == "" {
			t.Fatalf("definition without a name or type = %#v", definition)
		}
		members, namespaced := object["tools"].([]map[string]any)
		if !namespaced {
			declaredBuiltins++
			continue
		}
		if strings.TrimSpace(stringValue(object["type"])) != "namespace" {
			t.Fatalf("namespace definition = %#v", definition)
		}
		for _, member := range members {
			if strings.TrimSpace(stringValue(member["name"])) == "" {
				t.Fatalf("namespace member without a name = %#v", member)
			}
		}
		declaredMembers += len(members)
	}

	// Namespace members collapse into their namespace declaration, so every
	// model-visible spec is still accounted for exactly once.
	if want := len(runtime.modelVisibleSpecsForRequest(model.ToolModeDirect)); declaredMembers+declaredBuiltins != want {
		t.Fatalf("declared %d members + %d built-ins, want %d model-visible specs", declaredMembers, declaredBuiltins, want)
	}

	// A router without a tool surface declares nothing, so the diff starts from
	// an empty catalog instead of inventing entries.
	empty := NewRuntime(&RuntimeOptions{Router: tool.NewRouter(tool.NewRegistry())})
	if definitions := empty.ModelVisibleToolDefinitions("", false); len(definitions) != 0 {
		t.Fatalf("empty router definitions = %#v", definitions)
	}
	// A turn runtime without a tool router leaves the catalog empty instead of
	// panicking, so a caller can still fall back to its pre-#50540 shape.
	unrouted := NewRuntime(nil)
	if definitions := unrouted.ModelVisibleToolDefinitions("", false); len(definitions) != 0 {
		t.Fatalf("unrouted definitions = %#v", definitions)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
