package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputItemsFromRecordUsesRawItem(t *testing.T) {
	call := json.RawMessage(`{"type":"function_call","call_id":"call-1","name":"shell","arguments":"{}"}`)
	raw := json.RawMessage(`{"type":"function_call_output","call_id":"call-1","output":"done"}`)
	record := &Record{Items: []Item{{Type: "function_call", Raw: call}, {Type: "tool_output", Raw: raw}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	got := items[1].(map[string]any)
	if got["type"] != "function_call_output" || got["output"] != "done" {
		t.Fatalf("item = %#v", got)
	}
}

func TestInputItemsFromRecordNormalizesTextContentType(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "u1", Type: "user_message", Role: "user", Content: []ContentPart{{Type: "text", Text: "hello"}}},
		{ID: "a1", Type: "agent_message", Role: "assistant", Content: []ContentPart{{Type: "text", Text: "hi"}}},
	}}
	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	user := items[0].(map[string]any)
	userContent := user["content"].([]map[string]any)
	if userContent[0]["type"] != "input_text" || userContent[0]["text"] != "hello" {
		t.Fatalf("user content = %#v, want input_text", userContent[0])
	}
	assistant := items[1].(map[string]any)
	assistantContent := assistant["content"].([]map[string]any)
	if assistantContent[0]["type"] != "output_text" || assistantContent[0]["text"] != "hi" {
		t.Fatalf("assistant content = %#v, want output_text", assistantContent[0])
	}
}

func TestInputItemsFromRecordSanitizesRawTextContent(t *testing.T) {
	raw := json.RawMessage(`{"type":"message","role":"user","content":[{"type":"text","text":"hello","text_elements":[]}]}`)
	items := InputItemsFromItems([]Item{{Raw: raw}}, &HistoryBuildOptions{})
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %#v", items[0])
	}
	content := item["content"].([]any)
	block := content[0].(map[string]any)
	if block["type"] != "input_text" || block["text"] != "hello" {
		t.Fatalf("content block = %#v, want input_text", block)
	}
	if _, ok := block["text_elements"]; ok {
		t.Fatalf("text_elements leaked into API content: %#v", block)
	}
}

func TestHistoryContentTypeNormalizesTextByRole(t *testing.T) {
	if got := historyContentType("text", "user", ""); got != "input_text" {
		t.Fatalf("historyContentType(text,user) = %q", got)
	}
	if got := historyContentType("text", "assistant", ""); got != "output_text" {
		t.Fatalf("historyContentType(text,assistant) = %q", got)
	}
	if got := historyContentType("inputText", "user", ""); got != "input_text" {
		t.Fatalf("historyContentType(inputText,user) = %q", got)
	}
	if got := historyContentType("outputText", "assistant", ""); got != "output_text" {
		t.Fatalf("historyContentType(outputText,assistant) = %q", got)
	}
	if got := historyContentType("file", "user", ""); got != "input_file" {
		t.Fatalf("historyContentType(file,user) = %q", got)
	}
}

func TestInputItemsFromRecordRemovesOnlyTopLevelPassthroughMetadata(t *testing.T) {
	raw := json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","internal_chat_message_metadata_passthrough":{"nested":true}}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-secret"}}`)
	items := InputItemsFromItems([]Item{{Raw: raw}}, &HistoryBuildOptions{})
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %#v", items[0])
	}
	if _, ok := item["internal_chat_message_metadata_passthrough"]; ok {
		t.Fatalf("top-level passthrough metadata leaked: %#v", item)
	}
	content := item["content"].([]any)
	nested := content[0].(map[string]any)
	if _, ok := nested["internal_chat_message_metadata_passthrough"]; !ok {
		t.Fatalf("nested passthrough metadata was removed: %#v", nested)
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
		{ID: "review-enter-1", Type: "enteredReviewMode", Text: "changes against 'main'"},
		{ID: "review-enter-2", Type: "entered_review_mode", Text: "current changes"},
		{ID: "review-exit-1", Type: "exitedReviewMode", Text: "review output"},
		{ID: "review-exit-2", Type: "exited_review_mode", Text: "review output"},
		{ID: "compact-1", Type: "contextCompaction", Text: "must not reach the model"},
		{ID: "compact-2", Type: "context_compaction", Text: "must not reach the model"},
		{ID: "reasoning-summary", Type: "reasoning", Text: "summary without raw should not become user text"},
		{ID: "cmd-raw", Type: "command_execution", Raw: rawCommand},
		{ID: "review-enter-raw", Type: "enteredReviewMode", Raw: json.RawMessage(`{"type":"enteredReviewMode","id":"review-enter-raw","review":"current changes"}`)},
		{ID: "review-exit-raw", Type: "exitedReviewMode", Raw: json.RawMessage(`{"type":"exited_review_mode","id":"review-exit-raw","review":"review output"}`)},
		{ID: "compact-raw", Type: "contextCompaction", Raw: json.RawMessage(`{"type":"contextCompaction","id":"compact-raw"}`)},
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

func TestConfigurationUpdateHistoryItemsAreNotReplayed(t *testing.T) {
	record := &Record{Items: []Item{{
		ID:   "cfg-1",
		Type: "configuration_update",
		Data: map[string]any{"reasoning": map[string]any{"effort": "high"}},
	}}}
	items := InputItemsFromRecord(record, nil)
	if len(items) != 0 {
		t.Fatalf("configuration_update should be dropped from model history: %#v", items)
	}
}

func TestHarnessAuthoredConfigurationUpdateHistoryItemsAreReplayed(t *testing.T) {
	record := &Record{Items: []Item{{
		ID:   "cfg-1",
		Type: "configuration_update",
		Data: map[string]any{
			"reasoning":        map[string]any{"effort": "high"},
			"harness_metadata": json.RawMessage(`{"harness_authored_configuration":true}`),
		},
	}}}
	items := InputItemsFromRecord(record, nil)
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one trusted configuration update", items)
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["type"] != "configuration_update" {
		t.Fatalf("item = %#v", items[0])
	}
	reasoning, _ := item["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v", reasoning)
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

func TestInputItemsFromRecordKeepsToolOutputResponseItemIDLikeRust(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "fc_server", Type: "function_call", CallID: "call-1", Name: "echo", Text: `{}`},
		{ID: "fco_local", Type: "tool_output", CallID: "call-1", Text: "ok"},
	}}
	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	output, ok := items[1].(map[string]any)
	if !ok || output["id"] != "fco_local" {
		t.Fatalf("output = %#v", items[1])
	}
}

func TestInputItemsFromRecordNormalizesInterruptedAndOrphanToolsLikeRust(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "keep user history"},
		{ID: "call-missing", Type: "function_call", Name: "exec_command", CallID: "call-missing", Text: `{"cmd":"sleep 30"}`},
		{ID: "orphan", Type: "function_call_output", CallID: "call-orphan", Text: "must disappear"},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "keep assistant history"},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 4 {
		t.Fatalf("items = %#v, want user, call, synthetic output, assistant", items)
	}
	if got := items[0].(map[string]any)["role"]; got != "user" {
		t.Fatalf("first item role = %v", got)
	}
	call := items[1].(map[string]any)
	output := items[2].(map[string]any)
	if call["call_id"] != "call-missing" || output["type"] != "function_call_output" ||
		output["call_id"] != "call-missing" || output["output"] != "aborted" {
		t.Fatalf("normalized pair = %#v %#v", call, output)
	}
	if got := items[3].(map[string]any)["role"]; got != "assistant" {
		t.Fatalf("last item role = %v", got)
	}
}

func TestInputItemsFromRecordPreservesStandaloneNamedFunctionCallOutputLikeRust(t *testing.T) {
	// Rust #39782: standalone named function_call_output items (no call id)
	// are external context and survive normalization instead of being dropped
	// as orphans; paired outputs keep the existing orphan removal.
	record := &Record{Items: []Item{
		{ID: "standalone", Type: "function_call_output", Name: "notifications", Namespace: "slack", Text: "Alice mentioned you."},
		{ID: "orphan", Type: "function_call_output", CallID: "call-orphan", Text: "must disappear"},
	}}
	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 1 {
		t.Fatalf("items = %#v, want only the standalone output", items)
	}
	standalone := items[0].(map[string]any)
	if standalone["type"] != "function_call_output" {
		t.Fatalf("standalone type = %v", standalone["type"])
	}
	if _, ok := standalone["call_id"]; ok {
		t.Fatalf("standalone output must not carry a call_id: %#v", standalone)
	}
	if standalone["name"] != "notifications" || standalone["namespace"] != "slack" {
		t.Fatalf("standalone output name/namespace = %#v", standalone)
	}
}

func TestInputItemsFromRecordNormalizesToolSearchLikeRust(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "search", Type: "tool_search_call", CallID: "search-1", Text: `{"query":"weather"}`},
		{ID: "orphan-client", Type: "tool_search_output", CallID: "orphan-client", Data: map[string]any{"execution": "client"}},
		{ID: "server", Type: "tool_search_output", CallID: "server-search", Data: map[string]any{"execution": "server"}},
	}}
	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 3 {
		t.Fatalf("items = %#v, want call, synthetic client output, server output", items)
	}
	synthetic := items[1].(map[string]any)
	if synthetic["type"] != "tool_search_output" || synthetic["call_id"] != "search-1" || synthetic["execution"] != "client" {
		t.Fatalf("synthetic search output = %#v", synthetic)
	}
}

func TestInputItemsFromRecordKeepsExistingToolPairOnceLikeRust(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "call", Type: "function_call", Name: "exec_command", CallID: "call-1", Text: `{"cmd":"pwd"}`},
		{ID: "output", Type: "function_call_output", CallID: "call-1", Text: "ok"},
	}}
	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 2 {
		t.Fatalf("items = %#v, want existing pair exactly once", items)
	}
	if items[1].(map[string]any)["output"] != "ok" {
		t.Fatalf("output = %#v", items[1])
	}
}

func TestInputItemsFromRecordOmitsEmptyNamespacesForResponses(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "plain-call", Type: "function_call", Name: "shell", CallID: "call-1", Text: `{}`},
		{ID: "custom-call", Type: "custom_tool_call", Name: "imagegen", CallID: "call-2", Text: "draw", Namespace: "  "},
		{ID: "namespaced-call", Type: "function_call", Name: "am_list_functions", Namespace: "angr", CallID: "call-3", Text: `{}`},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 6 {
		t.Fatalf("items len = %d, want 3 calls plus 3 synthetic outputs", len(items))
	}
	plain := items[0].(map[string]any)
	if _, ok := plain["namespace"]; ok {
		t.Fatalf("plain call should omit namespace: %#v", plain)
	}
	custom := items[2].(map[string]any)
	if _, ok := custom["namespace"]; ok {
		t.Fatalf("custom call should omit blank namespace: %#v", custom)
	}
	namespaced := items[4].(map[string]any)
	if namespaced["namespace"] != "angr" {
		t.Fatalf("namespaced call = %#v", namespaced)
	}
}

func TestInputItemsFromRecordSanitizesRawEmptyNamespace(t *testing.T) {
	raw := json.RawMessage(`{"type":"function_call","call_id":"call-1","name":"shell","namespace":"","arguments":"{}"}`)
	record := &Record{Items: []Item{{Type: "function_call", Raw: raw}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 2 {
		t.Fatalf("items len = %d, want call plus synthetic output", len(items))
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

func TestInputItemsFromRecordRestoresPersistedGenericToolSearchOutputLikeRust(t *testing.T) {
	record := &Record{Items: []Item{
		{ID: "search-1", Type: "tool_search_call", CallID: "call-1"},
		{
			ID:     "search-out-1",
			Type:   "tool_output",
			CallID: "call-1",
			Data: map[string]any{"tools": []any{map[string]any{
				"type": "namespace", "name": "weather", "tools": []any{},
			}}},
			Metadata: map[string]any{"payloadKind": "tool_search"},
		},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	output, ok := items[1].(map[string]any)
	if !ok || output["type"] != "tool_search_output" || output["call_id"] != "call-1" {
		t.Fatalf("output = %#v", items[1])
	}
	tools, ok := output["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", output["tools"])
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
	cwd := t.TempDir()
	localImage := filepath.Join(cwd, "a.png")
	if err := os.WriteFile(localImage, sessionMinimalPNGBytes(), 0o600); err != nil {
		t.Fatalf("WriteFile local image: %v", err)
	}
	record := &Record{Items: []Item{{
		ID:   "u1",
		Type: "message",
		Role: "user",
		Content: []ContentPart{
			{Type: "image", ImageURL: "https://example.test/a.png", Detail: &detail},
			{Type: "localImage", ImageURL: "a.png"},
			{ImageURL: "https://example.test/b.png"},
		},
	}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{CWD: cwd})

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
	if !strings.HasPrefix(fmt.Sprint(content[1]["image_url"]), "data:image/png;base64,") {
		t.Fatalf("local image replay URL = %#v", content[1]["image_url"])
	}
}

func TestInputItemsFromRecordDoesNotReplayLocalPathAsImageURL(t *testing.T) {
	record := &Record{Items: []Item{{
		Type:    "message",
		Role:    "user",
		Content: []ContentPart{{Type: "localImage", ImageURL: `D:\missing\image.png`}},
	}}}
	items := InputItemsFromRecord(record, &HistoryBuildOptions{})
	message := items[0].(map[string]any)
	content := message["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "input_text" {
		t.Fatalf("content = %#v", content)
	}
	if _, exists := content[0]["image_url"]; exists {
		t.Fatalf("missing local image path leaked as image_url: %#v", content[0])
	}
}

func sessionMinimalPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0, 0x1f,
		0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		0xae, 0x42, 0x60, 0x82,
	}
}

func TestSanitizeHistoryInputItemPreservesContentItemKinds(t *testing.T) {
	input := map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": "hi"}},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"user.text"},
			"turn_id":            "secret",
		},
	}
	got := sanitizeHistoryInputItem(input)
	md, _ := got.(map[string]any)["internal_chat_message_metadata_passthrough"].(map[string]any)
	if md == nil {
		t.Fatal("passthrough missing (content_item_kinds must be preserved, Rust #40264)")
	}
	if _, ok := md["content_item_kinds"]; !ok {
		t.Fatalf("content_item_kinds not preserved: %#v", md)
	}
	if _, ok := md["turn_id"]; ok {
		t.Fatalf("non-model-visible passthrough should be stripped: %#v", md)
	}
}
