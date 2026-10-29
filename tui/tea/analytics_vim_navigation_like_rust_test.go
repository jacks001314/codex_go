package tea

import (
	"context"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/analytics"
)

// These tests mirror Rust #45772's analytics/navigation_tests.rs: h and l are
// horizontal aliases that defer to explicit, remapped or removed arrow
// bindings.

func newAnalyticsVimModel(t *testing.T, plan string, backend *analyticsTestBackend, configure func(*codextui.KeymapConfig)) *Model {
	t.Helper()
	model := newAnalyticsTestModel(t, plan, backend)
	if configure != nil {
		config := codextui.NewKeymapConfig()
		configure(config)
		model.keymapConfig = config
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	return model
}

func pressAnalyticsRune(model *Model, value rune) {
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{value}})
}

// TestAnalyticsVimHorizontalAliasesLikeRust checks that h/l move chart cursors
// exactly like the horizontal arrows in maximized and dashboard views.
func TestAnalyticsVimHorizontalAliasesLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "enterprise", `{"data": []}`)
	model := newAnalyticsVimModel(t, "enterprise", backend, nil)
	dashboard := model.analyticsDashboard
	for _, zoomed := range []bool{false, true} {
		for _, section := range []analytics.AnalyticsSection{
			analytics.AnalyticsSectionUsage,
			analytics.AnalyticsSectionActivity,
			analytics.AnalyticsSectionPlugins,
			analytics.AnalyticsSectionSkills,
			analytics.AnalyticsSectionCredits,
		} {
			dashboard.selectSection(section)
			dashboard.zoomed = zoomed
			dashboard.sections[section].Cursor = 3
			detail := 3
			dashboard.sections[section].Detail = &detail
			for _, value := range []rune{'h', 'h', 'l'} {
				pressAnalyticsRune(model, value)
			}
			state := dashboard.sections[section]
			if state.Cursor != 2 || state.Detail == nil || *state.Detail != 2 {
				t.Fatalf("zoomed=%v %v: cursor=%d detail=%v, want 2/2", zoomed, section, state.Cursor, state.Detail)
			}
		}
	}
}

// TestAnalyticsVimChatAliasesLikeRust checks the chat row semantics: l opens a
// detail and h closes it, matching move_right/move_left.
func TestAnalyticsVimChatAliasesLikeRust(t *testing.T) {
	backend := startAnalyticsChatsTestBackend(t, "business", map[string]int64{"chat-a": 100_000})
	model := newAnalyticsTestModel(t, "business", backend)
	model.analyticsChats = func(context.Context, *string, int) (analytics.AnalyticsChatPage, error) {
		return analytics.AnalyticsChatPage{Data: []analytics.AnalyticsChatThread{
			{ID: "chat-a", Name: analyticsChatTestPtr("Only chat"), UpdatedAt: time.Now().Unix()},
		}}, nil
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionChats)

	pressAnalyticsRune(model, 'l')
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail == nil || *detail != 0 {
		t.Fatalf("l detail = %v, want the first row", detail)
	}
	pressAnalyticsRune(model, 'h')
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail != nil {
		t.Fatalf("h left the detail open: %v", detail)
	}
}

// TestAnalyticsVimAliasesHonorConfiguredBindingsLikeRust keeps explicit
// bindings first: h/l never shadow a configured action.
func TestAnalyticsVimAliasesHonorConfiguredBindingsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsVimModel(t, "plus", backend, func(config *codextui.KeymapConfig) {
		if err := config.Set("list", "jump_top", []string{"h", "l"}); err != nil {
			t.Fatalf("configure jump_top: %v", err)
		}
	})
	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	for _, value := range []rune{'h', 'l'} {
		dashboard.sections[analytics.AnalyticsSectionUsage].Cursor = 3
		pressAnalyticsRune(model, value)
		if cursor := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; cursor != 0 {
			t.Fatalf("%q cursor = %d, want the configured jump top", value, cursor)
		}
	}
}

// TestAnalyticsVimAliasesFollowRemovedAndRemappedArrowsLikeRust checks that
// unbound arrows make h/l no-ops while remapped arrows stay authoritative.
func TestAnalyticsVimAliasesFollowRemovedAndRemappedArrowsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	removed := newAnalyticsVimModel(t, "plus", backend, func(config *codextui.KeymapConfig) {
		for _, action := range []string{"move_left", "move_right"} {
			if err := config.Set("list", action, nil); err != nil {
				t.Fatalf("unbind %s: %v", action, err)
			}
		}
	})
	dashboard := removed.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	dashboard.sections[analytics.AnalyticsSectionUsage].Cursor = 3
	for _, value := range []rune{'h', 'l'} {
		pressAnalyticsRune(removed, value)
		if cursor := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; cursor != 3 {
			t.Fatalf("%q moved the cursor with unbound arrows: %d", value, cursor)
		}
	}

	remapped := newAnalyticsVimModel(t, "plus", backend, func(config *codextui.KeymapConfig) {
		if err := config.Set("list", "move_up", []string{"right"}); err != nil {
			t.Fatalf("remap move_up: %v", err)
		}
		if err := config.Set("list", "move_down", []string{"left"}); err != nil {
			t.Fatalf("remap move_down: %v", err)
		}
	})
	dashboard = remapped.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	dashboard.sections[analytics.AnalyticsSectionUsage].Cursor = 3
	pressAnalyticsRune(remapped, 'h')
	if cursor := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; cursor != 4 {
		t.Fatalf("h cursor = %d, want move_down on the remapped left arrow", cursor)
	}
	pressAnalyticsRune(remapped, 'l')
	if cursor := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; cursor != 3 {
		t.Fatalf("l cursor = %d, want move_up on the remapped right arrow", cursor)
	}
}

// TestAnalyticsVimAliasesIgnoreModifiersLikeRust checks that modified h/l keys
// never reach the horizontal aliases.
func TestAnalyticsVimAliasesIgnoreModifiersLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsVimModel(t, "plus", backend, nil)
	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	dashboard.sections[analytics.AnalyticsSectionUsage].Cursor = 3
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'h'}, Alt: true})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'H'}})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'l'}, Alt: true})
	if cursor := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; cursor != 3 {
		t.Fatalf("modified aliases moved the cursor to %d", cursor)
	}
}
