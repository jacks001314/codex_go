package appserver

import (
	"fmt"
	"strings"
	"testing"
)

func analyticsTurnTestKey(threadID string, turnID string) string {
	key, _ := analyticsTurnKey(threadID, turnID)
	return key
}

func retainAnalyticsTurnForTest(router *RuntimeRouter, threadID string, turnID string) {
	router.rememberSampledToolCalls(threadID, turnID, "resp-"+turnID, []string{"call-" + turnID})
	router.recordToolItemReviewSummary(threadID, turnID, "item-"+turnID, toolItemReviewSummary{
		ReviewCount:          1,
		UserReviewCount:      1,
		FinalApprovalOutcome: "approved",
	})
	router.rememberMCPToolCallElicitation(threadID, turnID, "item-"+turnID, "approval")
}

// TestRetainedAnalyticsTurnsEvictOldestLikeRust covers #55847: the router keeps
// at most MAX_PENDING_TURNS turns of analytics evidence, and eviction releases
// the oldest turn's tool, review and elicitation state while newer turns keep
// reporting.
func TestRetainedAnalyticsTurnsEvictOldestLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID = "thread-retained"

	retainAnalyticsTurnForTest(router, threadID, "turn-oldest")
	for index := 0; index < maxRetainedAnalyticsTurns; index++ {
		retainAnalyticsTurnForTest(router, threadID, fmt.Sprintf("turn-%03d", index))
	}

	if got, want := len(router.retainedAnalyticsTurns), maxRetainedAnalyticsTurns; got != want {
		t.Fatalf("retained turns = %d, want %d", got, want)
	}
	evictedKey := analyticsTurnTestKey(threadID, "turn-oldest")
	router.sampledToolCallsMu.Lock()
	_, sampled := router.sampledToolCalls[evictedKey]
	router.sampledToolCallsMu.Unlock()
	if sampled {
		t.Fatal("evicted turn kept its sampled tool calls")
	}
	router.approvalSessionsMu.RLock()
	_, evictedReview := router.toolItemReviews[evictedKey+"\x00item-turn-oldest"]
	router.approvalSessionsMu.RUnlock()
	if evictedReview {
		t.Fatal("evicted turn kept its review summary")
	}
	for _, pending := range router.pendingMCPElicitations {
		if pending.turnID == "turn-oldest" {
			t.Fatal("evicted turn kept its pending MCP elicitation")
		}
	}

	newestTurn := fmt.Sprintf("turn-%03d", maxRetainedAnalyticsTurns-1)
	newestKey := analyticsTurnTestKey(threadID, newestTurn)
	router.sampledToolCallsMu.Lock()
	_, newestSampled := router.sampledToolCalls[newestKey]
	router.sampledToolCallsMu.Unlock()
	if !newestSampled {
		t.Fatal("newest turn lost its sampled tool calls")
	}
	router.approvalSessionsMu.RLock()
	_, newestReview := router.toolItemReviews[newestKey+"\x00item-"+newestTurn]
	router.approvalSessionsMu.RUnlock()
	if !newestReview {
		t.Fatal("newest turn lost its review summary")
	}

	retainAnalyticsTurnForTest(router, threadID, "turn-after-eviction")
	afterKey := analyticsTurnTestKey(threadID, "turn-after-eviction")
	router.sampledToolCallsMu.Lock()
	_, afterSampled := router.sampledToolCalls[afterKey]
	router.sampledToolCallsMu.Unlock()
	if !afterSampled {
		t.Fatal("a turn created after eviction did not retain its evidence")
	}
}

// TestClosedThreadDropsAnalyticsTurnsLikeRust covers the ThreadClosed half of
// #55847: closing a thread releases its turn, review and elicitation state,
// including reviews whose turn never completed, without touching other threads.
func TestClosedThreadDropsAnalyticsTurnsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const closedThread = "thread-closed"
	const liveThread = "thread-live"

	retainAnalyticsTurnForTest(router, closedThread, "turn-1")
	retainAnalyticsTurnForTest(router, closedThread, "turn-never-completed")
	retainAnalyticsTurnForTest(router, liveThread, "turn-1")

	router.markThreadUnloaded(closedThread)

	closedPrefix := closedThread + "\x00"
	router.sampledToolCallsMu.Lock()
	for key := range router.sampledToolCalls {
		if strings.HasPrefix(key, closedPrefix) {
			router.sampledToolCallsMu.Unlock()
			t.Fatalf("closed thread kept sampled tool calls for %q", key)
		}
	}
	router.sampledToolCallsMu.Unlock()

	router.approvalSessionsMu.RLock()
	for key := range router.toolItemReviews {
		if strings.HasPrefix(key, closedPrefix) {
			router.approvalSessionsMu.RUnlock()
			t.Fatalf("closed thread kept review summary %q", key)
		}
	}
	router.approvalSessionsMu.RUnlock()

	for _, pending := range router.pendingMCPElicitations {
		if pending.threadID == closedThread {
			t.Fatalf("closed thread kept pending elicitation for turn %q", pending.turnID)
		}
	}
	for _, key := range router.retainedAnalyticsTurns {
		if strings.HasPrefix(key, closedPrefix) {
			t.Fatalf("closed thread kept retained turn %q", key)
		}
	}

	liveKey := analyticsTurnTestKey(liveThread, "turn-1")
	router.sampledToolCallsMu.Lock()
	_, liveSampled := router.sampledToolCalls[liveKey]
	router.sampledToolCallsMu.Unlock()
	if !liveSampled {
		t.Fatal("closing one thread dropped another thread's sampled tool calls")
	}
	router.approvalSessionsMu.RLock()
	_, liveReview := router.toolItemReviews[liveKey+"\x00item-turn-1"]
	router.approvalSessionsMu.RUnlock()
	if !liveReview {
		t.Fatal("closing one thread dropped another thread's review summary")
	}
}
