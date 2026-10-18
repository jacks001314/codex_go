package tea

import (
	"strings"
	"testing"

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
	model.composerTips = newComposerTipState(42)
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
