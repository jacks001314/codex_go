package prompt

import (
	"strings"
	"testing"
)

func TestContinuationEscapesAndComputesBudget(t *testing.T) {
	budget := int64(100)
	prompt := Continuation(&Goal{Objective: "fix <x> & y", TokenBudget: &budget, TokensUsed: 40})
	if !strings.Contains(prompt, "fix &lt;x&gt; &amp; y") {
		t.Fatalf("objective not escaped: %s", prompt)
	}
	if !strings.Contains(prompt, "<remaining_tokens>60</remaining_tokens>") {
		t.Fatalf("remaining budget not rendered: %s", prompt)
	}
}

func TestBudgetLimit(t *testing.T) {
	prompt := BudgetLimit(&Goal{Objective: "ship", TokensUsed: 11, TimeUsedSeconds: 22})
	if !strings.Contains(prompt, "<token_budget>none</token_budget>") || !strings.Contains(prompt, "<time_used_seconds>22</time_used_seconds>") {
		t.Fatalf("unexpected prompt: %s", prompt)
	}
}

func TestObjectiveUpdatedUnbounded(t *testing.T) {
	prompt := ObjectiveUpdated(&Goal{Objective: "new"})
	if !strings.Contains(prompt, "<remaining_tokens>unbounded</remaining_tokens>") {
		t.Fatalf("unexpected prompt: %s", prompt)
	}
}
