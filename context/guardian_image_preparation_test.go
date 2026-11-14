package context

import (
	"reflect"
	"testing"
)

// Rust #61185 normalizes only new images before deduplication. This evidence
// representation already omits detail, so preparing a request must preserve
// its source snapshot while filtering previously admitted references.
func TestGuardianNewImagesHaveNoDetailAndPreserveEvidence(t *testing.T) {
	evidence := &NodeReplReviewEvidence{}
	evidence.RecordMultimodal("js", "cell", "call", []string{"evidence"}, []string{"image-old", "image-new", "image-new"})
	fragment := evidence.SnapshotSince(0)
	before := fragment.MultimodalInputItems()
	ledger := NewGuardianAdmittedImages()
	ledger.Admit(GuardianImageRef{Source: GuardianImageSourceInline, Ref: "image-old"})
	refs := ledger.RetainNewImages(fragment.ImageRefs(), nil)
	items := fragment.MultimodalInputItemsFiltered(func(ref GuardianImageRef) bool {
		for _, candidate := range refs {
			if ref == candidate {
				return true
			}
		}
		return false
	})
	count := 0
	for _, item := range items {
		if item["type"] != "image" {
			continue
		}
		count++
		if item["image_url"] != "image-new" {
			t.Fatalf("unexpected image: %#v", item)
		}
		if _, ok := item["detail"]; ok {
			t.Fatal("new image carries detail")
		}
		item["detail"] = "original"
	}
	if count != 1 {
		t.Fatalf("new images=%d, want 1", count)
	}
	if !reflect.DeepEqual(before, fragment.MultimodalInputItems()) {
		t.Fatal("request preparation mutated evidence snapshot")
	}
	if ledger.Admitted(GuardianImageRef{Source: GuardianImageSourceInline, Ref: "image-new"}) {
		t.Fatal("preparation committed unadmitted image")
	}
}
