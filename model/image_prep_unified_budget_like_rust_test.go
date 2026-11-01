package model

import (
	"testing"

	"codex_go/eventmap"
)

// Rust keeps an original-detail compatibility hint on images prepared under the
// unified image budget (image_preparation.rs resize_image) and strips it before
// a Responses Lite request; the detail hint itself no longer selects the budget.
func TestRequestImagePrepUsesUnifiedBudgetModeLikeRust(t *testing.T) {
	unified := prepareResponseInputImagesWithStoreMode(attachmentStoreTestItems(), nil, eventmap.ImagePrepOrigin{}, eventmap.ImagePrepModeUnifiedBudget)
	if detail := imagePrepTestBlockDetail(t, unified); detail != "original" {
		t.Fatalf("unified request image detail = %q, want original", detail)
	}
	legacy := prepareResponseInputImagesWithStoreMode(attachmentStoreTestItems(), nil, eventmap.ImagePrepOrigin{}, eventmap.ImagePrepModeDetailBased)
	if detail := imagePrepTestBlockDetail(t, legacy); detail != "high" {
		t.Fatalf("detail-based request image detail = %q, want high", detail)
	}
	if got := responsesImagePrepMode(&AgentRequest{UnifiedImageBudget: true}); got != eventmap.ImagePrepModeUnifiedBudget {
		t.Fatalf("responsesImagePrepMode(unified) = %v", got)
	}
	if got := responsesImagePrepMode(&AgentRequest{}); got != eventmap.ImagePrepModeDetailBased {
		t.Fatalf("responsesImagePrepMode(default) = %v", got)
	}
}

func imagePrepTestBlockDetail(t *testing.T, items []any) string {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	message, _ := items[0].(map[string]any)
	content, _ := message["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %#v", message)
	}
	block, _ := content[0].(map[string]any)
	detail, _ := block["detail"].(string)
	return detail
}
