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
	"codex_go/review"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

type interactiveReviewRouterStub struct {
	sink    appserver.NotificationSink
	release <-chan struct{}
}

func (r *interactiveReviewRouterStub) SetNotificationSink(sink appserver.NotificationSink) {
	r.sink = sink
}

func (r *interactiveReviewRouterStub) Close() error {
	return nil
}

func (r *interactiveReviewRouterStub) Handle(request *appserver.Request) *appserver.Response {
	switch request.Method {
	case appserver.MethodReviewStart:
		r.notify(appserver.NotificationItemStarted, &appserver.ItemStartedNotification{
			ThreadID: "thread-review", TurnID: "review-turn",
			Item: appserver.ThreadItemPayload{"id": "review-turn", "type": "enteredReviewMode", "review": "custom review"},
		})
		go func() {
			<-r.release
			exit := appserver.ThreadItemPayload{"id": "review-turn", "type": "exitedReviewMode", "review": "Found one issue"}
			r.notify(appserver.NotificationItemStarted, &appserver.ItemStartedNotification{ThreadID: "thread-review", TurnID: "review-turn", Item: exit})
			r.notify(appserver.NotificationItemCompleted, &appserver.ItemCompletedNotification{ThreadID: "thread-review", TurnID: "review-turn", Item: exit})
			r.notify(appserver.NotificationTurnCompleted, &appserver.TurnCompletedNotification{
				ThreadID: "thread-review", Turn: appserver.Turn{ID: "review-turn", Status: appserver.TurnStatusCompleted},
			})
		}()
		return appserver.OK(request.ID, &review.StartResponse{
			Turn: review.Turn{ID: "review-turn", Status: review.TurnStatusInProgress}, ReviewThreadID: "thread-review",
		})
	case appserver.MethodTurnInterrupt:
		return appserver.OK(request.ID, map[string]any{})
	default:
		return appserver.ErrorResponse(request.ID, -32601, "unexpected method", nil)
	}
}

func (r *interactiveReviewRouterStub) notify(method appserver.NotificationMethod, params any) {
	if r.sink != nil {
		r.sink.Notify(appserver.NewNotification(method, params))
	}
}

func TestInteractiveLocalReviewCommandStreamsLifecycle(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	release := make(chan struct{})
	router := &interactiveReviewRouterStub{release: release}
	command := interactiveLocalReviewStartCommand(context.Background(), state, nil, func() interactiveReviewRouter { return router })
	delivery := "inline"
	message := command(review.StartParams{
		ThreadID: "thread-review", Target: review.APITarget{Type: "custom", Instructions: "custom review"}, Delivery: &delivery,
	})()
	started, ok := message.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("command message = %T, want StreamStartedMsg", message)
	}

	var sawResult, sawEntered, sawExited, sawCompleted bool
	for msg := range started.Messages {
		switch value := msg.(type) {
		case codextea.ReviewStartResultMsg:
			sawResult = value.Err == nil && value.Response.Turn.ID == "review-turn"
			close(release)
		case codextea.ThreadEventMsg:
			if value.Event.Item != nil && value.Event.Type == "item.started" && value.Event.Item.Type == "enteredReviewMode" && value.Event.Item.Text == "custom review" {
				sawEntered = true
			}
			if value.Event.Item != nil && value.Event.Type == "item.completed" && value.Event.Item.Type == "exitedReviewMode" {
				sawExited = true
			}
		case codextea.TurnCompletedMsg:
			sawCompleted = value.Err == nil && value.ThreadID == "thread-review"
		}
	}
	if !sawResult || !sawEntered || !sawExited || !sawCompleted {
		t.Fatalf("review stream result=%v entered=%v exited=%v completed=%v", sawResult, sawEntered, sawExited, sawCompleted)
	}
}

func TestInteractiveRemoteReviewCommandKeepsConnectionForLifecycle(t *testing.T) {
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
			case string(appserver.MethodReviewStart):
				var params review.StartParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-review" || params.Target.Instructions != "custom review" {
					remoteTUITestSendErr(serverErrs, errors.New("unexpected review/start params"))
					return
				}
				remoteTUITestWrite(ctx, conn, reviewItemNotification(appserver.NotificationItemStarted, "enteredReviewMode", "custom review"))
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": review.StartResponse{Turn: review.Turn{ID: "review-turn", Status: review.TurnStatusInProgress}, ReviewThreadID: "thread-review"},
				})
				remoteTUITestWrite(ctx, conn, reviewItemNotification(appserver.NotificationItemCompleted, "exitedReviewMode", "Found one issue"))
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "method": string(appserver.NotificationTurnCompleted),
					"params": map[string]any{"threadId": "thread-review", "turn": map[string]any{"id": "review-turn", "items": []any{}, "status": "completed"}},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, errors.New("unexpected method: "+req.Method))
				return
			}
		}
	}))
	defer server.Close()

	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	command := interactiveRemoteReviewStartCommand(ctx, nil, endpoint, state, remoteTUIBrokers{}, nil)
	delivery := "inline"
	message := command(review.StartParams{
		ThreadID: "thread-review", Target: review.APITarget{Type: "custom", Instructions: "custom review"}, Delivery: &delivery,
	})()
	started, ok := message.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("command message = %T, want StreamStartedMsg", message)
	}

	var sawResult, sawEntered, sawExited, sawCompleted bool
	for msg := range started.Messages {
		switch value := msg.(type) {
		case codextea.ReviewStartResultMsg:
			sawResult = value.Err == nil && value.Response.Turn.ID == "review-turn"
		case codextea.ThreadEventMsg:
			if value.Event.Item != nil && value.Event.Item.Type == "enteredReviewMode" && value.Event.Item.Text == "custom review" {
				sawEntered = true
			}
			if value.Event.Item != nil && value.Event.Item.Type == "exitedReviewMode" && value.Event.Item.Text == "Found one issue" {
				sawExited = true
			}
		case codextea.TurnCompletedMsg:
			sawCompleted = value.Err == nil && value.ThreadID == "thread-review"
		}
	}
	if !sawResult || !sawEntered || !sawExited || !sawCompleted {
		t.Fatalf("review stream result=%v entered=%v exited=%v completed=%v", sawResult, sawEntered, sawExited, sawCompleted)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func reviewItemNotification(method appserver.NotificationMethod, itemType string, text string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "method": string(method),
		"params": map[string]any{
			"threadId": "thread-review", "turnId": "review-turn",
			"item": map[string]any{"id": "review-turn", "type": itemType, "review": text},
		},
	}
}
