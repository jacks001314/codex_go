package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/apps"
	"codex_go/session"
	"codex_go/turn"
)

func TestAppsCatalogGuidanceInTurnRequestAndHistory(t *testing.T) {
	home := t.TempDir()
	catalogPath := filepath.Join(home, "models.json")
	catalog := `{"models":[{"slug":"apps-test","display_name":"Apps test","base_instructions":"base","include_apps_usage_instructions":true,"model_messages":{"apps":{"usage_instructions":"literal catalog guidance"}}}]}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	agent := newRecordingRuntimeAgent("done")
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store), Turns: turn.NewTurnService(), Agent: agent, ThreadStatus: NewThreadStatusManager(),
		Apps: apps.NewAppService([]apps.AppEntry{{ID: "drive", Name: "Drive", IsAccessible: true, IsEnabled: true, EnabledExplicit: true}}),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)
	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: home, Model: "apps-test", Config: map[string]any{"model_catalog_json": catalogPath, "features.catalog_apps_instructions": true},
	}))
	if started.Error != nil {
		t.Fatal(started.Error)
	}
	id := started.Result.(*ThreadStartResponse).Thread.ID
	response := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: id, Prompt: "hello"}))
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if !agentRequestInputItemsContain(request, "<catalog_apps_instructions>literal catalog guidance</catalog_apps_instructions>") {
		data, _ := json.Marshal(request.InputItems)
		t.Fatalf("missing catalog guidance: %s", data)
	}
	waitForTurnCompletedStatus(t, sink, response.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
	record, err := store.Load(session.ThreadID(id))
	if err != nil {
		t.Fatal(err)
	}
	retained := latestAppsGuidance(record.Items)
	if retained == nil {
		t.Fatal("guidance not persisted")
	}
	replacement, err := router.buildCompactionReplacementContext(context.Background(), &runtimeCompactRequest{
		ThreadID: id, TurnID: "compact-apps", ReplacementModel: "apps-test",
		ReplacementParams: &turn.TurnStartParams{ThreadID: id, Model: "apps-test", CWD: home, Config: map[string]any{"model_catalog_json": catalogPath, "features.catalog_apps_instructions": true}},
	}, record)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(replacement.Items)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(encoded), "literal catalog guidance"); count == 0 {
		t.Fatalf("compaction lost apps guidance: %s", encoded)
	}

	// A later turn must keep the replacement context's guidance exactly once.
	// Reopen the persisted thread through a fresh router to cover recovery after
	// compaction rather than relying on in-memory turn state.
	record.Items = sessionItemsFromCompactItems(replacement.Items, time.Now().UTC())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	afterAgent := newRecordingRuntimeAgent("after compaction")
	afterSink := NewNotificationBuffer()
	afterRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store), Turns: turn.NewTurnService(), Agent: afterAgent, ThreadStatus: NewThreadStatusManager(),
		Apps: apps.NewAppService([]apps.AppEntry{{ID: "drive", Name: "Drive", IsAccessible: true, IsEnabled: true, EnabledExplicit: true}}),
	})
	t.Cleanup(func() { _ = afterRouter.Close() })
	afterRouter.SetNotificationSink(afterSink)
	after := afterRouter.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{ThreadID: id, Prompt: "after compaction"}))
	if after.Error != nil {
		t.Fatal(after.Error)
	}
	afterRequest := waitForRuntimeAgentRequest(t, afterAgent)
	afterJSON, _ := json.Marshal(afterRequest.InputItems)
	if count := strings.Count(string(afterJSON), "literal catalog guidance"); count != 1 {
		t.Fatalf("post-compaction guidance count = %d: %s", count, afterJSON)
	}
	waitForTurnCompletedStatus(t, afterSink, after.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
}
