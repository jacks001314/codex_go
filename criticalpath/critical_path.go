// Package criticalpath tracks persistent dependency paths and attributes turn
// time exclusively across their segments (Rust codex-utils-critical-path
// #57201).
//
// A path is an immutable linked list of observed work segments. Forks share
// their prefix, and a join keeps only the dependency that actually released the
// waiting operation. History is depth-limited; a path that exceeds the limit
// collapses into unknown work instead of claiming measured time.
package criticalpath

import (
	"time"
)

// MaxPathDepth bounds the retained segment history. Deeper observations keep
// their elapsed interval but lose the category detail.
const MaxPathDepth = 16_384

// WorkKind is the category attributed to one observed segment.
type WorkKind uint8

const (
	// WorkSampling is model sampling on the selected dependency path.
	WorkSampling WorkKind = iota
	// WorkCompaction is compaction work, including local bookkeeping.
	WorkCompaction
	// WorkAutoReview is automatic review work, such as reviewing a tool call
	// before execution.
	WorkAutoReview
	// WorkUserWait is time awaiting a human answer or approval on the selected
	// dependency path.
	WorkUserWait
	// WorkTools is tool execution work, including work after a dependency join
	// resumes. It is an aggregate category; tool-specific subtypes are not
	// represented by this calculator.
	WorkTools
	// WorkOverhead is time within the observed turn interval not covered by a
	// recorded work segment.
	WorkOverhead
	// WorkUnknown is work whose category is unavailable, including history
	// collapsed at the depth limit.
	WorkUnknown
)

// WorkInference is the legacy aggregate name for sampling work. New code should
// distinguish WorkSampling from WorkCompaction.
const WorkInference = WorkSampling

// Path is an immutable observed dependency path. The zero value is empty.
type Path struct {
	node *node
}

type node struct {
	previous Path
	kind     WorkKind
	reviews  []*ReviewExecution
	start    time.Time
	end      time.Time
	origin   time.Time
	depth    int
}

// Durations is the exclusive time attributed to each category within a
// requested interval.
type Durations struct {
	// Inference is the legacy aggregate of sampling and compaction.
	Inference  time.Duration
	Sampling   time.Duration
	Compaction time.Duration
	AutoReview time.Duration
	// AutoReviewOverhead excludes the union of observed reviewer child execution.
	// AutoReviewOverheadUnknown reports that attribution or history was incomplete.
	AutoReviewOverhead        time.Duration
	AutoReviewOverheadUnknown bool
	UserWait                  time.Duration
	Tools                     time.Duration
	Overhead                  time.Duration
	Unknown                   time.Duration
}

func (d *Durations) record(kind WorkKind, duration time.Duration) {
	switch kind {
	case WorkSampling:
		d.Sampling += duration
		d.Inference += duration
	case WorkCompaction:
		d.Compaction += duration
		d.Inference += duration
	case WorkAutoReview:
		d.AutoReview += duration
	case WorkUserWait:
		d.UserWait += duration
	case WorkTools:
		d.Tools += duration
	case WorkOverhead:
		d.Overhead += duration
	default:
		d.Unknown += duration
		d.AutoReviewOverheadUnknown = true
	}
}

// End reports the end of the last recorded segment. The bool is false for an
// empty path.
func (p Path) End() (time.Time, bool) {
	if p.node == nil {
		return time.Time{}, false
	}
	return p.node.end, true
}

// Empty reports whether the path holds no recorded segment.
func (p Path) Empty() bool {
	return p.node == nil
}

// Latest keeps the later-finishing path, choosing the receiver on a tie.
func (p Path) Latest(other Path) Path {
	otherEnd, otherOK := other.End()
	selfEnd, selfOK := p.End()
	if otherOK && (!selfOK || otherEnd.After(selfEnd)) {
		return other
	}
	return p
}

// Work appends work after the existing path ends, clipping overlap and ignoring
// empty segments. Exceeding the history limit collapses the retained interval
// into unknown work.
func (p Path) Work(start time.Time, end time.Time, kind WorkKind) Path {
	return p.append(start, end, kind, nil)
}

// AutoReview appends automatic review work together with the exact reviewer
// child executions active during the segment (Rust #59251).
func (p Path) AutoReview(start time.Time, end time.Time, reviews []*ReviewExecution) Path {
	return p.append(start, end, WorkAutoReview, reviews)
}

func (p Path) append(start time.Time, end time.Time, kind WorkKind, reviews []*ReviewExecution) Path {
	if previousEnd, ok := p.End(); ok && previousEnd.After(start) {
		start = previousEnd
	}
	if !start.Before(end) {
		return p
	}
	origin := start
	previousDepth := 0
	if p.node != nil {
		origin = p.node.origin
		previousDepth = p.node.depth
	}
	depth := previousDepth + 1
	if depth > MaxPathDepth {
		return Path{}.Work(origin, end, WorkUnknown)
	}
	return Path{node: &node{previous: p, kind: kind, reviews: reviews, start: start, end: end, origin: origin, depth: depth}}
}

// Join selects the full branch that released this join, not just its exposed
// waiting tail. Both branches carry their common causal prefix.
func (p Path) Join(start time.Time, end time.Time, dependency Path) Path {
	path := p.Latest(dependency)
	resumedAt := start
	if pathEnd, ok := path.End(); ok && pathEnd.After(resumedAt) {
		resumedAt = pathEnd
	}
	return path.Work(resumedAt, end, WorkTools)
}

// Durations attributes the requested interval exclusively, counting unrecorded
// gaps as overhead.
func (p Path) Durations(start time.Time, end time.Time) Durations {
	var totals Durations
	path := p
	for path.node != nil {
		if !end.After(start) {
			break
		}
		current := path.node
		segmentEnd := end
		if current.end.Before(segmentEnd) {
			segmentEnd = current.end
		}
		segmentStart := start
		if current.start.After(segmentStart) {
			segmentStart = current.start
		}
		if end.After(current.end) {
			gapStart := start
			if current.end.After(gapStart) {
				gapStart = current.end
			}
			totals.record(WorkOverhead, end.Sub(gapStart))
		}
		if segmentStart.Before(segmentEnd) {
			totals.record(current.kind, segmentEnd.Sub(segmentStart))
			if current.kind == WorkAutoReview && !totals.AutoReviewOverheadUnknown {
				if overhead, ok := reviewOverhead(current.reviews, segmentStart, segmentEnd); ok {
					totals.AutoReviewOverhead += overhead
				} else {
					totals.AutoReviewOverheadUnknown = true
				}
			}
		}
		if current.start.Before(end) {
			end = current.start
		}
		path = current.previous
	}
	if start.Before(end) {
		totals.record(WorkOverhead, end.Sub(start))
	}
	return totals
}

// DurationMS truncates a duration to whole milliseconds, saturating at the
// largest uint64 value like Rust's u64::try_from(as_millis()).
func DurationMS(duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	ms := duration.Milliseconds()
	if ms < 0 {
		return 0
	}
	return uint64(ms)
}
