package cloudbackend

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

type fakeCloud struct {
	mu        sync.Mutex
	activate  func(ctx context.Context, threadID string) (*cloudclient.ThreadActivation, error)
	attach    func(ctx context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error)
	closeHook func()
}

func (f *fakeCloud) Activate(ctx context.Context, threadID string) (*cloudclient.ThreadActivation, error) {
	if f.activate == nil {
		return nil, errors.New("no activation configured")
	}
	return f.activate(ctx, threadID)
}

func (f *fakeCloud) Attach(ctx context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error) {
	if f.attach == nil {
		return nil, nil, errors.New("no attach configured")
	}
	return f.attach(ctx, threadID)
}

func (f *fakeCloud) Close() error {
	if f.closeHook != nil {
		f.closeHook()
	}
	return nil
}

// newTestClient builds a backend whose notifications are queued locally so the
// attachment state machine can be exercised without the dispatch goroutine.
func newTestClient(source cloudSource) *Client {
	client := &Client{
		source: source,
		status: map[string]any{},
		outCh:  make(chan Notification, 16),
		closed: make(chan struct{}),
	}
	client.outCond = sync.NewCond(&client.outMu)
	return client
}

func (c *Client) testNotifications() []Notification {
	c.outMu.Lock()
	defer c.outMu.Unlock()
	out := append([]Notification(nil), c.out...)
	c.out = nil
	return out
}

func pendingStream() (<-chan cloudclient.Event, <-chan error, error) {
	return make(chan cloudclient.Event), make(chan error), nil
}

func streamOf(events ...cloudclient.Event) (<-chan cloudclient.Event, <-chan error, error) {
	ch := make(chan cloudclient.Event, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	errs := make(chan error)
	close(errs)
	return ch, errs, nil
}

// Mirrors Rust #59493: a replacement becomes active only after its stream
// opens, so failed replacements keep serving the previous conversation.
func TestFailedReplacementKeepsPreviousAttachmentLikeRust(t *testing.T) {
	failures := map[string]func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error){
		"attach error": func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return nil, nil, cloudclient.ErrInvalidResponse
		},
		"closed stream": func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			events := make(chan cloudclient.Event)
			close(events)
			errs := make(chan error)
			close(errs)
			return events, errs, nil
		},
		"immediate stream error": func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			events := make(chan cloudclient.Event)
			close(events)
			errs := make(chan error, 1)
			errs <- cloudclient.ErrInvalidResponse
			close(errs)
			return events, errs, nil
		},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			fake := &fakeCloud{}
			client := newTestClient(fake)
			fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
				return pendingStream()
			}
			if err := client.replaceAttachment(context.Background(), "old"); err != nil {
				t.Fatalf("attach old: %v", err)
			}
			fake.attach = failure
			if err := client.replaceAttachment(context.Background(), "new"); err == nil {
				t.Fatal("failed replacement was accepted")
			}
			if err := client.admitTurn("old"); err != nil {
				t.Fatalf("old attachment rejected: %v", err)
			}
			if err := client.admitTurn("new"); err == nil {
				t.Fatal("new attachment admitted without a stream")
			}
		})
	}
}

// Mirrors Rust #59493: a stream that ends after attachment stops admitting
// turns and reports the disconnection.
func TestTurnAdmissionFailsAfterStreamEndsLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	client := newTestClient(fake)
	events := make(chan cloudclient.Event)
	errs := make(chan error)
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		return events, errs, nil
	}
	if err := client.replaceAttachment(context.Background(), "new"); err != nil {
		t.Fatalf("attach new: %v", err)
	}
	close(events)
	close(errs)
	if err := client.admitTurn("new"); err == nil {
		t.Fatal("turn admitted after the stream ended")
	}
	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != appserver.NotificationError {
		t.Fatalf("notifications = %#v, want one disconnect error", notifications)
	}
	payload, err := json.Marshal(notifications[0].Params)
	if err != nil {
		t.Fatalf("marshal error notification: %v", err)
	}
	if !strings.Contains(string(payload), "Cloud stream ended. Reopen the conversation to reconnect.") {
		t.Fatalf("disconnect notification = %s", payload)
	}
}

func statusNotification(t *testing.T, payload string) cloudclient.Event {
	t.Helper()
	bytes, err := hex.DecodeString(strings.ReplaceAll(payload, " ", ""))
	if err != nil {
		t.Fatalf("decode status notification: %v", err)
	}
	return cloudclient.Event{Kind: cloudclient.EventNotification, Payload: bytes}
}

// Mirrors Rust #59493: the backend keeps each attached thread's latest status
// through further notifications and clears it on reattachment or detach.
func TestAttachmentStatusFollowsNotificationsLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	client := newTestClient(fake)
	events, errs, streamErr := streamOf(
		statusNotification(t, "1a0d120b0a0622040a02080112 0174"),
		statusNotification(t, "1a0912070a02120012 0178"),
		statusNotification(t, "1a0912070a02120012 0174"),
	)
	if streamErr != nil {
		t.Fatalf("stream: %v", streamErr)
	}
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		return events, errs, nil
	}
	if err := client.replaceAttachment(context.Background(), "t"); err != nil {
		t.Fatalf("attach t: %v", err)
	}
	if got := statusType(t, client.status["t"]); got != "active" {
		t.Fatalf("initial status = %q, want active", got)
	}
	client.forwardNextNonBlocking()
	if got := statusType(t, client.status["t"]); got != "active" {
		t.Fatalf("status after other thread = %q, want active", got)
	}
	client.forwardNextNonBlocking()
	if got := statusType(t, client.status["t"]); got != "idle" {
		t.Fatalf("status after idle notification = %q, want idle", got)
	}

	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		return pendingStream()
	}
	if err := client.replaceAttachment(context.Background(), "t"); err != nil {
		t.Fatalf("reattach t: %v", err)
	}
	if _, ok := client.status["t"]; ok {
		t.Fatalf("reattachment kept stale status: %#v", client.status["t"])
	}
}

func statusType(t *testing.T, status any) string {
	t.Helper()
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var decoded struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return decoded.Type
}

// Mirrors Rust #59524: a user receipt keeps its submission identity and text.
func TestUserMessageReceiptPreservesIdentityLikeRust(t *testing.T) {
	submission := "submission-1"
	payload, ok := itemSummaryPayload(cloudclient.ItemSummary{
		Kind: cloudclient.ItemUserMessage,
		User: cloudclient.UserMessageSummary{ID: "item-1", ClientID: &submission, Text: []string{"hello", "world"}},
	})
	if !ok {
		t.Fatal("user message was not reported")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	want := `{"clientId":"submission-1","completedAtMs":null,"content":[{"text":"hello","text_elements":[],"type":"text"},{"text":"world","text_elements":[],"type":"text"}],"id":"item-1","startedAtMs":null,"type":"userMessage"}`
	if string(encoded) != want {
		t.Fatalf("user payload = %s, want %s", encoded, want)
	}
}

// Mirrors Rust #59524: a completed reply keeps its text, questions and time.
func TestCompletedAgentMessagePreservesQuestionsLikeRust(t *testing.T) {
	options := []string{"One", "Two"}
	client := newTestClient(&fakeCloud{})
	client.forwardTranscript("thread", cloudclient.TranscriptEvent{
		ThreadID: "thread",
		Update: cloudclient.TranscriptUpdate{
			Kind: cloudclient.TranscriptItemCompleted,
			Item: cloudclient.ItemLifecycle{
				TurnID: "turn",
				AtMS:   45,
				Item: cloudclient.ItemSummary{
					Kind:  cloudclient.ItemAgentMessage,
					Agent: cloudclient.AgentMessageSummary{ID: "reply", Text: "Choose one", Questions: []cloudclient.AgentQuestion{{Title: "Which?", Options: &options}}, HasQuestions: true},
				},
			},
		},
	})
	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != appserver.NotificationItemCompleted {
		t.Fatalf("notifications = %#v, want one completed item", notifications)
	}
	completed, ok := notifications[0].Params.(appserver.ItemCompletedNotification)
	if !ok {
		t.Fatalf("params = %T, want ItemCompletedNotification", notifications[0].Params)
	}
	if completed.ThreadID != "thread" || completed.TurnID != "turn" || completed.CompletedAtMS != 45 {
		t.Fatalf("completed notification = %#v", completed)
	}
	if completed.Item["type"] != "agentMessage" || completed.Item["text"] != "Choose one" {
		t.Fatalf("completed item = %#v", completed.Item)
	}
	questions, ok := completed.Item["questions"].([]any)
	if !ok || len(questions) != 1 {
		t.Fatalf("completed item questions = %#v", completed.Item["questions"])
	}
}

// Mirrors Rust #59493: unsupported requests and approval replies report
// explicit errors instead of failing silently.
func TestUnsupportedRequestsAndApprovalRepliesLikeRust(t *testing.T) {
	client := newTestClient(&fakeCloud{})
	result, rpcErr := client.handleRequest(context.Background(), appserver.MethodThreadStart, json.RawMessage(`{}`))
	if result != nil || rpcErr == nil || rpcErr.Code != methodNotFoundCode {
		t.Fatalf("thread/start = %s, %#v; want a method-not-found error", result, rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "thread/start is not supported by the cloud backend yet.") {
		t.Fatalf("thread/start message = %q", rpcErr.Message)
	}
	if err := client.Notify(appserver.MethodThreadStart); err == nil || !strings.Contains(err.Error(), "Cloud client notifications are not supported yet.") {
		t.Fatalf("Notify() error = %v", err)
	}
	if err := client.AnswerServerRequest("item/commandExecution/requestApproval"); err == nil ||
		!strings.Contains(err.Error(), "Answer this cloud approval in another client") {
		t.Fatalf("AnswerServerRequest() error = %v", err)
	}
}

// Mirrors Rust #59493: resume only accepts an ID with excludeTurns set.
func TestResumeRequiresSavedSettingsLikeRust(t *testing.T) {
	client := newTestClient(&fakeCloud{})
	_, rpcErr := client.resumeThread(context.Background(), json.RawMessage(`{"threadId":"t","model":"gpt-5"}`))
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "Cloud conversations resume by ID with saved settings") {
		t.Fatalf("resume error = %#v", rpcErr)
	}
}

// Mirrors Rust #59493: an attached conversation answers its own account,
// resume, loaded-list, read and unsubscribe requests.
func TestCloudBackendServesAttachedConversationLikeRust(t *testing.T) {
	fake := &fakeCloud{
		activate: func(_ context.Context, threadID string) (*cloudclient.ThreadActivation, error) {
			return &cloudclient.ThreadActivation{
				Thread:         cloudclient.ThreadSnapshot{ID: threadID, Status: map[string]any{"type": "idle"}},
				Model:          "gpt-5",
				ModelProvider:  "openai",
				CWD:            "/repo",
				ApprovalPolicy: "on-request",
			}, nil
		},
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return pendingStream()
		},
	}
	client := Connect(context.Background(), fake, json.RawMessage(`{"account":{"type":"chatgpt"}}`))
	defer client.Close()

	ctx := context.Background()
	account, rpcErr := client.Request(ctx, appserver.MethodGetAccount, nil)
	if rpcErr != nil || !strings.Contains(string(account), `"type":"chatgpt"`) {
		t.Fatalf("account/read = %s, %#v", account, rpcErr)
	}
	resume, rpcErr := client.Request(ctx, appserver.MethodThreadResume, json.RawMessage(`{"threadId":"thread-1","excludeTurns":true}`))
	if rpcErr != nil {
		t.Fatalf("thread/resume error = %#v", rpcErr)
	}
	var settings appserver.ThreadResumeResponse
	if err := json.Unmarshal(resume, &settings); err != nil || settings.Thread == nil || settings.Thread.ID != "thread-1" || settings.Model != "gpt-5" {
		t.Fatalf("resume result = %s (err=%v)", resume, err)
	}
	loaded, rpcErr := client.Request(ctx, appserver.MethodThreadLoadedList, json.RawMessage(`{}`))
	if rpcErr != nil || !strings.Contains(string(loaded), `"thread-1"`) {
		t.Fatalf("thread/loaded/list = %s, %#v", loaded, rpcErr)
	}
	read, rpcErr := client.Request(ctx, appserver.MethodThreadRead, json.RawMessage(`{"threadId":"thread-1"}`))
	if rpcErr != nil || !strings.Contains(string(read), `"readState":null`) {
		t.Fatalf("thread/read = %s, %#v", read, rpcErr)
	}
	if _, rpcErr := client.Request(ctx, appserver.MethodThreadRead, json.RawMessage(`{"threadId":"thread-1","includeTurns":true}`)); rpcErr == nil {
		t.Fatal("includeTurns read was accepted")
	}
	unsubscribe, rpcErr := client.Request(ctx, appserver.MethodThreadUnsubscribe, json.RawMessage(`{"threadId":"thread-1"}`))
	if rpcErr != nil || !strings.Contains(string(unsubscribe), `"status":"unsubscribed"`) {
		t.Fatalf("thread/unsubscribe = %s, %#v", unsubscribe, rpcErr)
	}
}
