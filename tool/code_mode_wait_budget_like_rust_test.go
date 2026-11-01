package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust's ExecWaitArgs.max_tokens: Option<usize>
// (core/src/tools/code_mode/wait_handler.rs) resolved through
// resolve_max_tokens (core/src/unified_exec/mod.rs): an omitted budget keeps
// the 10_000-token default, while an explicit `max_tokens: 0` is a real
// zero-token budget that truncates the observation instead of restoring the
// default.

type budgetRecordingSession struct {
	waitBudget      *int
	waitBudgetSet   bool
	terminateBudget *int
	response        CodeModeRemoteResponse
}

func (s *budgetRecordingSession) Execute(context.Context, CodeModeRemoteExecuteRequest, *YieldSignal) (CodeModeRemoteResponse, error) {
	return CodeModeRemoteResponse{CellID: "budget-cell", State: "yielded"}, nil
}

func (s *budgetRecordingSession) Wait(_ context.Context, _ string, _ uint64, budget *int, _ *YieldSignal) (CodeModeRemoteResponse, error) {
	s.waitBudget = budget
	s.waitBudgetSet = true
	return s.response, nil
}

func (s *budgetRecordingSession) Terminate(_ context.Context, _ string, budget *int) (CodeModeRemoteResponse, error) {
	s.terminateBudget = budget
	return s.response, nil
}

func (s *budgetRecordingSession) Close() error { return nil }

type budgetRecordingProvider struct{ session *budgetRecordingSession }

func (p *budgetRecordingProvider) NewSession(CodeModeRemoteDelegate) CodeModeRemoteSession {
	return p.session
}

func runRemoteWaitWithBudget(t *testing.T, arguments string) (*budgetRecordingSession, error) {
	t.Helper()
	session := &budgetRecordingSession{response: CodeModeRemoteResponse{CellID: "budget-cell", State: "completed"}}
	runtime := NewCodeModeRuntime(&budgetRecordingProvider{session: session}, false)
	_, wait := runtime.Executors(NewRegistry())
	_, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "wait-budget",
		Payload: Payload{Kind: PayloadFunction, Arguments: arguments},
	})
	return session, err
}

func TestRemoteWaitForwardsAnExplicitZeroBudgetLikeRust(t *testing.T) {
	session, err := runRemoteWaitWithBudget(t, `{"cell_id":"budget-cell","max_tokens":0}`)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !session.waitBudgetSet {
		t.Fatalf("wait did not reach the remote session")
	}
	if session.waitBudget == nil || *session.waitBudget != 0 {
		t.Fatalf("forwarded wait budget = %#v, want an explicit 0", session.waitBudget)
	}
}

func TestRemoteWaitOmitsAnAbsentBudgetLikeRust(t *testing.T) {
	session, err := runRemoteWaitWithBudget(t, `{"cell_id":"budget-cell"}`)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !session.waitBudgetSet {
		t.Fatalf("wait did not reach the remote session")
	}
	if session.waitBudget != nil {
		t.Fatalf("forwarded wait budget = %#v, want none", session.waitBudget)
	}
}

func TestRemoteTerminateForwardsAnExplicitZeroBudgetLikeRust(t *testing.T) {
	session, err := runRemoteWaitWithBudget(t, `{"cell_id":"budget-cell","max_tokens":0,"terminate":true}`)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if session.terminateBudget == nil || *session.terminateBudget != 0 {
		t.Fatalf("forwarded terminate budget = %#v, want an explicit 0", session.terminateBudget)
	}
}

func TestRemoteWaitRejectsANegativeBudgetLikeRust(t *testing.T) {
	// Rust deserializes max_tokens into Option<usize>, so a negative value is a
	// parse error rather than a budget that reaches the host.
	session, err := runRemoteWaitWithBudget(t, `{"cell_id":"budget-cell","max_tokens":-1}`)
	if err == nil {
		t.Fatalf("negative budget was accepted (forwarded %#v)", session.waitBudget)
	}
	if session.waitBudgetSet {
		t.Fatalf("negative budget reached the remote session: %#v", session.waitBudget)
	}
}

// The client applies the same resolution: a zero budget truncates the
// observation instead of restoring the default.
func TestZeroTokenBudgetTruncatesInsteadOfUsingTheDefaultLikeRust(t *testing.T) {
	zero := 0
	response := CodeModeRemoteResponse{
		CellID:       "budget-cell",
		State:        "completed",
		ContentItems: []map[string]any{{"type": "input_text", "text": "abcdefgh"}},
	}
	budgeted, err := remoteResponseOutput("call", response, codeModeTokenLimit(&zero), CodeModeOutputTruncationClient, time.Millisecond, false)
	if err != nil {
		t.Fatalf("zero budget: %v", err)
	}
	if strings.Contains(budgeted.Body, "abcdefgh") {
		t.Fatalf("zero budget kept the text: %q", budgeted.Body)
	}
	if !strings.Contains(budgeted.Body, "omitted") && !strings.Contains(budgeted.Body, "truncated") {
		t.Fatalf("zero budget carries no notice: %q", budgeted.Body)
	}
	def, err := remoteResponseOutput("call", response, codeModeTokenLimit(nil), CodeModeOutputTruncationClient, time.Millisecond, false)
	if err != nil {
		t.Fatalf("default budget: %v", err)
	}
	if !strings.Contains(def.Body, "abcdefgh") {
		t.Fatalf("default budget dropped the text: %q", def.Body)
	}
}
