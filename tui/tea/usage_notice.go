package tea

import (
	"fmt"
	"strings"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	statuspkg "codex_go/tui/status"
)

// usageNoticeApplies mirrors Rust's composer_hint gates: the notice replaces
// the passive footer only while the transcript is following, no popup/modal or
// active interaction owns the row, and no backend banner takes precedence.
func (m *Model) usageNoticeApplies() bool {
	if m == nil || m.backendBannerApplicable() {
		return false
	}
	if m.modal != nil || m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil {
		return false
	}
	if m.vimSearchMode || m.mainFind.active() || m.activityFocus.active || m.escBacktrackHint {
		return false
	}
	if m.renderedSelection.active || m.backtrack.Primed || m.backtrack.OverlayPreviewActive {
		return false
	}
	return m.activityFollow || m.transcript.AtBottom()
}

// usageNoticeLine renders the account quota notice within Rust #47096's width
// policy: details prefer half the row, while the shortest complete warning may
// use its full width on narrow terminals.
func (m *Model) usageNoticeLine(width int) (string, bool) {
	if m == nil || width <= 0 {
		return "", false
	}
	snapshot, ok := m.rateLimitSnapshots["codex"]
	if !ok {
		snapshot, ok = m.rateLimitSnapshots[""]
	}
	if !ok {
		return "", false
	}
	if snapshot.Credits != nil && (snapshot.Credits.Unlimited || snapshot.Credits.HasCredits) {
		return "", false
	}
	planType := strings.TrimSpace(snapshot.PlanType)
	if planType == "" {
		planType = strings.TrimSpace(m.chatGPTPlanType)
	}
	window, secondary, ok := usageNoticeWindow(snapshot, planType)
	if !ok {
		return "", false
	}
	label := chatwidget.LimitLabelForWindow(window.WindowDurationMins, secondary)
	remaining := "<1%"
	if window.UsedPercent < 100 {
		remaining = fmt.Sprintf("%.0f%%", max(100-window.UsedPercent, 0))
	}
	emphasis := ""
	if window.UsedPercent >= 95 {
		emphasis = "only "
	}
	summary := fmt.Sprintf("⚠ %s limit: %s%s left", label, emphasis, remaining)
	compact := fmt.Sprintf("⚠ %s %s left", label, remaining)
	reset := ""
	now := m.currentTime()
	if window.ResetsAt != nil && *window.ResetsAt > now.Unix() {
		reset = " · resets at " + statuspkg.FormatResetTimestamp(time.Unix(*window.ResetsAt, 0), now)
	}
	candidates := []string{
		summary + reset + " · /status",
		summary + " · /status",
		summary,
		compact,
	}
	noticeWidth := min(max(width/2, codextui.DisplayWidth(compact)), width)
	for _, candidate := range candidates {
		if codextui.DisplayWidth(candidate) <= noticeWidth {
			return candidate, true
		}
	}
	return "", false
}

func usageNoticeWindow(snapshot chatwidget.RateLimitSnapshot, planType string) (*chatwidget.RateLimitWindow, bool, bool) {
	var selected *chatwidget.RateLimitWindow
	secondary := false
	for _, candidate := range []struct {
		window    *chatwidget.RateLimitWindow
		secondary bool
	}{
		{window: snapshot.Secondary, secondary: true},
		{window: snapshot.Primary},
	} {
		if candidate.window == nil ||
			candidate.window.UsedPercent < usageNoticeThreshold(planType, candidate.window.WindowDurationMins) {
			continue
		}
		if selected == nil || candidate.window.UsedPercent >= selected.UsedPercent {
			selected = candidate.window
			secondary = candidate.secondary
		}
	}
	return selected, secondary, selected != nil
}

func usageNoticeThreshold(planType string, windowMinutes *int64) float64 {
	planType = strings.ToLower(strings.TrimSpace(planType))
	if (planType == "plus" || planType == "team") && windowMinutes != nil {
		if label, ok := chatwidget.LimitsDuration(*windowMinutes); ok && label == "5h" {
			return 50
		}
	}
	return 75
}
