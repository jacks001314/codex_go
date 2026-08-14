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
	if !strings.Contains(prompt, "- Tokens used: 40") || !strings.Contains(prompt, "- Token budget: 100") || !strings.Contains(prompt, "- Tokens remaining: 60") {
		t.Fatalf("budget not rendered: %s", prompt)
	}
}

func TestBudgetLimit(t *testing.T) {
	prompt := BudgetLimit(&Goal{Objective: "ship", TokensUsed: 11, TimeUsedSeconds: 22})
	if !strings.Contains(prompt, "- Token budget: none") || !strings.Contains(prompt, "- Time spent pursuing goal: 22 seconds") {
		t.Fatalf("unexpected prompt: %s", prompt)
	}
}

func TestObjectiveUpdatedUnbounded(t *testing.T) {
	prompt := ObjectiveUpdated(&Goal{Objective: "new"})
	if !strings.Contains(prompt, "- Tokens remaining: unknown") {
		t.Fatalf("unexpected prompt: %s", prompt)
	}
}
