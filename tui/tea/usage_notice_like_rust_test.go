package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func usageNoticeModel(t *testing.T, snapshot chatwidget.RateLimitSnapshot, width int) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{Width: width, Height: 24})
	model.rateLimitSnapshots = map[string]chatwidget.RateLimitSnapshot{"codex": snapshot}
	model.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	model.View()
	model.activityFollow = true
	return model
}

func usageNoticeWindowPtr(used float64, minutes int64, resetsAt *int64) *chatwidget.RateLimitWindow {
	return &chatwidget.RateLimitWindow{
		UsedPercent:        used,
		WindowDurationMins: &minutes,
		ResetsAt:           resetsAt,
	}
}

// Rust #47096: details target half the row, but the shortest complete warning
// may use its full width on narrow terminals.
func TestUsageNoticeLineUsesHalfRowWidthLikeRust(t *testing.T) {
	reset := int64(1_700_003_600)
	model := usageNoticeModel(t, chatwidget.RateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "plus",
		Primary:  usageNoticeWindowPtr(80, 300, &reset),
	}, 80)
	line, ok := model.usageNoticeLine(78)
	if !ok || !strings.Contains(line, "⚠ 5h limit: 20% left") {
		t.Fatalf("half-row notice = %q ok=%v", line, ok)
	}
	if strings.Contains(line, "resets at") {
		t.Fatalf("half-row notice kept reset details: %q", line)
	}

	wide, ok := model.usageNoticeLine(200)
	if !ok || !strings.Contains(wide, "resets at") {
		t.Fatalf("wide notice = %q ok=%v, want reset details", wide, ok)
	}

	narrow, ok := model.usageNoticeLine(30)
	if !ok || narrow != "⚠ 5h 20% left" {
		t.Fatalf("narrow notice = %q ok=%v, want compact warning", narrow, ok)
	}
}

func TestUsageNoticeThresholdsAndCreditsLikeRust(t *testing.T) {
	primary := usageNoticeWindowPtr(55, 300, nil)
	model := usageNoticeModel(t, chatwidget.RateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "plus",
		Primary:  primary,
	}, 80)
	if _, ok := model.usageNoticeLine(78); !ok {
		t.Fatal("Plus 5h usage at 55% should show the notice")
	}
	primary.UsedPercent = 40
	if _, ok := model.usageNoticeLine(78); ok {
		t.Fatal("Plus 5h usage below 50% should hide the notice")
	}

	primary.UsedPercent = 55
	model.chatGPTPlanType = "free"
	model.rateLimitSnapshots["codex"] = chatwidget.RateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "free",
		Primary:  primary,
	}
	if _, ok := model.usageNoticeLine(78); ok {
		t.Fatal("non-Plus 5h usage below 75% should hide the notice")
	}

	model.rateLimitSnapshots["codex"] = chatwidget.RateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "plus",
		Primary:  usageNoticeWindowPtr(100, 300, nil),
		Credits:  &chatwidget.RateLimitCredits{HasCredits: true},
	}
	if _, ok := model.usageNoticeLine(78); ok {
		t.Fatal("workspace credits should suppress the usage notice")
	}
}

func TestUsageNoticeReplacesPassiveFooterLikeRust(t *testing.T) {
	model := usageNoticeModel(t, chatwidget.RateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "plus",
		Primary:  usageNoticeWindowPtr(96, 300, nil),
	}, 100)
	footer, _ := model.footerHelpLine()
	if !strings.Contains(footer, "⚠ 5h limit: only 4% left") {
		t.Fatalf("footer = %q, want usage notice", footer)
	}
	if strings.Contains(footer, footerHelpText) {
		t.Fatalf("footer kept passive hints: %q", footer)
	}

	model.slashPopup.Active = true
	footer, _ = model.footerHelpLine()
	if !strings.Contains(footer, footerHelpText) {
		t.Fatalf("popup footer = %q, want passive hints", footer)
	}
}
