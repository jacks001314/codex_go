package rollout

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

func TestPaginatedAppendSessionItemsWritesRustItemCompletedWire(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: "thread-rust-wire", SessionID: "thread-rust-wire",
		HistoryMode: "paginated", CWD: home, ModelProvider: "openai", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	item := session.Item{
		ID: "user-1", Type: "message", Role: "user", Text: "hello", CreatedAt: now.Add(time.Second),
		Content:  []session.ContentPart{{Type: "input_text", Text: "hello"}},
		Metadata: map[string]any{"turnId": "turn-1", "clientId": "client-1"},
	}
	if err := AppendSessionItems(recorder, []session.Item{item}, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(2*time.Second), 2_000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(rows) != 4 {
		t.Fatalf("rollout rows = %d, want 4\n%s", len(rows), data)
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(rows[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header["type"] != "session_meta" || header["ordinal"] != float64(0) || header["payload"] == nil || header["meta"] != nil {
		t.Fatalf("session header = %#v", header)
	}
	var completed struct {
		Type    string `json:"type"`
		Ordinal uint64 `json:"ordinal"`
		Payload struct {
			Type          string         `json:"type"`
			ThreadID      string         `json:"thread_id"`
			TurnID        string         `json:"turn_id"`
			StartedAtMS   int64          `json:"started_at_ms"`
			CompletedAtMS int64          `json:"completed_at_ms"`
			Item          map[string]any `json:"item"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(rows[2]), &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Type != "event_msg" || completed.Ordinal != 2 || completed.Payload.Type != "item_completed" || completed.Payload.ThreadID != "thread-rust-wire" || completed.Payload.TurnID != "turn-1" {
		t.Fatalf("completed event = %#v", completed)
	}
	if completed.Payload.StartedAtMS != now.Add(time.Second).UnixMilli() || completed.Payload.CompletedAtMS != now.Add(time.Second).UnixMilli() {
		t.Fatalf("item timing = %d..%d", completed.Payload.StartedAtMS, completed.Payload.CompletedAtMS)
	}
	if completed.Payload.Item["type"] != "UserMessage" || completed.Payload.Item["id"] != "user-1" || completed.Payload.Item["client_id"] != "client-1" {
		t.Fatalf("core item = %#v", completed.Payload.Item)
	}
	if strings.Contains(string(data), `"type":"item"`) {
		t.Fatalf("paginated rollout contains legacy item row:\n%s", data)
	}
}

func TestCoreTurnItemJSONFromSessionItemConvertsPlainToolItemsLikeRust(t *testing.T) {
	now := time.Date(2026, 8, 16, 1, 2, 3, 0, time.UTC)
	cases := []struct {
		name  string
		item  session.Item
		check func(t *testing.T, core map[string]any)
	}{
		{
			name: "plain function call becomes dynamic tool call",
			item: session.Item{
				ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1",
				Text: `{}`, CreatedAt: now,
				Data:     map[string]any{"arguments": `{}`},
				Metadata: map[string]any{"turnId": "turn-1"},
			},
			check: func(t *testing.T, core map[string]any) {
				if core["type"] != "DynamicToolCall" || core["id"] != "call-1" || core["tool"] != "echo" || core["status"] != "in_progress" {
					t.Fatalf("core = %#v", core)
				}
			},
		},
		{
			name: "command tool call becomes command execution",
			item: session.Item{
				ID: "call-cmd", Type: "function_call", Name: "shell", CallID: "call-cmd",
				Text: `{"cmd":"date"}`, CreatedAt: now,
				Data:     map[string]any{"arguments": `{"cmd":"date"}`, "command": "date"},
				Metadata: map[string]any{"turnId": "turn-1"},
			},
			check: func(t *testing.T, core map[string]any) {
				if core["type"] != "CommandExecution" || core["id"] != "call-cmd" {
					t.Fatalf("core = %#v", core)
				}
				if command, ok := core["command"].([]any); !ok || len(command) != 1 || command[0] != "date" {
					t.Fatalf("core command = %#v", core["command"])
				}
			},
		},
		{
			name: "plain tool output becomes completed dynamic tool call with content",
			item: session.Item{
				ID: "fco_1", Type: "tool_output", CallID: "call-1",
				Text: "tool says hi", CreatedAt: now,
				Data:     map[string]any{"call_id": "call-1", "success": true},
				Metadata: map[string]any{"turnId": "turn-1"},
			},
			check: func(t *testing.T, core map[string]any) {
				if core["type"] != "DynamicToolCall" || core["status"] != "completed" || core["success"] != true {
					t.Fatalf("core = %#v", core)
				}
				content, ok := core["content_items"].([]any)
				if !ok || len(content) != 1 {
					t.Fatalf("core content = %#v", core["content_items"])
				}
				block, ok := content[0].(map[string]any)
				if !ok || block["text"] != "tool says hi" || block["type"] != "inputText" {
					t.Fatalf("core content block = %#v", content[0])
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, turnID, err := CoreTurnItemJSONFromSessionItem(&tc.item)
			if err != nil {
				t.Fatalf("CoreTurnItemJSONFromSessionItem() error = %v", err)
			}
			if turnID != "turn-1" {
				t.Fatalf("turn id = %q, want turn-1", turnID)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("Unmarshal(%s) error = %v", raw, err)
			}
			tc.check(t, got)
		})
	}
}

func TestCoreAndPublicThreadItemWireConversionsMatchRustShapes(t *testing.T) {
	turnID := "turn-1"
	command := session.Item{
		ID: "tool-output", Type: "tool_output", CallID: "call-1", Text: "done\n", CreatedAt: time.Unix(1, 0),
		Data: map[string]any{
			"command": []string{"echo", "hello world"}, "cwd": t.TempDir(), "source": "userShell",
			"status": "completed", "commandActions": []any{map[string]any{"type": "unknown", "command": "echo 'hello world'"}},
			"exitCode": int64(0), "durationMs": int64(42),
		},
		Metadata: map[string]any{"turnId": turnID},
	}
	coreRaw, gotTurnID, err := CoreTurnItemJSONFromSessionItem(&command)
	if err != nil {
		t.Fatal(err)
	}
	if gotTurnID != turnID {
		t.Fatalf("turn id = %q", gotTurnID)
	}
	var core map[string]any
	if err := json.Unmarshal(coreRaw, &core); err != nil {
		t.Fatal(err)
	}
	if core["type"] != "CommandExecution" || core["id"] != "call-1" || core["source"] != "user_shell" || core["status"] != "completed" {
		t.Fatalf("core command = %#v", core)
	}
	if core["plugin_id"] != nil || core["script_path"] != nil || core["process_id"] != nil {
		t.Fatalf("optional core fields must be omitted: %#v", core)
	}
	publicRaw, itemID, itemType, err := PublicThreadItemJSONFromCore(coreRaw)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(publicRaw, &public); err != nil {
		t.Fatal(err)
	}
	if itemID != "call-1" || itemType != "commandExecution" || public["type"] != "commandExecution" || public["command"] != "echo 'hello world'" || public["source"] != "userShell" || public["durationMs"] != float64(42) {
		t.Fatalf("public command = %#v", public)
	}

	sleep := session.Item{ID: "sleep-1", Type: "sleep", Data: map[string]any{"durationMs": int64(250)}, Metadata: map[string]any{"turnId": turnID}}
	sleepCore, _, err := CoreTurnItemJSONFromSessionItem(&sleep)
	if err != nil {
		t.Fatal(err)
	}
	var sleepValue map[string]any
	if err := json.Unmarshal(sleepCore, &sleepValue); err != nil {
		t.Fatal(err)
	}
	if sleepValue["type"] != "Extension" || sleepValue["kind"] != "clock.sleep" || sleepValue["durationMs"] != float64(250) {
		t.Fatalf("sleep core = %#v", sleepValue)
	}
	sleepPublic, _, sleepType, err := PublicThreadItemJSONFromCore(sleepCore)
	if err != nil {
		t.Fatal(err)
	}
	var sleepAPI map[string]any
	_ = json.Unmarshal(sleepPublic, &sleepAPI)
	if sleepType != "sleep" || sleepAPI["durationMs"] != float64(250) {
		t.Fatalf("sleep public = %#v", sleepAPI)
	}
}

func TestPublicThreadItemJSONFromCoreConvertsUserInputAndMemoryCitation(t *testing.T) {
	core := json.RawMessage(`{"type":"AgentMessage","id":"agent-1","content":[{"type":"Text","text":"hello "},{"type":"Text","text":"world"}],"phase":"final_answer","memory_citation":{"entries":[{"path":"MEMORY.md","lineStart":1,"lineEnd":2,"note":"summary"}],"rolloutIds":["thread-1"]}}`)
	raw, id, kind, err := PublicThreadItemJSONFromCore(core)
	if err != nil {
		t.Fatal(err)
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatal(err)
	}
	if id != "agent-1" || kind != "agentMessage" || item["text"] != "hello world" || item["phase"] != "final_answer" {
		t.Fatalf("agent item = %#v", item)
	}
	citation, _ := item["memoryCitation"].(map[string]any)
	threadIDs, _ := citation["threadIds"].([]any)
	if len(threadIDs) != 1 || threadIDs[0] != "thread-1" || citation["rolloutIds"] != nil {
		t.Fatalf("memory citation = %#v", citation)
	}

	user := json.RawMessage(`{"type":"UserMessage","id":"user-1","content":[{"type":"text","text":"hi","text_elements":[]},{"type":"image","image_url":"https://example/image.png"}]}`)
	raw, _, _, err = PublicThreadItemJSONFromCore(user)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "text_elements") || strings.Contains(string(raw), "image_url") || !strings.Contains(string(raw), "textElements") || !strings.Contains(string(raw), `"url":"https://example/image.png"`) {
		t.Fatalf("user public wire = %s", raw)
	}
}
