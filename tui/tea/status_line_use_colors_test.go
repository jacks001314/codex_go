package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

func TestModelStatusLineUseColorsDefaultsAndOptions(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	if !model.statusLineUseColors {
		t.Fatal("status line colors should default to enabled")
	}

	disabled := false
	off := NewModel(codextui.NewState(nil), Options{StatusLineUseColors: &disabled})
	if off.statusLineUseColors {
		t.Fatal("an explicit disabled preference must be respected")
	}

	enabled := true
	on := NewModel(codextui.NewState(nil), Options{StatusLineUseColors: &enabled})
	if !on.statusLineUseColors {
		t.Fatal("an explicit enabled preference must be respected")
	}
}

func TestSettingsWriteResultAppliesStatusLineUseColors(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	model.ensureStatusControls()
	if !model.statusControls.StatusLineUseThemeColors {
		t.Fatal("status controls should default to theme colors")
	}

	disabled := false
	model.Update(SettingsWriteResultMsg{Result: SettingsWriteResult{StatusLineUseColors: &disabled}})
	if model.statusLineUseColors {
		t.Fatal("a settings result should disable status line colors")
	}
	if model.statusControls.StatusLineUseThemeColors {
		t.Fatal("status controls flag must follow the settings result")
	}

	enabled := true
	model.Update(SettingsWriteResultMsg{Result: SettingsWriteResult{StatusLineUseColors: &enabled}})
	if !model.statusLineUseColors || !model.statusControls.StatusLineUseThemeColors {
		t.Fatal("a settings result should re-enable status line colors")
	}
}

func TestStatusLineUseColorsSeedsStatusControlsAndOverview(t *testing.T) {
	disabled := false
	model := NewModel(codextui.NewState(nil), Options{StatusLineUseColors: &disabled})
	model.ensureStatusControls()
	if model.statusControls.StatusLineUseThemeColors {
		t.Fatal("status controls must seed from tui.status_line_use_colors")
	}

	view := &agentsoverview.View{}
	model.wireAgentsOverviewThemeColors(view)
	if view.UseThemeColors {
		t.Fatal("agents overview must respect tui.status_line_use_colors")
	}
}

// TestWireAgentsOverviewSetsMarkdownRenderer covers Rust #44752: the dashboard
// injects a markdown renderer so the task-details prompt preview is styled.
func TestWireAgentsOverviewSetsMarkdownRenderer(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 120, Height: 40, TUITheme: "catppuccin-mocha"})
	view := &agentsoverview.View{}
	model.wireAgentsOverviewThemeColors(view)
	if view.RenderMarkdown == nil {
		t.Fatal("agents overview markdown renderer is not wired")
	}
	lines := view.RenderMarkdown("**bold** text", 38)
	if len(lines) == 0 {
		t.Fatal("markdown renderer returned no lines")
	}
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "bold") {
		t.Fatalf("markdown render = %q", joined)
	}
}

func TestRenderStatusHeaderUsesThemeColors(t *testing.T) {
	enabled := true
	state := codextui.NewState(nil)
	state.ThreadID = "thread-abc"
	model := NewModel(state, Options{
		Width:               80,
		Height:              12,
		StatusLineItems:     []string{"raw-output", "thread-title"},
		TUITheme:            "catppuccin-mocha",
		StatusLineUseColors: &enabled,
	})
	model.rawOutput = true
	model.ensureStatusControls()

	header := model.renderStatusHeader()
	if !strings.Contains(header, "\x1b[38;2;") {
		t.Fatalf("styled status header has no truecolor spans: %q", header)
	}
	if !strings.Contains(header, "\x1b[2m") {
		t.Fatalf("styled status header has no dim separators: %q", header)
	}

	// Disabling the preference returns the plain, unstyled header text.
	disabled := false
	model.Update(SettingsWriteResultMsg{Result: SettingsWriteResult{StatusLineUseColors: &disabled}})
	if header := model.renderStatusHeader(); strings.Contains(header, "\x1b[38;2;") {
		t.Fatalf("disabled status header still styled: %q", header)
	}
}
