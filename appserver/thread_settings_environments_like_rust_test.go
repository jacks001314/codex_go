package appserver

import (
	"encoding/json"
	"testing"
	"time"

	execserverclient "codex_go/execserver"
	"codex_go/session"
)

// TestThreadSettingsEnvironmentsLikeRust mirrors Rust #60655: thread settings
// updates replace future-turn environments, preserve them on omission/null,
// clear them with [], and select provider defaults when only cwd changes.
func TestThreadSettingsEnvironmentsLikeRust(t *testing.T) {
	now := fixedTime()
	store := session.NewStore(t.TempDir())
	routerStore := NewRouter(store)
	routerStore.SetClock(func() time.Time { return now })
	cwd := t.TempDir()
	threadID := session.ThreadID("thread-settings-envs")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           cwd,
			ModelProvider: "openai",
			Source:        "cli",
			HistoryMode:   string(ThreadHistoryLegacy),
		},
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, cwd)
	if err := manager.ApplyProviderSnapshot(execserverclient.EnvironmentProviderSnapshot{
		Default: execserverclient.EnvironmentDefault{Kind: execserverclient.EnvironmentDefaultID, ID: "remote-a"},
		Environments: []execserverclient.NamedEnvironment{
			{ID: "remote-a", Transport: execserverclient.EnvironmentTransport{Kind: execserverclient.EnvironmentTransportWebSocket, WebSocketURL: "ws://127.0.0.1:1"}},
			{ID: "remote-b", Transport: execserverclient.EnvironmentTransport{Kind: execserverclient.EnvironmentTransportWebSocket, WebSocketURL: "ws://127.0.0.1:2"}},
		},
	}); err != nil {
		t.Fatalf("apply provider snapshot: %v", err)
	}

	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: routerStore, Environment: manager})
	router.SetNotificationSink(sink)

	selectionA := []map[string]any{{
		"environmentId":  "remote-a",
		"cwd":            cwd,
		"workspaceRoots": []string{cwd},
	}}
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{
		ThreadID:     string(threadID),
		Environments: &selectionA,
	}, true); err != nil {
		t.Fatalf("attach environment: %v", err)
	}
	assertThreadSettingsEnvironments(t, lastThreadSettingsNotification(t, sink, string(threadID)), []ThreadEnvironment{{
		EnvironmentID:         "remote-a",
		CWD:                   cwd,
		RuntimeWorkspaceRoots: []string{cwd},
	}})
	if got := persistedThreadEnvironments(t, router, threadID); len(got) != 1 || got[0].EnvironmentID != "remote-a" {
		t.Fatalf("persisted environments = %#v", got)
	}

	model := "gpt-5.1"
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: string(threadID), Model: &model}, true); err != nil {
		t.Fatalf("preserve environment: %v", err)
	}
	assertThreadSettingsEnvironments(t, lastThreadSettingsNotification(t, sink, string(threadID)), []ThreadEnvironment{{
		EnvironmentID:         "remote-a",
		CWD:                   cwd,
		RuntimeWorkspaceRoots: []string{cwd},
	}})

	cleared := []map[string]any{}
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: string(threadID), Environments: &cleared}, true); err != nil {
		t.Fatalf("clear environments: %v", err)
	}
	assertThreadSettingsEnvironments(t, lastThreadSettingsNotification(t, sink, string(threadID)), []ThreadEnvironment{})
	if record, err := router.threadRecord(threadID, true, false); err != nil || record == nil {
		t.Fatalf("read cleared record = %#v, %v", record, err)
	} else if _, ok := record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey]; ok {
		t.Fatalf("cleared record still carries environments: %#v", record.Metadata.Extra)
	}

	newCWD := t.TempDir()
	if _, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{ThreadID: string(threadID), CWD: &newCWD}, true); err != nil {
		t.Fatalf("cwd-only update: %v", err)
	}
	assertThreadSettingsEnvironments(t, lastThreadSettingsNotification(t, sink, string(threadID)), []ThreadEnvironment{
		{EnvironmentID: "remote-a", CWD: newCWD, RuntimeWorkspaceRoots: []string{newCWD}},
		{EnvironmentID: "remote-b", CWD: newCWD, RuntimeWorkspaceRoots: []string{newCWD}},
	})
}

func TestThreadSettingsEnvironmentsDecodeLikeRust(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		set  bool
		len  int
	}{
		{name: "omitted", body: `{"threadId":"t"}`, set: false},
		{name: "null", body: `{"threadId":"t","environments":null}`, set: false},
		{name: "empty", body: `{"threadId":"t","environments":[]}`, set: true, len: 0},
		{name: "selected", body: `{"threadId":"t","environments":[{"environmentId":"remote-a"}]}`, set: true, len: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var params SettingsUpdateParams
			if err := json.Unmarshal([]byte(test.body), &params); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if (params.Environments != nil) != test.set {
				t.Fatalf("environments set = %v, want %v", params.Environments != nil, test.set)
			}
			if params.Environments != nil && len(*params.Environments) != test.len {
				t.Fatalf("environments = %#v, want len %d", *params.Environments, test.len)
			}
		})
	}
}

func assertThreadSettingsEnvironments(t *testing.T, notification *SettingsUpdatedNotification, want []ThreadEnvironment) {
	t.Helper()
	if notification == nil || notification.ThreadSettings.Environments == nil {
		t.Fatalf("thread settings environments missing: %#v", notification)
	}
	got := *notification.ThreadSettings.Environments
	if len(got) != len(want) {
		t.Fatalf("thread settings environments = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i].EnvironmentID != want[i].EnvironmentID || got[i].CWD != want[i].CWD || len(got[i].RuntimeWorkspaceRoots) != len(want[i].RuntimeWorkspaceRoots) {
			t.Fatalf("thread settings environments = %#v, want %#v", got, want)
		}
		for j := range want[i].RuntimeWorkspaceRoots {
			if got[i].RuntimeWorkspaceRoots[j] != want[i].RuntimeWorkspaceRoots[j] {
				t.Fatalf("thread settings environments = %#v, want %#v", got, want)
			}
		}
	}
}

func persistedThreadEnvironments(t *testing.T, router *RuntimeRouter, threadID session.ThreadID) []ThreadEnvironment {
	t.Helper()
	record, err := router.threadRecord(threadID, true, false)
	if err != nil || record == nil {
		t.Fatalf("read record = %#v, %v", record, err)
	}
	return threadEnvironmentsFromSelections(environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey]))
}
