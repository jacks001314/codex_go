package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
)

// Direct RPC retries share the live approval reviewer, but never the actor's
// response chain or another parent's review history (Rust #61163).
func TestThreadGuardianReviewReusesHistoryWithinParent(t *testing.T) {
	var previous []string
	reviewer := newModelGuardianReviewer(guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		previous = append(previous, request.PreviousResponseID)
		return &model.AgentResponse{
			ResponseID: fmt.Sprintf("review-%d", len(previous)),
			Message:    `{"riskLevel":"medium","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`,
		}, nil
	}))
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"exec_command","call_id":"recorded-call","arguments":"{\"cmd\":\"echo reviewed > guardian-direct.txt\",\"sandbox_permissions\":\"require_escalated\"}"}`,
	)
	store := router.services.ThreadRouter.store
	parent, err := store.Load(session.ThreadID("guardian-review-thread"))
	if err != nil {
		t.Fatal(err)
	}
	before := mustJSON(t, parent.Items)
	other := *parent
	other.ID = session.ThreadID("other-parent")
	other.SessionID = string(other.ID)
	if err := store.Create(&other); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{string(parent.ID), string(parent.ID), string(other.ID), string(parent.ID)} {
		response := router.Handle(guardianReviewRequest(t, id))
		if response.Error != nil {
			t.Fatalf("review %q: %+v", id, response.Error)
		}
		if got := guardianReviewResponseJSON(t, response); !strings.Contains(got, `"rationale":"reviewed"`) {
			t.Fatalf("review %q did not run the model: %s", id, got)
		}
	}
	want := []string{"", "review-1", "", "review-2"}
	if fmt.Sprint(previous) != fmt.Sprint(want) {
		t.Fatalf("review continuation IDs = %q, want %q", previous, want)
	}
	decision, _, err := reviewer.Review(context.Background(), string(parent.ID), "live-turn", "live-call", state.Action{Type: "command", Command: "echo live", CWD: router.services.DefaultCWD})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("live approval after direct reviews: decision=%s err=%v", decision, err)
	}
	response := router.Handle(guardianReviewRequest(t, string(parent.ID)))
	if response.Error != nil {
		t.Fatalf("direct review after live approval: %+v", response.Error)
	}
	if len(previous) != 6 || previous[4] != "review-4" || previous[5] != "review-5" {
		t.Fatalf("direct/live shared continuation = %q", previous)
	}
	for _, id := range []session.ThreadID{parent.ID, other.ID} {
		after, err := store.Load(id)
		if err != nil {
			t.Fatal(err)
		}
		if string(mustJSON(t, after.Items)) != string(before) {
			t.Fatalf("review changed actor history for %q", id)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(router.services.DefaultCWD, "guardian-direct.txt")); err != nil || len(matches) != 0 {
		t.Fatalf("recorded command side effects: matches=%v err=%v", matches, err)
	}
}

// Mirrors Rust #58119 `thread/guardianReview`: an idle thread's final injected
// exec_command is reviewed without being executed, and the structured
// assessment is returned.

type assessmentGuardianReviewer struct {
	mu         sync.Mutex
	decision   state.ReviewDecision
	assessment *state.Assessment
	reason     string
	called     bool
	block      bool
	action     state.Action
	targetItem string
	ctx        context.Context
}

func (f *assessmentGuardianReviewer) Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
	decision, _, reason, err := f.ReviewAssessment(ctx, threadID, turnID, targetItemID, action)
	return decision, reason, err
}

func (f *assessmentGuardianReviewer) ReviewAssessment(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, *state.Assessment, string, error) {
	f.mu.Lock()
	f.called = true
	f.action = action
	f.targetItem = targetItemID
	f.ctx = ctx
	block := f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return state.DecisionAborted, nil, "", nil
	}
	return f.decision, f.assessment, f.reason, nil
}

func (f *assessmentGuardianReviewer) snapshot() (bool, state.Action, string, context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called, f.action, f.targetItem, f.ctx
}

func guardianReviewTestRouter(t *testing.T, reviewer GuardianReviewer, rawItems ...string) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(home)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	items := make([]session.Item, 0, len(rawItems))
	for i, raw := range rawItems {
		item, err := sessionItemFromRaw(json.RawMessage(raw), now, i)
		if err != nil {
			t.Fatalf("sessionItemFromRaw() error = %v", err)
		}
		items = append(items, item)
	}
	if err := store.Create(&session.Record{
		ID:        session.ThreadID("guardian-review-thread"),
		SessionID: "guardian-review-thread",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items:     items,
	}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	services := RuntimeServices{ThreadRouter: NewRouter(store), DefaultCWD: t.TempDir()}
	if reviewer != nil {
		services.GuardianReviewer = reviewer
	}
	router := NewRuntimeRouter(services)
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	return router
}

func guardianReviewRequest(t *testing.T, threadID string) *Request {
	t.Helper()
	return &Request{
		JSONRPC:      "2.0",
		ID:           IntID(1),
		Method:       MethodThreadGuardianReview,
		ConnectionID: "default",
		Params:       mustJSON(t, map[string]any{"threadId": threadID}),
	}
}

func guardianReviewResponseJSON(t *testing.T, response *Response) string {
	t.Helper()
	data, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return string(data)
}

func TestThreadGuardianReviewReturnsAssessmentLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{
		decision: state.DecisionApproved,
		assessment: &state.Assessment{
			Outcome:           state.OutcomeAllow,
			RiskLevel:         state.RiskMedium,
			UserAuthorization: state.AuthorizationHigh,
			Rationale:         "User requested this command.",
		},
	}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Create guardian-direct.txt."}]}`,
		`{"type":"function_call","name":"exec_command","call_id":"exec-review","arguments":"{\"cmd\":\"echo reviewed > guardian-direct.txt\",\"sandbox_permissions\":\"require_escalated\",\"justification\":\"Create the requested file.\"}"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error != nil {
		t.Fatalf("thread/guardianReview error = %+v", response.Error)
	}
	want := `{"callId":"exec-review","assessment":{"outcome":"allow","riskLevel":"medium","userAuthorization":"high","rationale":"User requested this command."}}`
	if got := guardianReviewResponseJSON(t, response); got != want {
		t.Fatalf("response = %s, want %s", got, want)
	}
	called, action, target, ctx := reviewer.snapshot()
	if !called || target != "exec-review" {
		t.Fatalf("reviewer was not asked to assess the recorded call: called=%v target=%q", called, target)
	}
	// The review sees the live exec_command approval action.
	if action.Type != "command" || !strings.Contains(action.Command, "echo reviewed > guardian-direct.txt") {
		t.Fatalf("action = %#v, want the recorded exec_command", action)
	}
	// The recorded call is delivered as the proposed action, so the transcript
	// omits its history copy.
	if got := guardianOmittedCallIDFromContext(ctx); got != "exec-review" {
		t.Fatalf("omitted call id = %q, want exec-review", got)
	}
	// Direct review never runs the command.
	matches, err := filepath.Glob(filepath.Join(router.services.DefaultCWD, "guardian-direct.txt"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("direct review executed the recorded command: %v", matches)
	}
}

func TestThreadGuardianReviewRecordedMCPLikeRust(t *testing.T) {
	newMCPRouter := func(t *testing.T, reviewer GuardianReviewer) (*RuntimeRouter, *mcp.MCPService) {
		t.Helper()
		router := guardianReviewTestRouter(t, reviewer,
			`{"type":"function_call","namespace":"mcp__docs","name":"search","call_id":"mcp-review","arguments":"{\"query\":\"release notes\"}"}`,
		)
		service := mcp.NewMCPService(nil)
		service.SetServer(mcp.MCPServerStatus{
			Name:  "docs",
			State: mcp.MCPServerReady,
			Tools: []mcp.MCPToolInfo{{
				Name:        "search",
				Title:       "Search docs",
				Description: "Search the documentation.",
				Annotations: map[string]any{"destructiveHint": true, "openWorldHint": false},
			}},
		})
		router.services.MCP = service
		return router, service
	}

	t.Run("reviews the current catalog entry without dispatching", func(t *testing.T) {
		reviewer := &assessmentGuardianReviewer{
			decision: state.DecisionApproved,
			assessment: &state.Assessment{
				Outcome:           state.OutcomeAllow,
				RiskLevel:         state.RiskLow,
				UserAuthorization: state.AuthorizationHigh,
				Rationale:         "The search is authorized.",
			},
		}
		router, service := newMCPRouter(t, reviewer)
		defer service.Close()
		response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
		if response.Error != nil {
			t.Fatalf("thread/guardianReview error = %+v", response.Error)
		}
		want := `{"callId":"mcp-review","assessment":{"outcome":"allow","riskLevel":"low","userAuthorization":"high","rationale":"The search is authorized."}}`
		if got := guardianReviewResponseJSON(t, response); got != want {
			t.Fatalf("response = %s, want %s", got, want)
		}
		called, action, target, _ := reviewer.snapshot()
		if !called || target != "mcp-review" {
			t.Fatalf("reviewer was not asked to assess the recorded MCP call: called=%v target=%q", called, target)
		}
		arguments, _ := action.Arguments.(map[string]any)
		if action.Type != "mcp_tool_call" || action.Server != "docs" || action.ToolName != "search" ||
			action.ToolTitle != "Search docs" || action.ToolDescription != "Search the documentation." || arguments["query"] != "release notes" {
			t.Fatalf("action = %#v, arguments = %#v", action, arguments)
		}
	})

	t.Run("uses a remembered session approval", func(t *testing.T) {
		reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
		router, service := newMCPRouter(t, reviewer)
		defer service.Close()
		router.rememberMCPToolApproval("guardian-review-thread", mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"})
		response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
		if response.Error != nil {
			t.Fatalf("thread/guardianReview error = %+v", response.Error)
		}
		if got := guardianReviewResponseJSON(t, response); got != `{"callId":"mcp-review","assessment":null}` {
			t.Fatalf("response = %s", got)
		}
		if called, _, _, _ := reviewer.snapshot(); called {
			t.Fatal("a remembered MCP approval must skip review")
		}
	})

	t.Run("rejects a tool outside the current catalog", func(t *testing.T) {
		reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
		router := guardianReviewTestRouter(t, reviewer,
			`{"type":"function_call","namespace":"mcp__missing","name":"search","call_id":"mcp-review","arguments":"{}"}`,
		)
		router.services.MCP = mcp.NewMCPService(nil)
		response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
		if response.Error == nil || !strings.Contains(response.Error.Message, "unsupported tool for direct review") {
			t.Fatalf("response = %+v, want an unsupported-tool error", response)
		}
	})
}

func TestThreadGuardianReviewInheritsParentPersistence(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		t.Run(fmt.Sprintf("ephemeral=%v", ephemeral), func(t *testing.T) {
			router := guardianReviewTestRouter(t, nil,
				`{"type":"function_call","name":"exec_command","call_id":"recorded-call","arguments":"{\"cmd\":\"echo reviewed > guardian-direct.txt\",\"sandbox_permissions\":\"require_escalated\"}"}`,
			)
			parent, err := router.services.ThreadRouter.store.Load(session.ThreadID("guardian-review-thread"))
			if err != nil {
				t.Fatal(err)
			}
			parent.ID = "persistence-parent"
			parent.SessionID = "persistence-session"
			parent.Metadata.Extra = map[string]any{"ephemeral": ephemeral}
			if err := router.services.ThreadRouter.store.Create(parent); err != nil {
				t.Fatal(err)
			}
			var captured *model.AgentRequest
			router.services.Agent = guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				captured = request
				return &model.AgentResponse{Message: `{"riskLevel":"medium","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`}, nil
			})
			response := router.Handle(guardianReviewRequest(t, string(parent.ID)))
			if response.Error != nil {
				t.Fatalf("direct review: %+v", response.Error)
			}
			if captured == nil {
				t.Fatalf("model not called: %s", guardianReviewResponseJSON(t, response))
			}
			if captured.Ephemeral != ephemeral || captured.SessionID != parent.SessionID {
				t.Fatalf("review persistence = %v/%q, want %v/%q", captured.Ephemeral, captured.SessionID, ephemeral, parent.SessionID)
			}
		})
	}
}

func TestThreadGuardianReviewRetryUsesCurrentApprovalPolicy(t *testing.T) {
	var previous []string
	reviewer := newModelGuardianReviewer(guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		previous = append(previous, request.PreviousResponseID)
		return &model.AgentResponse{ResponseID: fmt.Sprintf("policy-%d", len(previous)), Message: `{"riskLevel":"medium","userAuthorization":"high","outcome":"allow","rationale":"current policy"}`}, nil
	}))
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"request_permissions","call_id":"grant","arguments":"{\"permissions\":{\"network\":{\"enabled\":true}}}"}`,
	)
	for _, policy := range []string{"on-request", "never", "on-request"} {
		if _, err := router.services.ThreadExtras.UpdateSettings(&SettingsUpdateParams{ThreadID: "guardian-review-thread", ApprovalPolicy: &policy}); err != nil {
			t.Fatal(err)
		}
		response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
		if response.Error != nil {
			t.Fatalf("policy %q: %+v", policy, response.Error)
		}
		got := guardianReviewResponseJSON(t, response)
		if policy == "never" {
			if got != `{"callId":"grant","assessment":null}` || len(previous) != 1 {
				t.Fatalf("never policy should skip current review: response=%s calls=%d", got, len(previous))
			}
		} else if !strings.Contains(got, `"rationale":"current policy"`) {
			t.Fatalf("policy %q did not reassess: %s", policy, got)
		}
	}
	if len(previous) != 2 || previous[0] != "" || previous[1] != "policy-1" {
		t.Fatalf("review chain after policy change = %q, want empty then policy-1", previous)
	}
}

func TestThreadGuardianReviewSkipsAssessmentWhenPolicyAllowsLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"exec_command","call_id":"recorded","arguments":"{\"cmd\":\"git init guardian-skipped\"}"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error != nil {
		t.Fatalf("thread/guardianReview error = %+v", response.Error)
	}
	if got := guardianReviewResponseJSON(t, response); got != `{"callId":"recorded","assessment":null}` {
		t.Fatalf("response = %s", got)
	}
	if called, _, _, _ := reviewer.snapshot(); called {
		t.Fatal("a command the policy allows must not be assessed")
	}
}

func TestThreadGuardianReviewRejectsTrailingToolOutputLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"exec_command","call_id":"exec-review","arguments":"{\"cmd\":\"ls\"}"}`,
		`{"type":"function_call_output","call_id":"exec-review","output":"recorded output"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("response = %+v, want invalid params", response)
	}
	if called, _, _, _ := reviewer.snapshot(); called {
		t.Fatal("an invalid trailing item must not reach the reviewer")
	}
}

func TestThreadGuardianReviewRejectsRecordedPermissionGrantsLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"request_permissions","call_id":"grant","arguments":"{\"permissions\":{\"network\":{\"enabled\":true}}}"}`,
		`{"type":"function_call_output","call_id":"grant","output":"{}"}`,
		`{"type":"function_call","name":"exec_command","call_id":"after-grant","arguments":"{\"cmd\":\"git init guardian-skipped\"}"}`,
	)
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error == nil || !strings.Contains(response.Error.Message, "does not support replaying request_permissions") {
		t.Fatalf("response = %+v, want the request_permissions rejection", response)
	}
}

func TestThreadGuardianReviewRequiresIdleThreadLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"exec_command","call_id":"exec-review","arguments":"{\"cmd\":\"ls\"}"}`,
	)
	if err := router.threads.RegisterTurn("guardian-review-thread", "turn-live", func() {}, time.Now().UnixMilli(), nil); err != nil {
		t.Fatalf("register turn: %v", err)
	}
	response := router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("response = %+v, want an idle-thread rejection", response)
	}
	if called, _, _, _ := reviewer.snapshot(); called {
		t.Fatal("an active thread must not be reviewed")
	}
}

func TestThreadGuardianReviewCancelledByDisconnectLikeRust(t *testing.T) {
	reviewer := &assessmentGuardianReviewer{decision: state.DecisionApproved, block: true}
	router := guardianReviewTestRouter(t, reviewer,
		`{"type":"function_call","name":"exec_command","call_id":"exec-review","arguments":"{\"cmd\":\"ls\",\"sandbox_permissions\":\"require_escalated\",\"justification\":\"inspect\"}"}`,
	)
	done := make(chan *Response, 1)
	go func() {
		done <- router.Handle(guardianReviewRequest(t, "guardian-review-thread"))
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if called, _, _, _ := reviewer.snapshot(); called {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reviewer was never invoked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	router.ConnectionClosed("default")
	select {
	case response := <-done:
		if response.Error == nil || !strings.Contains(response.Error.Message, "cancelled") {
			t.Fatalf("response = %+v, want a cancelled review", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("review did not stop after the connection closed")
	}
}
