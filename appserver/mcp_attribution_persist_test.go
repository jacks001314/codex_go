package appserver

import (
	"encoding/json"
	"reflect"
	"testing"

	"codex_go/retainedctx"
	"codex_go/session"
)

// Mirrors codex-rollout's `should_persist_response_item`: only durable
// response-item kinds carry the checkpoint.
func TestShouldPersistResponseItemMatchesRust(t *testing.T) {
	persisted := []string{
		"message", "agent_message", "reasoning", "local_shell_call", "function_call",
		"tool_search_call", "function_call_output", "tool_search_output",
		"custom_tool_call", "custom_tool_call_output", "web_search_call",
		"image_generation_call", "configuration_update", "compaction", "context_compaction",
	}
	for _, kind := range persisted {
		if !shouldPersistResponseItem(session.Item{Type: kind}) {
			t.Fatalf("shouldPersistResponseItem(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"additional_tools", "compaction_trigger", "other", ""} {
		if shouldPersistResponseItem(session.Item{Type: kind}) {
			t.Fatalf("shouldPersistResponseItem(%q) = true, want false", kind)
		}
	}
}

// Mirrors `context_manager::is_user_turn_boundary`: an agent message, a
// non-contextual user message, or an assistant inter-agent instruction marks a
// boundary; contextual user evidence does not.
func TestIsMcpAttributionTurnBoundaryMatchesRust(t *testing.T) {
	cases := []struct {
		name string
		item session.Item
		want bool
	}{
		{name: "agent message", item: session.Item{Type: "agent_message"}, want: true},
		{name: "plain user message", item: session.Item{Type: "message", Role: "user", Text: "hello"}, want: true},
		{
			// `eventmap.IsContextualUserMessageContent` mirrors Rust's
			// `context::is_contextual_user_fragment`, so an injected user fragment
			// is not a turn boundary.
			name: "contextual user fragment",
			item: session.Item{Type: "message", Role: "user", Content: []session.ContentPart{{Type: "input_text", Text: "<environment_context>\n<cwd>/repo</cwd>\n</environment_context>"}}},
			want: false,
		},
		{
			name: "developer current_time reminder",
			item: session.Item{Type: "message", Role: "user", Content: []session.ContentPart{{Type: "input_text", Text: "<current_time>now</current_time>"}}},
			want: true,
		},
		{name: "assistant message", item: session.Item{Type: "message", Role: "assistant", Text: "done"}, want: false},
		{
			name: "assistant inter-agent instruction",
			item: session.Item{Type: "message", Role: "assistant", Content: []session.ContentPart{{
				Type: "output_text",
				Text: `{"author":"/root","recipient":"/root/worker","content":"do it","trigger_turn":true}`,
			}}},
			want: true,
		},
		{
			name: "assistant partial inter-agent json",
			item: session.Item{Type: "message", Role: "assistant", Content: []session.ContentPart{{
				Type: "output_text",
				Text: `{"author":"/root","recipient":"/root/worker"}`,
			}}},
			want: false,
		},
		{name: "function call", item: session.Item{Type: "function_call"}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isMcpAttributionTurnBoundary(testCase.item); got != testCase.want {
				t.Fatalf("isMcpAttributionTurnBoundary() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// Mirrors Rust's
// `mcp_attribution_checkpoints_cover_batch_prefixes_compaction_and_restore`: the
// first persisted item and every user-turn boundary carry the checkpoint, and
// acknowledging the revision leaves a clean batch without one.
func TestMcpAttributionCheckpointsCoverBatchPrefixesLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	const threadID = "thread-1"
	source := retainedctx.McpAttributionSource{ServerName: "example", ToolName: "search", FirstTurnID: "turn_1"}
	router.executedToolCallRecorder(threadID).RecordMcpSource(source)
	expected := retainedctx.McpAttribution{
		Status:  retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{source},
	}

	items := []session.Item{
		{ID: "1", Type: "function_call_output", CallID: "call_1"},
		{ID: "2", Type: "message", Role: "user", Text: "next turn"},
	}
	revision, ok := router.annotateMcpAttributionMetadata(threadID, items)
	if !ok {
		t.Fatal("expected a checkpoint for the recorded source")
	}
	for index, item := range items {
		metadata := harnessMetadataFromSessionItem(&item)
		if metadata == nil || metadata.McpAttribution == nil {
			t.Fatalf("item %d has no mcp_attribution: %#v", index, item.Data)
		}
		if got := *metadata.McpAttribution; !reflect.DeepEqual(got, expected) {
			t.Fatalf("item %d attribution = %#v, want %#v", index, got, expected)
		}
	}

	router.markMcpAttributionPersisted(threadID, revision)
	// A clean recorder with no turn boundary writes nothing.
	cleanItems := []session.Item{{ID: "3", Type: "function_call", CallID: "call_2"}}
	if _, ok := router.annotateMcpAttributionMetadata(threadID, cleanItems); ok {
		t.Fatal("acknowledged checkpoint rewrote a clean batch")
	}
	if cleanItems[0].Data != nil {
		t.Fatalf("clean batch annotated: %#v", cleanItems[0].Data)
	}
	// A turn boundary forces the unchanged checkpoint again.
	forcedItems := []session.Item{{ID: "4", Type: "message", Role: "user", Text: "another turn"}}
	if _, ok := router.annotateMcpAttributionMetadata(threadID, forcedItems); !ok {
		t.Fatal("turn boundary did not force the checkpoint")
	}
	metadata := harnessMetadataFromSessionItem(&forcedItems[0])
	if metadata == nil || metadata.McpAttribution == nil || !reflect.DeepEqual(*metadata.McpAttribution, expected) {
		t.Fatalf("forced checkpoint = %#v, want %#v", metadata, expected)
	}
}

// A batch with no durable carrier leaves the checkpoint unacknowledged so the
// next persisted batch writes it.
func TestMcpAttributionCheckpointSkipsUnpersistedBatches(t *testing.T) {
	router := &RuntimeRouter{}
	const threadID = "thread-2"
	router.executedToolCallRecorder(threadID).RecordMcpSource(retainedctx.McpAttributionSource{ServerName: "example", ToolName: "search", FirstTurnID: "turn_1"})
	items := []session.Item{{ID: "1", Type: "other"}}
	if _, ok := router.annotateMcpAttributionMetadata(threadID, items); ok {
		t.Fatal("batch without a persisted item reported a checkpoint")
	}
	if items[0].Data != nil {
		t.Fatalf("unpersisted batch annotated: %#v", items[0].Data)
	}
}

// Mirrors Rust's `ExecutedToolCalls::new(features, history)` restore: a resumed
// or forked thread seeds the recorder from its persisted checkpoints, pre-
// attribution history reports the missing-checkpoint error, and an empty thread
// starts clean.
func TestExecutedToolCallRecorderSeedsFromThreadHistory(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-mcp-attribution-seed")
	item := func(attribution string) session.Item {
		item := session.Item{ID: "item-" + attribution, Type: "message", Role: "user", Text: "hello"}
		if attribution != "" {
			item.Data = map[string]any{"harness_metadata": json.RawMessage(`{"mcp_attribution":` + attribution + `}`)}
		}
		return item
	}
	initial := `{"status":"complete","sources":[{"server_name":"example","tool_name":"search","first_turn_id":"turn_1"}]}`
	cumulative := `{"status":"complete","sources":[{"server_name":"example","tool_name":"search","first_turn_id":"turn_1"},{"server_name":"example","tool_name":"fetch","first_turn_id":"turn_2"}]}`
	if err := store.Save(&session.Record{ID: threadID, Items: []session.Item{item(initial), item(cumulative)}}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	restored := router.executedToolCallRecorder(string(threadID)).McpAttributionSnapshot()
	want := retainedctx.McpAttribution{
		Status: retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{
			{ServerName: "example", ToolName: "search", FirstTurnID: "turn_1"},
			{ServerName: "example", ToolName: "fetch", FirstTurnID: "turn_2"},
		},
	}
	if !reflect.DeepEqual(restored, want) {
		t.Fatalf("restored attribution = %#v, want %#v", restored, want)
	}
	if again := router.executedToolCallRecorder(string(threadID)); again != router.executedToolCallRecorder(string(threadID)) {
		t.Fatal("seeding replaced the cached recorder")
	}

	// An empty thread is a fresh history: no checkpoint and no error.
	if got := router.executedToolCallRecorder("thread-empty").McpAttributionSnapshot(); got.Status != retainedctx.McpAttributionStatusNone {
		t.Fatalf("empty thread attribution = %#v, want none", got)
	}

	// A legacy thread with items but no checkpoint cannot prove earlier context
	// was MCP-free.
	legacy := session.ThreadID("thread-mcp-attribution-legacy")
	if err := store.Save(&session.Record{ID: legacy, Items: []session.Item{item("")}}); err != nil {
		t.Fatalf("Save(legacy) error = %v", err)
	}
	got := router.executedToolCallRecorder(string(legacy)).McpAttributionSnapshot()
	if got.Status != retainedctx.McpAttributionStatusAttributionError ||
		got.ErrorReason == nil || *got.ErrorReason != retainedctx.McpAttributionErrorHistoryMissingCheckpoint {
		t.Fatalf("legacy thread attribution = %#v, want the missing-checkpoint error", got)
	}
}
