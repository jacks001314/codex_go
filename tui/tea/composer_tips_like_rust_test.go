package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

func composerTipTestModel(t *testing.T, width int) *Model {
	t.Helper()
	fullscreen := true
	show := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:                width,
		Height:               30,
		FullscreenTranscript: &fullscreen,
		ShowTooltips:         &show,
	})
	model.composerTips = newComposerTipState(42, nil)
	model.View()
	model.activityFollow = true
	return model
}

// Rust #46931: composer-gap catalog tips are stable across redraws and must
// fit on one row.
func TestComposerTipsStableAndSingleRowLikeRust(t *testing.T) {
	model := composerTipTestModel(t, 80)
	first, ok := model.composerTipLine()
	if !ok {
		t.Fatal("no composer tip selected")
	}
	second, ok := model.composerTipLine()
	if !ok || second != first {
		t.Fatalf("tip changed across redraws: %q -> %q", first, second)
	}
	plain := utils.StripANSI(first)
	if strings.Contains(plain, "\n") || codextui.DisplayWidth(first) > 78 {
		t.Fatalf("tip is not a single fitting row: %q width=%d", plain, codextui.DisplayWidth(first))
	}
	footer, _ := model.footerHelpLine()
	if footer != first {
		t.Fatalf("footer = %q, want composer tip %q", footer, first)
	}
}

func TestComposerTipsYieldToUsageDraftAndRunningLikeRust(t *testing.T) {
	model := composerTipTestModel(t, 100)
	model.rateLimitSnapshots = map[string]chatwidget.RateLimitSnapshot{
		"codex": {
			LimitID:  "codex",
			PlanType: "plus",
			Primary:  usageNoticeWindowPtr(92, 300, nil),
		},
	}
	footer, _ := model.footerHelpLine()
	if !strings.Contains(footer, "⚠") {
		t.Fatalf("usage notice did not outrank the tip: %q", footer)
	}

	model.rateLimitSnapshots = nil
	tip, tipOK := model.composerTipLine()
	if !tipOK {
		t.Fatal("no composer tip selected before interaction checks")
	}
	model.composer.SetValue("draft")
	footer, _ = model.footerHelpLine()
	if footer == tip {
		t.Fatalf("draft kept a composer tip: %q", footer)
	}

	model.composer.SetValue("")
	model.State.SetStatus("running")
	footer, _ = model.footerHelpLine()
	if footer == tip {
		t.Fatalf("running turn kept a composer tip: %q", footer)
	}
}

func TestComposerGapCtrlClickOpensOSC8LinkLikeRust(t *testing.T) {
	model := composerTipTestModel(t, 100)
	line := "Tip: " + codextui.OSC8Hyperlink("https://example.com/docs", "docs") + " end"
	model.renderedSelection.regions[renderedRegionFooter] = renderedRegion{
		kind: renderedRegionFooter,
		y:    10,
		x:    0,
		h:    1,
		text: []string{line},
	}
	opened := ""
	model.onOpenURL = func(url string) error {
		opened = url
		return nil
	}
	handled, cmd := model.handleComposerGapLinkClick(bubbletea.MouseMsg{
		X: 7, Y: 10, Ctrl: true, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	})
	if !handled || cmd == nil {
		t.Fatalf("Ctrl-click handled=%v cmd=%v", handled, cmd != nil)
	}
	model.Update(cmd())
	if opened != "https://example.com/docs" {
		t.Fatalf("opened = %q", opened)
	}

	if handled, _ := model.handleComposerGapLinkClick(bubbletea.MouseMsg{
		X: 7, Y: 10, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	}); handled {
		t.Fatal("plain click consumed a link hit test")
	}
}

// Rust #46931: a startup announcement is preferred until the first user turn,
// then stays dismissed even if history is cleared.
func TestComposerTipsPreferAnnouncementUntilFirstTurnLikeRust(t *testing.T) {
	model := composerTipTestModel(t, 100)
	announcement := "Welcome **back** to Codex"
	model.composerTips = newComposerTipState(42, &announcement)
	line, ok := model.composerTipLine()
	if !ok || !strings.Contains(utils.StripANSI(line), "Welcome back to Codex") {
		t.Fatalf("announcement tip = %q ok=%v", line, ok)
	}

	model.State.AddMessage(codextui.RoleUser, "first turn")
	line, ok = model.composerTipLine()
	if !ok || strings.Contains(utils.StripANSI(line), "Welcome back") {
		t.Fatalf("announcement survived the first turn: %q ok=%v", line, ok)
	}

	model.State.Messages = nil
	line, ok = model.composerTipLine()
	if !ok || strings.Contains(utils.StripANSI(line), "Welcome back") {
		t.Fatalf("cleared history restored the announcement: %q ok=%v", line, ok)
	}
}
