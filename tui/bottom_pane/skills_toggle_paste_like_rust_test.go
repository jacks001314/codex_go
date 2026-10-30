package bottompane

import (
	"reflect"
	"testing"
)

// TestSkillsToggleViewPastedSearchLikeRust covers Rust #58130: pasted text is
// normalized into the single-line search query, immediately filters the list,
// and a whitespace-only paste leaves the view untouched.
func TestSkillsToggleViewPastedSearchLikeRust(t *testing.T) {
	view := NewSkillsToggleView([]SkillsToggleItem{
		{Name: "Repo Scout", SkillName: "repo_scout", Description: "Summarize", Enabled: true, Path: "/skills/repo"},
		{Name: "Completion Helper", SkillName: "completion_helper", Description: "Complete", Enabled: false, Path: "/skills/completion"},
	})

	view.HandleKey("c")
	if !view.HandlePaste("  ompletion\r\n\t ") {
		t.Fatal("pasting search text must be handled")
	}
	if got, want := view.SearchQuery, "completion"; got != want {
		t.Fatalf("search query = %q, want %q", got, want)
	}
	before := append([]int(nil), view.FilteredIndices...)
	if len(before) != 1 || view.Items[before[0]].SkillName != "completion_helper" {
		t.Fatalf("filtered indices = %#v, want only the pasted match", before)
	}
	rows := view.Rows(96)

	if view.HandlePaste(" \r\n\t ") {
		t.Fatal("a whitespace-only paste must be ignored")
	}
	if !reflect.DeepEqual(view.FilteredIndices, before) {
		t.Fatalf("filtered indices changed after whitespace paste: %#v", view.FilteredIndices)
	}
	if !reflect.DeepEqual(view.Rows(96), rows) {
		t.Fatal("whitespace-only paste must not change the rendered rows")
	}
}
