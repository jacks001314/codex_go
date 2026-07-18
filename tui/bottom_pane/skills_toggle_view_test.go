package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/tui"
)

func TestSkillsToggleViewFiltersSortsAndRowsMatchRust(t *testing.T) {
	view := NewSkillsToggleView([]SkillsToggleItem{
		{Name: "Repo Scout", SkillName: "repo_scout", Description: "Summarize the repo layout", Enabled: true, Path: "/skills/repo"},
		{Name: "Changelog Writer", SkillName: "changelog_writer", Description: "Draft release notes", Enabled: false, Path: "/skills/changelog"},
		{Name: "Review Helper", SkillName: "review_helper", Description: "Review changes", Enabled: false, Path: "/skills/review"},
	})

	if got, want := view.Enabled, []string{"Repo Scout"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("enabled = %#v, want %#v", got, want)
	}
	view.InsertSearchRune('r')
	view.InsertSearchRune('e')
	names := []string{}
	for _, idx := range view.FilteredIndices {
		names = append(names, view.Items[idx].Name)
	}
	want := []string{"Repo Scout", "Review Helper", "Changelog Writer"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("filtered names = %#v, want %#v", names, want)
	}

	rows := view.Rows(80)
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("\u203a [x] Repo Scout        Summarize the repo layout")) {
		t.Fatalf("rows missing selected repo row:\n%s", strings.Join(rows, "\n"))
	}
	if !bottomPaneContainsRow(rows, SkillsSearchPrompt+"re") {
		t.Fatalf("rows missing search query:\n%s", strings.Join(rows, "\n"))
	}
}

func TestSkillsToggleViewToggleNavigationCloseAndSearchKeys(t *testing.T) {
	view := NewSkillsToggleView([]SkillsToggleItem{
		{Name: "Repo Scout", SkillName: "repo_scout", Description: "Summarize", Enabled: true, Path: "/skills/repo"},
		{Name: "Changelog Writer", SkillName: "changelog_writer", Description: "Draft", Enabled: false, Path: "/skills/changelog"},
	})

	view.HandleKey("down")
	view.HandleKey("space")
	if view.Items[1].Enabled != true {
		t.Fatalf("second skill should be enabled")
	}
	if len(view.Events) != 1 || view.Events[0].Kind != SkillsToggleEventSetEnabled || view.Events[0].Path != "/skills/changelog" || !view.Events[0].Enabled {
		t.Fatalf("toggle events = %#v", view.Events)
	}
	if got, want := view.Enabled, []string{"Repo Scout", "Changelog Writer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("enabled = %#v, want %#v", got, want)
	}

	view.HandleKey("c")
	view.HandleKey("h")
	if got := view.SearchQuery; got != "ch" {
		t.Fatalf("query = %q", got)
	}
	if selected, ok := view.selectedActualIdx(); !ok || view.Items[selected].Name != "Changelog Writer" {
		t.Fatalf("selected actual = %d ok=%v", selected, ok)
	}
	view.HandleKey("backspace")
	if got := view.SearchQuery; got != "c" {
		t.Fatalf("query after backspace = %q", got)
	}

	view.HandleKey("esc")
	if !view.Complete {
		t.Fatalf("view should be complete")
	}
	if len(view.Events) < 3 || view.Events[len(view.Events)-2].Kind != SkillsToggleEventClosed || view.Events[len(view.Events)-1].Kind != SkillsToggleEventReload {
		t.Fatalf("close events = %#v", view.Events)
	}
}

func TestSkillsToggleHelpersMatchRust(t *testing.T) {
	if got := TruncateSkillName("Very Long Skill Display Name"); got != "Very Long Skill Disp..." {
		t.Fatalf("truncated = %q", got)
	}
	if _, ok := MatchSkill("repo", "Repo Scout", "repo_scout"); !ok {
		t.Fatalf("expected display name to match")
	}
	if _, ok := MatchSkill("repo_s", "Repository", "repo_scout"); !ok {
		t.Fatalf("expected skill name fallback to match")
	}
}

func TestSkillsToggleRowsUseGenericDisplayWidth(t *testing.T) {
	view := NewSkillsToggleView([]SkillsToggleItem{
		{Name: "中文技能名称很长很长", SkillName: "wide", Description: "描述也很长很长", Enabled: true, Path: "/skills/wide"},
	})
	rows := view.Rows(24)
	found := false
	for _, row := range rows {
		if !strings.Contains(row, "\x1b[") {
			continue
		}
		found = true
		if width := tui.DisplayWidth(stripANSIForSelectionTest(row)); width > 24 {
			t.Fatalf("selected row exceeds width: %q width=%d rows=%#v", row, width, rows)
		}
	}
	if !found {
		t.Fatalf("selected row missing color: %#v", rows)
	}
}
