package tea

import "codex_go/tui/chatwidget"

// mergeRollingRateLimitSnapshot mirrors Rust UsageNoticeState::update for a
// rolling snapshot: metadata carries forward across sparse updates, and a
// lower usage count only confirms recovery when the reset window advanced.
func mergeRollingRateLimitSnapshot(previous chatwidget.RateLimitSnapshot, hasPrevious bool, incoming chatwidget.RateLimitSnapshot) chatwidget.RateLimitSnapshot {
	if !hasPrevious {
		return incoming
	}
	merged := incoming
	if merged.LimitID == "" {
		merged.LimitID = previous.LimitID
	}
	if merged.PlanType == "" {
		merged.PlanType = previous.PlanType
	}
	if merged.Credits == nil {
		merged.Credits = previous.Credits
	}
	planType := merged.PlanType
	merged.Primary = mergeRollingRateLimitWindow(previous.Primary, merged.Primary, planType)
	merged.Secondary = mergeRollingRateLimitWindow(previous.Secondary, merged.Secondary, planType)
	return merged
}

func mergeRollingRateLimitWindow(previous, incoming *chatwidget.RateLimitWindow, planType string) *chatwidget.RateLimitWindow {
	if incoming == nil {
		return previous
	}
	if previous == nil {
		return incoming
	}
	merged := *incoming
	if merged.WindowDurationMins == nil {
		merged.WindowDurationMins = cloneInt64PtrTea(previous.WindowDurationMins)
	}
	if merged.ResetsAt == nil {
		merged.ResetsAt = cloneInt64PtrTea(previous.ResetsAt)
	}
	threshold := usageNoticeThreshold(planType, merged.WindowDurationMins)
	if previous.UsedPercent >= threshold && merged.UsedPercent < threshold &&
		!rateLimitResetWindowAdvanced(previous.ResetsAt, merged.ResetsAt) {
		return previous
	}
	return &merged
}

func rateLimitResetWindowAdvanced(previous, incoming *int64) bool {
	return incoming != nil && (previous == nil || *incoming > *previous)
}
