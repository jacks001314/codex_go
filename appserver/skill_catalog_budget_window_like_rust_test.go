package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #44202 budgets the skill catalogs from the captured step model's
// resolved_context_window(). That metadata is resolved with the session's
// ModelInfoOverrides, which core/src/session/mod.rs captures when the thread is
// created, so a turn-local `model_context_window` tightens that turn's context
// management without shrinking the catalog budget.
func TestSkillCatalogBudgetUsesSessionModelWindowLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })

	var nextRequestID int64
	startThread := func(t *testing.T, configOverrides map[string]any) string {
		t.Helper()
		nextRequestID++
		response := router.Handle(requestWithParams(t, IntID(nextRequestID), MethodThreadStart, ThreadStartParams{
			CWD:    t.TempDir(),
			Config: configOverrides,
		}))
		if response.Error != nil {
			t.Fatalf("thread start error: %+v", response.Error)
		}
		return response.Result.(*ThreadStartResponse).Thread.ID
	}

	catalogWindow := int64(0)
	if info := router.modelInfoForRuntimeWithConfig(defaultModelForAppTurn(), &config.Config{Values: map[string]any{}}); info != nil {
		catalogWindow, _ = info.ResolvedContextWindow()
	}
	if catalogWindow <= 1 {
		t.Fatalf("default model catalog window = %d, want a usable window", catalogWindow)
	}

	threadID := startThread(t, nil)
	turnParams := &turn.TurnStartParams{
		ThreadID: threadID,
		Config:   map[string]any{"model_context_window": 1},
	}
	turnConfig, err := router.effectiveConfigForTurn(turnParams)
	if err != nil || turnConfig == nil {
		t.Fatalf("effectiveConfigForTurn(turn) = %v, %v", turnConfig, err)
	}
	if window := router.skillContextWindowForTurn(turnConfig, turnParams); window != catalogWindow {
		t.Fatalf("turn-level model_context_window skill window = %d, want the session window %d", window, catalogWindow)
	}

	sessionThreadID := startThread(t, map[string]any{"model_context_window": 1})
	sessionParams := &turn.TurnStartParams{ThreadID: sessionThreadID}
	sessionConfig, err := router.effectiveConfigForTurn(sessionParams)
	if err != nil || sessionConfig == nil {
		t.Fatalf("effectiveConfigForTurn(session) = %v, %v", sessionConfig, err)
	}
	if window := router.skillContextWindowForTurn(sessionConfig, sessionParams); window != 1 {
		t.Fatalf("thread-level model_context_window skill window = %d, want 1", window)
	}
}
