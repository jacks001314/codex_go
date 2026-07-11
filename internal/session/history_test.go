package session

import (
	"encoding/json"
	"testing"
)

func TestInputItemsFromRecordUsesRawItem(t *testing.T) {
	raw := json.RawMessage(`{"type":"function_call_output","call_id":"call-1","output":"done"}`)
	record := &Record{Items: []Item{{Type: "tool_output", Raw: raw}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	got := items[0].(map[string]any)
	if got["type"] != "function_call_output" || got["output"] != "done" {
		t.Fatalf("item = %#v", got)
	}
}

func TestInputItemsFromRecordOmitsNonModelVisibleThreadItemsLikeRust(t *testing.T) {
	rawCommand := json.RawMessage(`{"type":"command_execution","id":"cmd-raw","command":"pwd","aggregated_output":"workspace"}`)
	rawReasoning := json.RawMessage(`{"type":"reasoning","id":"reasoning-raw","summary":[],"encrypted_content":null}`)
	record := &Record{Items: []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "keep user text"},
		{ID: "cmd-1", Type: "command_execution", Text: "workspace"},
		{ID: "patch-1", Type: "file_change", Text: "changed files"},
		{ID: "mcp-1", Type: "mcp_tool_call", Text: "tool result"},
		{ID: "collab-1", Type: "collab_tool_call", Text: "spawned child"},
		{ID: "todo-1", Type: "todo_list", Text: "done"},
		{ID: "err-1", Type: "error", Text: "boom"},
		{ID: "reasoning-summary", Type: "reasoning", Text: "summary without raw should not become user text"},
		{ID: "cmd-raw", Type: "command_execution", Raw: rawCommand},
		{ID: "reasoning-raw", Type: "reasoning", Raw: rawReasoning},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "keep assistant text"},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 3 {
		t.Fatalf("items len = %d, want 3: %#v", len(items), items)
	}
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("item = %#v", item)
		}
		if nonModelVisibleHistoryItemType(raw["type"].(string)) {
			t.Fatalf("non-model-visible item was replayed: %#v", raw)
		}
	}
	if got := items[1].(map[string]any)["type"]; got != "reasoning" {
		t.Fatalf("raw reasoning type = %v, want reasoning", got)
	}
}

func TestInputItemsFromRecordBuildsMessagesAndToolItems(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "hello"},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "hi"},
		{ID: "call-1", Type: "function_call", Name: "shell", CallID: "call-1", Text: `{"cmd":"date"}`},
		{ID: "out-1", Type: "tool_output", CallID: "call-1", Text: "ok"},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 4 {
		t.Fatalf("items len = %d, want 4", len(items))
	}
	user := items[0].(map[string]any)
	assistant := items[1].(map[string]any)
	call := items[2].(map[string]any)
	output := items[3].(map[string]any)
	if user["role"] != "user" || assistant["role"] != "assistant" {
		t.Fatalf("messages = %#v %#v", user, assistant)
	}
	if call["type"] != "function_call" || call["name"] != "shell" || call["arguments"] != `{"cmd":"date"}` {
		t.Fatalf("call = %#v", call)
	}
	if _, ok := call["namespace"]; ok {
		t.Fatalf("plain function call should omit empty namespace: %#v", call)
	}
	if output["type"] != "function_call_output" || output["call_id"] != "call-1" || output["output"] != "ok" {
		t.Fatalf("output = %#v", output)
	}
}

func TestInputItemsFromRecordOmitsEmptyNamespacesForResponses(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "plain-call", Type: "function_call", Name: "shell", CallID: "call-1", Text: `{}`},
		{ID: "custom-call", Type: "custom_tool_call", Name: "imagegen", CallID: "call-2", Text: "draw", Namespace: "  "},
		{ID: "namespaced-call", Type: "function_call", Name: "am_list_functions", Namespace: "angr", CallID: "call-3", Text: `{}`},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 3 {
		t.Fatalf("items len = %d, want 3", len(items))
	}
	plain := items[0].(map[string]any)
	if _, ok := plain["namespace"]; ok {
		t.Fatalf("plain call should omit namespace: %#v", plain)
	}
	custom := items[1].(map[string]any)
	if _, ok := custom["namespace"]; ok {
		t.Fatalf("custom call should omit blank namespace: %#v", custom)
	}
	namespaced := items[2].(map[string]any)
	if namespaced["namespace"] != "angr" {
		t.Fatalf("namespaced call = %#v", namespaced)
	}
}

func TestInputItemsFromRecordSanitizesRawEmptyNamespace(t *testing.T) {
	raw := json.RawMessage(`{"type":"function_call","call_id":"call-1","name":"shell","namespace":"","arguments":"{}"}`)
	record := &Record{Items: []Item{{Type: "function_call", Raw: raw}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	call := items[0].(map[string]any)
	if _, ok := call["namespace"]; ok {
		t.Fatalf("raw call should have empty namespace removed: %#v", call)
	}
}

func TestInputItemsFromRecordReplaysImageGenerationCall(t *testing.T) {
	record := &Record{Items: []Item{{
		ID:     "ig_123",
		Type:   "imageGeneration",
		Status: "generating",
		Text:   "A small blue square",
		Data: map[string]any{
			"revisedPrompt": "A small blue square",
			"result":        "Zm9v",
		},
	}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	image := items[0].(map[string]any)
	if image["type"] != "image_generation_call" || image["id"] != "ig_123" || image["status"] != "completed" || image["result"] != "Zm9v" || image["revised_prompt"] != "A small blue square" {
		t.Fatalf("image = %#v", image)
	}
}

func TestInputItemsFromRecordNormalizesRawImageGenerationCall(t *testing.T) {
	raw := json.RawMessage(`{"id":"ig_123","type":"image_generation_call","status":"generating","revised_prompt":"A small blue square","result":"Zm9v"}`)
	record := &Record{Items: []Item{{Type: "imageGeneration", Raw: raw}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	image := items[0].(map[string]any)
	if image["status"] != "completed" || image["result"] != "Zm9v" {
		t.Fatalf("image = %#v", image)
	}
}

func TestInputItemsFromRecordToolSearchRustRequiredFields(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "search-1", Type: "tool_search_call"},
		{ID: "search-out-1", Type: "tool_search_output", CallID: "search-1"},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	call := items[0].(map[string]any)
	if _, ok := call["arguments"]; !ok || call["arguments"] != nil {
		t.Fatalf("tool search call = %#v", call)
	}
	output := items[1].(map[string]any)
	tools, ok := output["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tool search output = %#v", output)
	}
}

func TestInputItemsFromRecordToolSearchOutputNormalizesInternalToolSpecs(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "search-1", Type: "tool_search_call"},
		{
			ID:     "search-out-1",
			Type:   "tool_search_output",
			CallID: "search-1",
			Data: map[string]any{
				"tools": []any{map[string]any{
					"name": map[string]any{
						"namespace": "angr",
						"name":      "am_get_function",
					},
					"description": "Get function info",
				}},
			},
		},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	output := items[1].(map[string]any)
	tools := output["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	namespace := tools[0].(map[string]any)
	if namespace["type"] != "namespace" || namespace["name"] != "angr" {
		t.Fatalf("namespace = %#v", namespace)
	}
	children := namespace["tools"].([]map[string]any)
	if len(children) != 1 || children[0]["type"] != "function" || children[0]["name"] != "am_get_function" || children[0]["defer_loading"] != true {
		t.Fatalf("children = %#v", children)
	}
}

func TestInputItemsFromRecordNormalizesImageContentForResponses(t *testing.T) {
	detail := "high"
	record := &Record{Items: []Item{{
		ID:   "u1",
		Type: "message",
		Role: "user",
		Content: []ContentPart{
			{Type: "image", ImageURL: "https://example.test/a.png", Detail: &detail},
			{Type: "localImage", ImageURL: "D:/repo/a.png"},
			{ImageURL: "https://example.test/b.png"},
		},
	}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{})

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	message := items[0].(map[string]any)
	content := message["content"].([]map[string]any)
	if len(content) != 3 {
		t.Fatalf("content = %#v", content)
	}
	for i := range content {
		if content[i]["type"] != "input_image" || content[i]["image_url"] == "" {
			t.Fatalf("content[%d] = %#v", i, content[i])
		}
	}
	if content[0]["detail"] != "high" {
		t.Fatalf("detail = %#v", content[0]["detail"])
	}
}
