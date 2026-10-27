package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type guardianReviewTestExecutor struct {
	*ExecutorFunc
	optIn    bool
	executed int
}

func (e *guardianReviewTestExecutor) RequiresGuardianReview(*Invocation) bool {
	return e.optIn
}

func (e *guardianReviewTestExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	e.executed++
	return e.ExecutorFunc.Execute(ctx, invocation)
}

func guardianReviewTestInvocation(name string) *Invocation {
	return &Invocation{
		CallID:   "call-" + name,
		ToolName: PlainName(name),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"path":"/repo"}`},
	}
}

// Mirrors Rust #57433: the registry reviews an opted-in function call before
// dispatch and a rejection stops the handler from running.
func TestRouterReviewsOptedInCallBeforeDispatchLikeRust(t *testing.T) {
	registry := NewRegistry()
	reviewed := &guardianReviewTestExecutor{
		ExecutorFunc: NewExecutorFunc(Spec{Name: PlainName("reviewed")}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true}, nil
		}),
		optIn: true,
	}
	plain := &guardianReviewTestExecutor{
		ExecutorFunc: NewExecutorFunc(Spec{Name: PlainName("plain")}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true}, nil
		}),
	}
	for _, executor := range []*guardianReviewTestExecutor{reviewed, plain} {
		name := executor.Spec().Name
		if err := registry.Register(executor); err != nil {
			t.Fatalf("register %s: %v", name.Key(), err)
		}
	}
	router := NewRouter(registry)
	var reviewedCalls []string
	router.SetGuardianReview(func(_ context.Context, invocation *Invocation) error {
		reviewedCalls = append(reviewedCalls, invocation.CallID)
		return RespondToModel("review denied")
	})

	if _, err := router.Dispatch(context.Background(), guardianReviewTestInvocation("plain")); err != nil {
		t.Fatalf("plain dispatch error = %v", err)
	}
	if len(reviewedCalls) != 0 || plain.executed != 1 {
		t.Fatalf("plain call was reviewed or skipped: reviewed=%v executed=%d", reviewedCalls, plain.executed)
	}

	_, err := router.Dispatch(context.Background(), guardianReviewTestInvocation("reviewed"))
	var callErr *FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() || callErr.ModelMessage() != "review denied" {
		t.Fatalf("reviewed dispatch error = %v, want the model-facing rejection", err)
	}
	if len(reviewedCalls) != 1 || reviewedCalls[0] != "call-reviewed" {
		t.Fatalf("reviewed calls = %v, want the invocation", reviewedCalls)
	}
	if reviewed.executed != 0 {
		t.Fatalf("denied handler executed %d times", reviewed.executed)
	}
}

// Rust #57433: the review runs after pre-tool hooks, so it sees the rewritten
// arguments, and a denial is reported before the handler or tool-start
// notification runs.
func TestRouterReviewsRewrittenOptedInCallLikeRust(t *testing.T) {
	registry := NewRegistry()
	reviewed := &guardianReviewTestExecutor{
		ExecutorFunc: NewExecutorFunc(Spec{Name: PlainName("reviewed")}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true}, nil
		}),
		optIn: true,
	}
	if err := registry.Register(reviewed); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(registry)
	var reviewedArguments string
	router.SetGuardianReview(func(_ context.Context, invocation *Invocation) error {
		reviewedArguments = invocation.Payload.Arguments
		if !strings.Contains(reviewedArguments, "/root") {
			t.Fatalf("review saw %q, want the hook-rewritten arguments", reviewedArguments)
		}
		return RespondToModel("review denied")
	})
	started := false
	_, err := router.DispatchWithHooksAfterPreHooks(context.Background(), guardianReviewTestInvocation("reviewed"), &fakeToolHooks{
		pre: &PreToolUseHookOutcome{UpdatedInput: map[string]any{"path": "/root"}},
	}, func(*Invocation) {
		started = true
	})
	var callErr *FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() || callErr.ModelMessage() != "review denied" {
		t.Fatalf("dispatch error = %v, want the review rejection", err)
	}
	if reviewedArguments != `{"path":"/root"}` {
		t.Fatalf("reviewed arguments = %q", reviewedArguments)
	}
	if started {
		t.Fatal("tool start ran for a call the review rejected")
	}
	if reviewed.executed != 0 {
		t.Fatalf("denied handler executed %d times", reviewed.executed)
	}
}

type guardianReviewNestedTestExecutor struct {
	*ExecutorFunc
	review GuardianReviewFunc
}

func (e *guardianReviewNestedTestExecutor) SetNestedGuardianReview(review GuardianReviewFunc) {
	e.review = review
}

// Rust #57433: nested Code Mode dispatches share the registry review, so the
// wrapper cannot bypass a tool's opt-in.
func TestRouterPropagatesGuardianReviewToNestedCodeModeLikeRust(t *testing.T) {
	registry := NewRegistry()
	nested := &guardianReviewNestedTestExecutor{
		ExecutorFunc: NewExecutorFunc(Spec{Name: PlainName(CodeModeExecToolName)}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true}, nil
		}),
	}
	if err := registry.Register(nested); err != nil {
		t.Fatal(err)
	}
	review := func(context.Context, *Invocation) error { return nil }
	NewRouter(registry).SetGuardianReview(review)
	if nested.review == nil {
		t.Fatal("the Code Mode executor did not receive the guardian review")
	}
}
