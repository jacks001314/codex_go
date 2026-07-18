package streaming

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/streaming/table_holdback.rs.

type TableHoldbackKind int

const (
	TableHoldbackNone TableHoldbackKind = iota
	TableHoldbackPendingHeader
	TableHoldbackConfirmed
)

type TableHoldbackState struct {
	Kind        TableHoldbackKind
	SourceStart int
}

type previousLineState struct {
	sourceStart int
	fenceKind   tui.FenceKind
	isHeader    bool
}

type TableHoldbackScanner struct {
	sourceOffset        int
	fenceTracker        *tui.FenceTracker
	previousLine        *previousLineState
	pendingHeaderStart  *int
	confirmedTableStart *int
}

func NewTableHoldbackScanner() *TableHoldbackScanner {
	return &TableHoldbackScanner{fenceTracker: tui.NewFenceTracker()}
}

func (s *TableHoldbackScanner) Reset() {
	*s = *NewTableHoldbackScanner()
}

func (s *TableHoldbackScanner) State() TableHoldbackState {
	if s.confirmedTableStart != nil {
		return TableHoldbackState{Kind: TableHoldbackConfirmed, SourceStart: *s.confirmedTableStart}
	}
	if s.pendingHeaderStart != nil {
		return TableHoldbackState{Kind: TableHoldbackPendingHeader, SourceStart: *s.pendingHeaderStart}
	}
	return TableHoldbackState{Kind: TableHoldbackNone}
}

func (s *TableHoldbackScanner) PushSourceChunk(sourceChunk string) {
	if sourceChunk == "" {
		return
	}
	for _, sourceLine := range splitInclusiveNewline(sourceChunk) {
		s.pushLine(sourceLine)
	}
}

func (s *TableHoldbackScanner) pushLine(sourceLine string) {
	line := strings.TrimSuffix(sourceLine, "\n")
	sourceStart := s.sourceOffset
	fenceKind := s.fenceTracker.Kind()
	candidateText, hasCandidate := "", false
	if fenceKind != tui.FenceOther {
		candidateText, hasCandidate = tableCandidateText(line)
	}
	isHeader := hasCandidate && tui.IsTableHeaderLine(candidateText)
	isDelimiter := hasCandidate && tui.IsTableDelimiterLine(candidateText)

	if s.confirmedTableStart == nil && s.previousLine != nil &&
		s.previousLine.fenceKind != tui.FenceOther &&
		fenceKind != tui.FenceOther &&
		s.previousLine.isHeader &&
		isDelimiter {
		start := s.previousLine.sourceStart
		s.confirmedTableStart = &start
		s.pendingHeaderStart = nil
	}

	if s.confirmedTableStart == nil && strings.TrimSpace(line) != "" {
		if fenceKind != tui.FenceOther && isHeader {
			start := sourceStart
			s.pendingHeaderStart = &start
		} else {
			s.pendingHeaderStart = nil
		}
	}

	s.previousLine = &previousLineState{sourceStart: sourceStart, fenceKind: fenceKind, isHeader: isHeader}
	s.fenceTracker.Advance(line)
	s.sourceOffset += len(sourceLine)
}

func TableHoldbackStateFor(source string) TableHoldbackState {
	scanner := NewTableHoldbackScanner()
	scanner.PushSourceChunk(source)
	return scanner.State()
}

func tableCandidateText(line string) (string, bool) {
	stripped := strings.TrimSpace(tui.StripBlockquotePrefix(line))
	if _, ok := tui.ParseTableSegments(stripped); ok {
		return stripped, true
	}
	return "", false
}

func splitInclusiveNewline(text string) []string {
	if text == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i, r := range text {
		if r == '\n' {
			out = append(out, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}
