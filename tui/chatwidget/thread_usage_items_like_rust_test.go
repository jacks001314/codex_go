package chatwidget

import (
	"testing"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust #38282/#57251: the status line and terminal title expose the current
// thread's credit and dollar estimates, with ENT26 billed in native dollars.
func TestStatusLineThreadUsageItemsLikeRust(t *testing.T) {
	credits := int64(5_200_000)
	usd := int64(1_820_000)
	business := NewStatusControlsState(StatusControlsRuntime{
		PlanType:                      "business",
		ThreadUsageAvailable:          true,
		ThreadUsageCreditsMicros:      &credits,
		ThreadUsageEstimatedUSDMicros: &usd,
	})
	if got, ok := business.StatusLineValueForItem(bottompane.StatusLineThreadCredits); !ok || got != "5.2 credits" {
		t.Fatalf("business credits = %q ok=%v", got, ok)
	}
	if got, ok := business.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); !ok || got != "~$1.82" {
		t.Fatalf("business cost = %q ok=%v", got, ok)
	}

	native := int64(750_000)
	ent26 := NewStatusControlsState(StatusControlsRuntime{
		PlanType:                      "ent26",
		ThreadUsageAvailable:          true,
		ThreadUsageCreditsMicros:      &credits,
		ThreadUsageEstimatedUSDMicros: &usd,
		ThreadUsageNativeUSDMicros:    &native,
	})
	if got, ok := ent26.StatusLineValueForItem(bottompane.StatusLineThreadCredits); ok {
		t.Fatalf("ENT26 credits must be omitted, got %q", got)
	}
	if got, ok := ent26.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); !ok || got != "~$0.75" {
		t.Fatalf("ENT26 cost = %q ok=%v", got, ok)
	}

	missingNative := NewStatusControlsState(StatusControlsRuntime{
		PlanType:                      "ent26",
		ThreadUsageAvailable:          true,
		ThreadUsageCreditsMicros:      &credits,
		ThreadUsageEstimatedUSDMicros: &usd,
	})
	if got, ok := missingNative.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); ok {
		t.Fatalf("missing native USD must be omitted, got %q", got)
	}

	unavailable := NewStatusControlsState(StatusControlsRuntime{PlanType: "business"})
	if _, ok := unavailable.StatusLineValueForItem(bottompane.StatusLineThreadCredits); ok {
		t.Fatal("credits must be omitted without an estimate")
	}
	if _, ok := unavailable.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); ok {
		t.Fatal("cost must be omitted without an estimate")
	}
}

func TestTerminalTitleThreadUsageItemsLikeRust(t *testing.T) {
	credits := int64(5_200_000)
	usd := int64(1_820_000)
	state := NewStatusControlsState(StatusControlsRuntime{
		PlanType:                      "business",
		ThreadUsageAvailable:          true,
		ThreadUsageCreditsMicros:      &credits,
		ThreadUsageEstimatedUSDMicros: &usd,
	})
	state.TerminalTitleConfigured = true
	state.TerminalTitleIDs = []string{"thread-credits", "estimated-thread-cost"}
	result := state.RefreshStatusSurfaces()
	if !result.TerminalTitleRendered || result.TerminalTitleText != "5.2 credits | ~$1.82" {
		t.Fatalf("terminal title = %q rendered=%v", result.TerminalTitleText, result.TerminalTitleRendered)
	}

	ent26 := NewStatusControlsState(StatusControlsRuntime{
		PlanType:                      "ent26",
		ThreadUsageAvailable:          true,
		ThreadUsageCreditsMicros:      &credits,
		ThreadUsageEstimatedUSDMicros: &usd,
	})
	ent26.TerminalTitleConfigured = true
	ent26.TerminalTitleIDs = []string{"thread-credits", "estimated-thread-cost"}
	if result := ent26.RefreshStatusSurfaces(); result.TerminalTitleRendered {
		t.Fatalf("ENT26 title without native USD = %q", result.TerminalTitleText)
	}
}

func TestStatusSurfaceSelectionsUseThreadUsageLikeRust(t *testing.T) {
	if !NewStatusSurfaceSelections([]string{"thread-credits"}, nil).UsesThreadUsage() {
		t.Fatal("status line thread-credits must select thread usage")
	}
	if !NewStatusSurfaceSelections([]string{"estimated-thread-cost"}, nil).UsesThreadUsage() {
		t.Fatal("status line estimated-thread-cost must select thread usage")
	}
	if !NewStatusSurfaceSelections([]string{"model"}, []string{"estimated-thread-cost"}).UsesThreadUsage() {
		t.Fatal("terminal title estimated-thread-cost must select thread usage")
	}
	if NewStatusSurfaceSelections([]string{"model"}, nil).UsesThreadUsage() {
		t.Fatal("unrelated items must not select thread usage")
	}
}

func TestThreadUsagePreviewPlaceholdersLikeRust(t *testing.T) {
	line, ok := DefaultStatusSurfacePreviewData().StatusLineForItems([]bottompane.StatusLineItem{
		bottompane.StatusLineThreadCredits,
		bottompane.StatusLineEstimatedThreadCost,
	}, false)
	if !ok || line.PlainText() != "5.2 credits \u00b7 ~$1.82" {
		t.Fatalf("preview line = %q ok=%v", line.PlainText(), ok)
	}

	credits := int64(5_200_000)
	ent26 := NewStatusControlsState(StatusControlsRuntime{
		PlanType:                 "ent26",
		ThreadUsageAvailable:     true,
		ThreadUsageCreditsMicros: &credits,
	})
	if _, ok := ent26.StatusSurfacePreviewData().ValueFor(StatusPreviewEstimatedThreadCost); ok {
		t.Fatal("an estimate without a dollar amount must drop the cost placeholder")
	}
}

// Rust terminal_title_value_for_item returns None for unavailable values, so
// the real title never renders the setup picker's placeholders.
func TestTerminalTitleOmitsUnavailableValuesLikeRust(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{CWD: "/repo", ProjectName: "repo"})
	state.TerminalTitleConfigured = true
	state.TerminalTitleIDs = []string{"project-name", "git-branch"}
	result := state.RefreshStatusSurfaces()
	if !result.TerminalTitleRendered || result.TerminalTitleText != "repo" {
		t.Fatalf("terminal title = %q rendered=%v", result.TerminalTitleText, result.TerminalTitleRendered)
	}
	if value, ok := state.TerminalTitlePreviewData().ValueFor(StatusPreviewGitBranch); !ok || value != "feat/awesome-feature" {
		t.Fatalf("setup preview placeholder = %q ok=%v", value, ok)
	}
}

func TestThreadNameAndTitleItemsLikeRust(t *testing.T) {
	named := NewStatusControlsState(StatusControlsRuntime{ThreadID: "thread-uuid", ThreadName: "Release triage"})
	if got, ok := named.StatusLineValueForItem(bottompane.StatusLineThreadName); !ok || got != "Release triage" {
		t.Fatalf("thread name = %q ok=%v", got, ok)
	}
	if got, ok := named.StatusLineValueForItem(bottompane.StatusLineThreadTitle); !ok || got != "Release triage" {
		t.Fatalf("thread title = %q ok=%v", got, ok)
	}
	unnamed := NewStatusControlsState(StatusControlsRuntime{ThreadID: "thread-uuid"})
	if _, ok := unnamed.StatusLineValueForItem(bottompane.StatusLineThreadName); ok {
		t.Fatal("thread-name must be omitted when unnamed")
	}
	if got, ok := unnamed.StatusLineValueForItem(bottompane.StatusLineThreadTitle); !ok || got != "thread-uuid" {
		t.Fatalf("unnamed title = %q ok=%v", got, ok)
	}
}
