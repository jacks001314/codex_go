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

// Rust #57267 (codex-rs/tui/src/app/model_defaults.rs:124-133): when a
// server-owned conversation's app server does not accept
// thread/settings/update, the TUI shows the cloud-specific notice instead of a
// generic transport failure. A client-owned conversation keeps the generic
// failure and never shows the cloud notice.
func TestCloudModelSelectionUnsupportedNoticeLikeRust(t *testing.T) {
	cloudMethods, cloud := runModelSelectionAgainstRejectedSettings(t, ThreadParamsModeCloud)
	if !containsMethod(cloudMethods, string(appserver.MethodThreadSettingsUpdate)) {
		t.Fatalf("cloud methods = %v, want thread/settings/update", cloudMethods)
	}
	if cloud.ThreadSettingsNotice != codextea.CloudModelSelectionUnsupportedMessage {
		t.Fatalf("cloud notice = %q, want %q", cloud.ThreadSettingsNotice, codextea.CloudModelSelectionUnsupportedMessage)
	}
	if cloud.ThreadSettingsErr != nil {
		t.Fatalf("cloud thread settings error = %v, want the notice instead", cloud.ThreadSettingsErr)
	}
	if containsMethod(cloudMethods, string(appserver.MethodConfigBatchWrite)) {
		t.Fatalf("cloud methods = %v, must not persist client config", cloudMethods)
	}

	remoteMethods, remote := runModelSelectionAgainstRejectedSettings(t, ThreadParamsModeRemote)
	if !containsMethod(remoteMethods, string(appserver.MethodThreadSettingsUpdate)) {
		t.Fatalf("remote methods = %v, want thread/settings/update", remoteMethods)
	}
	if remote.ThreadSettingsNotice != "" {
		t.Fatalf("client-owned notice = %q, want none", remote.ThreadSettingsNotice)
	}
	if remote.ThreadSettingsErr == nil || !strings.Contains(remote.ThreadSettingsErr.Error(), "method not found") {
		t.Fatalf("client-owned thread settings error = %v, want the raw -32601 failure", remote.ThreadSettingsErr)
	}
}

// The notice decision sits behind an injectable seam so the Server/Client split
// is asserted without depending on the wire: the seam runs once, Cloud replaces
// the failure with the cloud notice, and Remote keeps the failure.
func TestModelSelectionNoticeSeamLikeRust(t *testing.T) {
	previous := remoteThreadSettingsUpdate
	t.Cleanup(func() { remoteThreadSettingsUpdate = previous })

	calls := 0
	remoteThreadSettingsUpdate = func(context.Context, *remoteAppServerTUIClient, appserver.SettingsUpdateParams) error {
		calls++
		return &remoteRPCError{Code: appserver.JSONRPCMethodNotFoundErrorCode, Message: "method not found"}
	}

	// The seam fails the update, but the endpoint still has to be reachable for
	// the client to open before the seam runs.
	_, cloud := runModelSelection(t, ThreadParamsModeCloud, false)
	if calls != 1 {
		t.Fatalf("settings update seam ran %d times, want 1", calls)
	}
	if cloud.ThreadSettingsNotice != codextea.CloudModelSelectionUnsupportedMessage || cloud.ThreadSettingsErr != nil {
		t.Fatalf("cloud seam result = %#v, want the cloud notice", cloud)
	}

	calls = 0
	_, remote := runModelSelection(t, ThreadParamsModeRemote, false)
	if calls != 1 {
		t.Fatalf("settings update seam ran %d times, want 1", calls)
	}
	if remote.ThreadSettingsNotice != "" || remote.ThreadSettingsErr == nil {
		t.Fatalf("remote seam result = %#v, want the generic failure", remote)
	}
}

// runModelSelectionAgainstRejectedSettings runs a completed picker decision
// against a websocket fake server that answers thread/settings/update with
// -32601, like a cloud app server without the method.
func runModelSelectionAgainstRejectedSettings(t *testing.T, mode ThreadParamsMode) ([]string, codextea.ModelSelectionSyncMsg) {
	t.Helper()
	return runModelSelection(t, mode, true)
}

func runModelSelection(t *testing.T, mode ThreadParamsMode, rejectSettings bool) ([]string, codextea.ModelSelectionSyncMsg) {
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
				if rejectSettings {
					remoteTUITestWrite(ctx, conn, map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error":   map[string]any{"code": appserver.JSONRPCMethodNotFoundErrorCode, "message": "method not found"},
					})
					continue
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodConfigBatchWrite):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  config.ConfigWriteResponse{Status: config.WriteOK},
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
