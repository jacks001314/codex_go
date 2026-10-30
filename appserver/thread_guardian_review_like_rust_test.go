package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

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
	if _, err := filepath.Glob(filepath.Join(router.services.DefaultCWD, "guardian-direct.txt")); err != nil {
		t.Fatalf("glob: %v", err)
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
