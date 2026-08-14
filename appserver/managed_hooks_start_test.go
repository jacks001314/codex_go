package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/session"
)

func TestThreadStartRejectsUnloadableRequiredManagedHooks(t *testing.T) {
	cfg := config.NewConfigService(t.TempDir())
	cfg.SetRequirements(&config.ConfigRequirements{Hooks: &config.ManagedHooksRequirements{
		PreToolUse: []config.ConfiguredHookGroup{{Hooks: []config.ConfiguredHookHandler{{Type: "prompt"}}}},
	}})
	router := NewRuntimeRouter(RuntimeServices{
		Config:       cfg,
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		ThreadStatus: NewThreadStatusManager(),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "failed to load required managed hooks") {
		t.Fatalf("thread start response = %+v", response)
	}
}
