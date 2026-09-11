package appserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func disabledPluginIDsPtr(values []string) *[]string {
	cloned := append([]string{}, values...)
	return &cloned
}

// TestThreadExtraSettingsDisabledPluginIDsReplacePreserveClearLikeRust covers
// the #44905 selection semantics: a supplied list replaces, omission/null
// preserves, and [] clears.
func TestThreadExtraSettingsDisabledPluginIDsReplacePreserveClearLikeRust(t *testing.T) {
	service := NewThreadExtraService()
	if _, err := service.UpdateSettings(&SettingsUpdateParams{ThreadID: "t", DisabledPluginIDs: disabledPluginIDsPtr([]string{"a@m"})}); err != nil {
		t.Fatalf("UpdateSettings replace: %v", err)
	}
	if got := service.Settings("t").DisabledPluginIDs; !reflect.DeepEqual(got, []string{"a@m"}) {
		t.Fatalf("selection = %#v, want [a@m]", got)
	}

	// An unrelated update preserves the selection.
	if _, err := service.UpdateSettings(&SettingsUpdateParams{ThreadID: "t", Model: stringPtrIfNotEmpty("gpt-5")}); err != nil {
		t.Fatalf("UpdateSettings preserve: %v", err)
	}
	if got := service.Settings("t").DisabledPluginIDs; !reflect.DeepEqual(got, []string{"a@m"}) {
		t.Fatalf("preserved selection = %#v, want [a@m]", got)
	}

	// A supplied list replaces.
	if _, err := service.UpdateSettings(&SettingsUpdateParams{ThreadID: "t", DisabledPluginIDs: disabledPluginIDsPtr([]string{"b@m"})}); err != nil {
		t.Fatalf("UpdateSettings replace 2: %v", err)
	}
	if got := service.Settings("t").DisabledPluginIDs; !reflect.DeepEqual(got, []string{"b@m"}) {
		t.Fatalf("replaced selection = %#v, want [b@m]", got)
	}

	// An explicit empty list clears it.
	if _, err := service.UpdateSettings(&SettingsUpdateParams{ThreadID: "t", DisabledPluginIDs: disabledPluginIDsPtr([]string{})}); err != nil {
		t.Fatalf("UpdateSettings clear: %v", err)
	}
	if got := service.Settings("t").DisabledPluginIDs; len(got) != 0 {
		t.Fatalf("cleared selection = %#v, want empty", got)
	}

	// JSON: null preserves, [] clears.
	var nullParams SettingsUpdateParams
	if err := json.Unmarshal([]byte(`{"threadId":"t","disabledPluginIds":null}`), &nullParams); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if nullParams.DisabledPluginIDs != nil {
		t.Fatalf("null decoded as %#v, want nil (preserve)", nullParams.DisabledPluginIDs)
	}
	var emptyParams SettingsUpdateParams
	if err := json.Unmarshal([]byte(`{"threadId":"t","disabledPluginIds":[]}`), &emptyParams); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if emptyParams.DisabledPluginIDs == nil || len(*emptyParams.DisabledPluginIDs) != 0 {
		t.Fatalf("[] decoded as %#v, want empty list (clear)", emptyParams.DisabledPluginIDs)
	}

	// Settings always serialize the selection as an array.
	data, err := json.Marshal(&Settings{})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if !strings.Contains(string(data), `"disabledPluginIds":[]`) {
		t.Fatalf("settings json = %s", data)
	}
}

// TestRuntimeRouterDisabledPluginIDsPersistAcrossResumeLikeRust covers the
// #44905 app-server behavior: thread/settings/update replaces the saved
// selection, notifies with it, persists it, and resume restores it.
func TestRuntimeRouterDisabledPluginIDsPersistAcrossResumeLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	// A completed turn gives the thread a rollout so it can be resumed.
	turnStart := router.Handle(requestWithParams(t, IntID(10), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Prompt: "hello"}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	ids := []string{"demo@marketplace"}
	update := router.Handle(requestWithParams(t, IntID(2), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:          threadID,
		DisabledPluginIDs: &ids,
	}))
	if update.Error != nil {
		t.Fatalf("thread/settings/update error: %+v", update.Error)
	}

	notified := false
	for _, notification := range sink.List() {
		if notification.Method != NotificationThreadSettingsUpdated {
			continue
		}
		switch payload := notification.Params.(type) {
		case *SettingsUpdatedNotification:
			if payload != nil && payload.ThreadID == threadID && reflect.DeepEqual(payload.ThreadSettings.DisabledPluginIDs, ids) {
				notified = true
			}
		case SettingsUpdatedNotification:
			if payload.ThreadID == threadID && reflect.DeepEqual(payload.ThreadSettings.DisabledPluginIDs, ids) {
				notified = true
			}
		}
	}
	if !notified {
		t.Fatalf("thread/settings/updated did not carry disabledPluginIds: %+v", sink.List())
	}

	resume := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if resume.Error != nil {
		t.Fatalf("thread/resume error: %+v", resume.Error)
	}
	if got := resume.Result.(*ThreadResumeResponse).DisabledPluginIDs; !reflect.DeepEqual(got, ids) {
		t.Fatalf("resume disabledPluginIds = %#v, want %#v", got, ids)
	}

	// A fork inherits the parent's saved selection.
	fork := router.Handle(requestWithParams(t, IntID(6), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error: %+v", fork.Error)
	}
	if got := fork.Result.(*ThreadForkResponse).DisabledPluginIDs; !reflect.DeepEqual(got, ids) {
		t.Fatalf("fork disabledPluginIds = %#v, want %#v", got, ids)
	}

	empty := []string{}
	if response := router.Handle(requestWithParams(t, IntID(4), MethodThreadSettingsUpdate, SettingsUpdateParams{ThreadID: threadID, DisabledPluginIDs: &empty})); response.Error != nil {
		t.Fatalf("clear settings error: %+v", response.Error)
	}
	resumeCleared := router.Handle(requestWithParams(t, IntID(5), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if resumeCleared.Error != nil {
		t.Fatalf("thread/resume after clear error: %+v", resumeCleared.Error)
	}
	if got := resumeCleared.Result.(*ThreadResumeResponse).DisabledPluginIDs; len(got) != 0 {
		t.Fatalf("cleared resume disabledPluginIds = %#v, want empty", got)
	}
}
