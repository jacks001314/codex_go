package appserver

import (
	"context"
	"strings"
	"testing"

	codexctx "codex_go/context"
	"codex_go/state"
)

// Mirrors Rust #57494 prepare_sync_images: images are admitted only when the
// prompt fits the fixed 10,000-token item budget and the review model's
// remaining context window.
func TestGuardianReviewDropsImagesBeyondContextWindowLikeRust(t *testing.T) {
	evidence := &codexctx.NodeReplReviewEvidence{}
	evidence.RecordMultimodal("js", "cell-1", "call-1", []string{"evidence"}, []string{"image-a"})
	reviewer, requests := guardianEvidenceReviewer(evidence)
	reviewer.syncImageContextWindow = func(_, _ string) (int64, bool) { return 1, true }
	action := state.Action{Type: "command", Command: "ls", CWD: "/repo"}
	if _, _, err := reviewer.Review(context.Background(), "thread-image-window", "turn-1", "call-1", action); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if got := imageInputItems((*requests)[0].InputItems); len(got) != 0 {
		t.Fatalf("images admitted beyond the context window: %#v", got)
	}
}

func TestGuardianImageAdmissionBudgetLikeRust(t *testing.T) {
	if guardianAdmitImages(guardianMaxImagePromptTokens+1, 0, 1<<30, true) {
		t.Fatal("prompt above the image item budget was admitted")
	}
	if guardianAdmitImages(1_000, 5_000, 5_000, true) {
		t.Fatal("prompt plus reviewer history above the context window was admitted")
	}
	if !guardianAdmitImages(1_000, 1_000, 5_000, true) {
		t.Fatal("prompt within the context window was rejected")
	}
	if got := guardianInputItemsTokenCount([]any{strings.Repeat("x", 40_000)}); got <= guardianMaxImagePromptTokens {
		t.Fatalf("token estimate = %d, want above the image budget", got)
	}
}

func TestGuardianReviewAdmitsImagesWithinBudgetLikeRust(t *testing.T) {
	evidence := &codexctx.NodeReplReviewEvidence{}
	evidence.RecordMultimodal("js", "cell-1", "call-1", []string{"evidence"}, []string{"image-a"})
	reviewer, requests := guardianEvidenceReviewer(evidence)
	reviewer.syncImageContextWindow = func(_, _ string) (int64, bool) { return 1 << 30, true }
	action := state.Action{Type: "command", Command: "ls", CWD: "/repo"}
	if _, _, err := reviewer.Review(context.Background(), "thread-image-fit", "turn-1", "call-1", action); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if got := imageInputItems((*requests)[0].InputItems); len(got) != 1 {
		t.Fatalf("images within budget were dropped: %#v", got)
	}
}
