package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// TestGuardianSessionRunnerSummaryFreeResetInvalidatesSession mirrors Rust
// #45413: a destructive parent history replacement without a compaction
// checkpoint starts a new Guardian trunk instead of reusing the pre-reset
// reviewer, so the fourth review carries neither the previous thread nor the
// prior rationale cursor.
func TestGuardianSessionRunnerSummaryFreeResetInvalidatesSession(t *testing.T) {
	agent := &guardianRecordingAgent{}
	runner := &guardianSessionRunner{agent: agent}

	// Attribution is captured before each review runs, like
	// newGuardianReviewAttribution in Review.
	first := &model.AgentRequest{Prompt: "first", TaskKind: model.AgentTaskReview}
	if kind, prior := runner.reviewSessionAttribution(); kind != "trunk_new" || prior == nil || *prior {
		t.Fatalf("first attribution = %q/%v, want trunk_new without prior context", kind, prior)
	}
	if _, err := runner.Run(context.Background(), first); err != nil {
		t.Fatalf("first review error = %v", err)
	}
	runner.markPriorReview()

	second := &model.AgentRequest{Prompt: "second", TaskKind: model.AgentTaskReview}
	if kind, prior := runner.reviewSessionAttribution(); kind != "trunk_reused" || prior == nil || !*prior {
		t.Fatalf("second attribution = %q/%v, want trunk_reused with prior context", kind, prior)
	}
	if _, err := runner.Run(context.Background(), second); err != nil {
		t.Fatalf("second review error = %v", err)
	}
	runner.markPriorReview()

	runner.ResetForParentHistoryReset()
	if kind, prior := runner.reviewSessionAttribution(); kind != "trunk_new" || prior == nil || *prior {
		t.Fatalf("post-reset attribution = %q/%v, want a fresh trunk", kind, prior)
	}
	third := &model.AgentRequest{Prompt: "third", TaskKind: model.AgentTaskReview}
	if _, err := runner.Run(context.Background(), third); err != nil {
		t.Fatalf("post-reset review error = %v", err)
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.requests) != 3 {
		t.Fatalf("review requests = %d, want 3", len(agent.requests))
	}
	if got := agent.requests[1].PreviousResponseID; got != "resp-1" {
		t.Fatalf("second request previous = %q, want the reused trunk", got)
	}
	if got := agent.requests[2].PreviousResponseID; got != "" {
		t.Fatalf("post-reset request previous = %q, want no pre-reset continuation", got)
	}
	if completed := runner.completedReviewCount(); completed != 0 {
		t.Fatalf("completed review count = %d, want a reset counter", completed)
	}
}

// TestRuntimeRouterColdResumeResetsGuardianSessionLikeRust checks the Go
// carrier for Rust's reset version: a cold resume replays the thread history
// (HistoryReplacement::Reset), so the reusable reviewer must not continue the
// pre-reset chain.
func TestRuntimeRouterColdResumeResetsGuardianSessionLikeRust(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	threadID := session.ThreadID("thread-guardian-reset")
	store := session.NewStore(filepath.Join(home, "sessions"))
	threadRouter := NewRouter(store)
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), SessionPrefix: session.PrefixForSessionID(string(threadID))},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	runner := &guardianSessionRunner{previous: "pre-reset-response", priorReview: true}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:     threadRouter,
		Config:           config.NewConfigService(home),
		GuardianReviewer: &modelGuardianReviewer{agent: runner},
	})
	resumed := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:     string(threadID),
		ExcludeTurns: true,
	}))
	if resumed.Error != nil {
		t.Fatalf("resume error = %+v", resumed.Error)
	}
	if kind, prior := runner.reviewSessionAttribution(); kind != "trunk_new" || prior == nil || *prior {
		t.Fatalf("attribution after cold resume = %q/%v, want a fresh trunk", kind, prior)
	}
	request := &model.AgentRequest{Prompt: "review"}
	runner.SeedForReview(request)
	if request.PreviousResponseID != "" {
		t.Fatalf("PreviousResponseID = %q, want no pre-reset continuation", request.PreviousResponseID)
	}
}

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

func TestGuardianParentCompactionUsesEncryptedReplayCacheBudgetLikeRust(t *testing.T) {
	newEncrypted := func(encoded int) *compact.Result {
		raw, err := json.Marshal(map[string]any{
			"type":              "compaction",
			"id":                "cmp_encrypted",
			"encrypted_content": strings.Repeat("a", encoded),
		})
		if err != nil {
			t.Fatal(err)
		}
		return &compact.Result{
			ResponseID: "cmp-encrypted",
			NewHistory: []compact.Item{{ID: "cmp_encrypted", Type: "compaction", Raw: raw}},
		}
	}

	// 18k ciphertext bytes estimate about 1.5k tokens, not 4.5k summary bytes.
	if got := guardianParentCompactionResponseID(newEncrypted(18_000), 7_000); got != "cmp-encrypted" {
		t.Fatalf("cached compaction response id = %q, want reuse", got)
	}
	if got := guardianParentCompactionResponseID(newEncrypted(18_000), 1_000); got != "" {
		t.Fatalf("token-oversized compaction response id = %q, want fail closed", got)
	}
	wireOversized := newEncrypted(guardianParentCompactionMaxWireBytes)
	if got := guardianParentCompactionResponseID(wireOversized, 100_000); got != "" {
		t.Fatalf("wire-oversized compaction response id = %q, want fail closed", got)
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

func TestGuardianParentCompactionSeedRequiresMatchingProvenanceLikeRust(t *testing.T) {
	testCases := []struct {
		name         string
		reuse        bool
		compacted    string
		current      string
		wantReusable bool
	}{
		{name: "matching", reuse: true, compacted: "hash-a", current: "hash-a", wantReusable: true},
		{name: "trimmed match", reuse: true, compacted: " hash-a ", current: "hash-a", wantReusable: true},
		{name: "mismatched", reuse: true, compacted: "hash-a", current: "hash-b", wantReusable: false},
		{name: "missing producer", reuse: true, compacted: "", current: "hash-a", wantReusable: false},
		{name: "missing current", reuse: true, compacted: "hash-a", current: "", wantReusable: false},
		{name: "both missing", reuse: true, compacted: "", current: "", wantReusable: false},
		{name: "reuse disabled", reuse: false, compacted: "hash-a", current: "hash-a", wantReusable: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := guardianParentCompactionSeedAllowed(testCase.reuse, testCase.compacted, testCase.current); got != testCase.wantReusable {
				t.Fatalf("guardianParentCompactionSeedAllowed() = %v, want %v", got, testCase.wantReusable)
			}
		})
	}
}
