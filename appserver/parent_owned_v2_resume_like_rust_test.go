package appserver

import (
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

func TestParentOwnedV2ChildResumeRequiresLoadedParentLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	records := []*session.Record{
		{
			ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-parent", HistoryMode: string(ThreadHistoryLegacy)},
		},
		{
			ID: "child-v2", SessionID: "parent", ParentThreadID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{
				CWD: t.TempDir(), Model: "gpt-child", HistoryMode: string(ThreadHistoryLegacy),
				ThreadSource: "subAgentThreadSpawn", MultiAgentVersion: string(agent.VersionV2),
			},
		},
		{
			ID: "child-v1", SessionID: "parent", ParentThreadID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{
				CWD: t.TempDir(), Model: "gpt-v1-child", HistoryMode: string(ThreadHistoryLegacy),
				ThreadSource: "subAgentThreadSpawn", MultiAgentVersion: string(agent.VersionV1),
			},
		},
	}
	for _, record := range records {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	blocked := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{ThreadID: "child-v2"}))
	if blocked.Error == nil || blocked.Error.Message != "cannot resume an unloaded multi-agent v2 sub-agent through its parent; resume the parent first, or use thread/read to inspect it" {
		t.Fatalf("unloaded-parent resume = %+v", blocked.Error)
	}

	v1 := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: "child-v1"}))
	if v1.Error != nil {
		t.Fatalf("V1 child resume error = %+v", v1.Error)
	}

	router.markResponseThreadLoaded(&ThreadResumeResponse{Thread: &Thread{ID: "parent"}}, "conn-parent")
	override := "gpt-public-override"
	resumed := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{
		ThreadID: "child-v2",
		Model:    &override,
	}))
	if resumed.Error != nil {
		t.Fatalf("parent-owned child resume error = %+v", resumed.Error)
	}
	response := resumed.Result.(*ThreadResumeResponse)
	if response.Model != "gpt-child" {
		t.Fatalf("resumed child model = %q, want stored child model", response.Model)
	}
	if !router.threadIsLoaded("child-v2") {
		t.Fatal("parent-owned child was not marked loaded after parent-controlled resume")
	}
}
