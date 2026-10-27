package appserver

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #55692: the tool-registration metric is tagged with the
// effective incremental-tool mode of the sampling request, so a
// non-Responses-Lite model stays false even when the feature is enabled.
func TestAppTurnConfigReportsEffectiveIncrementalToolsLikeRust(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UTC()
	for _, threadID := range []string{"thread-incremental-metric", "thread-legacy-metric"} {
		record := &session.Record{
			ID:        session.ThreadID(threadID),
			SessionID: threadID,
			CreatedAt: now,
			UpdatedAt: now,
			RecencyAt: now,
			Metadata:  session.Metadata{CWD: t.TempDir()},
		}
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", threadID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	cases := []struct {
		name     string
		threadID string
		model    string
		features map[string]any
		want     bool
	}{
		{
			name:     "responses lite uses incremental tools",
			threadID: "thread-incremental-metric",
			model:    "gpt-6.1-sol",
			want:     true,
		},
		{
			name:     "explicit disable keeps legacy declarations",
			threadID: "thread-incremental-metric",
			model:    "gpt-6.1-sol",
			features: map[string]any{"incremental_tools": false},
			want:     false,
		},
		{
			name:     "non-lite model never uses incremental tools",
			threadID: "thread-legacy-metric",
			model:    "gpt-5.2",
			want:     false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			params := &turn.TurnStartParams{
				ThreadID: testCase.threadID,
				Prompt:   "hello",
				Model:    testCase.model,
			}
			if testCase.features != nil {
				params.Config = map[string]any{"features": testCase.features}
			}
			runConfig, err := router.appTurnConfig(context.Background(), testCase.threadID, "turn-metric", "", params, now.UnixMilli(), nil)
			if err != nil {
				t.Fatalf("appTurnConfig() error = %v", err)
			}
			if runConfig == nil {
				t.Fatal("appTurnConfig() returned nil")
			}
			if runConfig.IncrementalTools != testCase.want {
				t.Fatalf("IncrementalTools = %v, want %v", runConfig.IncrementalTools, testCase.want)
			}
		})
	}
}
