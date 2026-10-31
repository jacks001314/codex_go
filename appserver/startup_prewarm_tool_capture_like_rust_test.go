package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// TestStartupPrewarmToolProviderCapturesStepToolsLikeRust mirrors Rust #47635:
// the startup prewarm captures the thread's model-visible tool inventory so the
// generate=false warmup carries the same tools as the first turn's request.
func TestStartupPrewarmToolProviderCapturesStepToolsLikeRust(t *testing.T) {
	dir := t.TempDir()
	store := session.NewStore(filepath.Join(dir, "sessions"))
	now := time.Now().UTC()
	const threadID = "thread-prewarm-tool-capture"
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: dir, Model: "gpt-6.1-sol"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Turns: turn.NewTurnService()})
	provider := router.startupPrewarmToolProvider(&turn.TurnStartParams{ThreadID: threadID, CWD: dir, Model: "gpt-6.1-sol"})
	if provider == nil {
		t.Fatal("startupPrewarmToolProvider() = nil")
	}
	tools := provider(context.Background())
	if len(tools) == 0 {
		t.Fatal("captured tools = empty, want the thread's tool inventory")
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"exec"`) {
		t.Fatalf("captured tools = %s, want the code-mode exec namespace", encoded)
	}
}
