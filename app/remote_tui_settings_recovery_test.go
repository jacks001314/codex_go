package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
)

func TestRemoteTUIThreadSettingsUpdateResumesIdleUnloadedThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const threadID = "01a156cb-d734-7499-a6c0-93f0c09b8016"
	requests := make(chan remoteTUITestRequest, 8)
	resumeParams := make(chan appserver.ThreadResumeParams, 1)
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		settingsUpdateCount := 0
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
				settingsUpdateCount++
				if settingsUpdateCount == 1 {
					remoteTUITestWrite(ctx, conn, map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error": map[string]any{
							"code":    appserver.JSONRPCInvalidRequestErrorCode,
							"message": "thread not found: " + threadID,
						},
					})
					continue
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  appserver.SettingsUpdateResponse{},
				})
			case string(appserver.MethodThreadResume):
				var params appserver.ThreadResumeParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				resumeParams <- params
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": appserver.ThreadResumeResponse{
						Thread: &appserver.Thread{
							ID:  threadID,
							CWD: `D:\repo`,
							Turns: []appserver.Turn{{
								ID:     "turn-1",
								Status: appserver.TurnStatusCompleted,
								Items: []appserver.ThreadItem{{
									ID:   "reasoning-1",
									Type: "reasoning",
									Data: map[string]any{
										"summary": []string{"checking the repository"},
										"content": []string{"private reasoning"},
									},
								}},
							}},
						},
					},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		t.Fatalf("openRemoteSessionClient() error = %v", err)
	}
	defer client.close()

	model := "gpt-5.2-codex"
	if err := remoteTUIUpdateThreadSettings(ctx, client, appserver.SettingsUpdateParams{
		ThreadID: threadID,
		Model:    &model,
	}); err != nil {
		t.Fatalf("remoteTUIUpdateThreadSettings() error = %v", err)
	}

	for _, want := range []appserver.Method{
		appserver.MethodInitialize,
		appserver.MethodThreadSettingsUpdate,
		appserver.MethodThreadResume,
		appserver.MethodThreadSettingsUpdate,
	} {
		if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(want) {
			t.Fatalf("request = %q, want %q", got.Method, want)
		}
	}
	select {
	case params := <-resumeParams:
		if params.ThreadID != threadID {
			t.Fatalf("resume thread id = %q, want %q", params.ThreadID, threadID)
		}
	case <-time.After(time.Second):
		t.Fatal("thread/resume was not sent")
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestRemoteTUIThreadSettingsUpdateDoesNotResumeClosedThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requests := make(chan remoteTUITestRequest, 4)
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
			if req.Method == string(appserver.MethodInitialize) {
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				continue
			}
			remoteTUITestWrite(ctx, conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    appserver.JSONRPCInvalidRequestErrorCode,
					"message": "thread is archived",
				},
			})
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		t.Fatalf("openRemoteSessionClient() error = %v", err)
	}
	defer client.close()

	model := "gpt-5.2-codex"
	err = remoteTUIUpdateThreadSettings(ctx, client, appserver.SettingsUpdateParams{
		ThreadID: "thread-archived",
		Model:    &model,
	})
	if err == nil || !strings.Contains(err.Error(), "thread is archived") {
		t.Fatalf("remoteTUIUpdateThreadSettings() error = %v, want archived error", err)
	}
	for _, want := range []appserver.Method{
		appserver.MethodInitialize,
		appserver.MethodThreadSettingsUpdate,
	} {
		if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(want) {
			t.Fatalf("request = %q, want %q", got.Method, want)
		}
	}
	select {
	case request := <-requests:
		t.Fatalf("unexpected request after archived error: %q", request.Method)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
