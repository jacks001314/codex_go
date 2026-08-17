package appserver

import (
	"context"
	"strings"
	"sync"
	"testing"

	"codex_go/compact"
	"codex_go/model"
)

func TestGuardianSessionRunnerResetAfterParentCompaction(t *testing.T) {
	agent := &guardianRecordingAgent{}
	runner := &guardianSessionRunner{agent: agent}
	// Seed a compaction response ID.
	runner.ResetAfterParentCompaction("compaction-1")
	request := &model.AgentRequest{Prompt: "review"}
	runner.SeedForReview(request)
	if request.PreviousResponseID != "compaction-1" {
		t.Fatalf("PreviousResponseID = %q, want compaction-1", request.PreviousResponseID)
	}
	// A second review continues the session from the compaction.
	request = &model.AgentRequest{Prompt: "review-2"}
	runner.SeedForReview(request)
	if request.PreviousResponseID != "compaction-1" {
		t.Fatalf("PreviousResponseID = %q, want still compaction-1", request.PreviousResponseID)
	}
	// Empty compaction keeps the existing reviewer context.
	runner.ResetAfterParentCompaction("")
	request = &model.AgentRequest{Prompt: "review-3"}
	runner.SeedForReview(request)
	if request.PreviousResponseID != "compaction-1" {
		t.Fatalf("PreviousResponseID = %q, want preserved after empty compaction", request.PreviousResponseID)
	}
}

func TestGuardianSessionRunnerNewCompactionReplacesSeed(t *testing.T) {
	agent := &guardianRecordingAgent{}
	runner := &guardianSessionRunner{agent: agent}
	runner.ResetAfterParentCompaction("compaction-1")
	runner.ResetAfterParentCompaction("compaction-2")
	request := &model.AgentRequest{Prompt: "review"}
	runner.SeedForReview(request)
	if request.PreviousResponseID != "compaction-2" {
		t.Fatalf("PreviousResponseID = %q, want compaction-2", request.PreviousResponseID)
	}
}

func TestGuardianSessionRunnerRunsWithSeed(t *testing.T) {
	agent := &guardianRecordingAgent{}
	runner := &guardianSessionRunner{agent: agent}
	runner.ResetAfterParentCompaction("compaction-1")
	request := &model.AgentRequest{Prompt: "review", TaskKind: model.AgentTaskReview}
	runner.SeedForReview(request)
	response, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || response.ResponseID != "resp-1" {
		t.Fatalf("response = %+v", response)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.requests) != 1 || agent.requests[0].PreviousResponseID != "compaction-1" {
		t.Fatalf("previous = %q, want seeded compaction-1", agent.requests[0].PreviousResponseID)
	}
}

// TestGuardianParentCompactionResponseIDBoundsOversizedSummary mirrors Rust
// encrypted_parent_compaction_rejects_oversized_latest_item (#38980): the
// latest compaction is reused only when its complete serialized item fits
// within the configured max_parent_compaction_tokens bound; oversized items
// (including exact-boundary failures at +1 byte) fail closed instead of
// resurrecting older context.
func TestGuardianParentCompactionResponseIDBoundsOversizedSummary(t *testing.T) {
	const maxTokens = 256
	maxBytes := maxTokens * 4

	bounded := &compact.Result{ResponseID: "cmp-bounded", Summary: strings.Repeat("a", maxBytes)}
	if got := guardianParentCompactionResponseID(bounded, maxTokens); got != "cmp-bounded" {
		t.Fatalf("exact-boundary response id = %q, want reuse", got)
	}

	oversized := &compact.Result{ResponseID: "cmp-oversized", Summary: strings.Repeat("a", maxBytes+1)}
	if got := guardianParentCompactionResponseID(oversized, maxTokens); got != "" {
		t.Fatalf("oversized response id = %q, want fail closed", got)
	}

	if got := guardianParentCompactionResponseID(&compact.Result{ResponseID: "x"}, 0); got != "x" {
		t.Fatalf("zero config must fall back to the default bound; got %q", got)
	}
	big := &compact.Result{ResponseID: "big", Summary: strings.Repeat("b", 25_000*4+1)}
	if got := guardianParentCompactionResponseID(big, 0); got != "" {
		t.Fatalf("oversized default-bound response id = %q, want fail closed", got)
	}
}

type guardianRecordingAgent struct {
	mu       sync.Mutex
	requests []*model.AgentRequest
}

func (a *guardianRecordingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	return &model.AgentResponse{ResponseID: "resp-1"}, nil
}

func TestResetGuardianMessageText(t *testing.T) {
	if !strings.Contains("reuse encrypted parent compaction when restarting Guardian review sessions", "encrypted parent compaction") {
		t.Fatal("missing feature description")
	}
}
