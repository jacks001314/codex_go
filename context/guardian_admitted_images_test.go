package context

import "testing"

// Mirrors the shared-deduplication half of guardian-context/src/images.rs
// (#54278, enforcement_tests.rs::image_accounting_preserves_later_eviction_policy):
// a host-prepared reference matches delivered history even when the source detail
// differs, because the compared identity carries no detail.
func TestRetainNewGuardianImagesDeduplicatesPreparedReferences(t *testing.T) {
	// The host prepared (for example resized) the inline image, so the delivered
	// reference is the prepared data URL rather than the original one.
	delivered := []GuardianImageRef{{Source: GuardianImageSourceInline, Ref: "prepared-image"}}
	candidates := []GuardianImageRef{
		{Source: GuardianImageSourceInline, Ref: "original-image"},
		{Source: GuardianImageSourceFile, Ref: "file-1"},
	}
	prepare := func(ref GuardianImageRef) (GuardianImageRef, bool) {
		if ref.Source == GuardianImageSourceInline {
			return GuardianImageRef{Source: GuardianImageSourceInline, Ref: "prepared-image"}, true
		}
		return ref, true
	}
	kept := RetainNewGuardianImages(candidates, delivered, prepare)
	if len(kept) != 1 || kept[0] != (GuardianImageRef{Source: GuardianImageSourceFile, Ref: "file-1"}) {
		t.Fatalf("kept = %#v, want the undelivered file reference", kept)
	}
	// Once the file reference is admitted too, nothing is new.
	admitted := append(append([]GuardianImageRef(nil), delivered...), kept...)
	if again := RetainNewGuardianImages(candidates, admitted, prepare); again != nil {
		t.Fatalf("second pass = %#v, want nothing", again)
	}
	// A host that rejects an image keeps it out of the review even before delivery.
	reject := func(GuardianImageRef) (GuardianImageRef, bool) { return GuardianImageRef{}, false }
	if got := RetainNewGuardianImages([]GuardianImageRef{{Source: GuardianImageSourceFile, Ref: "file-2"}}, nil, reject); got != nil {
		t.Fatalf("rejected candidate = %#v, want nothing", got)
	}
	// Empty references carry no identity, so they never match admitted history.
	if got := RetainNewGuardianImages([]GuardianImageRef{{Source: GuardianImageSourceInline, Ref: ""}}, delivered, nil); got != nil {
		t.Fatalf("empty reference = %#v, want nothing", got)
	}
}

// Mirrors the LazyCell half of guardian-context/src/images.rs (#54278): an
// image-free continuation must not scan retained history (or hash screenshot
// bytes), so no candidate is ever prepared against an empty image set.
func TestGuardianAdmittedImagesSkipsHistoryWhenNoImages(t *testing.T) {
	ledger := NewGuardianAdmittedImages()
	ledger.Admit(GuardianImageRef{Source: GuardianImageSourceInline, Ref: "data:image/png;base64,AAAA"})
	prepared := 0
	kept := ledger.RetainNewImages(nil, func(ref GuardianImageRef) (GuardianImageRef, bool) {
		prepared++
		return ref, true
	})
	if kept != nil || prepared != 0 {
		t.Fatalf("image-free continuation = %#v (prepared %d), want no scan", kept, prepared)
	}
	// The same reference is admitted, so a later review of the same evidence keeps
	// nothing; an unadmitted reference still passes through.
	admittedRef := GuardianImageRef{Source: GuardianImageSourceInline, Ref: "data:image/png;base64,AAAA"}
	if got := ledger.RetainNewImages([]GuardianImageRef{admittedRef}, nil); got != nil {
		t.Fatalf("admitted image = %#v, want nothing", got)
	}
	fresh := GuardianImageRef{Source: GuardianImageSourceFile, Ref: "file-9"}
	got := ledger.RetainNewImages([]GuardianImageRef{fresh}, nil)
	if len(got) != 1 || got[0] != fresh {
		t.Fatalf("fresh image = %#v", got)
	}
	ledger.Admit(got...)
	if !ledger.Admitted(fresh) {
		t.Fatalf("ledger did not admit %#v", fresh)
	}
	if ledger.Admitted(GuardianImageRef{Source: GuardianImageSourceFile, Ref: "never-seen"}) {
		t.Fatalf("ledger admitted an unseen reference")
	}
}

// The fragment filter is the injection point Rust #54278 deduplicates at: it must
// keep the text evidence, keep only the accepted images, and treat a nil filter
// as the pre-#54278 behavior used by MultimodalInputItems (Rust #38454).
func TestNodeReplReviewEvidenceFiltersImagesWithoutDroppingText(t *testing.T) {
	evidence := &NodeReplReviewEvidence{}
	evidence.RecordMultimodal("js", "cell-1", "call-1", []string{"evidence text"}, []string{"image-a", "image-b"})
	fragment := evidence.SnapshotSince(0)
	if fragment == nil {
		t.Fatal("fragment is nil")
	}
	if refs := fragment.ImageRefs(); len(refs) != 2 || refs[0].Ref != "image-a" || refs[1].Ref != "image-b" ||
		refs[0].Source != GuardianImageSourceInline {
		t.Fatalf("ImageRefs() = %#v", refs)
	}
	if all := fragment.MultimodalInputItems(); len(all) != 3 {
		t.Fatalf("unfiltered items = %#v", all)
	}
	filtered := fragment.MultimodalInputItemsFiltered(func(ref GuardianImageRef) bool { return ref.Ref == "image-b" })
	if len(filtered) != 2 || filtered[0]["type"] != "text" || filtered[1]["image_url"] != "image-b" {
		t.Fatalf("filtered items = %#v", filtered)
	}
	none := fragment.MultimodalInputItemsFiltered(func(GuardianImageRef) bool { return false })
	if len(none) != 1 || none[0]["type"] != "text" {
		t.Fatalf("fully deduplicated items = %#v", none)
	}
}
