package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"codex_go/rollout"
)

// threadHistoryLineageWalk resolves immutable rollout ranges from the selected
// leaf toward its ancestors on demand (Rust #61840 LocalThreadStore LineageWalk).
// Segments are stored newest first so discovering one older segment is a push;
// only the final oldest-first view reverses them.
type threadHistoryLineageWalk struct {
	runtime           *StateRuntime
	ctx               context.Context
	requestedThreadID string
	historyPage       bool
	project           bool
	db                *sql.DB
	segments          []threadHistoryLineageSegment
	seen              map[string]bool
	nextThreadID      string
	nextEnd           *rollout.HistoryPosition
	done              bool
}

func (r *StateRuntime) newThreadHistoryLineageWalk(ctx context.Context, threadID string, historyPage, project bool) *threadHistoryLineageWalk {
	if ctx == nil {
		ctx = context.Background()
	}
	threadID = strings.TrimSpace(threadID)
	return &threadHistoryLineageWalk{
		runtime:           r,
		ctx:               ctx,
		requestedThreadID: threadID,
		historyPage:       historyPage,
		project:           project,
		seen:              map[string]bool{},
		nextThreadID:      threadID,
		segments:          make([]threadHistoryLineageSegment, 0, 1),
	}
}

// nextOlder resolves one more segment, projecting it when the walk serves
// history pages. It reports false once the lineage is exhausted, and never
// opens an ancestor the caller did not ask for.
func (w *threadHistoryLineageWalk) nextOlder() (bool, error) {
	if w == nil || w.done {
		return false, nil
	}
	threadID := w.nextThreadID
	if w.seen[threadID] {
		return false, invalidPaginatedLineage(w.requestedThreadID, "cycle detected")
	}
	w.seen[threadID] = true
	existing, meta, err := w.runtime.resolveLineageRolloutCopy(w.ctx, threadID, w.requestedThreadID)
	if err != nil {
		return false, err
	}
	end := w.nextEnd
	// History pages read projected rows and only need the ancestor's metadata
	// for lineage bounds, so an incomplete ancestor tail does not invalidate the
	// page (Rust #58960). Context loading and fork preparation still require the
	// cutoff itself to be durable.
	if end != nil && !w.historyPage {
		if end.EndOrdinalExclusive == 0 {
			return false, invalidPaginatedLineage(w.requestedThreadID, "cutoff cannot include source session metadata")
		}
		if byteLength, lengthErr := rollout.RolloutByteLength(existing); lengthErr != nil {
			return false, lengthErr
		} else if end.EndByteOffset > byteLength {
			return false, invalidPaginatedLineage(w.requestedThreadID, "cutoff byte offset is past the source rollout")
		}
	}
	start := uint64(1)
	if meta.HistoryBase != nil {
		if meta.HistoryBase.EndOrdinalExclusive == ^uint64(0) {
			return false, invalidPaginatedLineage(w.requestedThreadID, "source ordinal overflow")
		}
		start = meta.HistoryBase.EndOrdinalExclusive + 1
	}
	// Reverts can point to earlier records of the same child. Stop at the
	// original parent bookmark so views never traverse inherited history.
	atParent := false
	if w.historyPage && meta.AttachedThread && meta.HistoryBase != nil {
		if forkOrdinal := rollout.ForkedFromOrdinalAtPath(meta, existing); forkOrdinal != nil && meta.HistoryBase.EndOrdinalExclusive <= *forkOrdinal {
			atParent = true
			adjusted, adjustErr := rollout.AttachedHistoryStart(existing, start)
			if adjustErr != nil {
				return false, fmt.Errorf("resolve attached history start for %s: %w", threadID, adjustErr)
			}
			start = adjusted
		}
	}
	segment := threadHistoryLineageSegment{ThreadID: threadID, Path: existing, Start: start, End: cloneRolloutHistoryPosition(end), Meta: meta}
	if w.project {
		if err := w.materialize(segment); err != nil {
			return false, err
		}
	}
	w.segments = append(w.segments, segment)
	if meta.HistoryBase == nil || atParent {
		w.done = true
		return true, nil
	}
	w.nextThreadID = meta.HistoryBase.ThreadID
	w.nextEnd = cloneRolloutHistoryPosition(meta.HistoryBase)
	return true, nil
}

func (w *threadHistoryLineageWalk) materialize(segment threadHistoryLineageSegment) error {
	if w.db == nil {
		db, err := w.runtime.ThreadHistoryDB(w.ctx)
		if err != nil {
			return err
		}
		w.db = db
	}
	initialOrdinal := uint64(0)
	if segment.Meta.HistoryBase != nil {
		initialOrdinal = segment.Meta.HistoryBase.EndOrdinalExclusive
	}
	return w.runtime.MaterializeThreadHistory(w.ctx, w.db, segment.ThreadID, segment.Path, initialOrdinal, segment.Meta.SubagentHistoryStartOrdinal)
}

// atDepth returns the segment at the given depth from the leaf (0 is newest)
// together with every newer segment, resolving more ancestors only when the
// requested depth needs them.
func (w *threadHistoryLineageWalk) atDepth(depth int) (threadHistoryLineageSegment, []threadHistoryLineageSegment, bool, error) {
	if depth < 0 {
		return threadHistoryLineageSegment{}, nil, false, nil
	}
	for len(w.segments) <= depth {
		more, err := w.nextOlder()
		if err != nil {
			return threadHistoryLineageSegment{}, nil, false, err
		}
		if !more {
			return threadHistoryLineageSegment{}, nil, false, nil
		}
	}
	return w.segments[depth], w.segments[:depth:depth], true, nil
}

// resolveAll opens every remaining ancestor and returns the lineage oldest
// first, matching the full-resolution callers (ascending reads, update replay,
// item anchors and inherited summaries).
func (w *threadHistoryLineageWalk) resolveAll() ([]threadHistoryLineageSegment, error) {
	for {
		more, err := w.nextOlder()
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
	}
	out := make([]threadHistoryLineageSegment, len(w.segments))
	for i := range w.segments {
		out[len(w.segments)-1-i] = w.segments[i]
	}
	return out, nil
}

// threadHistoryPageSegment is one page range plus the newer segments whose
// turns shadow it.
type threadHistoryPageSegment struct {
	segment threadHistoryLineageSegment
	newer   []threadHistoryLineageSegment
	cursor  *historyCursor
}

// threadHistoryPageSegments supplies page ranges in sort order, seeking a
// cursor and walking ancestors only as far as the page needs (Rust #61840
// thread_history::page_segments::PageSegments).
type threadHistoryPageSegments struct {
	walk      *threadHistoryLineageWalk
	step      int
	nextDepth int
	exhausted bool
	cursor    *historyCursor
}

func newThreadHistoryPageSegments(walk *threadHistoryLineageWalk, direction ThreadHistorySortDirection, cursor *historyCursor) (*threadHistoryPageSegments, error) {
	if walk == nil {
		return nil, fmt.Errorf("nil thread history lineage walk")
	}
	segments := &threadHistoryPageSegments{walk: walk, cursor: cursor}
	if direction == ThreadHistorySortAsc {
		// Ascending reads must discover the oldest segment up front.
		all, err := walk.resolveAll()
		if err != nil {
			return nil, err
		}
		segments.step = -1
		segments.nextDepth = len(all) - 1
	} else {
		// Descending reads start at the leaf and seek only as far as the cursor.
		segments.step = 1
		segments.nextDepth = 0
	}
	if cursor != nil {
		for {
			segment, _, ok, err := walk.atDepth(segments.nextDepth)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, invalidThreadHistory("invalid cursor: position outside thread lineage")
			}
			if historySegmentContainsOrdinal(segment, cursor.RolloutOrdinal) {
				break
			}
			segments.nextDepth += segments.step
		}
	}
	return segments, nil
}

func (p *threadHistoryPageSegments) next() (*threadHistoryPageSegment, error) {
	if p == nil || p.exhausted {
		return nil, nil
	}
	segment, newer, ok, err := p.walk.atDepth(p.nextDepth)
	if err != nil {
		return nil, err
	}
	p.nextDepth += p.step
	if p.nextDepth < 0 || !ok {
		p.exhausted = true
	}
	if !ok {
		return nil, nil
	}
	selected := &threadHistoryPageSegment{segment: segment, newer: newer}
	selected.cursor = p.cursor
	p.cursor = nil
	return selected, nil
}

func historySegmentContainsOrdinal(segment threadHistoryLineageSegment, ordinal uint64) bool {
	return ordinal >= segment.Start && (segment.End == nil || ordinal < segment.End.EndOrdinalExclusive)
}
