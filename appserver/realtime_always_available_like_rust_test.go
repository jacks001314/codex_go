package appserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/realtime"
	"codex_go/session"
)

// Mirrors Rust #42377: `thread/realtime/start` is always available. The retired
// `features.realtime_conversation` setting is accepted but has no effect, and
// forked or cold-resumed threads start realtime sessions like any other thread.
func TestRealtimeStartIsAlwaysAvailableLikeRust(t *testing.T) {
	for _, legacy := range []struct {
		name   string
		config string
	}{
		{name: "flag absent"},
		{name: "flag disabled", config: "[features]\nrealtime_conversation = false\n"},
		{name: "flag enabled", config: "[features]\nrealtime_conversation = true\n"},
	} {
		for _, load := range []struct {
			name   string
			forked bool
		}{
			{name: "cold resume"},
			{name: "fork", forked: true},
		} {
			t.Run(legacy.name+" "+load.name, func(t *testing.T) {
				home := t.TempDir()
				if legacy.config != "" {
					if err := os.WriteFile(config.ConfigPath(home), []byte(legacy.config), 0o600); err != nil {
						t.Fatalf("write config error = %v", err)
					}
				}
				store := session.NewStore(filepath.Join(home, "sessions"))
				threadID := "thread-realtime-" + load.name
				now := time.Now().UTC()
				record := &session.Record{
					ID:        session.ThreadID(threadID),
					SessionID: threadID,
					CreatedAt: now,
					UpdatedAt: now,
					RecencyAt: now,
					Metadata:  session.Metadata{CWD: t.TempDir(), ModelProvider: "openai"},
				}
				if load.forked {
					record.ForkedFromID = "thread-realtime-parent"
				}
				if err := store.Save(record); err != nil {
					t.Fatalf("save thread record error = %v", err)
				}
				router := NewRuntimeRouter(RuntimeServices{
					Config:       config.NewConfigService(home),
					ThreadRouter: NewRouter(store),
					Realtime:     realtime.NewManager(),
				})
				t.Cleanup(func() { _ = router.Close() })
				response := router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeStart, realtime.StartParams{
					ThreadID:       threadID,
					OutputModality: realtime.OutputText,
				}))
				if response.Error != nil {
					t.Fatalf("thread/realtime/start error = %+v", response.Error)
				}
				if _, ok := response.Result.(*realtime.StartResponse); !ok {
					t.Fatalf("thread/realtime/start result = %#v", response.Result)
				}
			})
		}
	}
}
