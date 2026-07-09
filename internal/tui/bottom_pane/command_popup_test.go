package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/internal/tui"
)

func TestCommandPopupFiltersExactPrefixAndKeepsRustOrder(t *testing.T) {
	popup := NewCommandPopup(CommandPopupFlags{}, nil)
	popup.OnComposerTextChange("/m")

	got := commandPopupItemNames(popup.FilteredItems())
	want := []string{"model", "memories", "mention", "mcp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered names = %#v, want %#v", got, want)
	}

	popup.OnComposerTextChange("/mo")
	selected, ok := popup.SelectedItem()
	if !ok || selected.Name != "model" {
		t.Fatalf("selected = %#v ok=%v, want model", selected, ok)
	}
}

func TestCommandPopupServiceTierCommandUsesCatalogNameAndDescription(t *testing.T) {
	popup := NewCommandPopup(
		CommandPopupFlags{ServiceTierCommandsEnabled: true},
		[]ServiceTierCommand{{
			ID:          "priority",
			Name:        "fast",
			Description: "Fastest inference with increased plan usage",
		}},
	)
	popup.OnComposerTextChange("/fa")

	selected, ok := popup.SelectedItem()
	if !ok || selected.Kind != SlashCommandItemServiceTier || selected.Name != "fast" {
		t.Fatalf("selected = %#v ok=%v, want service tier fast", selected, ok)
	}
	if selected.Description != "Fastest inference with increased plan usage" {
		t.Fatalf("description = %q", selected.Description)
	}
}

func TestCommandPopupAliasesHiddenByDefaultButShownForPrefix(t *testing.T) {
	popup := NewCommandPopup(CommandPopupFlags{}, nil)
	popup.OnComposerTextChange("/")
	names := commandPopupItemNames(popup.FilteredItems())
	if containsString(names, "quit") || containsString(names, "btw") {
		t.Fatalf("aliases should be hidden for empty filter, got %#v", names)
	}

	popup.OnComposerTextChange("/qu")
	if selected, ok := popup.SelectedItem(); !ok || selected.Name != "quit" {
		t.Fatalf("selected = %#v ok=%v, want quit alias", selected, ok)
	}

	popup.OnComposerTextChange("/bt")
	if selected, ok := popup.SelectedItem(); !ok || selected.Name != "btw" {
		t.Fatalf("selected = %#v ok=%v, want btw alias", selected, ok)
	}

	popup.OnComposerTextChange("/keys")
	if selected, ok := popup.SelectedItem(); ok {
		t.Fatalf("dispatch-only keymap alias should not appear in popup, selected=%#v", selected)
	}
}

func TestCommandPopupFlagsMatchRustVisibility(t *testing.T) {
	popup := NewCommandPopup(CommandPopupFlags{}, nil)
	popup.OnComposerTextChange("/plan")
	if selected, ok := popup.SelectedItem(); ok && selected.Name == "plan" {
		t.Fatalf("plan should be hidden when collaboration modes are disabled")
	}

	popup = NewCommandPopup(CommandPopupFlags{CollaborationModesEnabled: true, PersonalityCommandEnabled: true}, nil)
	popup.OnComposerTextChange("/plan")
	if selected, ok := popup.SelectedItem(); !ok || selected.Name != "plan" {
		t.Fatalf("selected = %#v ok=%v, want plan", selected, ok)
	}

	popup = NewCommandPopup(CommandPopupFlags{SideConversationActive: true, TokenActivityCommandEnabled: true}, nil)
	popup.OnComposerTextChange("/")
	got := commandPopupItemNames(popup.FilteredItems())
	want := []string{"ide", "copy", "raw", "diff", "mention", "status", "usage"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("side commands = %#v, want %#v", got, want)
	}
}

func TestCommandPopupRustOrderIncludesPlanGoalUsageAndPlugins(t *testing.T) {
	flags := CommandPopupFlags{
		CollaborationModesEnabled:    true,
		ConnectorsEnabled:            true,
		PluginsCommandEnabled:        true,
		TokenActivityCommandEnabled:  true,
		GoalCommandEnabled:           true,
		PersonalityCommandEnabled:    true,
		WindowsDegradedSandboxActive: true,
	}
	popup := NewCommandPopup(flags, nil)
	popup.OnComposerTextChange("/")
	names := commandPopupItemNames(popup.FilteredItems())

	assertBefore := func(left string, right string) {
		t.Helper()
		leftIndex, rightIndex := indexOfString(names, left), indexOfString(names, right)
		if leftIndex < 0 || rightIndex < 0 || leftIndex >= rightIndex {
			t.Fatalf("order mismatch: want %s before %s in %#v", left, right, names)
		}
	}
	assertBefore("compact", "plan")
	assertBefore("plan", "goal")
	assertBefore("status", "usage")
	assertBefore("mcp", "plugins")
	assertBefore("clear", "personality")
}

func TestCommandPopupRowsUseSelectedColorBarAndScroll(t *testing.T) {
	popup := NewCommandPopup(CommandPopupFlags{WindowsDegradedSandboxActive: true}, nil)
	popup.OnComposerTextChange("/")
	for i := 0; i < MaxPopupRows; i++ {
		popup.MoveDown()
	}
	if popup.state.ScrollTop == 0 {
		t.Fatalf("expected popup to scroll after moving down, state=%#v", popup.state)
	}
	rows := popup.Rows(72)
	selected, ok := popup.SelectedItem()
	if !ok {
		t.Fatalf("expected selected item")
	}
	if !commandPopupRowsContainSelected(rows, "/"+selected.Name) {
		t.Fatalf("rows missing selected rendered line for %q:\n%s", selected.Name, strings.Join(rows, "\n"))
	}

	popup.OnComposerTextChange("/st")
	if popup.state.ScrollTop != 0 {
		t.Fatalf("filter change should reset scroll, state=%#v", popup.state)
	}
	if selected, ok := popup.SelectedItem(); !ok || selected.Name != "status" {
		t.Fatalf("selected = %#v ok=%v, want status", selected, ok)
	}
}

func TestCommandPopupRowsWrapDescriptionsWithoutLegacyEllipsis(t *testing.T) {
	popup := NewCommandPopup(
		CommandPopupFlags{ServiceTierCommandsEnabled: true},
		[]ServiceTierCommand{{
			ID:          "priority",
			Name:        "fast",
			Description: "中文描述很长很长 should wrap instead of legacy ellipsis truncation",
		}},
	)
	popup.OnComposerTextChange("/fa")

	rows := popup.Rows(28)
	if len(rows) < 2 {
		t.Fatalf("expected wrapped rows, got %#v", rows)
	}
	for _, row := range rows {
		if strings.Contains(row, "...") {
			t.Fatalf("command popup should not use legacy ellipsis truncation:\n%s", strings.Join(rows, "\n"))
		}
		if width := tui.DisplayWidth(stripANSIForSelectionTest(row)); width > 28 {
			t.Fatalf("row exceeds width: %q width=%d", row, width)
		}
	}
}

func TestSlashCommandHelpersMatchRustGatingAndAliases(t *testing.T) {
	flags := BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		ServiceTierCommandsEnabled:  true,
		GoalCommandEnabled:          true,
		PersonalityCommandEnabled:   true,
		AllowElevateSandbox:         true,
	}
	if command, ok := FindBuiltinCommand("goooooal", flags); !ok || command.Name != "goal" {
		t.Fatalf("goal alias = %#v ok=%v", command, ok)
	}
	if command, ok := FindBuiltinCommand("clean", flags); !ok || command.CommandText() != "clean" || command.Command != tui.CommandStop {
		t.Fatalf("clean alias = %#v ok=%v", command, ok)
	}
	flags.ServiceTierCommandsEnabled = false
	if _, ok := FindSlashCommand("fast", flags, []ServiceTierCommand{{ID: "priority", Name: "fast"}}); ok {
		t.Fatalf("service tier should be hidden when disabled")
	}
	flags.ServiceTierCommandsEnabled = true
	if !HasSlashCommandPrefix("mdl", flags, nil) {
		t.Fatalf("expected fuzzy slash command prefix to match model")
	}
	if !HasSlashCommandPrefix("key", flags, nil) {
		t.Fatalf("expected canonical key prefix to match keymap")
	}
	if HasSlashCommandPrefix("keys", flags, nil) {
		t.Fatalf("dispatch-only keymap alias should not match popup prefix")
	}
	if command, ok := FindBuiltinCommand("keys", flags); !ok || command.Command != tui.CommandKeymap {
		t.Fatalf("keymap dispatch alias = %#v ok=%v", command, ok)
	}
	if command, ok := FindBuiltinCommand("btw", flags); !ok || command.Command != tui.CommandSide || !command.SupportsInlineArgs() {
		t.Fatalf("btw alias = %#v ok=%v", command, ok)
	}
}

func commandPopupItemNames(items []CommandPopupItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func indexOfString(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func commandPopupRowsContainSelected(rows []string, command string) bool {
	for _, row := range rows {
		if strings.Contains(row, "\x1b[") && strings.Contains(stripANSIForSelectionTest(row), command) {
			return true
		}
	}
	return false
}
