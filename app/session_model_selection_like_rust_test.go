package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

func TestInteractiveLocalSessionModelSelectionSkipsConfigWriteLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	cmd := interactiveLocalModelSelectionCommand(&cli.RootOptions{}, &codextea.PickerDecision{
		Kind:            "session_model",
		Value:           "gpt-5.1-codex",
		ReasoningEffort: "high",
	})
	if cmd != nil {
		t.Fatal("session-only model selection produced a config command")
	}
	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("session-only model selection wrote config.toml: %v", err)
	}
}

func TestInteractiveRemoteSessionModelSelectionSyncsThreadOnlyLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 4)
	settingsParams := make(chan appserver.SettingsUpdateParams, 1)
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
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadSettingsUpdate):
				var params appserver.SettingsUpdateParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				settingsParams <- params
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": appserver.SettingsUpdateResponse{}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", CWD: `D:\repo`})
	state.ThreadID = "thread-session"
	cmd := interactiveRemoteModelSelectionCommand(ctx, endpoint, state, &codextea.PickerDecision{
		Kind:            "session_model",
		Value:           "gpt-5.1-codex",
		ReasoningEffort: "high",
	})
	if cmd == nil {
		t.Fatal("session-only model selection produced no remote command")
	}
	msg := cmd()
	sync, ok := msg.(codextea.ModelSelectionSyncMsg)
	if !ok {
		t.Fatalf("session-only sync msg = %T", msg)
	}
	if sync.ThreadSettingsErr != nil || sync.PersistErr != nil || sync.PersistLabel != "" {
		t.Fatalf("session-only sync = %+v", sync)
	}
	select {
	case params := <-settingsParams:
		if params.Model == nil || *params.Model != "gpt-5.1-codex" {
			t.Fatalf("settings model = %#v", params.Model)
		}
		if params.Effort == nil || *params.Effort != "high" {
			t.Fatalf("settings effort = %#v", params.Effort)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("thread/settings/update was not sent")
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodThreadSettingsUpdate) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case request := <-requests:
		t.Fatalf("session-only selection wrote config: %q", request.Method)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
