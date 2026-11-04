package model

import "testing"

func TestDowngradeCustomToolGrammarsToTextKeepsOtherTools(t *testing.T) {
	tools := []any{
		map[string]any{"type": "function", "name": "echo", "parameters": map[string]any{"type": "object"}},
		map[string]any{"type": "custom", "name": "apply_patch", "format": map[string]any{
			"type": "grammar", "syntax": "lark", "definition": `start: "patch"`,
		}},
		map[string]any{"type": "custom", "name": "exec", "format": map[string]any{"type": "text"}},
		map[string]any{"type": "namespace", "name": "multi_agent_v1", "tools": []any{
			map[string]any{"type": "custom", "name": "inner", "format": map[string]any{
				"type": "grammar", "syntax": "lark", "definition": `start: "inner"`,
			}},
		}},
	}

	got := DowngradeCustomToolGrammarsToText(tools)

	applyPatch := got[1].(map[string]any)
	if format := applyPatch["format"].(map[string]any); format["type"] != "text" || len(format) != 1 {
		t.Fatalf("apply_patch format = %#v", format)
	}
	if _, ok := tools[1].(map[string]any)["format"].(map[string]any)["definition"]; !ok {
		t.Fatal("downgrade must not mutate the caller's tool declarations")
	}
	exec := got[2].(map[string]any)
	if format := exec["format"].(map[string]any); format["type"] != "text" {
		t.Fatalf("text custom tool changed: %#v", format)
	}
	namespace := got[3].(map[string]any)
	inner := namespace["tools"].([]any)[0].(map[string]any)
	if format := inner["format"].(map[string]any); format["type"] != "text" {
		t.Fatalf("namespaced custom tool format = %#v", format)
	}
	if got[0].(map[string]any)["type"] != "function" {
		t.Fatalf("function tool = %#v", got[0])
	}
}

func TestProviderInfoSupportsCustomToolGrammar(t *testing.T) {
	base := map[string]any{
		"model_providers": map[string]any{
			"qax": map[string]any{
				"name":     "qax",
				"base_url": "http://127.0.0.1:40001/v1",
				"wire_api": "responses",
			},
		},
	}
	provider, err := ProviderForConfigID(base, "qax", "")
	if err != nil {
		t.Fatalf("ProviderForConfigID error = %v", err)
	}
	if !provider.SupportsCustomToolGrammar() {
		t.Fatal("grammar custom tools must stay enabled without an override")
	}

	base["model_providers"].(map[string]any)["qax"].(map[string]any)["capabilities"] = map[string]any{
		"custom_tool_grammar": false,
	}
	provider, err = ProviderForConfigID(base, "qax", "")
	if err != nil {
		t.Fatalf("ProviderForConfigID error = %v", err)
	}
	if provider.SupportsCustomToolGrammar() {
		t.Fatal("capabilities.custom_tool_grammar=false must disable grammar custom tools")
	}
	if provider.Capabilities.RemoteCompaction != nil {
		t.Fatalf("unrelated capabilities changed: %#v", provider.Capabilities)
	}
}

func TestProviderCapabilitiesCustomToolGrammarRejectsNonBool(t *testing.T) {
	values := map[string]any{"custom_tool_grammar": "no"}
	if _, err := providerCapabilitiesOverrideFromConfig(values); err == nil {
		t.Fatal("non-boolean custom_tool_grammar must be rejected")
	}
}

func TestFlattenCustomToolCallOutputPartsKeepsOtherItems(t *testing.T) {
	items := []any{
		map[string]any{"type": "message", "role": "user"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": []any{
			map[string]any{"type": "input_text", "text": "first"},
			map[string]any{"type": "input_text", "text": "second"},
		}},
		map[string]any{"type": "function_call_output", "call_id": "c2", "output": []any{
			map[string]any{"type": "input_text", "text": "untouched"},
		}},
	}

	got := FlattenCustomToolCallOutputParts(items)

	if output, ok := got[1].(map[string]any)["output"].(string); !ok || output != "firstsecond" {
		t.Fatalf("custom output = %#v", got[1].(map[string]any)["output"])
	}
	if _, ok := got[2].(map[string]any)["output"].([]any); !ok {
		t.Fatalf("function output must stay structured: %#v", got[2])
	}
	if _, ok := items[1].(map[string]any)["output"].([]any); !ok {
		t.Fatal("flatten must not mutate the caller's items")
	}
}