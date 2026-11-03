package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/config"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #57267 (codex-rs/tui/src/app/recap.rs:190-195 and
// app/thread_title.rs:71-77): a server-owned conversation never starts the
// local recap or title-generation turn.
func TestCloudSkipsLocalRecapGenerationLikeRust(t *testing.T) {
	calls := 0
	local := func(string, codextea.RecapThreadOptions, string, map[string]any) (string, error) {
		calls++
		return "local recap", nil
	}
	_, err := recapGenerateFuncForMode(ThreadParamsModeCloud, local)("thread-1", codextea.RecapThreadOptions{}, "prompt", nil)
	if calls != 0 {
		t.Fatalf("cloud recap invoked the local generator %d times, want 0", calls)
	}
	if err == nil || err.Error() != cloudRecapUnavailableMessage {
		t.Fatalf("cloud recap error = %v, want %q", err, cloudRecapUnavailableMessage)
	}
	for _, mode := range []ThreadParamsMode{ThreadParamsModeEmbedded, ThreadParamsModeRemote} {
		calls = 0
		got, err := recapGenerateFuncForMode(mode, local)("thread-1", codextea.RecapThreadOptions{}, "prompt", nil)
		if err != nil || calls != 1 || got != "local recap" {
			t.Fatalf("%s recap = %q (err %v, calls %d), want the local generator once", mode, got, err, calls)
		}
	}
}

// Rust #57267 (app/model_defaults.rs:124-133): a server-owned conversation
// sends thread/settings/update but never falls back to writing client config.
func TestCloudModelSelectionSkipsLocalConfigWriteLikeRust(t *testing.T) {
	methods, result := runModelSelectionForMode(t, ThreadParamsModeCloud)
	if !containsMethod(methods, string(appserver.MethodThreadSettingsUpdate)) {
		t.Fatalf("cloud methods = %v, want thread/settings/update", methods)
	}
	if containsMethod(methods, string(appserver.MethodConfigBatchWrite)) {
		t.Fatalf("cloud methods = %v, must not persist client config", methods)
	}
	if result.ThreadSettingsErr != nil || result.PersistErr != nil {
		t.Fatalf("cloud selection result = %#v", result)
	}
	if result.PersistLabel != "" || result.PersistOverridden {
		t.Fatalf("cloud selection persisted %q (overridden=%v), want no local persistence", result.PersistLabel, result.PersistOverridden)
	}
}

// Client-owned sessions keep writing the user-level defaults.
func TestClientOwnedModelSelectionPersistsLikeRust(t *testing.T) {
	methods, result := runModelSelectionForMode(t, ThreadParamsModeRemote)
	if !containsMethod(methods, string(appserver.MethodThreadSettingsUpdate)) {
		t.Fatalf("remote methods = %v, want thread/settings/update", methods)
	}
	if !containsMethod(methods, string(appserver.MethodConfigBatchWrite)) {
		t.Fatalf("remote methods = %v, want the client config write", methods)
	}
	if result.PersistErr != nil || result.PersistLabel == "" {
		t.Fatalf("remote selection result = %#v, want persisted edits", result)
	}
}

func runModelSelectionForMode(t *testing.T, mode ThreadParamsMode) ([]string, codextea.ModelSelectionSyncMsg) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	methods := make([]string, 0, 6)
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || ctx.Err() != nil {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			mu.Lock()
			methods = append(methods, req.Method)
			mu.Unlock()
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadSettingsUpdate):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodConfigBatchWrite):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  config.ConfigWriteResponse{Status: config.WriteOKOverridden},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", CWD: `D:\repo`})
	state.ThreadID = "thread-1"
	cmd := interactiveRemoteModelSelectionCommandForMode(mode, ctx, endpoint, state, &codextea.PickerDecision{
		Kind:            "model_reasoning",
		Value:           "gpt-5.1-codex",
		ReasoningEffort: "high",
	})
	if cmd == nil {
		t.Fatal("model selection produced no command")
	}
	msg := cmd()
	result, ok := msg.(codextea.ModelSelectionSyncMsg)
	if !ok {
		t.Fatalf("model selection msg = %T", msg)
	}
	mu.Lock()
	captured := append([]string(nil), methods...)
	mu.Unlock()
	select {
	case serverErr := <-serverErrs:
		t.Fatalf("%s server error: %v", mode, serverErr)
	default:
	}
	return captured, result
}
