package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/model"
	"codex_go/session"
)

// incrementalCatalogLikeRust builds the serialized Responses Lite catalog shape
// `create_tools_json_for_responses_lite` emits: namespaces of named members plus
// built-ins identified by type (Rust #50540).
func incrementalCatalogLikeRust(execDescription string, extra ...map[string]any) []any {
	namespace := map[string]any{
		"type":        "namespace",
		"name":        "functions",
		"description": "Built-in tools",
		"tools": []any{
			map[string]any{"type": "custom", "name": "exec", "description": execDescription},
			map[string]any{"type": "function", "name": "exec_command"},
		},
	}
	definitions := []any{namespace}
	for _, tool := range extra {
		definitions = append(definitions, tool)
	}
	return definitions
}

func incrementalToolSearchLikeRust() map[string]any {
	return map[string]any{"type": "tool_search", "name": "tool_search"}
}

func incrementalInputType(t *testing.T, item any) string {
	t.Helper()
	object, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("item = %#v, want object", item)
	}
	return strings.TrimSpace(stringFromAny(object["type"]))
}

func incrementalHistoryItems(t *testing.T, router *RuntimeRouter, threadID string) []any {
	t.Helper()
	record, err := router.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	return session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true})
}

// TestIncrementalToolCatalogRecordsWindowStartLikeRust covers the request-history
// scenario of Rust #50540
// (`incremental_tools_append_changed_catalog_without_rewriting_history`): the
// first turn of a window records the whole catalog and the base instructions in
// history, and the next unchanged turn appends no tool definitions.
func TestIncrementalToolCatalogRecordsWindowStartLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental")
	created := time.Now().UTC()
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	instructions := "Use the available tools to help the user."

	first, err := router.incrementalToolCatalogForTurn("thread-incremental", nil, definitions, instructions, "turn-1", created)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Active || !first.Prefix {
		t.Fatalf("window start result = %#v, want active prefix", first)
	}
	if len(first.Items) != 2 {
		t.Fatalf("window start items = %#v, want catalog + instructions", first.Items)
	}
	catalog, _ := first.Items[0].(map[string]any)
	if incrementalInputType(t, first.Items[0]) != "additional_tools" || strings.TrimSpace(stringFromAny(catalog["id"])) == "" {
		t.Fatalf("catalog item = %#v", first.Items[0])
	}
	tools, _ := catalog["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("catalog tools = %#v", catalog["tools"])
	}
	declarations, _ := first.Items[1].(map[string]any)
	if incrementalInputType(t, first.Items[1]) != "message" || strings.TrimSpace(stringFromAny(declarations["role"])) != "developer" {
		t.Fatalf("base instructions item = %#v", first.Items[1])
	}
	content, _ := declarations["content"].([]map[string]any)
	if len(content) != 1 || content[0]["text"] != instructions {
		t.Fatalf("base instructions content = %#v", declarations["content"])
	}
	if len(first.SessionItems) != 2 || first.SessionItems[0].Type != "additional_tools" {
		t.Fatalf("recorded session items = %#v", first.SessionItems)
	}

	// The window records its declarations ahead of the first user message.
	record, err := router.threadRecord(session.ThreadID("thread-incremental"), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	if len(record.Items) != 2 || record.Items[0].Type != "additional_tools" {
		t.Fatalf("recorded items = %#v, want the declarations first", record.Items)
	}

	// The unchanged second turn appends nothing: the catalog is already in
	// history, so only the user input follows it.
	history := incrementalHistoryItems(t, router, "thread-incremental")
	second, err := router.incrementalToolCatalogForTurn("thread-incremental", history, definitions, instructions, "turn-2", created)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Active || second.Prefix || len(second.Items) != 0 || len(second.SessionItems) != 0 {
		t.Fatalf("unchanged turn result = %#v, want no updates", second)
	}
}

// Rust #53872: a same-model catalog refresh appends replacement instructions
// once, model-only changes remain distinct, and empty instructions explicitly
// remove the previous base without rewriting the window prefix.
func TestIncrementalToolCatalogRefreshesModelInstructionsLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental-instructions")
	created := time.Now().UTC()
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	const (
		initial   = "Help the user complete their task."
		refreshed = "Check your work before responding."
	)
	if _, err := router.incrementalToolCatalogForTurnWithModel(
		"thread-incremental-instructions", nil, definitions, initial, "gpt-test", "turn-1", created,
	); err != nil {
		t.Fatal(err)
	}

	history := incrementalHistoryItems(t, router, "thread-incremental-instructions")
	changed, err := router.incrementalToolCatalogForTurnWithModel(
		"thread-incremental-instructions", history, definitions, refreshed, "gpt-test", "turn-2", created.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Active || changed.Prefix || len(changed.Items) != 1 {
		t.Fatalf("instruction refresh = %#v", changed)
	}
	item, _ := changed.Items[0].(map[string]any)
	text := textFromInputItemContent(item["content"])
	if !strings.Contains(text, "The following instructions replace the previous base instructions:") || !strings.Contains(text, refreshed) {
		t.Fatalf("instruction refresh item = %#v", item)
	}
	metadata, _ := item["internal_chat_message_metadata_passthrough"].(map[string]any)
	kinds, _ := metadata["content_item_kinds"].([]string)
	if len(kinds) != 1 || kinds[0] != contextfrag.ModelInstructionsContentKind {
		t.Fatalf("instruction refresh content kind = %#v", item)
	}

	history = incrementalHistoryItems(t, router, "thread-incremental-instructions")
	unchanged, err := router.incrementalToolCatalogForTurnWithModel(
		"thread-incremental-instructions", history, definitions, refreshed, "gpt-test", "turn-3", created.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Items) != 0 {
		t.Fatalf("unchanged instructions = %#v", unchanged.Items)
	}

	history = incrementalHistoryItems(t, router, "thread-incremental-instructions")
	switched, err := router.incrementalToolCatalogForTurnWithModel(
		"thread-incremental-instructions", history, definitions, refreshed, "gpt-next", "turn-4", created.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(switched.Items) != 1 {
		t.Fatalf("model switch = %#v", switched.Items)
	}
	item, _ = switched.Items[0].(map[string]any)
	text = textFromInputItemContent(item["content"])
	if !strings.Contains(text, "The current model is gpt-next.") || strings.Contains(text, "replace the previous base instructions") {
		t.Fatalf("model-only instruction item = %#v", item)
	}

	history = incrementalHistoryItems(t, router, "thread-incremental-instructions")
	removed, err := router.incrementalToolCatalogForTurnWithModel(
		"thread-incremental-instructions", history, definitions, "", "gpt-next", "turn-5", created.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Items) != 1 {
		t.Fatalf("instruction removal = %#v", removed.Items)
	}
	item, _ = removed.Items[0].(map[string]any)
	if text := textFromInputItemContent(item["content"]); !strings.Contains(text, "The previous base instructions no longer apply. No replacement base instructions are active.") {
		t.Fatalf("instruction removal item = %#v", item)
	}
}

// TestIncrementalToolCatalogAppendsChangedCatalogLikeRust covers the model-switch
// half of Rust #50540: a changed member appends only that member with the
// incremental namespace hint, leaving the window's earlier declarations in place.
func TestIncrementalToolCatalogAppendsChangedCatalogLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental")
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())

	if _, err := router.incrementalToolCatalogForTurn("thread-incremental", nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}
	history := incrementalHistoryItems(t, router, "thread-incremental")
	updated := incrementalCatalogLikeRust("Updated execution instructions.", incrementalToolSearchLikeRust())

	result, err := router.incrementalToolCatalogForTurn("thread-incremental", history, updated, instructions, "turn-2", created)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Active || result.Prefix {
		t.Fatalf("changed catalog result = %#v, want an appended update", result)
	}
	if len(result.Items) != 2 || incrementalInputType(t, result.Items[0]) != "message" || incrementalInputType(t, result.Items[1]) != "additional_tools" {
		t.Fatalf("changed catalog items = %#v", result.Items)
	}
	if len(result.SessionItems) != 2 {
		t.Fatalf("changed catalog session items = %#v", result.SessionItems)
	}
	hintKinds := sessionItemContentItemKinds(&result.SessionItems[0])
	if len(hintKinds) != 1 || hintKinds[0] != session.ToolCatalogUpdateHintKind {
		t.Fatalf("update hint content kinds = %#v, want %q", hintKinds, session.ToolCatalogUpdateHintKind)
	}
	if result.SessionItems[0].Text != session.ToolUpdateHint {
		t.Fatalf("update hint = %q, want %q", result.SessionItems[0].Text, session.ToolUpdateHint)
	}
	catalog, _ := result.Items[1].(map[string]any)
	tools, _ := catalog["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("incremental tools = %#v, want only the changed member", catalog["tools"])
	}
	namespace, _ := tools[0].(map[string]any)
	if namespace["name"] != "functions" {
		t.Fatalf("incremental namespace = %#v", tools[0])
	}
	members, _ := namespace["tools"].([]any)
	if len(members) != 1 {
		t.Fatalf("incremental namespace members = %#v", namespace["tools"])
	}
	member, _ := members[0].(map[string]any)
	if member["name"] != "exec" {
		t.Fatalf("incremental member = %#v", members[0])
	}
	description, _ := namespace["description"].(string)
	if strings.Contains(description, session.ToolUpdateHint) {
		t.Fatalf("namespace description acquired the separate hint: %q", description)
	}

	// The window's earlier declarations are untouched and the update follows the
	// conversation, not the prefix.
	record, err := router.threadRecord(session.ThreadID("thread-incremental"), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	if len(record.Items) != 4 || record.Items[0].Type != "additional_tools" || record.Items[2].Type != "message" || record.Items[3].Type != "additional_tools" {
		t.Fatalf("recorded items = %#v, want the update appended after the window start", record.Items)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	hashes, ok := session.TopLevelToolsSnapshotForWindow(state.TopLevelTools, 0)
	if !ok || hashes["functions.exec"] == "" {
		t.Fatalf("catalog snapshot = %s", string(state.TopLevelTools))
	}
}

// Rust #52295: a full-history fork keeps the update notice next to the
// namespace declarations it introduces, and keeps it exactly once.
func TestIncrementalToolUpdateNoticeSurvivesFullHistoryForkLikeRust(t *testing.T) {
	const parentID = "thread-incremental-fork-parent"
	router, parent := newResponsesLiteDeclarationRouter(t, parentID)
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	if _, err := router.incrementalToolCatalogForTurn(parentID, nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}
	history := incrementalHistoryItems(t, router, parentID)
	updated := incrementalCatalogLikeRust("Updated execution instructions.", incrementalToolSearchLikeRust())
	if _, err := router.incrementalToolCatalogForTurn(parentID, history, updated, instructions, "turn-2", created.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	controller, ok := newRuntimeAgentControllerWithVersion(router, parentID, parent.Metadata.CWD, 2, agent.VersionV2).(agent.V2ToolController)
	if !ok {
		t.Fatal("runtime controller does not implement V2ToolController")
	}
	message := "Continue the parent task."
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{
		TaskName: "worker",
		Message:  &message,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := router.threadRecord(session.ThreadID(spawned.AgentID), true, true)
	if err != nil || child == nil {
		t.Fatalf("child threadRecord error = %v", err)
	}
	hintCount := 0
	catalogCount := 0
	for i := range child.Items {
		if incrementalDeclarationContainsKind(&child.Items[i], session.ToolCatalogUpdateHintKind) {
			hintCount++
			if child.Items[i].Text != session.ToolUpdateHint {
				t.Fatalf("forked update hint = %q, want %q", child.Items[i].Text, session.ToolUpdateHint)
			}
		}
		if child.Items[i].Type == "additional_tools" {
			catalogCount++
		}
	}
	if hintCount != 1 || catalogCount != 2 {
		t.Fatalf("forked items = %#v, want one update notice with both declaration batches", child.Items)
	}
}

// TestIncrementalToolCatalogRemovalNoticesLikeRust covers Rust #51202
// (`incremental_tool_removals`): removed namespaces and removed individual tools
// travel as one standalone developer notice with separate headers.
func TestIncrementalToolCatalogRemovalNoticesLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental")
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	if _, err := router.incrementalToolCatalogForTurn("thread-incremental", nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}

	// Dropping one member and the built-in lists both under the tools header.
	history := incrementalHistoryItems(t, router, "thread-incremental")
	trimmed := []any{map[string]any{
		"type":        "namespace",
		"name":        "functions",
		"description": "Built-in tools",
		"tools":       []any{map[string]any{"type": "custom", "name": "exec", "description": "Run a command."}},
	}}
	result, err := router.incrementalToolCatalogForTurn("thread-incremental", history, trimmed, instructions, "turn-2", created)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || incrementalInputType(t, result.Items[0]) != "message" {
		t.Fatalf("removal items = %#v", result.Items)
	}
	notice, _ := result.Items[0].(map[string]any)
	content, _ := notice["content"].([]map[string]any)
	if len(content) != 1 {
		t.Fatalf("removal notice content = %#v", notice["content"])
	}
	text, _ := content[0]["text"].(string)
	want := "The following tools are no longer available. Do not call them:\n- functions.exec_command\n- tool_search"
	if strings.TrimSpace(text) != want {
		t.Fatalf("removal notice = %q, want %q", text, want)
	}

	// Dropping the whole namespace lists it separately and swallows its members.
	namespaceRouter, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental-ns")
	if _, err := namespaceRouter.incrementalToolCatalogForTurn("thread-incremental-ns", nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}
	namespaceHistory := incrementalHistoryItems(t, namespaceRouter, "thread-incremental-ns")
	withoutNamespace := []any{incrementalToolSearchLikeRust()}
	result, err = namespaceRouter.incrementalToolCatalogForTurn("thread-incremental-ns", namespaceHistory, withoutNamespace, instructions, "turn-2", created)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || incrementalInputType(t, result.Items[0]) != "message" {
		t.Fatalf("namespace removal items = %#v", result.Items)
	}
	notice, _ = result.Items[0].(map[string]any)
	content, _ = notice["content"].([]map[string]any)
	text, _ = content[0]["text"].(string)
	if !strings.HasPrefix(strings.TrimSpace(text), "The following namespaces are no longer available.") {
		t.Fatalf("namespace removal notice = %q", text)
	}
	if !strings.Contains(text, "- functions") {
		t.Fatalf("namespace removal notice = %q, want the namespace", text)
	}
	if strings.Contains(text, "functions.exec") {
		t.Fatalf("namespace removal notice = %q, want members swallowed", text)
	}
}

// TestIncrementalToolCatalogLegacyResumeLikeRust covers the legacy-resume case of
// Rust #51480 (`scenarios_incremental_tools_resume::legacy_resume_window_reset`):
// an existing window that never recorded a catalog keeps the pre-#50540 shape,
// so the caller keeps rebuilding its declaration prefix.
func TestIncrementalToolCatalogLegacyResumeLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental")
	created := time.Now().UTC()
	history := []any{map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": "Begin with the legacy tool prefix."}}}}

	result, err := router.incrementalToolCatalogForTurn("thread-incremental", history, incrementalCatalogLikeRust("Run a command."), "Use the tools.", "turn-1", created)
	if err != nil {
		t.Fatal(err)
	}
	if result.Active || len(result.Items) != 0 {
		t.Fatalf("legacy resume result = %#v, want the legacy shape", result)
	}
	// The decision is frozen: later turns of the same window stay legacy too.
	result, err = router.incrementalToolCatalogForTurn("thread-incremental", history, incrementalCatalogLikeRust("Run a command."), "Use the tools.", "turn-2", created)
	if err != nil {
		t.Fatal(err)
	}
	if result.Active {
		t.Fatalf("frozen legacy result = %#v", result)
	}
	// No catalog snapshot is recorded for a legacy window: only the caller's
	// frozen-prefix path decides how its declarations travel.
	record, err := router.threadRecord(session.ThreadID("thread-incremental"), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := session.TopLevelToolsSnapshotForWindow(state.TopLevelTools, 0); ok {
		t.Fatalf("legacy window snapshot = %s, want none", string(state.TopLevelTools))
	}
}

// TestIncrementalToolCatalogWindowReplacementLikeRust covers the window
// replacement of Rust #51188: a compaction or context reset advances the window,
// so the replacement window re-records the full catalog and the base
// instructions instead of diffing against the previous window.
func TestIncrementalToolCatalogWindowReplacementLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental")
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	if _, err := router.incrementalToolCatalogForTurn("thread-incremental", nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}
	history := incrementalHistoryItems(t, router, "thread-incremental")

	router.advanceWindowNumber("thread-incremental")
	updated := incrementalCatalogLikeRust("Updated execution instructions.", incrementalToolSearchLikeRust())
	result, err := router.incrementalToolCatalogForTurn("thread-incremental", history, updated, instructions, "turn-2", created)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Active || !result.Prefix || len(result.Items) != 2 {
		t.Fatalf("window replacement result = %#v, want a fresh window catalog", result)
	}
	catalog, _ := result.Items[0].(map[string]any)
	tools, _ := catalog["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("replacement catalog tools = %#v, want the whole catalog", catalog["tools"])
	}
	record, err := router.threadRecord(session.ThreadID("thread-incremental"), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := session.TopLevelToolsSnapshotForWindow(state.TopLevelTools, 1); !ok {
		t.Fatalf("replacement snapshot = %s, want window 1", string(state.TopLevelTools))
	}
	if _, ok := session.TopLevelToolsSnapshotForWindow(state.TopLevelTools, 0); ok {
		t.Fatalf("stale snapshot = %s, want the previous window replaced", string(state.TopLevelTools))
	}
}

// TestIncrementalToolsEnabledForTurnLikeRust checks the gate of Rust
// `StepContext::incremental_tools_enabled`: Responses Lite uses the stable,
// default-on feature, while regular Responses served by one of the two bundled
// Amazon Bedrock providers opt in through `incremental_tools_legacy`. Rust
// #60040 (762f87d5bb) narrowed that legacy gate from OpenAI to Bedrock.
func TestIncrementalToolsEnabledForTurnLikeRust(t *testing.T) {
	enabled := &config.Config{Values: map[string]any{"features": map[string]any{"incremental_tools": true}}}
	disabled := &config.Config{Values: map[string]any{}}
	explicitlyDisabled := &config.Config{Values: map[string]any{"features": map[string]any{"incremental_tools": false}}}
	legacy := &config.Config{Values: map[string]any{"features": map[string]any{"incremental_tools_legacy": true}}}
	liteStableOff := &config.Config{Values: map[string]any{"features": map[string]any{"incremental_tools": false, "incremental_tools_legacy": true}}}
	lite := &model.ModelInfo{UseResponsesLite: true}
	full := &model.ModelInfo{UseResponsesLite: false}

	if !incrementalToolsEnabledForTurn(enabled, lite, model.OpenAIProviderID) {
		t.Fatal("lite + feature must enable incremental tools")
	}
	if !incrementalToolsEnabledForTurn(disabled, lite, model.OpenAIProviderID) {
		t.Fatal("lite must enable incremental tools by default")
	}
	if incrementalToolsEnabledForTurn(explicitlyDisabled, lite, model.OpenAIProviderID) {
		t.Fatal("an explicit disable must keep legacy tool declarations")
	}
	if incrementalToolsEnabledForTurn(liteStableOff, lite, model.OpenAIProviderID) {
		t.Fatal("the legacy flag must not enable lite windows on its own")
	}
	if !incrementalToolsEnabledForTurn(disabled, full, model.AmazonBedrockProviderID) {
		t.Fatal("regular Amazon Bedrock Responses enable incremental tools by default")
	}
	legacyOff := &config.Config{Values: map[string]any{"features": map[string]any{"incremental_tools_legacy": false}}}
	if incrementalToolsEnabledForTurn(legacyOff, full, model.AmazonBedrockProviderID) ||
		incrementalToolsEnabledForTurn(legacyOff, full, model.AmazonBedrockRuntimeProviderID) {
		t.Fatal("an explicit incremental_tools_legacy = false must keep legacy declarations")
	}
	if !incrementalToolsEnabledForTurn(legacy, full, model.AmazonBedrockProviderID) {
		t.Fatal("regular Amazon Bedrock Responses must honor incremental_tools_legacy")
	}
	if !incrementalToolsEnabledForTurn(legacy, full, model.AmazonBedrockRuntimeProviderID) {
		t.Fatal("regular Amazon Bedrock Runtime Responses must honor incremental_tools_legacy")
	}
	if incrementalToolsEnabledForTurn(legacy, full, model.OpenAIProviderID) {
		t.Fatal("the legacy feature no longer applies to the OpenAI provider")
	}
	if incrementalToolsEnabledForTurn(legacy, full, "azure") {
		t.Fatal("the legacy feature only applies to the Amazon Bedrock providers")
	}
	if incrementalToolsEnabledForTurn(nil, lite, "") || incrementalToolsEnabledForTurn(enabled, nil, "") {
		t.Fatal("missing config or model info must stay legacy")
	}
}

// TestIncrementalToolCatalogRequestsReplayFromHistoryLikeRust checks the record
// round trip: the recorded catalog entries rebuild into the same request items,
// so a resumed window replays its declarations instead of re-deriving them.
func TestIncrementalToolCatalogRequestsReplayFromHistoryLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental")
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	first, err := router.incrementalToolCatalogForTurn("thread-incremental", nil, definitions, instructions, "turn-1", created)
	if err != nil {
		t.Fatal(err)
	}
	history := incrementalHistoryItems(t, router, "thread-incremental")
	if len(history) != len(first.Items) {
		t.Fatalf("history = %#v, want the recorded declarations", history)
	}
	for i := range first.Items {
		want, err := json.Marshal(first.Items[i])
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(history[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != string(got) {
			t.Fatalf("replayed item %d = %s, want %s", i, string(got), string(want))
		}
	}
}

// TestIncrementalToolCatalogReplacementPrefixFirstLikeRust covers the position
// semantics of Rust #51188 (`compact::assemble_compaction_history`, compared in
// `assemble_compaction_history_keeps_prefix_first_and_summary_last` and
// `assemble_compaction_history_keeps_compaction_last`): a window replacement -
// a compaction or a context reset - rebuilds the history, and the re-derived
// prefix (the `additional_tools` catalog plus the base-instruction developer
// message) must open that rebuilt history while the compaction summary stays
// last.
func TestIncrementalToolCatalogReplacementPrefixFirstLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-incremental")
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	if _, err := router.incrementalToolCatalogForTurn("thread-incremental", nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}

	// Replace the history the way a compaction does: the rebuilt conversation
	// ends with the compaction summary and the compaction item.
	record, err := router.threadRecord(session.ThreadID("thread-incremental"), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	record.Items = []session.Item{
		{ID: "kept-user", Type: "message", Role: "user", Text: "earlier request", CreatedAt: created},
		{ID: "summary", Type: "message", Role: "user", Text: "compaction summary", CreatedAt: created,
			Data: map[string]any{"kind": "compaction_summary"}},
		{ID: "compact-item", Type: "contextCompaction", CreatedAt: created},
	}
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatal(err)
	}
	router.advanceWindowNumber("thread-incremental")

	replacement := incrementalCatalogLikeRust("Updated execution instructions.", incrementalToolSearchLikeRust())
	result, err := router.incrementalToolCatalogForTurn("thread-incremental", nil, replacement, instructions, "turn-2", created.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Active || !result.Prefix || len(result.SessionItems) != 2 {
		t.Fatalf("replacement result = %#v, want a fresh window prefix", result)
	}

	record, err = router.threadRecord(session.ThreadID("thread-incremental"), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	if len(record.Items) != 5 {
		t.Fatalf("rebuilt history = %#v, want the prefix plus the three compacted items", record.Items)
	}
	prefixCatalog, _ := record.Items[0].Data["kind"].(string)
	if record.Items[0].Type != "additional_tools" || prefixCatalog != incrementalToolCatalogKind {
		t.Fatalf("rebuilt history must open with the tool catalog, got %#v", record.Items[0])
	}
	if record.Items[1].Type != "message" || record.Items[1].Role != "developer" || record.Items[1].Text != instructions {
		t.Fatalf("rebuilt history must continue with the base instructions, got %#v", record.Items[1])
	}
	if record.Items[2].ID != "kept-user" || record.Items[3].ID != "summary" || record.Items[4].ID != "compact-item" {
		t.Fatalf("rebuilt history must keep the compacted items last, got %#v", record.Items)
	}

	// The model-visible history places the declarations ahead of the summary, so
	// the next turn of the replaced window replays them in place.
	history := incrementalHistoryItems(t, router, "thread-incremental")
	if len(history) < 2 || incrementalInputType(t, history[0]) != "additional_tools" {
		t.Fatalf("model-visible history = %#v, want the catalog first", history)
	}
	summaryIndex := -1
	for i := range history {
		if object, ok := history[i].(map[string]any); ok && strings.Contains(textFromInputItemContent(object["content"]), "compaction summary") {
			summaryIndex = i
			break
		}
	}
	if summaryIndex < 2 {
		t.Fatalf("summary index = %d in %#v, want the declarations ahead of it", summaryIndex, history)
	}

	// The replaced window keeps replaying its recorded catalog: no further
	// updates and no re-recording, since the prefix is already in history.
	replay, err := router.incrementalToolCatalogForTurn("thread-incremental", history, replacement, instructions, "turn-3", created.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Active || replay.Prefix || len(replay.SessionItems) != 0 {
		t.Fatalf("replaced window turn = %#v, want a replay without updates", replay)
	}
}

// TestIncrementalToolCatalogSuppressesModelChangeAnnouncementsLikeRust mirrors
// Rust #58405: with suppress_model_change_announcements enabled, a model switch
// in an existing window sends no model ID announcement while changed
// instructions still produce an update.
func TestIncrementalToolCatalogSuppressesModelChangeAnnouncementsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nsuppress_model_change_announcements = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{
		ID:        session.ThreadID("thread-suppressed"),
		SessionID: "thread-suppressed",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4"},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
	if !router.suppressModelChangeAnnouncements() {
		t.Fatal("suppressModelChangeAnnouncements() = false, want the configured feature")
	}
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	instructions := "Use the available tools to help the user."
	first, err := router.incrementalToolCatalogForTurnWithModel("thread-suppressed", nil, definitions, instructions, "gpt-5.4", "turn-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Active || !first.Prefix {
		t.Fatalf("window start = %#v, want an active prefix", first)
	}
	history := incrementalHistoryItems(t, router, "thread-suppressed")
	switched, err := router.incrementalToolCatalogForTurnWithModel("thread-suppressed", history, definitions, instructions, "gpt-next", "turn-2", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range switched.Items {
		if strings.Contains(anyJSON(t, item), "The current model is") {
			t.Fatalf("suppressed model switch emitted a model announcement: %#v", item)
		}
	}
	changed, err := router.incrementalToolCatalogForTurnWithModel("thread-suppressed", history, definitions, "Updated instructions.", "gpt-next", "turn-3", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Items) != 1 || !strings.Contains(anyJSON(t, changed.Items[0]), "Updated instructions.") {
		t.Fatalf("suppressed model switch with new instructions = %#v, want only the instruction update", changed.Items)
	}
	if strings.Contains(anyJSON(t, changed.Items[0]), "The current model is") {
		t.Fatalf("instruction update announced the model: %#v", changed.Items[0])
	}
}

func anyJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	return string(data)
}
