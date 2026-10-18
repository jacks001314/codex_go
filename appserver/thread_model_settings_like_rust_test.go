package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
)

func stringPtrModelSettings(value string) *string { return &value }

// TestThreadReadReportsLiveModelSettingsLikeRust covers Rust #42151: a loaded
// thread reports its current settings instead of the persisted start snapshot.
func TestThreadReadReportsLiveModelSettingsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-live-model")
	if err := store.Save(&session.Record{ID: threadID, Metadata: session.Metadata{
		CWD:   t.TempDir(),
		Model: "persisted-model",
		Extra: map[string]any{"config": map[string]any{"model_reasoning_effort": "low"}},
	}}); err != nil {
		t.Fatalf("Save thread error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService()})
	router.requireThreadStatus().UpsertThread(string(threadID), false)
	if _, err := router.requireThreadExtras().UpdateSettings(&SettingsUpdateParams{
		ThreadID: string(threadID),
		Model:    stringPtrModelSettings("live-model"),
		Effort:   stringPtrModelSettings("high"),
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: string(threadID)}))
	if response.Error != nil {
		t.Fatalf("thread/read error = %+v", response.Error)
	}
	thread := response.Result.(*ThreadReadResponse).Thread
	if thread.Model == nil || *thread.Model != "live-model" {
		t.Fatalf("live model = %#v", thread.Model)
	}
	if thread.ReasoningEffort == nil || *thread.ReasoningEffort != "high" {
		t.Fatalf("live reasoning effort = %#v", thread.ReasoningEffort)
	}
}

// TestThreadListReportsLiveAndPersistedModelSettingsLikeRust covers the list
// path: loaded threads use live settings while unloaded threads keep the
// persisted snapshot.
func TestThreadListReportsLiveAndPersistedModelSettingsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	liveID := session.ThreadID("thread-list-live")
	persistedID := session.ThreadID("thread-list-persisted")
	now := time.Now().UTC()
	for _, record := range []*session.Record{
		{
			ID:        liveID,
			SessionID: string(liveID),
			CreatedAt: now,
			UpdatedAt: now,
			RecencyAt: now,
			Metadata: session.Metadata{
				CWD:           t.TempDir(),
				Model:         "persisted-live",
				ModelProvider: "openai",
				Source:        string(SessionSourceCli),
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		},
		{
			ID:        persistedID,
			SessionID: string(persistedID),
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
			RecencyAt: now.Add(time.Minute),
			Metadata: session.Metadata{
				CWD:           t.TempDir(),
				Model:         "persisted-only",
				ModelProvider: "openai",
				Source:        string(SessionSourceCli),
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService()})
	router.requireThreadStatus().UpsertThread(string(liveID), false)
	if _, err := router.requireThreadExtras().UpdateSettings(&SettingsUpdateParams{
		ThreadID: string(liveID),
		Model:    stringPtrModelSettings("live-list-model"),
		Effort:   stringPtrModelSettings("medium"),
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	response := router.Handle(&Request{
		JSONRPC: "2.0",
		ID:      IntID(1),
		Method:  MethodThreadList,
		Params:  json.RawMessage(`{"modelProviders":[]}`),
	})
	if response.Error != nil {
		t.Fatalf("thread/list error = %+v", response.Error)
	}
	list := response.Result.(*ThreadListResponse)
	byID := map[string]*Thread{}
	for i := range list.Data {
		byID[list.Data[i].ID] = &list.Data[i]
	}
	if thread := byID[string(liveID)]; thread == nil || thread.Model == nil || *thread.Model != "live-list-model" ||
		thread.ReasoningEffort == nil || *thread.ReasoningEffort != "medium" {
		t.Fatalf("live list thread = %#v (ids = %#v)", thread, byID)
	}
	if thread := byID[string(persistedID)]; thread == nil || thread.Model == nil || *thread.Model != "persisted-only" ||
		thread.ReasoningEffort != nil {
		t.Fatalf("persisted list thread = %#v", thread)
	}
}

// TestThreadSettingsUpdatePersistsModelSettingsLikeRust covers restart
// reporting: model and reasoning effort are durable even though the live
// settings service is process-local.
func TestThreadSettingsUpdatePersistsModelSettingsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-settings-persist")
	if err := store.Save(&session.Record{ID: threadID, Metadata: session.Metadata{CWD: t.TempDir()}}); err != nil {
		t.Fatalf("Save thread error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService()})
	router.requireThreadStatus().UpsertThread(string(threadID), false)
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID: string(threadID),
		Model:    stringPtrModelSettings("persisted-update-model"),
		Effort:   stringPtrModelSettings("xhigh"),
	}))
	if response.Error != nil {
		t.Fatalf("thread/settings/update error = %+v", response.Error)
	}
	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	if record.Metadata.Model != "persisted-update-model" {
		t.Fatalf("persisted model = %q", record.Metadata.Model)
	}
	overrides := threadRecordConfigOverrides(record)
	if overrides["model_reasoning_effort"] != "xhigh" {
		t.Fatalf("persisted effort = %#v", overrides)
	}

	// A fresh router has no live settings; the persisted snapshot must be
	// reported for the unloaded thread.
	restarted := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService()})
	read := restarted.Handle(requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{ThreadID: string(threadID)}))
	if read.Error != nil {
		t.Fatalf("restarted thread/read error = %+v", read.Error)
	}
	thread := read.Result.(*ThreadReadResponse).Thread
	if thread.Model == nil || *thread.Model != "persisted-update-model" ||
		thread.ReasoningEffort == nil || *thread.ReasoningEffort != "xhigh" {
		t.Fatalf("persisted thread settings = model %#v, effort %#v", thread.Model, thread.ReasoningEffort)
	}
}
