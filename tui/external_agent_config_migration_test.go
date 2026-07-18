package tui

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/config"
)

func TestExternalAgentConfigMigrationGroupsMatchRust(t *testing.T) {
	items := sampleExternalMigrationItems()
	groups := ExternalAgentConfigMigrationGroups(items)
	if len(groups) != 3 {
		t.Fatalf("groups = %#v", groups)
	}
	if groups[0].Label != "Tools & setup" || groups[1].Label != "Current project" || groups[2].Label != "Chat sessions (1)" {
		t.Fatalf("group labels = %#v", groups)
	}
	if !reflect.DeepEqual(groups[0].ItemIndices, []int{0}) || !reflect.DeepEqual(groups[1].ItemIndices, []int{1, 2}) || !reflect.DeepEqual(groups[2].ItemIndices, []int{3}) {
		t.Fatalf("group indices = %#v", groups)
	}
}

func TestExternalAgentConfigMigrationModelHelpersMatchRust(t *testing.T) {
	items := sampleExternalMigrationItems()
	if got := ExternalAgentConfigMigrationItemLabel(items[2]); got != "Plugins" {
		t.Fatalf("plugin label = %q", got)
	}
	if got := ExternalAgentConfigMigrationCountSummary(items); got != "Settings 1, Instructions 1, Plugins 3, Chat sessions 1" {
		t.Fatalf("count summary = %q", got)
	}

	skills := config.ExternalAgentConfigMigrationItem{
		ItemType: config.MigrationSkills,
		Details: &config.MigrationDetails{
			Skills: []config.NamedMigration{{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}, {Name: "delta"}, {Name: "epsilon"}},
		},
	}
	detail, ok := ExternalAgentConfigMigrationItemDetail(skills)
	if !ok || detail != "5 skills: alpha, beta, gamma, delta" {
		t.Fatalf("skill detail = %q, %v", detail, ok)
	}
}

func TestExternalAgentConfigMigrationScreenProceedAndToggleMatchRust(t *testing.T) {
	items := sampleExternalMigrationItems()
	screen := NewExternalAgentConfigMigrationScreen(items, items, "")
	screen.HandleKey("enter")
	if !screen.IsDone() || screen.Outcome().Kind != ExternalAgentConfigMigrationProceed || !reflect.DeepEqual(screen.Outcome().Items, items) {
		t.Fatalf("proceed outcome = %#v done=%v", screen.Outcome(), screen.IsDone())
	}

	screen = NewExternalAgentConfigMigrationScreen(items, items, "")
	screen.HandleKey("c")
	screen.HandleKey(" ")
	screen.HandleKey("b")
	screen.HandleKey("1")
	if screen.Outcome().Kind != ExternalAgentConfigMigrationProceed || len(screen.Outcome().Items) != len(items)-1 {
		t.Fatalf("toggle proceed outcome = %#v", screen.Outcome())
	}
	if reflect.DeepEqual(screen.Outcome().Items[0], items[0]) {
		t.Fatalf("first item should have been toggled off: %#v", screen.Outcome().Items)
	}
}

func TestExternalAgentConfigMigrationScreenEmptySelectionUsesCustomize(t *testing.T) {
	items := sampleExternalMigrationItems()
	screen := NewExternalAgentConfigMigrationScreen(items, nil, "")
	screen.HandleKey("enter")
	if screen.IsDone() || screen.View() != ExternalAgentMigrationViewCustomize {
		t.Fatalf("empty enter view=%v done=%v outcome=%#v", screen.View(), screen.IsDone(), screen.Outcome())
	}
	screen.HandleKey("n")
	screen.HandleKey("b")
	screen.HandleKey("1")
	if screen.View() != ExternalAgentMigrationViewCustomize {
		t.Fatalf("numeric shortcut should customize when proceed disabled, view=%v", screen.View())
	}
}

func TestExternalAgentConfigMigrationRowsUseSelectionColorBar(t *testing.T) {
	items := sampleExternalMigrationItems()
	screen := NewExternalAgentConfigMigrationScreen(items, items, "")
	rows := screen.Rows()
	if !containsRow(rows, RenderSelectedRow(NumberedSelectionPrefix(0, true)+"Import selected")) {
		t.Fatalf("summary rows missing selected action: %#v", rows)
	}
	screen.HandleKey("c")
	rows = screen.Rows()
	if !containsSubstring(rows, "\x1b[") || !containsSubstring(rows, "Settings (settings.json -> config.toml)") {
		t.Fatalf("customize rows missing selected item color/style: %#v", rows)
	}
}

func TestExternalAgentConfigMigrationDisplayDescriptionMatchesRust(t *testing.T) {
	item := sampleExternalMigrationItems()[2]
	got := ExternalAgentConfigMigrationDisplayDescription(item)
	want := "Import enabled plugins from /workspace/project/.claude/settings.json (2 marketplaces, 3 plugins)"
	if got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func sampleExternalMigrationItems() []config.ExternalAgentConfigMigrationItem {
	cwd := "/workspace/project"
	title := "Investigate migration UX"
	return []config.ExternalAgentConfigMigrationItem{
		{
			ItemType:    config.MigrationConfig,
			Description: "Migrate /home/alex/.claude/settings.json into /home/alex/.codex/config.toml",
		},
		{
			ItemType:    config.MigrationAgentsMD,
			Description: "Migrate /workspace/project/CLAUDE.md to /workspace/project/AGENTS.md",
			CWD:         &cwd,
		},
		{
			ItemType:    config.MigrationPlugins,
			Description: "Migrate enabled plugins from /workspace/project/.claude/settings.json",
			CWD:         &cwd,
			Details: &config.MigrationDetails{
				Plugins: []config.PluginMigration{
					{MarketplaceName: "acme-tools", PluginNames: []string{"deployer", "formatter"}},
					{MarketplaceName: "team", PluginNames: []string{"asana"}},
				},
			},
		},
		{
			ItemType:    config.MigrationSessions,
			Description: "Migrate recent Claude Code sessions",
			Details: &config.MigrationDetails{
				Sessions: []config.SessionMigration{{Path: "/tmp/session.jsonl", CWD: cwd, Title: &title}},
			},
		},
	}
}

func containsRow(rows []string, want string) bool {
	for _, row := range rows {
		if row == want {
			return true
		}
	}
	return false
}

func containsSubstring(rows []string, want string) bool {
	for _, row := range rows {
		if strings.Contains(row, want) {
			return true
		}
	}
	return false
}
