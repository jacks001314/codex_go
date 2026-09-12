package chatwidget

// Live voice transcript rendering layered on the split-flap board. This is the
// Go counterpart of the Rust SplitFlapTranscriptCell: the inner history cell
// owns the text and the copy-friendly raw lines, and the board owns the flip.
//
// Go's history-cell model is plain text (DisplayLines/RawLines), so the Rust
// per-span colours (black board, cyan user, magenta assistant afterglow, dark
// grey while flipping) are not expressible at this layer and are applied, if at
// all, by the renderer that owns styling.

import (
	"strings"
	"time"

	historycell "codex_go/tui/history_cell"
)

// SplitFlapTranscriptCell animates one transcript cell.
type SplitFlapTranscriptCell struct {
	Inner historycell.HistoryCell
	Board *SplitFlapBoard
	// Now overrides the clock for deterministic frames.
	Now func() time.Time
}

// NewSplitFlapTranscriptCell builds the animated cell for one transcript row.
// previous reuses settled tiles when it is the same speaker's retained prefix.
func NewSplitFlapTranscriptCell(inner historycell.HistoryCell, role string, previous *SplitFlapTranscriptCell, discardedPrefixBytes int, animated bool, now time.Time) SplitFlapTranscriptCell {
	var previousBoard *SplitFlapBoard
	if previous != nil {
		previousBoard = previous.Board
	}
	return SplitFlapTranscriptCell{
		Inner: inner,
		Board: NewSplitFlapBoard(role, splitFlapCellTarget(inner), previousBoard, discardedPrefixBytes, animated, now),
	}
}

// DisplayLines renders the animated caption.
func (c SplitFlapTranscriptCell) DisplayLines(width int) []string {
	if c.Inner == nil {
		return nil
	}
	lines := c.Inner.DisplayLines(width)
	if c.Board == nil {
		return lines
	}
	return c.Board.AnimateLines(lines, width, c.elapsed())
}

// RawLines preserves the copy-friendly text, bypassing the animation.
func (c SplitFlapTranscriptCell) RawLines() []string {
	if c.Inner == nil {
		return nil
	}
	return c.Inner.RawLines()
}

// AnimationTick reports the current frame while the board animates.
func (c SplitFlapTranscriptCell) AnimationTick() (int, bool) {
	if c.Board == nil {
		return 0, false
	}
	return c.Board.AnimationTick(c.elapsed())
}

func (c SplitFlapTranscriptCell) elapsed() time.Duration {
	if c.Board == nil {
		return 0
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	return now().Sub(c.Board.StartedAt)
}

func splitFlapCellTarget(inner historycell.HistoryCell) string {
	if inner == nil {
		return ""
	}
	return strings.Join(inner.DisplayLines(1<<20), "\n")
}
