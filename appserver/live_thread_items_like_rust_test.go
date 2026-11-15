package appserver

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

func TestRuntimeLiveThreadItemsRecordCompletedTurnItemsLikeRust(t *testing.T) {
	root := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(root)),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	live := router.openLiveThreadItems("thread-live-items", "turn-2")
	if live == nil {
		t.Fatal("openLiveThreadItems() = nil")
	}
	defer live.Close()

	now := time.Now().UTC()
	router.recordLiveResponseItem(live, "turn-2", model.AgentItem{ID: "msg-live", Type: "message", Text: "Inspecting the repo."})
	router.recordLiveResponseItem(live, "turn-2", model.AgentItem{ID: "rs-live", Type: "reasoning", Text: "checking"})
	// Tool calls are recorded from ToolCompleted, not from the raw response
	// item, so the fork sees the same representation the final turn persists.
	router.recordLiveResponseItem(live, "turn-2", model.AgentItem{ID: "fc-live", Type: "function_call", Name: "shell", CallID: "call-live", Arguments: `{"command":"git status"}`})
	router.recordLiveToolExecution(live, "turn-2", &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "call-live",
			ToolName: tool.PlainName("shell"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"command":"git status"}`},
		},
		Output:     &tool.Output{Body: "clean", Success: true},
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
	})

	items := live.Snapshot()
	if len(items) != 4 {
		t.Fatalf("live items = %#v, want assistant, reasoning, and tool call/output", items)
	}
	if items[0].Type != "agent_message" || items[0].Text != "Inspecting the repo." {
		t.Fatalf("assistant live item = %#v", items[0])
	}
	if items[1].Type != "reasoning" || items[1].Role != "" || items[1].Text != "checking" {
		t.Fatalf("reasoning live item = %#v", items[1])
	}
	if items[2].Type != "function_call" || items[2].CallID != "call-live" {
		t.Fatalf("tool call live item = %#v", items[2])
	}
	if items[3].Type != "tool_output" || items[3].CallID != "call-live" || items[3].Text != "clean" {
		t.Fatalf("tool output live item = %#v", items[3])
	}
}

func TestActiveForkSnapshotPlacesLiveItemsBeforeInterruptMarker(t *testing.T) {
	now := time.Now().UTC()
	record := &session.Record{ID: "thread-fork-live-order", CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	active := activeRuntimeTurnForkSnapshot{
		TurnID: "turn-2",
		Params: &turn.TurnStartParams{ThreadID: "thread-parent", Prompt: "parent question"},
	}
	annotateActiveForkSourceSnapshot(record, active, now, []session.Item{
		{ID: "assistant-2", Type: "agent_message", Role: "assistant", Text: "Inspecting the repo.", CreatedAt: now},
	})
	if len(record.Items) != 3 {
		t.Fatalf("fork items = %#v, want user prompt, live suffix, and interrupt marker", record.Items)
	}
	if record.Items[0].Role != "user" || record.Items[0].Text != "parent question" {
		t.Fatalf("fork prompt = %#v", record.Items[0])
	}
	if record.Items[1].ID != "assistant-2" {
		t.Fatalf("fork live suffix = %#v", record.Items[1])
	}
	if record.Items[2].ID != "turn-aborted-turn-2" {
		t.Fatalf("fork interrupt marker = %#v", record.Items[2])
	}
}
