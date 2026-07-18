package streaming

import "time"

// Rust parity: codex-rs/tui/src/streaming/chunking.rs.

const (
	enterQueueDepthLines  = 8
	enterOldestAge        = 120 * time.Millisecond
	exitQueueDepthLines   = 2
	exitOldestAge         = 40 * time.Millisecond
	exitHold              = 250 * time.Millisecond
	reenterCatchUpHold    = 250 * time.Millisecond
	severeQueueDepthLines = 64
	severeOldestAge       = 300 * time.Millisecond
)

type ChunkingMode int

const (
	ChunkingSmooth ChunkingMode = iota
	ChunkingCatchUp
)

type QueueSnapshot struct {
	QueuedLines int
	OldestAge   *time.Duration
}

type DrainPlanKind int

const (
	DrainSingle DrainPlanKind = iota
	DrainBatch
)

type DrainPlan struct {
	Kind  DrainPlanKind
	Limit int
}

type ChunkingDecision struct {
	Mode           ChunkingMode
	EnteredCatchUp bool
	DrainPlan      DrainPlan
}

type AdaptiveChunkingPolicy struct {
	mode                    ChunkingMode
	belowExitThresholdSince *time.Time
	lastCatchUpExitAt       *time.Time
}

func NewAdaptiveChunkingPolicy() *AdaptiveChunkingPolicy {
	return &AdaptiveChunkingPolicy{}
}

func (p *AdaptiveChunkingPolicy) Mode() ChunkingMode {
	if p == nil {
		return ChunkingSmooth
	}
	return p.mode
}

func (p *AdaptiveChunkingPolicy) Reset() {
	p.mode = ChunkingSmooth
	p.belowExitThresholdSince = nil
	p.lastCatchUpExitAt = nil
}

func (p *AdaptiveChunkingPolicy) Decide(snapshot QueueSnapshot, now time.Time) ChunkingDecision {
	if snapshot.QueuedLines == 0 {
		p.noteCatchUpExit(now)
		p.mode = ChunkingSmooth
		p.belowExitThresholdSince = nil
		return ChunkingDecision{Mode: p.mode, DrainPlan: DrainPlan{Kind: DrainSingle, Limit: 1}}
	}
	entered := false
	switch p.mode {
	case ChunkingSmooth:
		entered = p.maybeEnterCatchUp(snapshot, now)
	case ChunkingCatchUp:
		p.maybeExitCatchUp(snapshot, now)
	}
	plan := DrainPlan{Kind: DrainSingle, Limit: 1}
	if p.mode == ChunkingCatchUp {
		plan = DrainPlan{Kind: DrainBatch, Limit: max(snapshot.QueuedLines, 1)}
	}
	return ChunkingDecision{Mode: p.mode, EnteredCatchUp: entered, DrainPlan: plan}
}

func (p *AdaptiveChunkingPolicy) maybeEnterCatchUp(snapshot QueueSnapshot, now time.Time) bool {
	if !shouldEnterCatchUp(snapshot) {
		return false
	}
	if p.reentryHoldActive(now) && !isSevereBacklog(snapshot) {
		return false
	}
	p.mode = ChunkingCatchUp
	p.belowExitThresholdSince = nil
	p.lastCatchUpExitAt = nil
	return true
}

func (p *AdaptiveChunkingPolicy) maybeExitCatchUp(snapshot QueueSnapshot, now time.Time) {
	if !shouldExitCatchUp(snapshot) {
		p.belowExitThresholdSince = nil
		return
	}
	if p.belowExitThresholdSince == nil {
		p.belowExitThresholdSince = &now
		return
	}
	if now.Sub(*p.belowExitThresholdSince) >= exitHold {
		p.mode = ChunkingSmooth
		p.belowExitThresholdSince = nil
		p.lastCatchUpExitAt = &now
	}
}

func (p *AdaptiveChunkingPolicy) noteCatchUpExit(now time.Time) {
	if p.mode == ChunkingCatchUp {
		p.lastCatchUpExitAt = &now
	}
}

func (p *AdaptiveChunkingPolicy) reentryHoldActive(now time.Time) bool {
	return p.lastCatchUpExitAt != nil && now.Sub(*p.lastCatchUpExitAt) < reenterCatchUpHold
}

func shouldEnterCatchUp(snapshot QueueSnapshot) bool {
	return snapshot.QueuedLines >= enterQueueDepthLines ||
		(snapshot.OldestAge != nil && *snapshot.OldestAge >= enterOldestAge)
}

func shouldExitCatchUp(snapshot QueueSnapshot) bool {
	return snapshot.QueuedLines <= exitQueueDepthLines &&
		snapshot.OldestAge != nil &&
		*snapshot.OldestAge <= exitOldestAge
}

func isSevereBacklog(snapshot QueueSnapshot) bool {
	return snapshot.QueuedLines >= severeQueueDepthLines ||
		(snapshot.OldestAge != nil && *snapshot.OldestAge >= severeOldestAge)
}
