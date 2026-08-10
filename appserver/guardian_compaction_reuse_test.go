package appserver

import (
	"context"
	"strings"
	"sync"
	"testing"

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
