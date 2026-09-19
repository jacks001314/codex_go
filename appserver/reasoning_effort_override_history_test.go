package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// TestAppTurnConfigFiltersSavedConfigurationUpdatesWhenDisabledLikeRust mirrors
// Rust #46291: a resumed thread whose history still carries a harness-authored
// configuration_update must not send it once the reasoning_effort_override
// feature is disabled, while the persisted history keeps it and other input
// items survive.
func TestAppTurnConfigFiltersSavedConfigurationUpdatesWhenDisabledLikeRust(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UTC()
	record := &session.Record{
		ID:        "thread-cfg-history",
		SessionID: "thread-cfg-history",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: t.TempDir(), Model: "gpt-5"},
		Items: []session.Item{
			{ID: "msg-user", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
			{
				ID:        "cfg-1",
				Type:      "configuration_update",
				CreatedAt: now,
				Data: map[string]any{
					"reasoning":        map[string]any{"effort": "high"},
					"harness_metadata": json.RawMessage(`{"harness_authored_configuration":true}`),
				},
				Metadata: map[string]any{"turnId": "turn-1"},
			},
			{ID: "msg-agent", Type: "message", Role: "assistant", Text: "done", CreatedAt: now},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create(record) error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	for _, tc := range []struct {
		name           string
		featureEnabled bool
		wantUpdate     bool
	}{
		{name: "disabled drops the saved update", featureEnabled: false, wantUpdate: false},
		{name: "enabled keeps the saved update", featureEnabled: true, wantUpdate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := &turn.TurnStartParams{
				ThreadID: "thread-cfg-history",
				Prompt:   "next",
				Config: map[string]any{
					"features": map[string]any{"reasoning_effort_override": tc.featureEnabled},
				},
			}
			runConfig, err := router.appTurnConfig(context.Background(), "thread-cfg-history", "turn-2", "", params, now.UnixMilli(), nil)
			if err != nil {
				t.Fatalf("appTurnConfig() error = %v", err)
			}
			if runConfig == nil {
				t.Fatal("appTurnConfig() returned nil")
			}
			updates := 0
			userMessages := 0
			for _, item := range runConfig.InputItems {
				if isConfigurationUpdateInputItem(item) {
					updates++
				}
				if payload, ok := item.(map[string]any); ok && payload["role"] == "user" {
					userMessages++
				}
			}
			if got := updates > 0; got != tc.wantUpdate {
				t.Fatalf("configuration_update present = %v, want %v (items=%#v)", got, tc.wantUpdate, runConfig.InputItems)
			}
			if userMessages == 0 {
				t.Fatalf("saved user message dropped from the request input: %#v", runConfig.InputItems)
			}
		})
	}

	// The persisted history keeps the saved update either way.
	reloaded, err := store.Read("thread-cfg-history", true, true)
	if err != nil || reloaded == nil {
		t.Fatalf("Read(record) = %#v, %v", reloaded, err)
	}
	found := false
	for _, item := range reloaded.Items {
		if item.Type == "configuration_update" {
			found = true
		}
	}
	if !found {
		t.Fatal("persisted history lost the configuration_update item")
	}
}
