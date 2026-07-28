package app

import (
	"testing"
	"time"

	"codex_go/session"
	"codex_go/tui"
)

func TestInteractiveLocalAgentCallbacksReadLineageAndSwitchThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Unix(1_900_000_000, 0).UTC()
	records := []*session.Record{
		{ID: "thread-main", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{Source: "cli"}},
		{
			ID:             "thread-worker",
			ParentThreadID: "thread-main",
			CreatedAt:      now.Add(time.Second),
			UpdatedAt:      now.Add(time.Second),
			Metadata: session.Metadata{
				Source:        "subagent:thread_spawn",
				ThreadSource:  "subAgentThreadSpawn",
				AgentNickname: "Scout",
				AgentRole:     "review",
				AgentPath:     "/root/scout",
				RolloutTurns:  []session.TurnSnapshot{{ID: "turn-worker", Status: "inProgress"}},
			},
			Items: []session.Item{
				{ID: "user-worker", Type: "user_message", Role: "user", Text: "inspect the change"},
				{ID: "assistant-worker", Type: "agent_message", Role: "assistant", Text: "inspection underway", Data: map[string]any{"phase": "final_answer"}},
			},
		},
		{
			ID:             "thread-nested",
			ParentThreadID: "thread-worker",
			CreatedAt:      now.Add(2 * time.Second),
			UpdatedAt:      now.Add(2 * time.Second),
			Metadata: session.Metadata{
				Source:    "subagent:thread_spawn",
				AgentRole: "explorer",
				AgentPath: "/root/scout/explorer",
			},
		},
		{
			ID:             "thread-fork",
			ParentThreadID: "thread-main",
			CreatedAt:      now.Add(3 * time.Second),
			UpdatedAt:      now.Add(3 * time.Second),
			Metadata:       session.Metadata{Source: "cli"},
		},
		{
			ID:             "thread-side",
			ParentThreadID: "thread-main",
			CreatedAt:      now.Add(4 * time.Second),
			UpdatedAt:      now.Add(4 * time.Second),
			Metadata:       session.Metadata{Source: "cli", Extra: map[string]any{"tui_side_conversation": true}},
		},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
	}

	read, switchThread := interactiveLocalAgentCallbacks(func() *session.Store { return store })
	entries, err := read(" thread-nested ")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].ThreadID != "thread-main" || !entries[0].IsPrimary {
		t.Fatalf("primary entry = %#v", entries[0])
	}
	if entries[1].ThreadID != "thread-worker" || entries[1].AgentPath != "/root/scout" || !entries[1].IsRunning {
		t.Fatalf("worker entry = %#v", entries[1])
	}
	if entries[2].ThreadID != "thread-nested" || entries[2].AgentPath != "/root/scout/explorer" {
		t.Fatalf("nested entry = %#v", entries[2])
	}
	if entries[3].ThreadID != "thread-side" || entries[3].IsPrimary {
		t.Fatalf("side entry = %#v", entries[3])
	}

	response, err := switchThread(" thread-worker ")
	if err != nil {
		t.Fatal(err)
	}
	if response.Entry.ThreadID != "thread-worker" || response.Entry.IsPrimary || response.Status != "running" {
		t.Fatalf("switch response = %#v", response)
	}
	if len(response.Messages) != 2 || response.Messages[0].Role != tui.RoleUser || response.Messages[1].Text != "inspection underway" {
		t.Fatalf("switch messages = %#v", response.Messages)
	}
}

func TestInteractiveLocalAgentCallbacksReturnSyntheticPrimaryBeforePersistence(t *testing.T) {
	store := session.NewStore(t.TempDir())
	read, _ := interactiveLocalAgentCallbacks(func() *session.Store { return store })
	entries, err := read("thread-started")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ThreadID != "thread-started" || !entries[0].IsPrimary {
		t.Fatalf("entries = %#v", entries)
	}
}
