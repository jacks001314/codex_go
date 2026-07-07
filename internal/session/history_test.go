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
	if output["type"] != "function_call_output" || output["call_id"] != "call-1" || output["output"] != "ok" {
		t.Fatalf("output = %#v", output)
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
