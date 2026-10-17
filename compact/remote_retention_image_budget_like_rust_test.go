package compact

import (
	"strings"
	"testing"
)

func remoteImageBudgetMessageForTest(id string, text string) Item {
	return Item{
		ID: id, Type: "message", Role: "user", Kind: "user_message", Text: text,
		Content: []ContentPart{{Type: "input_text", Text: text}},
	}
}

func remoteImageBudgetImageMessageForTest(id string, text string) Item {
	parts := []ContentPart{
		{Type: "input_text", Text: `<image name=[Image #1] path="C:\shots\a.png">`},
		{Type: "input_image", ImageURL: "data:image/png;base64,AAAA", Detail: stringPtrForImageBudgetTest("auto")},
		{Type: "input_text", Text: "</image>"},
	}
	if text != "" {
		parts = append(parts, ContentPart{Type: "input_text", Text: text})
	}
	return Item{
		ID: id, Type: "message", Role: "user", Kind: "user_message", Text: text, Content: parts,
	}
}

func stringPtrForImageBudgetTest(value string) *string { return &value }

// TestRemoteRetentionImageBudgetChargesImagesLikeRust pins Rust #40280: the
// opt-in feature charges retained images, while the disabled path keeps the
// existing text-only accounting.
func TestRemoteRetentionImageBudgetChargesImagesLikeRust(t *testing.T) {
	items := []Item{remoteImageBudgetImageMessageForTest("image", "short")}

	disabled := retainedMessagesForRemoteCompaction(items, 100, retentionPolicy{})
	if len(disabled) != 1 || retainedMessageImageCount(disabled[0]) != 1 {
		t.Fatalf("disabled retained = %#v, want the uncharged image message", disabled)
	}

	enabled := retainedMessagesForRemoteCompaction(items, 100, retentionPolicy{imageBudget: true})
	// The boundary walk keeps later text and drops the oversized image group.
	if len(enabled) != 1 || retainedMessageImageCount(enabled[0]) != 0 || enabled[0].Text != "short" {
		t.Fatalf("enabled retained = %#v, want the image dropped and text kept", enabled)
	}
}

// TestRemoteRetentionKeepsImageLabelsAtomicLikeRust pins the whole-entry
// behavior: the image and both labels stay together when they fit.
func TestRemoteRetentionKeepsImageLabelsAtomicLikeRust(t *testing.T) {
	message := remoteImageBudgetImageMessageForTest("image", strings.Repeat("w", 400))
	retained := retainedMessagesForRemoteCompaction([]Item{message}, 2200, retentionPolicy{imageBudget: true})
	if len(retained) != 1 {
		t.Fatalf("retained = %#v", retained)
	}
	parts := retained[0].Content
	if len(parts) != 4 {
		t.Fatalf("parts = %#v, want open label, image, close label, text", parts)
	}
	if parts[0].Text != `<image name=[Image #1] path="C:\shots\a.png">` ||
		parts[1].Type != "input_image" || parts[2].Text != "</image>" {
		t.Fatalf("image group = %#v", parts[:3])
	}
}

// TestRemoteRetentionStopsBackfillAfterBoundaryImageLikeRust pins that an image
// at the truncation boundary which does not fit leaves the remaining budget
// unused instead of retaining older messages.
func TestRemoteRetentionStopsBackfillAfterBoundaryImageLikeRust(t *testing.T) {
	items := []Item{
		remoteImageBudgetMessageForTest("older", "older message"),
		remoteImageBudgetImageMessageForTest("image", ""),
	}
	retained := retainedMessagesForRemoteCompaction(items, 1000, retentionPolicy{imageBudget: true})
	if len(retained) != 0 {
		t.Fatalf("retained = %#v, want no backfill from older messages", retained)
	}
}

func TestResolveRetentionPolicyReadsImageBudgetFeatureLikeRust(t *testing.T) {
	if policy := resolveRetentionPolicy(map[string]bool{CompactionImageBudgetKey: true}, false, false); !policy.imageBudget {
		t.Fatal("image budget feature was not resolved")
	}
	if policy := resolveRetentionPolicy(map[string]bool{CompactionImageBudgetKey: false}, false, false); policy.imageBudget {
		t.Fatal("image budget was not disabled by an explicit false")
	}
}
