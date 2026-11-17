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
		if !modelVisibleResponseItem(raw) {
			t.Fatalf("non-model-visible item was replayed: %#v", raw)
		}
	}
	if got := items[1].(map[string]any)["type"]; got != "reasoning" {
		t.Fatalf("raw reasoning type = %v, want reasoning", got)
	}
}

// Rust keeps TurnItem display projections out of the model history, but Go's
// record stores them next to the response items using the public camelCase
// names. Replay must drop those too: a recorded commandExecution carries the
// full aggregated output, and treating it as a user message inflates a resumed
// turn (or a forked side conversation) past the model context window.
func TestInputItemsFromRecordOmitsCamelCaseDisplayItemsLikeRust(t *testing.T) {
	hugeOutput := strings.Repeat("command output line\n", 50000)
	record := &Record{Items: []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "keep user text"},
		{ID: "cmd-1", Type: "commandExecution", Text: hugeOutput},
		{ID: "patch-1", Type: "fileChange", Text: "changed files"},
		{ID: "mcp-1", Type: "mcpToolCall", Text: "tool result"},
		{ID: "dyn-1", Type: "dynamicToolCall", Text: "dynamic result"},
		{ID: "collab-1", Type: "collabAgentToolCall", Text: "spawned child"},
		{ID: "sub-1", Type: "subAgentActivity", Text: "child progress"},
		{ID: "todo-1", Type: "todoList", Text: "done"},
		{ID: "plan-1", Type: "plan", Text: "- step"},
		{ID: "search-1", Type: "webSearch", Text: "query"},
		{ID: "view-1", Type: "imageView", Text: "image"},
		{ID: "sleep-1", Type: "sleep", Text: "slept"},
		{ID: "err-1", Type: "error", Text: "boom"},
		{ID: "cmd-raw", Type: "commandExecution", Raw: json.RawMessage(`{"type":"commandExecution","id":"cmd-raw","command":"rg","aggregatedOutput":"workspace"}`)},
		{ID: "cmd-1-call", Type: "function_call", CallID: "call-1", Name: "shell", Text: `{"cmd":"rg"}`},
		{ID: "cmd-1-out", Type: "tool_output", CallID: "call-1", Text: "keep tool output"},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "keep assistant text"},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 4 {
		t.Fatalf("items len = %d, want 4: %#v", len(items), items)
	}
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("item = %#v", item)
		}
		if !modelVisibleResponseItem(raw) {
			t.Fatalf("non-model-visible item was replayed: %#v", raw)
		}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	if len(encoded) > 4096 {
		t.Fatalf("replayed history is %d bytes; display items leaked into the request", len(encoded))
	}
	if strings.Contains(string(encoded), "command output line") {
		t.Fatalf("commandExecution output leaked into the request: %s", encoded[:200])
	}
}

// Rust's `is_api_message` (core/src/context_manager/history.rs) is a whitelist
// over response-item types. Go keeps TurnItem display projections in the same
// record, so replay accepts exactly the response-item vocabulary and drops
// everything else, including display-only renderings that reuse a type name.
func TestModelVisibleHistoryItemMatchesRustAPIMessage(t *testing.T) {
	visible := []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "prompt"},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer"},
		{ID: "c1", Type: "function_call", CallID: "c1", Name: "shell", Text: "{}"},
		{ID: "o1", Type: "tool_output", CallID: "c1", Text: "ok"},
		{ID: "cc1", Type: "custom_tool_call", CallID: "cc1", Name: "apply_patch", Text: "x"},
		{ID: "co1", Type: "custom_tool_call_output", CallID: "cc1", Text: "ok"},
		{ID: "ts1", Type: "tool_search_call", CallID: "ts1"},
		{ID: "tso1", Type: "tool_search_output", CallID: "ts1"},
		{ID: "at1", Type: "additional_tools", Role: "developer", Data: map[string]any{"tools": []any{map[string]any{"name": "x"}}}},
		{ID: "cfg-trusted", Type: "configuration_update", Data: map[string]any{
			"reasoning":        map[string]any{"effort": "high"},
			"harness_metadata": map[string]any{"harness_authored_configuration": true},
		}},
		{ID: "r1", Type: "reasoning", Raw: json.RawMessage(`{"type":"reasoning","id":"r1","summary":[],"encrypted_content":"enc"}`)},
		{ID: "ws1", Type: "webSearch", Raw: json.RawMessage(`{"type":"web_search_call","id":"ws1","status":"completed"}`)},
		{ID: "img1", Type: "imageGeneration", Raw: json.RawMessage(`{"type":"image_generation_call","id":"img1","status":"completed","result":"aGk="}`)},
		{ID: "cmp1", Type: "contextCompaction", Raw: json.RawMessage(`{"type":"context_compaction","id":"cmp1","encrypted_content":"abc"}`)},
		{ID: "a-turn", Type: "agent_message", Role: "assistant", Text: "answer", Raw: json.RawMessage(`{"type":"AgentMessage","id":"msg_1","content":[{"type":"Text","text":"answer"}]}`)},
		{ID: "a-comm", Type: "agent_message", Raw: json.RawMessage(`{"type":"agent_message","author":"/root","recipient":"/root/w","content":[{"type":"input_text","text":"task"}]}`)},
	}
	hidden := []Item{
		{ID: "sys1", Type: "message", Role: "system", Text: "internal"},
		{ID: "sys2", Type: "message", Role: "system", Raw: json.RawMessage(`{"type":"message","role":"system","content":[{"type":"input_text","text":"internal"}]}`)},
		{ID: "cmd1", Type: "commandExecution", Text: "output"},
		{ID: "patch1", Type: "fileChange", Text: "changes"},
		{ID: "mcp1", Type: "mcpToolCall", Text: "result"},
		{ID: "dyn1", Type: "dynamicToolCall", Text: "result"},
		{ID: "collab1", Type: "collabAgentToolCall", Text: "child"},
		{ID: "sub1", Type: "subAgentActivity", Text: "child progress"},
		{ID: "todo1", Type: "todoList", Text: "done"},
		{ID: "plan1", Type: "plan", Text: "- step"},
		{ID: "hook1", Type: "hookPrompt", Text: "hook text"},
		{ID: "sleep1", Type: "sleep", Text: "slept"},
		{ID: "err1", Type: "error", Text: "boom"},
		{ID: "syserr1", Type: "systemError", Text: "status"},
		{ID: "view1", Type: "imageView", Text: "image"},
		{ID: "marker1", Type: "externalSessionImportMarker", Text: "imported"},
		{ID: "cmp2", Type: "contextCompaction", Text: "checkpoint"},
		{ID: "ws-tool", Type: "webSearch", Text: "web.run result"},
		{ID: "trigger", Type: "compaction_trigger"},
		{ID: "cfg", Type: "configuration_update", Data: map[string]any{"reasoning": map[string]any{"effort": "high"}}},
		{ID: "other", Type: "other", Text: "unknown"},
	}
	for i := range visible {
		if !ModelVisibleHistoryItem(&visible[i]) {
			t.Fatalf("ModelVisibleHistoryItem(%s/%s) = false, want true", visible[i].Type, visible[i].ID)
		}
	}
	for i := range hidden {
		if ModelVisibleHistoryItem(&hidden[i]) {
			t.Fatalf("ModelVisibleHistoryItem(%s/%s) = true, want false", hidden[i].Type, hidden[i].ID)
		}
	}
}

func TestAdditionalToolsHistoryItemKeepsModelPayload(t *testing.T) {
	record := &Record{Items: []Item{{
		ID:   "at-1",
		Type: "additional_tools",
		Role: "developer",
		Data: map[string]any{"tools": []any{map[string]any{"name": "list", "description": "list files"}}},
	}}}
	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one additional_tools item", items)
	}
	item := items[0].(map[string]any)
	if item["type"] != "additional_tools" || item["role"] != "developer" || item["id"] != "at-1" {
		t.Fatalf("item = %#v", item)
	}
	if tools, _ := item["tools"].([]any); len(tools) != 1 {
		t.Fatalf("tools = %#v", item["tools"])
	}
}

// Go's rollout replays TUI-facing TurnItems from `event_msg item_completed`
// lines next to the canonical response items. Rust never sends those shapes to
// the model, so the raw fast path must rebuild `UserMessage`/`AgentMessage`
// into Responses `message` items instead of forwarding their wire payload.
func TestInputItemsFromRecordRebuildsTurnItemMessagesLikeRust(t *testing.T) {
	rawUser := json.RawMessage(`{"client_id":"2cfe2c5d-de67-497d-a247-6757a3a92f84","content":[{"text":"hello","text_elements":[],"type":"text"}],"id":"user-turn-1","type":"UserMessage"}`)
	rawAgent := json.RawMessage(`{"content":[{"text":"hi there","type":"Text"}],"id":"msg_123","type":"AgentMessage"}`)
	record := &Record{Items: []Item{
		{ID: "user-turn-1", Type: "message", Role: "user", Raw: rawUser},
		{ID: "msg_123", Type: "agent_message", Role: "assistant", Raw: rawAgent},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2: %#v", len(items), items)
	}
	user := items[0].(map[string]any)
	if user["type"] != "message" || user["role"] != "user" {
		t.Fatalf("user item = %#v, want message/user", user)
	}
	userContent := user["content"].([]map[string]any)
	if userContent[0]["type"] != "input_text" || userContent[0]["text"] != "hello" {
		t.Fatalf("user content = %#v, want input_text", userContent[0])
	}
	agent := items[1].(map[string]any)
	if agent["type"] != "message" || agent["role"] != "assistant" {
		t.Fatalf("agent item = %#v, want message/assistant", agent)
	}
	agentContent := agent["content"].([]map[string]any)
	if agentContent[0]["type"] != "output_text" || agentContent[0]["text"] != "hi there" {
		t.Fatalf("agent content = %#v, want output_text", agentContent[0])
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	for _, leaked := range []string{`"UserMessage"`, `"AgentMessage"`, `"Text"`, "client_id", "text_elements"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("TurnItem payload leaked %s: %s", leaked, encoded)
		}
	}
}

// TurnItem::Reasoning carries display-only `raw_content`/`summary_text`; the
// model-visible reasoning item is persisted separately as a lowercase
// `response_item`. Replaying the TurnItem shape fails provider validation.
func TestInputItemsFromRecordOmitsTurnItemReasoningLikeRust(t *testing.T) {
	rawReasoning := json.RawMessage(`{"id":"rs_1","raw_content":["think"],"summary_text":["think"],"type":"Reasoning"}`)
	record := &Record{Items: []Item{
		{ID: "u1", Type: "message", Role: "user", Text: "keep"},
		{ID: "rs_1", Type: "reasoning", Raw: rawReasoning},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(items), items)
	}
	got := items[0].(map[string]any)
	if got["type"] != "message" || got["role"] != "user" {
		t.Fatalf("item = %#v, want only the user message", got)
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

// `agent_message` is a Responses item used for subagent communication; only
// the capitalized `AgentMessage` TurnItem projection is rebuilt.
func TestInputItemsFromRecordKeepsAgentCommunicationResponseItemLikeRust(t *testing.T) {
	raw := json.RawMessage(`{"type":"agent_message","author":"/root","recipient":"/root/worker","content":[{"type":"input_text","text":"task"}]}`)
	record := &Record{Items: []Item{{ID: "agent-task", Type: "agent_message", Raw: raw}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})

	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(items), items)
	}
	got := items[0].(map[string]any)
	if got["type"] != "agent_message" || got["author"] != "/root" || got["recipient"] != "/root/worker" {
		t.Fatalf("item = %#v, want the agent_message response item", got)
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

// TestInputItemsFromRecordPreservesToolDeclarationsLikeRust covers the history
// half of Rust #51480: a context window's recorded `additional_tools` item
// (written as a trusted response_item rollout line, Rust #50435) must rebuild
// into the request input, so the model runner can reuse the window's existing
// declarations instead of rebuilding and moving them.
func TestInputItemsFromRecordPreservesToolDeclarationsLikeRust(t *testing.T) {
	declarations := json.RawMessage(`{"id":"at_1","type":"additional_tools","role":"developer","tools":[{"type":"function","name":"echo"}]}`)
	instructions := json.RawMessage(`{"id":"msg_1","type":"message","role":"developer","content":[{"type":"input_text","text":"You are a helpful assistant."}]}`)
	record := &Record{Items: []Item{
		{Type: "additional_tools", Raw: declarations},
		{Type: "message", Raw: instructions},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2: %#v", len(items), items)
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["type"] != "additional_tools" {
		t.Fatalf("first item = %#v, want additional_tools", items[0])
	}
	if first["id"] != "at_1" {
		t.Fatalf("additional_tools id = %v, want at_1", first["id"])
	}
	if tools, ok := first["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("additional_tools.tools = %#v", first["tools"])
	}
	second, ok := items[1].(map[string]any)
	if !ok || second["type"] != "message" || second["role"] != "developer" {
		t.Fatalf("second item = %#v, want developer message", items[1])
	}
}
