package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/cloudbackend"
	"codex_go/cloudclient"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// fakeCloudConversationRequester answers one whole hosted conversation: thread
// start, the pre-turn reattach, turn start, and the completion notifications.
type fakeCloudConversationRequester struct {
	notifications chan cloudbackend.Notification

	mu         sync.Mutex
	methods    []appserver.Method
	turnParams map[string]any
	closed     bool
}

func (f *fakeCloudConversationRequester) Request(_ context.Context, method appserver.Method, params json.RawMessage) (json.RawMessage, *cloudbackend.RPCError) {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
	switch method {
	case appserver.MethodInitialize:
		return json.RawMessage(`{}`), nil
	case appserver.MethodThreadStart:
		return json.RawMessage(`{"thread":{"id":"thread-cloud"},"cwd":"/cloud/workspace"}`), nil
	case appserver.MethodThreadResume:
		return json.RawMessage(`{"thread":{"id":"thread-cloud"}}`), nil
	case appserver.MethodTurnStart:
		var body map[string]any
		if err := json.Unmarshal(params, &body); err != nil {
			return nil, &cloudbackend.RPCError{Code: -32602, Message: err.Error()}
		}
		f.mu.Lock()
		f.turnParams = body
		f.mu.Unlock()
		go func() {
			f.notifications <- cloudbackend.Notification{
				Method: appserver.NotificationItemCompleted,
				Params: map[string]any{
					"threadId": "thread-cloud",
					"item": map[string]any{
						"id":   "item-1",
						"type": "agent_message",
						"text": "Hello from cloud",
					},
				},
			}
			f.notifications <- cloudbackend.Notification{
				Method: appserver.NotificationTurnCompleted,
				Params: map[string]any{
					"threadId": "thread-cloud",
					"turn":     map[string]any{"id": "turn-cloud", "status": "completed"},
				},
			}
		}()
		return json.RawMessage(`{"turn":{"id":"turn-cloud"}}`), nil
	}
	return nil, &cloudbackend.RPCError{Code: -32601, Message: "Method not found"}
}

func (f *fakeCloudConversationRequester) SubmitUserMessage(context.Context, turn.TurnStartParams) (cloudbackend.CloudUserMessageReceipt, *cloudbackend.RPCError) {
	return cloudbackend.CloudUserMessageReceipt{}, &cloudbackend.RPCError{Code: -32601, Message: "Method not found"}
}

func (f *fakeCloudConversationRequester) Notifications() <-chan cloudbackend.Notification {
	return f.notifications
}

func (f *fakeCloudConversationRequester) AnswerServerRequest(string) error { return nil }

func (f *fakeCloudConversationRequester) SetEnvironmentSelection(*cloudclient.EnvironmentSelection) {}

func (f *fakeCloudConversationRequester) ListSavedEnvironments(context.Context) ([]cloudclient.SavedEnvironment, error) {
	return nil, nil
}

func (f *fakeCloudConversationRequester) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// TestCloudConversationEndToEndLikeRust drives one hosted conversation through
// the real remote TUI client: connect, thread/start, the Cloud reattach,
// turn/start and the completion notifications. The Cloud thread is server-owned,
// so the wire turn must not carry the client's execution settings (Rust #57267).
func TestCloudConversationEndToEndLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	requester := &fakeCloudConversationRequester{notifications: make(chan cloudbackend.Notification, 8)}
	previous := openCloudTUIAppServerForSession
	openCloudTUIAppServerForSession = func(context.Context, *cli.RootOptions) (cloudAppServerRequester, error) {
		return requester, nil
	}
	defer func() { openCloudTUIAppServerForSession = previous }()

	messages := make(chan bubbletea.Msg, 128)
	state := codextui.NewState(nil)
	go runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, appserverdaemon.NewCloudEndpoint(), state,
		codextea.SubmitRequest{Prompt: "hello"}, messages, newRemoteTUIBrokers(), nil)

	answer := ""
	completed := false
	for message := range messages {
		switch msg := message.(type) {
		case codextea.ThreadEventMsg:
			if msg.Event.Item != nil && msg.Event.Item.Text != "" {
				answer = msg.Event.Item.Text
			}
		case codextea.TurnCompletedMsg:
			completed = true
		}
	}
	if !completed {
		t.Fatal("cloud conversation did not complete its turn")
	}
	if answer != "Hello from cloud" {
		t.Fatalf("assistant answer = %q, want Hello from cloud", answer)
	}
	if got := state.ThreadID; got != "thread-cloud" {
		t.Fatalf("attached thread = %q, want thread-cloud", got)
	}
	requester.mu.Lock()
	turnParams := requester.turnParams
	methods := append([]appserver.Method(nil), requester.methods...)
	closed := requester.closed
	requester.mu.Unlock()
	if turnParams == nil {
		t.Fatalf("cloud conversation sent no turn/start: %#v", methods)
	}
	for _, key := range []string{
		"cwd", "model", "approvalPolicy", "sandboxPolicy", "permissions", "config",
		"effort", "summary", "collaborationMode", "runtimeWorkspaceRoots",
	} {
		if _, ok := turnParams[key]; ok {
			t.Fatalf("cloud turn/start carried client-owned %q: %#v", key, turnParams[key])
		}
	}
	if !closed {
		t.Fatal("cloud transport did not close the requester")
	}
}
