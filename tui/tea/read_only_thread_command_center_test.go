package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

func readOnlyCommandCenterModel(t *testing.T, options Options) *Model {
	t.Helper()
	options.Width = 120
	options.Height = 40
	model := NewModel(codextui.NewState(nil), options)
	model.State.SetThreadID("thread-a")
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a"},
		ReadOnly: true,
	})
	if !model.readOnlyThread {
		t.Fatal("the model must enter the read-only state")
	}
	return model
}

func availableCommandCenterOptions() Options {
	return Options{
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) { return nil, nil },
	}
}

// Mirrors Rust #48132: Left (with the composer's move_left binding intact) or Esc
// returns to the command center from a read-only conversation, and the notice
// advertises `<-/Esc command center`.
func TestReadOnlyViewReturnsToCommandCenterWithLeftOrEscape(t *testing.T) {
	for _, testCase := range []struct {
		name string
		key  bubbletea.KeyMsg
	}{
		{name: "Esc", key: bubbletea.KeyMsg{Type: bubbletea.KeyEsc}},
		{name: "Left", key: bubbletea.KeyMsg{Type: bubbletea.KeyLeft}},
	} {
		model := readOnlyCommandCenterModel(t, availableCommandCenterOptions())
		notice := model.renderReadOnlyThreadNotice()
		if !strings.Contains(notice, "\u2190/Esc command center") || !strings.Contains(notice, "ctrl+c/q exit") {
			t.Fatalf("%s: notice = %q", testCase.name, notice)
		}
		_, command := model.Update(testCase.key)
		if command == nil {
			t.Fatalf("%s: returning to the command center must request a refresh", testCase.name)
		}
		if model.agentsOverview == nil {
			t.Fatalf("%s: the command center did not open", testCase.name)
		}
	}
}

// A remapped composer move_left (or an action that claims Left) keeps Esc as the
// command-center key, matching Rust's keymap gate.
func TestReadOnlyViewKeepsEscapeWhenLeftIsUnavailable(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		configure  func(t *testing.T, config *codextui.KeymapConfig)
		afterModel func(t *testing.T, model *Model)
	}{
		{
			name: "vim normal move_left remapped",
			configure: func(t *testing.T, config *codextui.KeymapConfig) {
				t.Helper()
				if err := config.Set("vim_normal", "move_left", []string{"h"}); err != nil {
					t.Fatalf("remap move_left: %v", err)
				}
			},
			afterModel: func(t *testing.T, model *Model) {
				t.Helper()
				model.vimMode = true
				model.vimInsert = false
			},
		},
		{
			name: "global action claims left",
			configure: func(t *testing.T, config *codextui.KeymapConfig) {
				t.Helper()
				if err := config.Set("global", "open_transcript", []string{"left"}); err != nil {
					t.Fatalf("bind left globally: %v", err)
				}
			},
		},
	} {
		options := availableCommandCenterOptions()
		config := codextui.NewKeymapConfig()
		testCase.configure(t, config)
		options.KeymapConfig = config
		model := readOnlyCommandCenterModel(t, options)
		if testCase.afterModel != nil {
			testCase.afterModel(t, model)
		}

		notice := model.renderReadOnlyThreadNotice()
		if !strings.Contains(notice, "Esc command center") || strings.Contains(notice, "\u2190/Esc") {
			t.Fatalf("%s: notice = %q", testCase.name, notice)
		}
		if model.agentsNavigationKeyAvailable() {
			t.Fatalf("%s: Left must be unavailable for navigation", testCase.name)
		}
		model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyLeft})
		if model.agentsOverview != nil {
			t.Fatalf("%s: Left opened the command center", testCase.name)
		}
		// Esc still returns to the command center (checked on a fresh model,
		// since Left's remapped or claimed meaning may have changed the view).
		fresh := readOnlyCommandCenterModel(t, options)
		if testCase.afterModel != nil {
			testCase.afterModel(t, fresh)
		}
		if _, command := fresh.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc}); command == nil || fresh.agentsOverview == nil {
			t.Fatalf("%s: Esc did not open the command center", testCase.name)
		}
	}
}

// The embedded TUI has no command center, so the notice keeps the exit grouping
// and neither Left nor Esc opens the dashboard.
func TestReadOnlyViewWithoutCommandCenterKeepsExitKeys(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		options Options
	}{
		{name: "embedded", options: Options{OnAgentsOverviewRefresh: availableCommandCenterOptions().OnAgentsOverviewRefresh, AgentsOverviewEmbedded: true}},
		{name: "no dashboard callback", options: Options{}},
	} {
		model := readOnlyCommandCenterModel(t, testCase.options)
		notice := model.renderReadOnlyThreadNotice()
		if !strings.Contains(notice, "Esc/ctrl+c/q exit") || strings.Contains(notice, "command center") {
			t.Fatalf("%s: notice = %q", testCase.name, notice)
		}
		if _, command := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyLeft}); command != nil || model.agentsOverview != nil {
			t.Fatalf("%s: Left must be inert without a command center", testCase.name)
		}
		if _, command := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc}); command == nil {
			t.Fatalf("%s: Esc must exit without a command center", testCase.name)
		}
	}
}
