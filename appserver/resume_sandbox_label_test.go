package appserver

import (
	"bytes"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
	"log/slog"
	"strings"
	"testing"
)

func TestResumeSandboxOverrideUsesCanonicalProfileLabel(t *testing.T) {
	for _, tc := range []struct {
		network, requested string
		want               bool
	}{
		{"restricted", "danger-full-access", true},
		{"restricted", "workspace-write", false},
		{"enabled", "danger-full-access", true},
		{"enabled", "read-only", false},
	} {
		profile, err := sandbox.ParseRuntimePermissionProfileJSON("{\"type\":\"managed\",\"network\":\"" + tc.network + "\",\"file_system\":{\"type\":\"unrestricted\"}}")
		if err != nil {
			t.Fatal(err)
		}
		if got := resumeSandboxLabelMatches(tc.requested, profile, t.TempDir()); got != tc.want {
			t.Errorf("network=%s requested=%s: match=%v want %v", tc.network, tc.requested, got, tc.want)
		}
	}
}

func TestRunningResumeLogsOnlyMismatchedSandboxLabel(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService(), ThreadStatus: NewThreadStatusManager()})
	t.Cleanup(func() { _ = router.Close() })
	cwd := t.TempDir()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd, Prompt: "seed"}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, id)
	if err := router.registerActiveRuntimeTurn(id, "running", func() {}, fixedTime().UnixMilli(), &turn.TurnStartParams{ThreadID: id, CWD: cwd}); err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"managed","network":"restricted","file_system":{"type":"unrestricted"}}`
	router.threads.UpdateTurn(id, "running", func(active *activeRuntimeTurn) { active.RunConfig = &appTurnRunConfig{PermissionProfileJSON: raw} })
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	for _, requested := range []string{"danger-full-access", "read-only"} {
		logs.Reset()
		response := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: id, Sandbox: requested}))
		if response.Error != nil {
			t.Fatal(response.Error)
		}
		warned := strings.Contains(logs.String(), "sandbox override ignored")
		if warned != (requested == "read-only") {
			t.Fatalf("requested=%s logs=%s", requested, logs.String())
		}
		router.threads.UpdateTurn(id, "running", func(active *activeRuntimeTurn) {
			if active.RunConfig.PermissionProfileJSON != raw {
				t.Error("resume changed active permissions")
			}
		})
	}
}
