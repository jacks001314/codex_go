package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

type interactiveCompactRouterStub struct {
	sink    appserver.NotificationSink
	methods []appserver.Method
	loaded  bool
}

func (r *interactiveCompactRouterStub) SetNotificationSink(sink appserver.NotificationSink) {
	r.sink = sink
}

func (r *interactiveCompactRouterStub) Close() error {
	return nil
}

func (r *interactiveCompactRouterStub) Handle(request *appserver.Request) *appserver.Response {
	r.methods = append(r.methods, request.Method)
	switch request.Method {
	case appserver.MethodInitialize:
		return appserver.OK(request.ID, &appserver.InitializeResponse{})
	case appserver.MethodThreadResume:
		r.loaded = true
		return appserver.OK(request.ID, &appserver.ThreadResumeResponse{})
	case appserver.MethodThreadCompactStart:
		if !r.loaded {
			return appserver.ErrorResponse(request.ID, -32600, "thread not loaded", nil)
		}
		item := appserver.ThreadItemPayload{"id": "compact-item", "type": "contextCompaction"}
		r.notify(appserver.NotificationItemStarted, &appserver.ItemStartedNotification{ThreadID: "thread-compact", TurnID: "compact-turn", Item: item})
		r.notify(appserver.NotificationItemCompleted, &appserver.ItemCompletedNotification{ThreadID: "thread-compact", TurnID: "compact-turn", Item: item})
		return appserver.OK(request.ID, &appserver.ThreadCompactStartResponse{})
	default:
		return appserver.ErrorResponse(request.ID, -32601, "unexpected method", nil)
	}
}

func (r *interactiveCompactRouterStub) notify(method appserver.NotificationMethod, params any) {
	if r.sink != nil {
		r.sink.Notify(appserver.NewNotification(method, params))
	}
}

func TestInteractiveLocalCompactCommandResumesAndStreamsActivity(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-compact")
	router := &interactiveCompactRouterStub{}
	command := interactiveLocalCompactStartCommand(context.Background(), state, func() interactiveCompactRouter { return router })
	started, ok := command("thread-compact")().(codextea.StreamStartedMsg)
	if !ok {
		t.Fatal("local compact command did not start a message stream")
	}

	var sawStarted, sawCompleted, sawResult bool
	for message := range started.Messages {
		switch value := message.(type) {
		case codextea.ThreadEventMsg:
			if value.Event.Item != nil && value.Event.Item.Type == "contextCompaction" {
				sawStarted = sawStarted || value.Event.Type == "item.started"
				sawCompleted = sawCompleted || value.Event.Type == "item.completed"
			}
		case codextea.CompactStartResultMsg:
			sawResult = value.Err == nil
		}
	}
	if !sawStarted || !sawCompleted || !sawResult {
		t.Fatalf("compact stream started=%v completed=%v result=%v", sawStarted, sawCompleted, sawResult)
	}
	if len(router.methods) != 3 || router.methods[0] != appserver.MethodInitialize || router.methods[1] != appserver.MethodThreadResume || router.methods[2] != appserver.MethodThreadCompactStart {
		t.Fatalf("compact methods = %#v", router.methods)
	}
}

func TestInteractiveRemoteCompactCommandStreamsActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadCompactStart):
				var params appserver.ThreadCompactStartParams
				if err := json.Unmarshal(req.Params, &params); err != nil || params.ThreadID != "thread-compact" {
					remoteTUITestSendErr(serverErrs, errors.New("unexpected thread/compact/start params"))
					return
				}
				item := map[string]any{"id": "compact-item", "type": "contextCompaction"}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "method": string(appserver.NotificationItemStarted),
					"params": map[string]any{"threadId": "thread-compact", "turnId": "compact-turn", "item": item},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "method": string(appserver.NotificationItemCompleted),
					"params": map[string]any{"threadId": "thread-compact", "turnId": "compact-turn", "item": item},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				return
			default:
				remoteTUITestSendErr(serverErrs, errors.New("unexpected method: "+req.Method))
				return
			}
		}
	}))
	defer server.Close()

	state := codextui.NewState(nil)
	state.SetThreadID("thread-compact")
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	command := interactiveRemoteCompactStartCommand(ctx, nil, endpoint, state, remoteTUIBrokers{})
	started, ok := command("thread-compact")().(codextea.StreamStartedMsg)
	if !ok {
		t.Fatal("remote compact command did not start a message stream")
	}
	var sawStarted, sawCompleted, sawResult bool
	for message := range started.Messages {
		switch value := message.(type) {
		case codextea.ThreadEventMsg:
			if value.Event.Item != nil && value.Event.Item.Type == "contextCompaction" {
				sawStarted = sawStarted || value.Event.Type == "item.started"
				sawCompleted = sawCompleted || value.Event.Type == "item.completed"
			}
		case codextea.CompactStartResultMsg:
			sawResult = value.Err == nil
		}
	}
	if !sawStarted || !sawCompleted || !sawResult {
		t.Fatalf("compact stream started=%v completed=%v result=%v", sawStarted, sawCompleted, sawResult)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
