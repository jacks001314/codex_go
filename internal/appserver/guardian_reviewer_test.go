package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/internal/auth"
	"codex_go/internal/model"
	"codex_go/internal/session"
	"codex_go/internal/state"
	"codex_go/internal/tool"
	"codex_go/internal/turn"
)

type guardianAgentFunc func(context.Context, *model.AgentRequest) (*model.AgentResponse, error)

func (f guardianAgentFunc) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	return f(ctx, request)
}

type guardianPrewarmAgent struct {
	mu            sync.Mutex
	requests      []*model.AgentRequest
	prewarms      int
	prewarmErr    error
	websocketRuns int
	websocketErr  error
}

func (a *guardianPrewarmAgent) Prewarm(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prewarms++
	if a.prewarmErr != nil {
		return nil, a.prewarmErr
	}
	return &model.AgentResponse{ResponseID: "warm-1"}, nil
}

func (a *guardianPrewarmAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	copyRequest := *request
	a.requests = append(a.requests, &copyRequest)
	return &model.AgentResponse{ResponseID: fmt.Sprintf("review-%d", len(a.requests)), Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
}

func (a *guardianPrewarmAgent) RunWebSocket(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.websocketRuns++
	err := a.websocketErr
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return a.Run(ctx, request)
}

func (a *guardianPrewarmAgent) snapshot() (int, int, []*model.AgentRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prewarms, a.websocketRuns, append([]*model.AgentRequest(nil), a.requests...)
}

func TestModelGuardianReviewerMapsAssessmentDecision(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcome  string
		decision state.ReviewDecision
	}{
		{name: "allow", outcome: "allow", decision: state.DecisionApproved},
		{name: "deny", outcome: "deny", decision: state.DecisionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				if request.TaskKind != model.AgentTaskReview || request.Originator != "guardian" || request.ClientMetadata["x-openai-subagent"] != "guardian" || request.OutputSchema == nil {
					t.Fatalf("request = %#v", request)
				}
				return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"` + tc.outcome + `","rationale":"reviewed"}`}, nil
			})}
			decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
			if err != nil || decision != tc.decision || reason != "reviewed" {
				t.Fatalf("decision=%s reason=%q err=%v", decision, reason, err)
			}
		})
	}
}

func TestModelGuardianReviewerMapsTimeout(t *testing.T) {
	reviewer := &modelGuardianReviewer{
		timeout: time.Millisecond,
		agent: guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionTimedOut || reason != state.GuardianTimeoutMessage() {
		t.Fatalf("decision=%s reason=%q err=%v", decision, reason, err)
	}
}

func TestModelGuardianReviewerRejectsMalformedAssessment(t *testing.T) {
	reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{Message: `not-json`}, nil
	})}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err == nil || decision != state.DecisionAborted {
		t.Fatalf("decision=%s err=%v", decision, err)
	}
}

func TestModelGuardianReviewerReadsAssessmentFromAgentMessageItem(t *testing.T) {
	reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{Items: []model.AgentItem{{Type: "agent_message", Text: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"item"}`}}}, nil
	})}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionApproved || reason != "item" {
		t.Fatalf("decision=%s reason=%q err=%v", decision, reason, err)
	}
}

func TestModelGuardianReviewerEmitsLifecycleAndRecordsDenial(t *testing.T) {
	store := state.NewReviewStore()
	breaker := state.NewCircuitBreaker()
	var events []*state.Event
	reviewer := &modelGuardianReviewer{
		store: store, breaker: breaker,
		notify: func(threadID string, event *state.Event) {
			if threadID != "thread-1" {
				t.Fatalf("threadID=%q", threadID)
			}
			events = append(events, event)
		},
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`}, nil
		}),
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
	if err != nil || decision != state.DecisionDenied || len(events) != 2 {
		t.Fatalf("decision=%s events=%#v err=%v", decision, events, err)
	}
	if events[0].ID != events[1].ID || events[0].Status != state.StatusInProgress || events[1].Status != state.StatusDenied || events[1].TargetItemID != "call-1" {
		t.Fatalf("events=%#v", events)
	}
	action := breaker.RecordDenial("turn-1")
	if action.ConsecutiveDenials != 2 {
		t.Fatalf("breaker action=%#v", action)
	}
}

func TestModelGuardianReviewerInterruptsAfterDenialThreshold(t *testing.T) {
	interrupts := 0
	reviewer := &modelGuardianReviewer{
		store: state.NewReviewStore(), breaker: state.NewCircuitBreaker(),
		interrupt: func(threadID, turnID string) {
			if threadID != "thread-1" || turnID != "turn-1" {
				t.Fatalf("interrupt=%s/%s", threadID, turnID)
			}
			interrupts++
		},
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`}, nil
		}),
	}
	for i := 0; i < 4; i++ {
		decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
		if err != nil || decision != state.DecisionDenied {
			t.Fatalf("review %d decision=%s err=%v", i, decision, err)
		}
	}
	if interrupts != 1 {
		t.Fatalf("interrupts=%d", interrupts)
	}
}

func TestGuardianSessionRunnerReusesPreviousResponse(t *testing.T) {
	var requests []*model.AgentRequest
	runner := &guardianSessionRunner{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		copyRequest := *request
		requests = append(requests, &copyRequest)
		return &model.AgentResponse{ResponseID: "resp-" + string(rune('1'+len(requests))), Message: "ok"}, nil
	})}
	for i := 0; i < 2; i++ {
		if _, err := runner.Run(context.Background(), &model.AgentRequest{Prompt: "review", Store: true, ClientMetadata: map[string]string{"x": "y"}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 2 || requests[0].PreviousResponseID != "" || requests[1].PreviousResponseID != "resp-2" || requests[0].Store || requests[1].Store {
		t.Fatalf("requests=%#v", requests)
	}
}

func TestGuardianSessionRunnerReusesPrewarmResponseForFirstReview(t *testing.T) {
	agent := &guardianPrewarmAgent{}
	session := &guardianSessionRunner{agent: agent}
	if err := session.Prewarm(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := session.Run(context.Background(), &model.AgentRequest{Prompt: "review"}); err != nil {
			t.Fatal(err)
		}
	}
	_, websocketRuns, requests := agent.snapshot()
	if len(requests) != 2 || requests[0].PreviousResponseID != "warm-1" || requests[1].PreviousResponseID != "review-1" {
		t.Fatalf("requests=%#v", requests)
	}
	if websocketRuns != 2 {
		t.Fatalf("websocketRuns=%d", websocketRuns)
	}
}

func TestEnsureGuardianReviewerPrewarmsOnceAndFallsBackLazily(t *testing.T) {
	t.Run("once", func(t *testing.T) {
		agent := &guardianPrewarmAgent{}
		router := NewRuntimeRouter(RuntimeServices{Agent: agent})
		first := router.ensureGuardianReviewer(agent)
		second := router.ensureGuardianReviewer(agent)
		if first == nil || first != second {
			t.Fatalf("reviewers=%p/%p", first, second)
		}
		deadline := time.Now().Add(time.Second)
		prewarms, _, _ := agent.snapshot()
		for prewarms == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
			prewarms, _, _ = agent.snapshot()
		}
		if prewarms != 1 {
			t.Fatalf("prewarms=%d", prewarms)
		}
	})
	t.Run("failure", func(t *testing.T) {
		agent := &guardianPrewarmAgent{prewarmErr: errors.New("websocket unavailable")}
		router := NewRuntimeRouter(RuntimeServices{Agent: agent})
		reviewer := router.ensureGuardianReviewer(agent)
		deadline := time.Now().Add(time.Second)
		prewarms, _, _ := agent.snapshot()
		for prewarms == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
			prewarms, _, _ = agent.snapshot()
		}
		decision, _, err := reviewer.Review(context.Background(), "thread", "turn", "call", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "read"})
		_, _, requests := agent.snapshot()
		if err != nil || decision != state.DecisionApproved || len(requests) != 1 || requests[0].PreviousResponseID != "" {
			t.Fatalf("decision=%s requests=%#v err=%v", decision, requests, err)
		}
	})
}

func TestGuardianSessionRunnerFallsBackWhenWebSocketReviewFails(t *testing.T) {
	agent := &guardianPrewarmAgent{websocketErr: errors.New("websocket closed")}
	session := &guardianSessionRunner{agent: agent}
	if err := session.Prewarm(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := session.Run(context.Background(), &model.AgentRequest{Prompt: "review"})
	if err != nil || response == nil || response.ResponseID != "review-1" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	_, websocketRuns, requests := agent.snapshot()
	if websocketRuns != 1 || len(requests) != 1 || requests[0].PreviousResponseID != "" {
		t.Fatalf("websocketRuns=%d requests=%#v", websocketRuns, requests)
	}
}

func TestApproveGuardianDeniedActionInjectsExactAction(t *testing.T) {
	mailbox := turn.NewSteerMailbox()
	router := NewRuntimeRouter(RuntimeServices{SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.ephemeralThreads["thread-1"] = &session.Record{ID: "thread-1", Items: []session.Item{}}
	if err := router.registerActiveRuntimeTurn("thread-1", "turn-1", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	event := state.Event{ID: "review-1", TurnID: "turn-1", Status: state.StatusDenied, Action: state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write", Extra: map[string]any{"arguments": map[string]any{"title": "Lunch"}}}}
	raw, _ := json.Marshal(event)
	response, err := router.handleThreadApproveGuardianDeniedActionRuntime(requestWithParams(t, IntID(1), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{ThreadID: "thread-1", Event: raw}))
	if err != nil || response == nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	record, ok := router.ephemeralThreadRecord("thread-1", true)
	if !ok || len(record.Items) != 1 || record.Items[0].Role != "developer" || !strings.Contains(record.Items[0].Text, state.DeniedActionApprovalPrefix) || !strings.Contains(record.Items[0].Text, `"outcome": "allowed"`) {
		t.Fatalf("record=%#v", record)
	}
	items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: "thread-1", TurnID: "turn-1"})
	if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0]), "exact action") {
		t.Fatalf("mailbox=%#v", items)
	}
}

func TestApproveGuardianDeniedActionIgnoresNonDeniedAndRejectsInvalidJSON(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	router.ephemeralThreads["thread-1"] = &session.Record{ID: "thread-1"}
	approved, _ := json.Marshal(state.Event{ID: "review-1", Status: state.StatusApproved})
	if _, err := router.handleThreadApproveGuardianDeniedActionRuntime(requestWithParams(t, IntID(1), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{ThreadID: "thread-1", Event: approved})); err != nil {
		t.Fatal(err)
	}
	record, _ := router.ephemeralThreadRecord("thread-1", true)
	if len(record.Items) != 0 {
		t.Fatalf("items=%#v", record.Items)
	}
	_, err := router.handleThreadApproveGuardianDeniedActionRuntime(&Request{JSONRPC: "2.0", ID: IntID(2), Method: MethodThreadApproveGuardianDeniedAction, Params: json.RawMessage(`{"threadId":"thread-1","event":"bad"}`)})
	if err == nil || !strings.Contains(err.Error(), "invalid Guardian denial event") {
		t.Fatalf("err=%v", err)
	}
}

func TestApproveGuardianDeniedActionRuntimeDispatchUsesLiveHandler(t *testing.T) {
	mailbox := turn.NewSteerMailbox()
	threadRouter := NewRouter(session.NewStore(t.TempDir()))
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatalf("thread start=%#v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	if err := router.registerActiveRuntimeTurn(threadID, "turn-1", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatal(err)
	}
	event := state.Event{ID: "review-dispatch", TurnID: "turn-1", Status: state.StatusDenied, Action: state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"}}
	raw, _ := json.Marshal(event)
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{ThreadID: threadID, Event: raw}))
	if response.Error != nil {
		t.Fatalf("response=%#v", response.Error)
	}
	if items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: threadID, TurnID: "turn-1"}); len(items) != 1 {
		t.Fatalf("mailbox=%#v", items)
	}
}

func TestThreadInjectItemsRuntimeDispatchFeedsActiveTurn(t *testing.T) {
	mailbox := turn.NewSteerMailbox()
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatal(started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	if err := router.registerActiveRuntimeTurn(threadID, "turn-inject", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"message","role":"developer","content":[{"type":"input_text","text":"injected context"}]}`)
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{ThreadID: threadID, Items: []json.RawMessage{raw}}))
	if response.Error != nil {
		t.Fatalf("response=%#v", response.Error)
	}
	items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: threadID, TurnID: "turn-inject"})
	if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0]), "injected context") {
		t.Fatalf("mailbox=%#v", items)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil || len(record.Items) == 0 || !strings.Contains(record.Items[len(record.Items)-1].Text, "injected context") {
		t.Fatalf("record=%#v err=%v", record, err)
	}
}

func TestThreadElicitationRuntimePausesUnifiedExec(t *testing.T) {
	manager := tool.NewUnifiedExecManager()
	defer manager.Close()
	threadRouter := NewRouter(session.NewStore(t.TempDir()))
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, UnifiedExec: manager, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	increment := router.Handle(requestWithParams(t, IntID(2), MethodThreadIncrementElicitation, ThreadIncrementElicitationParams{ThreadID: threadID}))
	if increment.Error != nil || !manager.ThreadElicitationPaused(threadID) {
		t.Fatalf("increment=%#v paused=%v", increment.Error, manager.ThreadElicitationPaused(threadID))
	}
	decrement := router.Handle(requestWithParams(t, IntID(3), MethodThreadDecrementElicitation, ThreadDecrementElicitationParams{ThreadID: threadID}))
	if decrement.Error != nil || manager.ThreadElicitationPaused(threadID) {
		t.Fatalf("decrement=%#v paused=%v", decrement.Error, manager.ThreadElicitationPaused(threadID))
	}
}

func TestThreadRollbackRejectsActiveTurnLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	if err := router.registerActiveRuntimeTurn(threadID, "turn-active", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatal(err)
	}
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadRollback, ThreadRollbackParams{ThreadID: threadID, NumTurns: 1}))
	if response.Error == nil || response.Error.Message != "Cannot rollback while a turn is in progress." {
		t.Fatalf("response=%#v", response.Error)
	}
}

func TestAccountDeviceLoginRuntimeCompletesAndCancels(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		home := t.TempDir()
		jwt := testJWTForGuardianLogin(map[string]any{"chatgpt_account_id": "account-1"})
		issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/accounts/deviceauth/usercode":
				_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "0"})
			case "/api/accounts/deviceauth/token":
				_ = json.NewEncoder(w).Encode(map[string]string{"authorization_code": "code", "code_challenge": "challenge", "code_verifier": "verifier"})
			case "/oauth/token":
				_ = json.NewEncoder(w).Encode(map[string]string{"id_token": jwt, "access_token": "access", "refresh_token": "refresh"})
			default:
				http.NotFound(w, request)
			}
		}))
		defer issuer.Close()
		sink := NewNotificationBuffer()
		router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Second}})
		router.SetNotificationSink(sink)
		response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
		if response.Error != nil {
			t.Fatalf("response=%#v", response.Error)
		}
		login := response.Result.(*auth.LoginAccountResponse)
		if login.UserCode != "CODE-1" || login.VerificationURL != issuer.URL+"/codex/device" {
			t.Fatalf("login=%#v", login)
		}
		waitForAccountLoginNotification(t, sink, login.LoginID, true)
		if account := router.requireAccount().GetAccount(nil).Account; account == nil || account.Type != auth.AccountChatGPT {
			t.Fatalf("account=%#v", account)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		home := t.TempDir()
		issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/accounts/deviceauth/usercode":
				_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "1"})
			case "/api/accounts/deviceauth/token":
				http.Error(w, "pending", http.StatusForbidden)
			default:
				http.NotFound(w, request)
			}
		}))
		defer issuer.Close()
		router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Minute}})
		loginResponse := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
		login := loginResponse.Result.(*auth.LoginAccountResponse)
		cancelResponse := router.Handle(requestWithParams(t, IntID(2), MethodCancelLoginAccount, auth.CancelLoginAccountParams{LoginID: login.LoginID}))
		if cancelResponse.Error != nil || cancelResponse.Result.(*auth.CancelLoginAccountResponse).Status != auth.CancelLoginCanceled {
			t.Fatalf("cancel=%#v", cancelResponse)
		}
	})
}

func TestAccountBrowserLoginRuntimeStartsAndCancelsRealServer(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, CallbackPort: 0}})
	response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: auth.AccountChatGPT}))
	if response.Error != nil {
		t.Fatalf("response=%#v", response.Error)
	}
	login := response.Result.(*auth.LoginAccountResponse)
	parsed, err := url.Parse(login.AuthURL)
	if err != nil || parsed.Query().Get("redirect_uri") == "" || !strings.Contains(parsed.Query().Get("redirect_uri"), "/auth/callback") {
		t.Fatalf("authURL=%q err=%v", login.AuthURL, err)
	}
	cancelResponse := router.Handle(requestWithParams(t, IntID(2), MethodCancelLoginAccount, auth.CancelLoginAccountParams{LoginID: login.LoginID}))
	if cancelResponse.Error != nil || cancelResponse.Result.(*auth.CancelLoginAccountResponse).Status != auth.CancelLoginCanceled {
		t.Fatalf("cancel=%#v", cancelResponse)
	}
}

func TestAccountLogoutCancelsPendingLoginRuntime(t *testing.T) {
	home := t.TempDir()
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "1"})
		case "/api/accounts/deviceauth/token":
			http.Error(w, "pending", http.StatusForbidden)
		default:
			http.NotFound(w, request)
		}
	}))
	defer issuer.Close()
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Minute}})
	loginResponse := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
	if loginResponse.Error != nil {
		t.Fatal(loginResponse.Error)
	}
	logoutResponse := router.Handle(&Request{JSONRPC: "2.0", ID: IntID(2), Method: MethodLogoutAccount})
	if logoutResponse.Error != nil {
		t.Fatal(logoutResponse.Error)
	}
	router.loginRuntimeMu.Lock()
	pending := len(router.loginRuntimeCancels)
	router.loginRuntimeMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending runtimes=%d", pending)
	}
	time.Sleep(30 * time.Millisecond)
	resolved, err := auth.NewStore(home).Resolve()
	if err != nil || resolved != nil {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
}

func TestRuntimeRouterCloseCancelsPendingLoginRuntime(t *testing.T) {
	home := t.TempDir()
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/accounts/deviceauth/usercode" {
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "1"})
			return
		}
		if request.URL.Path == "/api/accounts/deviceauth/token" {
			http.Error(w, "pending", http.StatusForbidden)
			return
		}
		http.NotFound(w, request)
	}))
	defer issuer.Close()
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Minute}})
	response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if err := router.Close(); err != nil {
		t.Fatal(err)
	}
	router.loginRuntimeMu.Lock()
	pending := len(router.loginRuntimeCancels)
	router.loginRuntimeMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending runtimes=%d", pending)
	}
}

func testJWTForGuardianLogin(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(body) + ".signature"
}

func waitForAccountLoginNotification(t *testing.T, sink *NotificationBuffer, loginID string, success bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationAccountLoginCompleted {
				continue
			}
			params, ok := notification.Params.(*auth.AccountLoginCompletedNotification)
			if ok && params.LoginID != nil && *params.LoginID == loginID && params.Success == success {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("login notification %s success=%v not found", loginID, success)
}
