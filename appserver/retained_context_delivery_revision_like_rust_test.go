package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
)

// TestDeliveredAssistantRevisionsSurviveRolloutReplayLikeRust pins Rust #60839:
// the revision captured when a delivered assistant message was recorded is
// persisted with the tool output, so replaying the thread reuses that revision
// instead of minting a new one.
func TestDeliveredAssistantRevisionsSurviveRolloutReplayLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-delivery-revision")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	now := fixedTime()

	initial := sessionItemForRetainedTest("user-1", "Only deploy privately.", []string{"user.text"}, []string{"input_text"})
	initial.CreatedAt = now
	if _, err := router.runtimeAppendItems(threadID, []session.Item{initial}); err != nil {
		t.Fatalf("runtimeAppendItems(initial) error = %v", err)
	}
	execution := userMessagingToolExecution("Continue?", true, now)
	call, ok := sessionItemForAppToolCall("turn-question", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolCall() = false")
	}
	output, ok := sessionItemForAppToolOutput("turn-question", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolOutput() = false")
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{call}); err != nil {
		t.Fatalf("runtimeAppendItems(call) error = %v", err)
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{output}); err != nil {
		t.Fatalf("runtimeAppendItems(output) error = %v", err)
	}

	live := router.retainedContextForThread(string(threadID))
	if live == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	liveSources := deliveredAssistantSources(live)
	if len(liveSources) != 1 {
		t.Fatalf("live delivered revisions = %#v, want one", liveSources)
	}
	record, err := store.Read(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("Read() = %#v/%v", record, err)
	}
	persisted := false
	for index := range record.Items {
		metadata := harnessMetadataFromSessionItem(&record.Items[index])
		if metadata == nil || metadata.DeliveredAssistantMessage == nil || metadata.RetainedSource == nil {
			continue
		}
		if metadata.RetainedSource.Revision != liveSources[0].Revision {
			t.Fatalf("persisted revision = %q, live revision = %q", metadata.RetainedSource.Revision, liveSources[0].Revision)
		}
		persisted = true
	}
	if !persisted {
		t.Fatalf("delivery revision was not persisted: %#v", record.Items)
	}

	replayed := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	}).retainedContextForThread(string(threadID))
	if replayed == nil {
		t.Fatal("replayed retainedContextForThread() = nil")
	}
	replayedSources := deliveredAssistantSources(replayed)
	if len(replayedSources) != len(liveSources) {
		t.Fatalf("replayed delivered revisions = %d, want %d", len(replayedSources), len(liveSources))
	}
	for index := range liveSources {
		if *replayedSources[index] != *liveSources[index] {
			t.Fatalf("replayed source = %#v, live source = %#v", replayedSources[index], liveSources[index])
		}
	}
}

func deliveredAssistantSources(context *retainedctx.RetainedContext) []*retainedctx.RetainedSource {
	var sources []*retainedctx.RetainedSource
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.AssistantMessage == nil {
			continue
		}
		if source := context.Source(entry.Entry); source != nil {
			sources = append(sources, source)
		}
	}
	return sources
}
