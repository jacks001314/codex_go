package appserver

import (
	"context"
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust #44569: an optional review that exhausts its local input budget defers
// to the user, while a review the policy requires still fails closed.
func TestGuardianOptionalReviewBudgetExhaustionDefersToUserLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		required bool
		want     state.ReviewDecision
	}{
		{name: "optional review defers", required: false, want: state.DecisionAskUser},
		{name: "required review fails closed", required: true, want: state.DecisionDenied},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reviewer := &modelGuardianReviewer{
				agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
					t.Error("an over-budget review must not sample the model")
					return nil, nil
				}),
				store:                  state.NewReviewStore(),
				syncImageContextWindow: func(string, string) (int64, bool) { return 1, true },
				requiredReview:         func(string, string) bool { return testCase.required },
			}
			decision, _, err := reviewer.Review(context.Background(), "thread-budget", "turn-budget", "call-budget", state.Action{
				Type: "command", Command: "ls", CWD: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			if decision != testCase.want {
				t.Fatalf("decision = %q, want %q", decision, testCase.want)
			}
			if reviewer.existingSessionFor("thread-budget") != nil {
				t.Fatal("an exhausted reviewer session must be discarded")
			}
		})
	}
}

// Rust #44569: a deferred review must not deny the action; the approval falls
// back to the ordinary user approval request.
func TestGuardianAskUserFallsBackToUserApprovalLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5.4\"\napprovals_reviewer = \"auto_review\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	reviewer := &fakeGuardianReviewer{decision: state.DecisionAskUser, reason: guardianInputBudgetMessage}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:       cwd,
		Config:           config.NewConfigService(home),
		GuardianReviewer: reviewer,
	})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	prompted := false
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestCommandExecutionApproval {
			return
		}
		prompted = true
		router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{
			Decision: CommandExecutionApprovalAccept,
		}))
	}))

	decision, err := router.shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), &tool.ShellApprovalRequest{
		Request:    &tool.ShellRequest{HookCommand: "rm -rf build", CWD: cwd, Justification: "clean the build"},
		Invocation: &tool.Invocation{CallID: "call-1"},
	})
	if err != nil {
		t.Fatalf("shell approval error = %v", err)
	}
	if !decision.Approved {
		t.Fatalf("decision = %#v, want the user's approval", decision)
	}
	if !prompted {
		t.Fatal("a budget-deferred review did not reach the user approval request")
	}
}

// Rust #44569: cached evidence cannot cover an oversized action. The serving
// assessment still approves a small action, while an action that cannot fit the
// reviewer budget falls through to the full review and defers to the user.
func TestGuardianServingAssessmentDoesNotCoverOversizedActionLikeRust(t *testing.T) {
	newReviewer := func(t *testing.T) *modelGuardianReviewer {
		t.Helper()
		return &modelGuardianReviewer{
			agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
				t.Error("a cached decision or rejected action must not sample the model")
				return nil, nil
			}),
			store:                  state.NewReviewStore(),
			syncImageContextWindow: func(string, string) (int64, bool) { return 400, true },
			servingAssessment:      func(string, string, string) *state.ServingAssessment { return &state.ServingAssessment{} },
			servingAuthorization:   func(string, string) state.ServingAuthorization { return state.ServingAuthorization{} },
		}
	}
	small := state.Action{Type: "command", Command: "ls", CWD: "/repo"}
	decision, _, err := newReviewer(t).Review(context.Background(), "thread-cache", "turn-1", "call-1", small)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("small action decision = %q, want the cached approval", decision)
	}

	oversized := state.Action{Type: "command", Command: strings.Repeat("x", 4_000), CWD: "/repo"}
	decision, _, err = newReviewer(t).Review(context.Background(), "thread-cache", "turn-1", "call-2", oversized)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionAskUser {
		t.Fatalf("oversized action decision = %q, want the user fallback", decision)
	}
}
