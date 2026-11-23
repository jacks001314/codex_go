package tea

import (
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// Rust #62335: a chat blocked by a provider error advertises and accepts a plain
// Left press back to the command center when the target supports one.
func TestProviderErrorOpensCommandCenterWithLeftLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 40,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	model.remoteAppServer = true
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a", Title: "Blocked"},
		ReadOnly: true,
		ProviderError: &appserver.ModelProviderError{
			Kind:          appserver.ModelProviderNotConfigured,
			ModelProvider: "missing",
		},
	})
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "← command center") {
		t.Fatalf("provider notice missing the command center hint:\n%s", view)
	}

	updated, _ := model.Update(key(keymsg.KeyLeft))
	model = updated.(*Model)
	if model.agentsOverview == nil {
		t.Fatal("Left did not open the command center from the provider error")
	}
}

// A target without command center support keeps the provider notice's original
// shortcuts and ignores Left.
func TestProviderErrorWithoutCommandCenterOmitsHintLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 120, Height: 40})
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a", Title: "Blocked"},
		ReadOnly: true,
		ProviderError: &appserver.ModelProviderError{
			Kind:          appserver.ModelProviderNotConfigured,
			ModelProvider: "missing",
		},
	})
	view := utils.StripANSI(model.viewString())
	if strings.Contains(view, "command center") {
		t.Fatalf("provider notice advertised a command center that is unavailable:\n%s", view)
	}
	updated, _ := model.Update(key(keymsg.KeyLeft))
	if updated.(*Model).agentsOverview != nil {
		t.Fatal("Left opened the command center without target support")
	}
}
