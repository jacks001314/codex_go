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
	"codex_go/config"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestInteractiveLocalModelSelectionPersistsConfig covers the embedded TUI's
// persistence half: the picked model and effort are written to the user config
// and an empty effort clears the saved default.
func TestInteractiveLocalModelSelectionPersistsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	cmd := interactiveLocalModelSelectionCommand(&cli.RootOptions{}, &codextea.PickerDecision{
		Kind:            "model_reasoning",
		Value:           "gpt-5.1-codex",
		ReasoningEffort: "high",
	})
	if cmd == nil {
		t.Fatal("model selection produced no command")
	}
	if sync, ok := cmd().(codextea.ModelSelectionSyncMsg); !ok || sync.PersistErr != nil {
		t.Fatalf("model selection sync = %#v", cmd())
	}
	readConfig := func() string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		return string(data)
	}
	text := readConfig()
	if !strings.Contains(text, `model = "gpt-5.1-codex"`) || !strings.Contains(text, `model_reasoning_effort = "high"`) {
		t.Fatalf("config = %q", text)
	}

	cmd = interactiveLocalModelSelectionCommand(&cli.RootOptions{}, &codextea.PickerDecision{
		Kind:  "model",
		Value: "gpt-5",
	})
	if cmd == nil {
		t.Fatal("model-only selection produced no command")
	}
	if sync, ok := cmd().(codextea.ModelSelectionSyncMsg); !ok || sync.PersistErr != nil {
		t.Fatalf("model-only sync = %#v", cmd())
	}
	text = readConfig()
	if !strings.Contains(text, `model = "gpt-5"`) || strings.Contains(text, "model_reasoning_effort") {
		t.Fatalf("cleared config = %q", text)
	}
}

// TestInteractiveRemoteModelSelectionSyncsThreadAndConfig covers the Rust app
// fan-out for a picked model/effort: thread/settings/update carries the model,
// effort, and effective collaboration mode, and config/batchWrite persists the
// user-level model defaults (reporting an overridden write).
func TestInteractiveRemoteModelSelectionSyncsThreadAndConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 4)
	settingsParams := make(chan appserver.SettingsUpdateParams, 1)
	writeParams := make(chan config.ConfigBatchWriteParams, 1)
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
			case string(appserver.MethodConfigBatchWrite):
				var params config.ConfigBatchWriteParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				writeParams <- params
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": config.ConfigWriteResponse{Status: config.WriteOKOverridden},
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
	cmd := interactiveRemoteModelSelectionCommand(ctx, endpoint, state, &codextea.PickerDecision{
		Kind:            "model_reasoning",
		Value:           "gpt-5.1-codex",
		ReasoningEffort: "high",
	})
	if cmd == nil {
		t.Fatal("model selection produced no command")
	}
	msg := cmd()
	sync, ok := msg.(codextea.ModelSelectionSyncMsg)
	if !ok {
		t.Fatalf("model selection msg = %T", msg)
	}
	if sync.ThreadSettingsErr != nil || sync.PersistErr != nil {
		t.Fatalf("model selection errors = %+v", sync)
	}
	if !sync.PersistOverridden {
		t.Fatalf("overridden write was not reported: %+v", sync)
	}

	select {
	case params := <-settingsParams:
		if params.ThreadID != "thread-1" || params.Model == nil || *params.Model != "gpt-5.1-codex" {
			t.Fatalf("settings params = %#v", params)
		}
		if params.Effort == nil || *params.Effort != "high" {
			t.Fatalf("settings effort = %#v", params.Effort)
		}
		if params.CollaborationMode == nil || params.CollaborationMode["mode"] != "default" {
			t.Fatalf("collaboration mode = %#v", params.CollaborationMode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("thread/settings/update was not sent")
	}

	select {
	case params := <-writeParams:
		if !params.ReloadUserConfig || len(params.Edits) != 2 {
			t.Fatalf("write params = %#v", params)
		}
		values := map[string]any{}
		for _, edit := range params.Edits {
			values[edit.KeyPath] = edit.Value
			if edit.MergeStrategy != config.MergeReplace {
				t.Fatalf("merge strategy = %q", edit.MergeStrategy)
			}
		}
		if values["model"] != "gpt-5.1-codex" || values["model_reasoning_effort"] != "high" {
			t.Fatalf("write edits = %#v", values)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("config/batchWrite was not sent")
	}

	for _, method := range []string{
		string(appserver.MethodInitialize),
		string(appserver.MethodThreadSettingsUpdate),
		string(appserver.MethodInitialize),
		string(appserver.MethodConfigBatchWrite),
	} {
		if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != method {
			t.Fatalf("request = %q, want %q", got.Method, method)
		}
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestInteractiveRemoteModelSelectionSkipsPersistenceForReserve covers the
// temporary Reserve exception: the effort reaches the thread but no model
// default is written and no thread model is sent.
func TestInteractiveRemoteModelSelectionSkipsPersistenceForReserve(t *testing.T) {
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
	state := codextui.NewState(&codextui.Options{Model: codextea.LunaReserveModel, CWD: `D:\repo`})
	state.ThreadID = "thread-reserve"
	cmd := interactiveRemoteModelSelectionCommand(ctx, endpoint, state, &codextea.PickerDecision{
		Kind:            "model_reasoning",
		Value:           codextea.LunaReserveModel,
		ReasoningEffort: "medium",
	})
	if cmd == nil {
		t.Fatal("reserve selection produced no command")
	}
	msg := cmd()
	sync, ok := msg.(codextea.ModelSelectionSyncMsg)
	if !ok {
		t.Fatalf("reserve msg = %T", msg)
	}
	if sync.ThreadSettingsErr != nil || sync.PersistErr != nil || sync.PersistLabel != "" {
		t.Fatalf("reserve sync = %+v, want a settings-only update", sync)
	}
	select {
	case params := <-settingsParams:
		if params.Model != nil {
			t.Fatalf("reserve sent a model: %#v", *params.Model)
		}
		if params.Effort == nil || *params.Effort != "medium" {
			t.Fatalf("reserve effort = %#v", params.Effort)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reserve settings update was not sent")
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodThreadSettingsUpdate) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case request := <-requests:
		t.Fatalf("reserve persisted config: %q", request.Method)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
