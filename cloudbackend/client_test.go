package cloudbackend

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"codex_go/turn"
)

type fakeCloud struct {
	mu          sync.Mutex
	activate    func(ctx context.Context, threadID string) (*cloudclient.ThreadActivation, error)
	attach      func(ctx context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error)
	listThreads func(ctx context.Context, query cloudclient.ThreadListQuery) (cloudclient.ThreadPage, error)
	prewarm     func(ctx context.Context, model string, modelProvider string) (*cloudclient.ThreadActivation, error)
	listTurns   func(ctx context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error)
	listItems   func(ctx context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error)
	addMessage  func(ctx context.Context, request cloudclient.AddUserMessageRequest) (*cloudclient.Admission, error)
	interrupt   func(ctx context.Context, request cloudclient.InterruptTurnRequest) error
	closeHook   func()
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

func (f *fakeCloud) ListThreads(ctx context.Context, query cloudclient.ThreadListQuery) (cloudclient.ThreadPage, error) {
	if f.listThreads == nil {
		return cloudclient.ThreadPage{}, errors.New("no thread list configured")
	}
	return f.listThreads(ctx, query)
}

func (f *fakeCloud) PrewarmDefaultThread(ctx context.Context, model string, modelProvider string) (*cloudclient.ThreadActivation, error) {
	if f.prewarm == nil {
		return nil, errors.New("no prewarm configured")
	}
	return f.prewarm(ctx, model, modelProvider)
}

func (f *fakeCloud) ListTurns(ctx context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
	if f.listTurns == nil {
		return cloudclient.HistoryPage[cloudclient.HistoryTurn]{}, errors.New("no turn list configured")
	}
	return f.listTurns(ctx, request)
}

func (f *fakeCloud) ListItems(ctx context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
	if f.listItems == nil {
		return cloudclient.HistoryPage[cloudclient.HistoryItem]{}, errors.New("no item list configured")
	}
	return f.listItems(ctx, request)
}

func (f *fakeCloud) AddUserMessage(ctx context.Context, request cloudclient.AddUserMessageRequest) (*cloudclient.Admission, error) {
	if f.addMessage == nil {
		return nil, errors.New("no message admission configured")
	}
	return f.addMessage(ctx, request)
}

func (f *fakeCloud) InterruptTurn(ctx context.Context, request cloudclient.InterruptTurnRequest) error {
	if f.interrupt == nil {
		return errors.New("no interrupt configured")
	}
	return f.interrupt(ctx, request)
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

// Mirrors Rust #59693 and #59746: cloud conversation requests map onto native
// pagination and admissions, while typed submissions preserve receipt identity.
func TestCloudConversationRequestsAndTypedReceiptsLikeRust(t *testing.T) {
	var listedQuery cloudclient.ThreadListQuery
	var prewarmed [2]string
	var turnsRequest cloudclient.ListTurnsRequest
	var itemsRequest cloudclient.ListItemsRequest
	var admissions []cloudclient.AddUserMessageRequest
	var interrupt cloudclient.InterruptTurnRequest
	fake := &fakeCloud{
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return pendingStream()
		},
		listThreads: func(_ context.Context, query cloudclient.ThreadListQuery) (cloudclient.ThreadPage, error) {
			listedQuery = query
			return cloudclient.ThreadPage{Data: []cloudclient.ThreadSnapshot{{
				ID: "listed", Preview: "preview", HistoryMode: "legacy", ModelProvider: "openai",
				CWD: "/repo", CLIVersion: "1", Status: map[string]any{"type": "idle"}, Source: "cli",
			}}, NextCursor: stringPointerForCloudTest("page-2")}, nil
		},
		prewarm: func(_ context.Context, model string, modelProvider string) (*cloudclient.ThreadActivation, error) {
			prewarmed = [2]string{model, modelProvider}
			return &cloudclient.ThreadActivation{
				Thread: cloudclient.ThreadSnapshot{ID: "new-thread", HistoryMode: "legacy", ModelProvider: modelProvider, CWD: "/repo", Status: map[string]any{"type": "idle"}, Source: "cli"},
				Model:  model, ModelProvider: modelProvider, CWD: "/repo", ApprovalPolicy: "on-request",
			}, nil
		},
		listTurns: func(_ context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
			turnsRequest = request
			return cloudclient.HistoryPage[cloudclient.HistoryTurn]{Data: []cloudclient.HistoryTurn{{Summary: cloudclient.TurnSummary{ID: "turn-1", Status: cloudclient.TurnInProgress}}}}, nil
		},
		listItems: func(_ context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
			itemsRequest = request
			clientID := "submit-1"
			started := int64(10)
			completed := int64(20)
			return cloudclient.HistoryPage[cloudclient.HistoryItem]{Data: []cloudclient.HistoryItem{
				{TurnID: "turn-1", Summary: &cloudclient.ItemSummary{Kind: cloudclient.ItemUserMessage, User: cloudclient.UserMessageSummary{ID: "item-1", ClientID: &clientID, Text: []string{"hello"}}}, StartedAtMS: &started, CompletedAtMS: &completed},
				{TurnID: "turn-1", Summary: nil},
			}}, nil
		},
		addMessage: func(_ context.Context, request cloudclient.AddUserMessageRequest) (*cloudclient.Admission, error) {
			admissions = append(admissions, request)
			if request.Mode == cloudclient.SubmissionSteer {
				original := "turn-prior"
				return &cloudclient.Admission{Outcome: cloudclient.AdmissionOutcome{Kind: cloudclient.AdmissionDuplicate, TurnID: &original}}, nil
			}
			started := cloudclient.TurnSummary{ID: "turn-started", Status: cloudclient.TurnInProgress}
			return &cloudclient.Admission{Outcome: cloudclient.AdmissionOutcome{Kind: cloudclient.AdmissionStarted, Turn: &started}}, nil
		},
		interrupt: func(_ context.Context, request cloudclient.InterruptTurnRequest) error {
			interrupt = request
			return nil
		},
	}
	client := Connect(context.Background(), fake, json.RawMessage(`{"account":null}`))
	defer client.Close()
	ctx := context.Background()

	listed, rpcErr := client.Request(ctx, appserver.MethodThreadList, json.RawMessage(`{"currentAccountOnly":true,"limit":25,"sortKey":"updated_at","sortDirection":"desc","sourceKinds":["cli","vscode"],"useStateDbOnly":true}`))
	if rpcErr != nil {
		t.Fatalf("thread/list error = %#v", rpcErr)
	}
	var page appserver.ThreadListResponse
	if err := json.Unmarshal(listed, &page); err != nil || len(page.Data) != 1 || page.Data[0].ID != "listed" || page.Data[0].HistoryMode != appserver.ThreadHistoryPaginated {
		t.Fatalf("thread/list = %s (err=%v)", listed, err)
	}
	if listedQuery.Limit == nil || *listedQuery.Limit != 25 || listedQuery.SortKey != cloudclient.ThreadSortUpdatedAt || listedQuery.SortDirection != cloudclient.ThreadSortDescending {
		t.Fatalf("thread list query = %#v", listedQuery)
	}

	startedRaw, rpcErr := client.Request(ctx, appserver.MethodThreadStart, json.RawMessage(`{"model":"gpt-5","modelProvider":"openai","ephemeral":false}`))
	if rpcErr != nil {
		t.Fatalf("thread/start error = %#v", rpcErr)
	}
	var started appserver.ThreadStartResponse
	if err := json.Unmarshal(startedRaw, &started); err != nil || started.Thread == nil || started.Thread.ID != "new-thread" || started.Thread.HistoryMode != appserver.ThreadHistoryPaginated {
		t.Fatalf("thread/start = %s (err=%v)", startedRaw, err)
	}
	if prewarmed != [2]string{"gpt-5", "openai"} {
		t.Fatalf("prewarm = %#v", prewarmed)
	}

	turnsRaw, rpcErr := client.Request(ctx, appserver.MethodThreadTurnsList, json.RawMessage(`{"threadId":"new-thread","itemsView":"notLoaded"}`))
	if rpcErr != nil || !strings.Contains(string(turnsRaw), `"turn-1"`) {
		t.Fatalf("thread/turns/list = %s, %#v", turnsRaw, rpcErr)
	}
	if turnsRequest.Page.Order != cloudclient.HistoryDescending {
		t.Fatalf("turn order = %v", turnsRequest.Page.Order)
	}
	itemsRaw, rpcErr := client.Request(ctx, appserver.MethodThreadItemsList, json.RawMessage(`{"threadId":"new-thread","sortDirection":"asc"}`))
	if rpcErr != nil {
		t.Fatalf("thread/items/list error = %#v", rpcErr)
	}
	var items appserver.ThreadItemsListResponse
	if err := json.Unmarshal(itemsRaw, &items); err != nil || len(items.Data) != 1 || items.Data[0].Item.ID != "item-1" || items.Data[0].StartedAtMS == nil || *items.Data[0].StartedAtMS != 10 {
		t.Fatalf("thread/items/list = %s (err=%v)", itemsRaw, err)
	}
	if itemsRequest.Page.Order != cloudclient.HistoryAscending {
		t.Fatalf("item order = %v", itemsRequest.Page.Order)
	}
	select {
	case notification := <-client.Notifications():
		if notification.Method != appserver.NotificationWarning {
			t.Fatalf("partial-history notification = %#v", notification)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing partial-history warning")
	}

	trigger := "safety_recourse_follow_up"
	receipt, rpcErr := client.SubmitUserMessage(ctx, turn.TurnStartParams{
		ThreadID: "new-thread", ClientUserMessageID: "submit-1", TurnTrigger: trigger,
		Input: []turn.TurnUserInput{{Type: "text", Text: "one"}, {Type: "text", Text: "two"}},
	})
	if rpcErr != nil || receipt.Kind != CloudUserMessageDuplicate || receipt.TurnID == nil || *receipt.TurnID != "turn-prior" {
		t.Fatalf("typed receipt = %#v, %#v", receipt, rpcErr)
	}
	if len(admissions) != 1 {
		t.Fatalf("admissions = %d, want one typed submission", len(admissions))
	}
	admitted := admissions[0]
	if admitted.Mode != cloudclient.SubmissionSteer || admitted.TurnTrigger == nil || *admitted.TurnTrigger != trigger {
		t.Fatalf("typed admission = %#v", admissions)
	}
	// Two app-server text inputs stay two ordered parts instead of being joined.
	if len(admitted.Input) != 2 || admitted.Input[0].Kind != cloudclient.UserMessageText || admitted.Input[0].Text != "one" ||
		admitted.Input[1].Kind != cloudclient.UserMessageText || admitted.Input[1].Text != "two" {
		t.Fatalf("typed admission parts = %#v, want the two text parts in order", admitted.Input)
	}
	_, rpcErr = client.SubmitUserMessage(ctx, turn.TurnStartParams{
		ThreadID: "new-thread", ClientUserMessageID: "invalid", Model: "other",
		Input: []turn.TurnUserInput{{Type: "text", Text: "no"}},
	})
	if rpcErr == nil || rpcErr.Code != invalidParamsCode || len(admissions) != 1 {
		t.Fatalf("invalid typed submission = %#v, admissions=%d", rpcErr, len(admissions))
	}

	generic, rpcErr := client.Request(ctx, appserver.MethodTurnStart, json.RawMessage(`{"threadId":"new-thread","clientUserMessageId":"generic","input":[{"type":"text","text":"hello","text_elements":[]}]}`))
	if rpcErr != nil || !strings.Contains(string(generic), `"turn-started"`) {
		t.Fatalf("turn/start = %s, %#v", generic, rpcErr)
	}
	if len(admissions) != 2 || admissions[1].Mode != cloudclient.SubmissionStartIfIdle {
		t.Fatalf("generic admission = %#v", admissions)
	}
	if _, rpcErr := client.Request(ctx, appserver.MethodTurnInterrupt, json.RawMessage(`{"threadId":"new-thread","turnId":"turn-started"}`)); rpcErr != nil {
		t.Fatalf("turn/interrupt error = %#v", rpcErr)
	}
	if interrupt.ThreadID != "new-thread" || interrupt.TurnID != "turn-started" {
		t.Fatalf("interrupt = %#v", interrupt)
	}
}

func stringPointerForCloudTest(value string) *string { return &value }

// Mirrors Rust #59493: unsupported requests and approval replies report
// explicit errors instead of failing silently.
func TestUnsupportedRequestsAndApprovalRepliesLikeRust(t *testing.T) {
	client := newTestClient(&fakeCloud{})
	result, rpcErr := client.handleRequest(context.Background(), appserver.MethodThreadFork, json.RawMessage(`{}`), cloudclient.SubmissionStartIfIdle, false)
	if result != nil || rpcErr == nil || rpcErr.Code != methodNotFoundCode {
		t.Fatalf("thread/fork = %s, %#v; want a method-not-found error", result, rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "thread/fork is not supported by the cloud backend yet.") {
		t.Fatalf("thread/fork message = %q", rpcErr.Message)
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
