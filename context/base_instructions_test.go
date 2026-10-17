package context

import "testing"

func TestBaseInstructionsFragmentLikeRust(t *testing.T) {
	fragment := BaseInstructionsFragment{Text: "Follow the repository instructions."}
	if fragment.Role() != RoleDeveloper {
		t.Fatalf("Role() = %q, want developer", fragment.Role())
	}
	if open, close := fragment.Markers(); open != "" || close != "" {
		t.Fatalf("Markers() = (%q, %q), want empty", open, close)
	}
	if fragment.Body() != "Follow the repository instructions." {
		t.Fatalf("Body() = %q", fragment.Body())
	}
	if fragment.ContentKind() != BaseInstructionsContentKind || fragment.ContentKind() != "model.base_instructions" {
		t.Fatalf("ContentKind() = %q", fragment.ContentKind())
	}
	rendered := Render(fragment)
	if rendered == nil || rendered.Role != RoleDeveloper || rendered.ContentKind != BaseInstructionsContentKind {
		t.Fatalf("Render() = %#v", rendered)
	}
}
