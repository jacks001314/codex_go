package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestThreadStartedJSONShape(t *testing.T) {
	data, err := json.Marshal(ThreadStarted("thread-1"))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	got := string(data)
	want := `{"type":"thread.started","thread_id":"thread-1"}`
	if got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}

func TestCommandExecutionItemAttributionJSONShape(t *testing.T) {
	event := ItemStarted(CommandExecutionItemWithAttribution(
		"cmd-1",
		"python scripts/run.py",
		"",
		nil,
		"in_progress",
		"sample@openai-curated",
		"scripts/run.py",
	))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.started","item":{"id":"cmd-1","type":"command_execution","command":"python scripts/run.py","plugin_id":"sample@openai-curated","script_path":"scripts/run.py","aggregated_output":"","status":"in_progress"}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestAgentMessageItemCompletedJSONShape(t *testing.T) {
	event := ItemCompleted(AgentMessageItem("item-1", "hello"))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got := string(data)
	want := `{"type":"item.completed","item":{"id":"item-1","type":"agent_message","text":"hello"}}`
	if got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}

func TestAgentMessageItemWithPhaseJSONShape(t *testing.T) {
	event := ItemCompleted(AgentMessageItemWithPhase("item-1", "先查询天气。", "commentary"))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"phase":"commentary"`)) {
		t.Fatalf("event JSON = %s, want commentary phase", data)
	}
}

func TestAsyncAgentMessageItemJSONShape(t *testing.T) {
	// Rust #39312: async delivery marker survives on agentMessage items.
	event := ItemCompleted(AsyncAgentMessageItem("item-async", "still working"))
	got, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	text := string(got)
	for _, want := range []string{
		`"type":"item.completed"`,
		`"type":"agent_message"`,
		`"text":"still working"`,
		`"delivery":"async"`,
		`"phase":"final_answer"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("async item JSON missing %q:\n%s", want, text)
		}
	}
}

func TestToolItemJSONShape(t *testing.T) {
	started := ItemStarted(ToolCallItem("tool-call-1", "exec_command", `{"cmd":"date"}`))
	data, err := json.Marshal(started)
	if err != nil {
		t.Fatalf("Marshal started returned error: %v", err)
	}
	wantStarted := `{"type":"item.started","item":{"id":"tool-call-1","type":"tool_call","tool_name":"exec_command","input":"{\"cmd\":\"date\"}"}}`
	if string(data) != wantStarted {
		t.Fatalf("started json = %s, want %s", data, wantStarted)
	}

	completed := ItemCompleted(ToolOutputItem("tool-output-1", "exec_command", "ok", true))
	data, err = json.Marshal(completed)
	if err != nil {
		t.Fatalf("Marshal completed returned error: %v", err)
	}
	wantCompleted := `{"type":"item.completed","item":{"id":"tool-output-1","type":"tool_output","tool_name":"exec_command","output":"ok","success":true}}`
	if string(data) != wantCompleted {
		t.Fatalf("completed json = %s, want %s", data, wantCompleted)
	}

	withMetadata := ItemCompleted(ToolOutputItemWithMetadata("tool-output-2", "exec_command", "Approval required", false, map[string]any{
		"approval_required": true,
		"reason":            "command requested sandbox permissions",
	}))
	data, err = json.Marshal(withMetadata)
	if err != nil {
		t.Fatalf("Marshal metadata completed returned error: %v", err)
	}
	wantWithMetadata := `{"type":"item.completed","item":{"id":"tool-output-2","type":"tool_output","tool_name":"exec_command","output":"Approval required","success":false,"metadata":{"approval_required":true,"reason":"command requested sandbox permissions"}}}`
	if string(data) != wantWithMetadata {
		t.Fatalf("metadata completed json = %s, want %s", data, wantWithMetadata)
	}
}

func TestTodoListItemJSONShape(t *testing.T) {
	event := ItemCompleted(TodoListItem("todo-list-call-1", []TodoItem{
		{Text: "step one", Completed: false},
		{Text: "step two", Completed: true},
	}))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.completed","item":{"id":"todo-list-call-1","type":"todo_list","items":[{"text":"step one","completed":false},{"text":"step two","completed":true}]}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestWebSearchItemJSONShape(t *testing.T) {
	event := ItemCompleted(WebSearchItem("search-1", "rust async await", map[string]any{
		"type":  "search",
		"query": "rust async await",
	}))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.completed","item":{"id":"search-1","type":"web_search","query":"rust async await","action":{"query":"rust async await","type":"search"}}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}

	started := ItemStarted(WebSearchItem("search-1", "", nil))
	data, err = json.Marshal(started)
	if err != nil {
		t.Fatalf("Marshal started returned error: %v", err)
	}
	wantStarted := `{"type":"item.started","item":{"id":"search-1","type":"web_search","action":{"type":"other"}}}`
	if string(data) != wantStarted {
		t.Fatalf("started json = %s, want %s", data, wantStarted)
	}
}

func TestFileChangeItemJSONShape(t *testing.T) {
	event := ItemCompleted(FileChangeItem("file-change-1", []FileChange{
		{Path: "a/added.txt", Kind: "add"},
		{Path: "b/deleted.txt", Kind: "delete"},
		{Path: "c/modified.txt", Kind: "update", Diff: "@@ -1 +1 @@\n-old\n+new", MovePath: "c/renamed.txt"},
	}, "completed"))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.completed","item":{"id":"file-change-1","type":"file_change","changes":[{"path":"a/added.txt","kind":"add"},{"path":"b/deleted.txt","kind":"delete"},{"path":"c/modified.txt","kind":"update"}],"status":"completed"}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestMCPToolCallItemJSONShape(t *testing.T) {
	event := ItemCompleted(MCPToolCallItem("mcp-1", "server_a", "tool_x", map[string]any{"key": "value"}, &MCPToolResult{
		Content: []any{map[string]any{"type": "text", "text": "done"}},
		StructuredContent: map[string]any{
			"status": "ok",
		},
	}, nil, "completed"))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.completed","item":{"id":"mcp-1","type":"mcp_tool_call","server":"server_a","tool":"tool_x","arguments":{"key":"value"},"result":{"content":[{"text":"done","type":"text"}],"structured_content":{"status":"ok"}},"error":null,"status":"completed"}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}

	started := ItemStarted(MCPToolCallItem("mcp-1", "server_a", "tool_x", map[string]any{"key": "value"}, nil, nil, "in_progress"))
	data, err = json.Marshal(started)
	if err != nil {
		t.Fatalf("Marshal started returned error: %v", err)
	}
	wantStarted := `{"type":"item.started","item":{"id":"mcp-1","type":"mcp_tool_call","server":"server_a","tool":"tool_x","arguments":{"key":"value"},"result":null,"error":null,"status":"in_progress"}}`
	if string(data) != wantStarted {
		t.Fatalf("started json = %s, want %s", data, wantStarted)
	}

	failed := ItemCompleted(MCPToolCallItem("mcp-2", "server_b", "tool_y", nil, nil, &MCPToolError{Message: "tool exploded"}, "failed"))
	data, err = json.Marshal(failed)
	if err != nil {
		t.Fatalf("Marshal failed returned error: %v", err)
	}
	wantFailed := `{"type":"item.completed","item":{"id":"mcp-2","type":"mcp_tool_call","server":"server_b","tool":"tool_y","arguments":null,"result":null,"error":{"message":"tool exploded"},"status":"failed"}}`
	if string(data) != wantFailed {
		t.Fatalf("failed json = %s, want %s", data, wantFailed)
	}
}

func TestCollabToolCallItemJSONShape(t *testing.T) {
	prompt := "draft a plan"
	started := ItemStarted(CollabToolCallItem("collab-1", "spawn_agent", "thread-parent", nil, &prompt, nil, "in_progress"))
	data, err := json.Marshal(started)
	if err != nil {
		t.Fatalf("Marshal started returned error: %v", err)
	}
	wantStarted := `{"type":"item.started","item":{"id":"collab-1","type":"collab_tool_call","tool":"spawn_agent","sender_thread_id":"thread-parent","receiver_thread_ids":[],"prompt":"draft a plan","agents_states":{},"status":"in_progress"}}`
	if string(data) != wantStarted {
		t.Fatalf("started json = %s, want %s", data, wantStarted)
	}

	completed := ItemCompleted(CollabToolCallItem("collab-1", "spawn_agent", "thread-parent", []string{"thread-child"}, &prompt, map[string]CollabAgentState{
		"thread-child": {Status: "running"},
	}, "completed"))
	data, err = json.Marshal(completed)
	if err != nil {
		t.Fatalf("Marshal completed returned error: %v", err)
	}
	wantCompleted := `{"type":"item.completed","item":{"id":"collab-1","type":"collab_tool_call","tool":"spawn_agent","sender_thread_id":"thread-parent","receiver_thread_ids":["thread-child"],"prompt":"draft a plan","agents_states":{"thread-child":{"status":"running","message":null}},"status":"completed"}}`
	if string(data) != wantCompleted {
		t.Fatalf("completed json = %s, want %s", data, wantCompleted)
	}
}

func TestCommandExecutionItemJSONShape(t *testing.T) {
	exitCode := 0
	event := ItemCompleted(CommandExecutionItem("cmd-1", "ls", "a.txt\n", &exitCode, "completed"))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.completed","item":{"id":"cmd-1","type":"command_execution","command":"ls","aggregated_output":"a.txt\n","exit_code":0,"status":"completed"}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}

	started := ItemStarted(CommandExecutionItem("cmd-1", "ls", "", nil, "in_progress"))
	data, err = json.Marshal(started)
	if err != nil {
		t.Fatalf("Marshal started returned error: %v", err)
	}
	wantStarted := `{"type":"item.started","item":{"id":"cmd-1","type":"command_execution","command":"ls","aggregated_output":"","status":"in_progress"}}`
	if string(data) != wantStarted {
		t.Fatalf("started json = %s, want %s", data, wantStarted)
	}
}

func TestErrorItemJSONShape(t *testing.T) {
	event := ItemCompleted(ErrorItem("item_0", "model rerouted: gpt-5 -> gpt-5-mini (HighRiskCyberActivity)"))
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	data := strings.TrimSpace(buf.String())
	want := `{"type":"item.completed","item":{"id":"item_0","type":"error","message":"model rerouted: gpt-5 -> gpt-5-mini (HighRiskCyberActivity)"}}`
	if data != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestRustExecJSONLEventTypeSurfaceParity(t *testing.T) {
	// Rust source: codex-rs/exec/src/exec_events.rs ThreadEvent.
	events := []ThreadEvent{
		ThreadStarted("thread-1"),
		TurnStarted(),
		TurnCompleted(Usage{}),
		TurnFailed("failed"),
		ItemStarted(ToolCallItem("call-1", "exec_command", "{}")),
		ItemUpdated(TodoListItem("todo-1", []TodoItem{{Text: "step", Completed: false}})),
		ItemCompleted(AgentMessageItem("msg-1", "done")),
		ErrorEvent("boom"),
	}
	want := []string{
		"thread.started",
		"turn.started",
		"turn.completed",
		"turn.failed",
		"item.started",
		"item.updated",
		"item.completed",
		"error",
	}
	if len(events) != len(want) {
		t.Fatalf("events = %d, want %d", len(events), len(want))
	}
	for i := range events {
		if events[i].Type != want[i] {
			t.Fatalf("event %d type = %q, want %q", i, events[i].Type, want[i])
		}
		if _, err := json.Marshal(events[i]); err != nil {
			t.Fatalf("Marshal(%q) error = %v", events[i].Type, err)
		}
	}
}

func TestRustExecJSONLEventTypesDerivedFromSource(t *testing.T) {
	rustPath := rustExecEventsSourcePath(t)
	rustEventTypes := rustSerdeRenameValues(t, rustPath, "pub enum ThreadEvent")
	goEventTypes := map[string]ThreadEvent{
		"thread.started": ThreadStarted("thread-1"),
		"turn.started":   TurnStarted(),
		"turn.completed": TurnCompleted(Usage{}),
		"turn.failed":    TurnFailed("failed"),
		"item.started":   ItemStarted(TodoListItem("todo-1", nil)),
		"item.updated":   ItemUpdated(TodoListItem("todo-1", nil)),
		"item.completed": ItemCompleted(AgentMessageItem("msg-1", "done")),
		"error":          ErrorEvent("boom"),
	}
	for _, rustType := range rustEventTypes {
		event, ok := goEventTypes[rustType]
		if !ok {
			t.Fatalf("Go exec JSONL surface missing Rust ThreadEvent type %q derived from %s", rustType, rustPath)
		}
		if event.Type != rustType {
			t.Fatalf("Go event constructor for %q produced type %q", rustType, event.Type)
		}
	}
}

func TestRustExecThreadItemDetailsDerivedFromSource(t *testing.T) {
	rustPath := rustExecEventsSourcePath(t)
	rustItems := rustEnumVariantNames(t, rustPath, "pub enum ThreadItemDetails")
	goItems := map[string]ThreadItem{
		"AgentMessage":     AgentMessageItem("agent-1", "done"),
		"Reasoning":        {ID: "reasoning-1", Type: "reasoning", Text: "thinking"},
		"CommandExecution": CommandExecutionItem("cmd-1", "ls", "", nil, "in_progress"),
		"FileChange":       FileChangeItem("patch-1", []FileChange{{Path: "a.txt", Kind: "update"}}, "completed"),
		"McpToolCall":      MCPToolCallItem("mcp-1", "docs", "search", nil, nil, nil, "in_progress"),
		"CollabToolCall":   CollabToolCallItem("collab-1", "spawn_agent", "thread-1", nil, nil, nil, "in_progress"),
		"WebSearch":        WebSearchItem("search-1", "codex", map[string]any{"type": "search", "query": "codex"}),
		"TodoList":         TodoListItem("todo-1", []TodoItem{{Text: "write", Completed: false}}),
		"Error":            ErrorItem("error-1", "failed"),
	}
	for _, rustVariant := range rustItems {
		item, ok := goItems[rustVariant]
		if !ok {
			t.Fatalf("Go exec ThreadItem surface missing Rust variant %q derived from %s", rustVariant, rustPath)
		}
		if _, err := json.Marshal(ItemCompleted(item)); err != nil {
			t.Fatalf("Marshal Go item for Rust variant %q error = %v", rustVariant, err)
		}
	}
}

func rustExecEventsSourcePath(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_ROOT"); env != "" {
		candidates = append(candidates, filepath.Join(env, "exec", "src", "exec_events.rs"))
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs", "exec", "src", "exec_events.rs"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs", "exec", "src", "exec_events.rs"),
		filepath.Join("..", "codex-main", "codex-rs", "exec", "src", "exec_events.rs"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	t.Skip("Rust exec_events.rs not found; set CODEX_RUST_ROOT")
	return ""
}

func rustSerdeRenameValues(t *testing.T, path string, enumHeader string) []string {
	t.Helper()
	block := rustEnumBlock(t, path, enumHeader)
	matches := regexp.MustCompile(`#\[serde\(rename = "([^"]+)"\)\]`).FindAllStringSubmatch(block, -1)
	if len(matches) == 0 {
		t.Fatalf("no serde rename values found in %s for %s", path, enumHeader)
	}
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, match[1])
	}
	return values
}

func rustEnumVariantNames(t *testing.T, path string, enumHeader string) []string {
	t.Helper()
	block := rustEnumBlock(t, path, enumHeader)
	re := regexp.MustCompile(`(?m)^\s*([A-Z][A-Za-z0-9_]*)\s*(?:\(|,|\{)`)
	matches := re.FindAllStringSubmatch(block, -1)
	if len(matches) == 0 {
		t.Fatalf("no enum variants found in %s for %s", path, enumHeader)
	}
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, match[1])
	}
	return values
}

func rustEnumBlock(t *testing.T, path string, enumHeader string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	source := string(data)
	start := strings.Index(source, enumHeader)
	if start < 0 {
		t.Fatalf("%s not found in %s", enumHeader, path)
	}
	open := strings.Index(source[start:], "{")
	if open < 0 {
		t.Fatalf("%s has no body in %s", enumHeader, path)
	}
	pos := start + open
	depth := 0
	for i := pos; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[pos : i+1]
			}
		}
	}
	t.Fatalf("%s body was not closed in %s", enumHeader, path)
	return ""
}

func TestItemUpdatedJSONShape(t *testing.T) {
	event := ItemUpdated(TodoListItem("todo-list-call-1", []TodoItem{
		{Text: "step one", Completed: false},
	}))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.updated","item":{"id":"todo-list-call-1","type":"todo_list","items":[{"text":"step one","completed":false}]}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestTurnTerminalEventJSONShape(t *testing.T) {
	completed := TurnCompleted(Usage{
		InputTokens:           1,
		CachedInputTokens:     2,
		CacheWriteInputTokens: 5,
		OutputTokens:          3,
		ReasoningOutputTokens: 4,
	})
	data, err := json.Marshal(completed)
	if err != nil {
		t.Fatalf("Marshal completed returned error: %v", err)
	}
	wantCompleted := `{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":2,"cache_write_input_tokens":5,"output_tokens":3,"reasoning_output_tokens":4}}`
	if string(data) != wantCompleted {
		t.Fatalf("completed json = %s, want %s", data, wantCompleted)
	}

	failed := TurnFailed("model exploded")
	data, err = json.Marshal(failed)
	if err != nil {
		t.Fatalf("Marshal failed returned error: %v", err)
	}
	wantFailed := `{"type":"turn.failed","error":{"message":"model exploded"}}`
	if string(data) != wantFailed {
		t.Fatalf("failed json = %s, want %s", data, wantFailed)
	}
}
