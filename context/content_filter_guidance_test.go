package context

import "testing"

// Rust #49119 (upstream 8bd5a136ff, prompts/src/model_messages.rs
// `ResolvedModelMessages::content_filter_guidance`): a catalog value is used
// only when present, not blank, and at most 512 UTF-8 bytes.
func TestResolveContentFilterGuidanceLikeRust(t *testing.T) {
	override := "Offer a permitted alternative."
	if got := ResolveContentFilterGuidance(&override); got != override {
		t.Fatalf("override = %q", got)
	}
	blank := "   \n\t"
	if got := ResolveContentFilterGuidance(&blank); got != ContentFilterGuidanceDefault {
		t.Fatalf("blank = %q, want the bundled guidance", got)
	}
	empty := ""
	if got := ResolveContentFilterGuidance(&empty); got != ContentFilterGuidanceDefault {
		t.Fatalf("empty = %q, want the bundled guidance", got)
	}
	if got := ResolveContentFilterGuidance(nil); got != ContentFilterGuidanceDefault {
		t.Fatalf("nil = %q, want the bundled guidance", got)
	}
	atLimit := make([]byte, MaxContentFilterGuidanceBytes)
	for i := range atLimit {
		atLimit[i] = 'a'
	}
	if got := ResolveContentFilterGuidance(pointerTo(string(atLimit))); got != string(atLimit) {
		t.Fatalf("at-limit value was not kept: %q", got)
	}
	overLimit := append(append([]byte(nil), atLimit...), 'b')
	if got := ResolveContentFilterGuidance(pointerTo(string(overLimit))); got != ContentFilterGuidanceDefault {
		t.Fatalf("oversized value = %q, want the bundled guidance (no truncation)", got)
	}
}

func pointerTo(value string) *string { return &value }

// Rust #49119 (core/src/context/content_filter_guidance.rs): the guidance is a
// developer fragment wrapped in <content_filter_guidance> markers with the
// "generic.content_filter_guidance" content kind.
func TestContentFilterGuidanceFragmentLikeRust(t *testing.T) {
	rendered := Render(NewContentFilterGuidance("Offer a permitted alternative."))
	if rendered == nil {
		t.Fatal("Render() = nil")
	}
	if rendered.Role != RoleDeveloper {
		t.Fatalf("role = %q, want %q", rendered.Role, RoleDeveloper)
	}
	want := "<content_filter_guidance>\nOffer a permitted alternative.\n</content_filter_guidance>"
	if rendered.Content != want {
		t.Fatalf("content = %q, want %q", rendered.Content, want)
	}
	if rendered.ContentKind != "generic.content_filter_guidance" {
		t.Fatalf("kind = %q", rendered.ContentKind)
	}
}
