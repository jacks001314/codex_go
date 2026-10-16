package appserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	codexctx "codex_go/context"
	"codex_go/model"
	"codex_go/state"
)

// guardianEvidenceReviewer wires a reviewer whose node_repl evidence comes from
// one shared store, mirroring Rust's single reviewer session (#54278).
func guardianEvidenceReviewer(evidence *codexctx.NodeReplReviewEvidence) (*modelGuardianReviewer, *[]*model.AgentRequest) {
	requests := &[]*model.AgentRequest{}
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			*requests = append(*requests, request)
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
		nodeReplEvidence: func(threadID string, reviewedSequence uint64) *codexctx.NodeReplReviewEvidenceFragment {
			return evidence.SnapshotSince(reviewedSequence)
		},
	}
	return reviewer, requests
}

// Mirrors the shared image-deduplication half of Rust #54278
// (core/src/guardian/review_session.rs passing reviewer_history to
// ComposedContext::retain_new_images): only admitted history proves delivery, so
// a reviewer conversation is shown each image reference once even when newer
// evidence repeats it, while the newer evidence's text keeps flowing.
func TestGuardianReviewInjectsEachRetainedImageOnceLikeRust(t *testing.T) {
	evidence := &codexctx.NodeReplReviewEvidence{}
	evidence.RecordMultimodal("js", "cell-1", "call-1", []string{"first evidence"}, []string{"data:image/png;base64,AAAA"})
	reviewer, requests := guardianEvidenceReviewer(evidence)
	action := state.Action{Type: "command", Command: "ls", CWD: "/repo"}
	for i := 0; i < 2; i++ {
		if _, _, err := reviewer.Review(context.Background(), "thread-images", fmt.Sprintf("turn-%d", i), fmt.Sprintf("call-%d", i), action); err != nil {
			t.Fatalf("Review() #%d error = %v", i, err)
		}
		if i == 0 {
			// Newer evidence repeats the already delivered image reference.
			evidence.RecordMultimodal("js", "cell-2", "call-2", []string{"second evidence"}, []string{"data:image/png;base64,AAAA"})
		}
	}
	if len(*requests) != 2 {
		t.Fatalf("review requests = %d, want 2", len(*requests))
	}
	if got := imageInputItems((*requests)[0].InputItems); len(got) != 1 {
		t.Fatalf("first review image items = %#v, want the retained image", got)
	}
	if got := imageInputItems((*requests)[1].InputItems); len(got) != 0 {
		t.Fatalf("second review re-injected an admitted image: %#v", got)
	}
	if !strings.Contains(textInputItems((*requests)[1].InputItems), "second evidence") {
		t.Fatalf("second review lost the new evidence text: %#v", (*requests)[1].InputItems)
	}
}

// Mirrors the reviewer cursor of Rust #54278: the reviewer session keeps what it
// already admitted, so a later review carries only evidence admitted after it.
func TestGuardianReviewAdvancesEvidenceCursorLikeRust(t *testing.T) {
	evidence := &codexctx.NodeReplReviewEvidence{}
	evidence.Record("js", "cell-1", "call-1", []string{"first evidence"})
	reviewer, requests := guardianEvidenceReviewer(evidence)
	action := state.Action{Type: "command", Command: "ls", CWD: "/repo"}
	for i := 0; i < 2; i++ {
		if _, _, err := reviewer.Review(context.Background(), "thread-cursor", fmt.Sprintf("turn-%d", i), fmt.Sprintf("call-%d", i), action); err != nil {
			t.Fatalf("Review() #%d error = %v", i, err)
		}
		if i == 0 {
			evidence.Record("js", "cell-2", "call-2", []string{"second evidence"})
		}
	}
	if len(*requests) != 2 {
		t.Fatalf("review requests = %d, want 2", len(*requests))
	}
	if !strings.Contains((*requests)[0].Prompt, "first evidence") {
		t.Fatalf("first review prompt lost its evidence:\n%s", (*requests)[0].Prompt)
	}
	second := (*requests)[1].Prompt
	if !strings.Contains(second, "second evidence") {
		t.Fatalf("second review prompt lost the new evidence:\n%s", second)
	}
	if strings.Contains(second, "first evidence") {
		t.Fatalf("second review replayed already admitted evidence:\n%s", second)
	}
}

// Deduplication must not suppress evidence: a reference the ledger never admitted
// is still delivered (Rust #54278 retains everything else).
func TestGuardianReviewDeliversUnadmittedImagesLikeRust(t *testing.T) {
	evidence := &codexctx.NodeReplReviewEvidence{}
	evidence.RecordMultimodal("js", "cell-1", "call-1", []string{"first evidence"}, []string{"image-a"})
	reviewer, requests := guardianEvidenceReviewer(evidence)
	action := state.Action{Type: "command", Command: "ls", CWD: "/repo"}
	for i := 0; i < 2; i++ {
		if _, _, err := reviewer.Review(context.Background(), "thread-new-images", fmt.Sprintf("turn-%d", i), fmt.Sprintf("call-%d", i), action); err != nil {
			t.Fatalf("Review() #%d error = %v", i, err)
		}
		if i == 0 {
			evidence.RecordMultimodal("js", "cell-2", "call-2", []string{"second evidence"}, []string{"image-b"})
		}
	}
	if len(*requests) != 2 {
		t.Fatalf("review requests = %d, want 2", len(*requests))
	}
	second := imageInputItems((*requests)[1].InputItems)
	if len(second) != 1 || second[0].(map[string]any)["image_url"] != "image-b" {
		t.Fatalf("second review image items = %#v, want only the new image", second)
	}
}

func imageInputItems(items []any) []any {
	var images []any
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if ok && entry["type"] == "image" {
			images = append(images, entry)
		}
	}
	return images
}

func textInputItems(items []any) string {
	var builder strings.Builder
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || entry["type"] != "text" {
			continue
		}
		if text, ok := entry["text"].(string); ok {
			builder.WriteString(text)
		}
	}
	return builder.String()
}
