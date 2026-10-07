package context

import (
	"strings"
	"testing"
)

// Mirrors Rust #49598 (`codex-rs/core/src/context/user_goal.rs`): the fragment
// markers, the rendered body, and the host-owned content kind. The Rust enum is
// covered by the app-server suite `user_goal_updates_survive_resume_and_clear`;
// these assertions pin the rendering the wire text depends on.
func TestUserGoalUpdateRenderingLikeRust(t *testing.T) {
	objective := "Send the approved report."
	status := "paused"
	set := NewUserGoalSet(&objective, &status)

	rendered := RenderStandalone(set)
	if rendered == nil {
		t.Fatal("RenderStandalone(set) = nil")
	}
	want := "<codex_internal_context source=\"user_goal\">\n" +
		"User set the goal: \"Send the approved report.\"\n" +
		"User set goal status: \"paused\".\n" +
		"</codex_internal_context>"
	if rendered.Content != want {
		t.Fatalf("set content = %q, want %q", rendered.Content, want)
	}
	if rendered.Role != RoleUser {
		t.Fatalf("set role = %q, want %q", rendered.Role, RoleUser)
	}
	if rendered.ContentKind != UserGoalContentKind {
		t.Fatalf("set content kind = %q, want %q", rendered.ContentKind, UserGoalContentKind)
	}

	clear := NewUserGoalClear()
	renderedClear := RenderStandalone(clear)
	wantClear := "<codex_internal_context source=\"user_goal\">\n" +
		"User cleared the goal.\n" +
		"</codex_internal_context>"
	if renderedClear == nil || renderedClear.Content != wantClear {
		t.Fatalf("clear content = %#v, want %q", renderedClear, wantClear)
	}
	if renderedClear.ContentKind != UserGoalContentKind {
		t.Fatalf("clear content kind = %q, want %q", renderedClear.ContentKind, UserGoalContentKind)
	}
}

// The objective survives JSON round-tripping with literal spaces, `%`, and `#`
// preserved, and an oversized objective is omitted whole rather than truncated.
func TestUserGoalUpdatePreservesLiteralCharactersLikeRust(t *testing.T) {
	objective := "fix 100% of C# issues  in  report #7"
	set := NewUserGoalSet(&objective, nil)
	rendered := RenderStandalone(set)
	if rendered == nil {
		t.Fatal("RenderStandalone(set) = nil")
	}
	if !strings.Contains(rendered.Content, `User set the goal: "fix 100% of C# issues  in  report #7"`) {
		t.Fatalf("literal objective not preserved: %q", rendered.Content)
	}
	if rendered.ContentKind != UserGoalContentKind {
		t.Fatalf("content kind = %q, want %q", rendered.ContentKind, UserGoalContentKind)
	}

	oversized := strings.Repeat("x", maxUserGoalObjectiveBytes)
	omitted := NewUserGoalSet(&oversized, nil)
	renderedOmitted := RenderStandalone(omitted)
	if renderedOmitted == nil {
		t.Fatal("RenderStandalone(oversized) = nil")
	}
	if renderedOmitted.ContentKind != UserGoalOmittedObjectiveKind {
		t.Fatalf("oversized content kind = %q, want %q", renderedOmitted.ContentKind, UserGoalOmittedObjectiveKind)
	}
	if !strings.Contains(renderedOmitted.Content, "User set the goal: [objective omitted; exceeds the evidence limit].") {
		t.Fatalf("oversized objective was not omitted: %q", renderedOmitted.Content)
	}
	if strings.Contains(renderedOmitted.Content, oversized[:32]) {
		t.Fatalf("oversized objective leaked into the instruction: %q", renderedOmitted.Content)
	}
}
