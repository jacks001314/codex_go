package appserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/session"
)

// TestAttachedForkKeepsInheritedPreviewLikeRust mirrors Rust #60559: a
// reference-backed fork has no local opening user message, yet its inherited
// preview must survive thread listings and later rollout observations.
func TestAttachedForkKeepsInheritedPreviewLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	const (
		parentID     = "0198f006-0000-7000-8000-000000000001"
		parentTurnID = "0198f006-0000-7000-8000-0000000000t1"
		preview      = "Saved user message"
	)
	parent := h.createPaginatedThreadWithTurn(t, parentID, t.TempDir(), parentTurnID, preview)

	listed := func(id int64, threadID string) string {
		t.Helper()
		response := h.router.Handle(requestWithParams(t, IntID(id), MethodThreadList, ThreadListParams{UseStateDBOnly: true}))
		if response.Error != nil {
			t.Fatalf("thread/list error = %+v", response.Error)
		}
		for _, item := range response.Result.(*ThreadListResponse).Data {
			if item.ID == threadID {
				return item.Preview
			}
		}
		return ""
	}
	if got := listed(0, string(parent.ID)); got != preview {
		t.Fatalf("parent listing preview = %q, want %q", got, preview)
	}

	fork := h.router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID: string(parent.ID),
	}))
	if fork.Error != nil {
		t.Fatalf("attached thread/fork error = %+v", fork.Error)
	}
	child := fork.Result.(*ThreadForkResponse).Thread
	if child == nil || strings.TrimSpace(child.Preview) != preview {
		t.Fatalf("fork preview = %+v, want %q", child, preview)
	}
	if got := listed(2, child.ID); got != preview {
		t.Fatalf("listing preview before the first local turn = %q, want %q", got, preview)
	}

	record, err := h.store.Read(session.ThreadID(child.ID), true, true)
	if err != nil {
		t.Fatalf("store.Read(%s) error = %v", child.ID, err)
	}
	path, _ := record.Metadata.Extra["rollout_path"].(string)

	// A later rollout observation that carries no preview must preserve the
	// inherited one instead of clearing it.
	if err := h.state.ReconcileRollout(context.Background(), path, false); err != nil {
		t.Fatalf("ReconcileRollout(%s) error = %v", path, err)
	}
	if got := listed(3, child.ID); got != preview {
		t.Fatalf("listing preview after the rollout observation = %q, want %q", got, preview)
	}
}
