package model

import (
	"strings"
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

// Rust strips the unified-budget compatibility hint before a Responses Lite
// request (client_common.rs normalize_image_detail: lite clears every image
// detail). The prepared request copy keeps the hint for accounting, so the
// stored items are unchanged.
func TestResponsesLiteStripsUnifiedImageBudgetDetailHintLikeRust(t *testing.T) {
	prepared := prepareResponseInputImagesWithStoreMode(attachmentStoreTestItems(), nil, eventmap.ImagePrepOrigin{}, eventmap.ImagePrepModeUnifiedBudget)
	if detail := imagePrepTestBlockDetail(t, prepared); detail != "original" {
		t.Fatalf("prepared image detail = %q, want original", detail)
	}
	lite := responsesLiteDeclaredInputItems(prepared)
	if len(lite) != 1 {
		t.Fatalf("lite items = %#v", lite)
	}
	message, _ := lite[0].(map[string]any)
	content, _ := message["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("lite content = %#v", message)
	}
	block, _ := content[0].(map[string]any)
	if _, ok := block["detail"]; ok {
		t.Fatalf("lite image still carries detail: %#v", block)
	}
	if url, _ := block["image_url"].(string); !strings.HasPrefix(url, "data:image/") {
		t.Fatalf("lite image url = %q, want data URL", url)
	}
	if detail := imagePrepTestBlockDetail(t, prepared); detail != "original" {
		t.Fatalf("responses lite mutated the prepared items: %q", detail)
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
