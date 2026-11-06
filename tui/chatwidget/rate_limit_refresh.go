package chatwidget

import (
	"math"
	"strings"
	"time"
)

// Rate-limit polling cadence, mirrored from Rust app/rate_limit_refresh.rs
// (#42372): the TUI re-reads account usage more often as the window fills so a
// Luna Reserve entry or a recovery is noticed without waiting a minute.
const (
	rateLimitRefreshCritical = 5 * time.Second
	rateLimitRefreshHigh     = 15 * time.Second
	rateLimitRefreshElevated = 30 * time.Second
	rateLimitRefreshIdle     = 60 * time.Second
)

// RateLimitRefreshInterval reports how long the TUI may wait before reading
// account limits again. Only the aggregate "codex" snapshot and the snapshot
// whose limit id matches the current model contribute, mirroring Rust's filter
// on limit_id == "codex" || snapshot.limit_name == current_model. Non-finite
// used percentages are ignored; no reading at all keeps the idle cadence.
func RateLimitRefreshInterval(snapshots map[string]RateLimitSnapshot, currentModel string) time.Duration {
	highest := 0.0
	currentModel = strings.TrimSpace(currentModel)
	for limitID, snapshot := range snapshots {
		if !strings.EqualFold(strings.TrimSpace(limitID), "codex") && !strings.EqualFold(currentModel, strings.TrimSpace(limitID)) {
			continue
		}
		for _, window := range []*RateLimitWindow{snapshot.Primary, snapshot.Secondary} {
			if window == nil || math.IsNaN(window.UsedPercent) || math.IsInf(window.UsedPercent, 0) {
				continue
			}
			if window.UsedPercent > highest {
				highest = window.UsedPercent
			}
		}
	}
	switch {
	case highest >= 99.0:
		return rateLimitRefreshCritical
	case highest >= 90.0:
		return rateLimitRefreshHigh
	case highest >= 75.0:
		return rateLimitRefreshElevated
	default:
		return rateLimitRefreshIdle
	}
}
