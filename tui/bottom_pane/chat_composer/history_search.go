package chatcomposer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	codextui "codex_go/tui"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/chat_composer/history_search.rs.

type HistorySearchStatus string

const (
	HistorySearchIdle      HistorySearchStatus = "idle"
	HistorySearchSearching HistorySearchStatus = "searching"
	HistorySearchMatch     HistorySearchStatus = "match"
	HistorySearchNoMatch   HistorySearchStatus = "no_match"
)

type HistorySearchDirection string

const (
	HistorySearchOlder HistorySearchDirection = "older"
	HistorySearchNewer HistorySearchDirection = "newer"
)

type HistorySearchResultKind string

const (
	HistorySearchResultFound      HistorySearchResultKind = "found"
	HistorySearchResultPending    HistorySearchResultKind = "pending"
	HistorySearchResultAtBoundary HistorySearchResultKind = "at_boundary"
	HistorySearchResultNotFound   HistorySearchResultKind = "not_found"
)

type HistorySearchResult struct {
	Kind  HistorySearchResultKind
	Entry string
}

type HistorySearchSession struct {
	OriginalDraft DraftState
	PreviewDraft  DraftState
	Query         string
	Status        HistorySearchStatus
	Active        bool

	entries []string
	matches []string
	index   int
}

func NewHistorySearchSession(originalDraft DraftState, entries []string) *HistorySearchSession {
	original := cloneDraftState(originalDraft)
	return &HistorySearchSession{
		OriginalDraft: original,
		PreviewDraft:  cloneDraftState(original),
		Status:        HistorySearchIdle,
		Active:        true,
		entries:       append([]string(nil), entries...),
		index:         -1,
	}
}

func BeginHistorySearch(originalDraft DraftState, entries []string) *HistorySearchSession {
	return NewHistorySearchSession(originalDraft, entries)
}

func (s *HistorySearchSession) SetEntries(entries []string) {
	if s == nil {
		return
	}
	s.entries = append([]string(nil), entries...)
	s.matches = nil
	s.index = -1
}

func (s *HistorySearchSession) UpdateQuery(query string) (DraftState, HistorySearchResult) {
	if s == nil {
		return DraftState{}, HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	s.Query = query
	s.Status = HistorySearchSearching
	s.PreviewDraft = cloneDraftState(s.OriginalDraft)
	if query == "" {
		s.resetSearch()
		s.Status = HistorySearchIdle
		return cloneDraftState(s.OriginalDraft), HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	result := s.search(query, HistorySearchOlder, true)
	return s.ApplyResult(result), result
}

func (s *HistorySearchSession) SearchInDirection(direction HistorySearchDirection) (DraftState, HistorySearchResult) {
	if s == nil {
		return DraftState{}, HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	if s.Query == "" {
		s.resetSearch()
		s.Status = HistorySearchIdle
		s.PreviewDraft = cloneDraftState(s.OriginalDraft)
		return cloneDraftState(s.OriginalDraft), HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	result := s.search(s.Query, direction, false)
	return s.ApplyResult(result), result
}

func (s *HistorySearchSession) ApplyResult(result HistorySearchResult) DraftState {
	if s == nil {
		return DraftState{}
	}
	switch result.Kind {
	case HistorySearchResultFound:
		s.Status = HistorySearchMatch
		s.PreviewDraft = draftFromHistoryText(s.OriginalDraft, result.Entry)
	case HistorySearchResultPending:
		s.Status = HistorySearchSearching
	case HistorySearchResultAtBoundary:
		s.Status = HistorySearchMatch
	case HistorySearchResultNotFound:
		s.Status = HistorySearchNoMatch
		s.PreviewDraft = cloneDraftState(s.OriginalDraft)
	default:
		s.Status = HistorySearchNoMatch
		s.PreviewDraft = cloneDraftState(s.OriginalDraft)
	}
	return cloneDraftState(s.PreviewDraft)
}

func (s *HistorySearchSession) AppendQueryRune(r rune) (DraftState, HistorySearchResult) {
	if s == nil {
		return DraftState{}, HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	return s.UpdateQuery(s.Query + string(r))
}

func (s *HistorySearchSession) BackspaceQuery() (DraftState, HistorySearchResult) {
	if s == nil || s.Query == "" {
		return DraftState{}, HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	_, size := utf8.DecodeLastRuneInString(s.Query)
	if size <= 0 {
		return s.UpdateQuery("")
	}
	return s.UpdateQuery(s.Query[:len(s.Query)-size])
}

func (s *HistorySearchSession) ClearQuery() (DraftState, HistorySearchResult) {
	if s == nil {
		return DraftState{}, HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	return s.UpdateQuery("")
}

func (s *HistorySearchSession) Cancel() DraftState {
	if s == nil {
		return DraftState{}
	}
	s.Active = false
	s.resetSearch()
	s.Status = HistorySearchIdle
	s.PreviewDraft = cloneDraftState(s.OriginalDraft)
	return cloneDraftState(s.OriginalDraft)
}

func (s *HistorySearchSession) Accept() (DraftState, bool) {
	if s == nil || s.Status != HistorySearchMatch {
		if s == nil {
			return DraftState{}, false
		}
		return cloneDraftState(s.PreviewDraft), false
	}
	s.Active = false
	s.resetSearch()
	accepted := cloneDraftState(s.PreviewDraft)
	accepted.Cursor = len(accepted.Text)
	s.PreviewDraft = cloneDraftState(accepted)
	return accepted, true
}

func (s *HistorySearchSession) FooterLine() (string, bool) {
	if s == nil || !s.Active {
		return "", false
	}
	line := "reverse-i-search: " + s.Query
	switch s.Status {
	case HistorySearchSearching:
		line += "  searching"
	case HistorySearchMatch:
		line += "  enter accept | esc cancel"
	case HistorySearchNoMatch:
		line += "  no match"
	}
	return line, true
}

func (s *HistorySearchSession) CursorColumn(indent int, width int) (int, bool) {
	if s == nil || !s.Active {
		return 0, false
	}
	column := indent + codextui.DisplayWidth("reverse-i-search: ") + codextui.DisplayWidth(s.Query)
	if width > 0 && column >= width {
		column = width - 1
	}
	if column < 0 {
		column = 0
	}
	return column, true
}

func (s *HistorySearchSession) HighlightRanges() []TextRange {
	if s == nil || s.Status != HistorySearchMatch || s.Query == "" {
		return nil
	}
	return CaseInsensitiveMatchRanges(s.PreviewDraft.Text, s.Query)
}

func (s *HistorySearchSession) search(query string, direction HistorySearchDirection, restart bool) HistorySearchResult {
	if s == nil {
		return HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	if restart || s.matches == nil {
		s.matches = uniqueHistoryMatchesNewestFirst(s.entries, query)
		if len(s.matches) == 0 {
			s.index = -1
			return HistorySearchResult{Kind: HistorySearchResultNotFound}
		}
		s.index = 0
		return HistorySearchResult{Kind: HistorySearchResultFound, Entry: s.matches[0]}
	}
	if len(s.matches) == 0 || s.index < 0 {
		return HistorySearchResult{Kind: HistorySearchResultNotFound}
	}
	switch direction {
	case HistorySearchNewer:
		if s.index == 0 {
			return HistorySearchResult{Kind: HistorySearchResultAtBoundary}
		}
		s.index--
	default:
		if s.index+1 >= len(s.matches) {
			return HistorySearchResult{Kind: HistorySearchResultAtBoundary}
		}
		s.index++
	}
	return HistorySearchResult{Kind: HistorySearchResultFound, Entry: s.matches[s.index]}
}

func (s *HistorySearchSession) resetSearch() {
	if s == nil {
		return
	}
	s.matches = nil
	s.index = -1
}

func SearchHistory(entries []string, query string) []string {
	return uniqueHistoryMatchesNewestFirst(entries, query)
}

func uniqueHistoryMatchesNewestFirst(entries []string, query string) []string {
	queryLower := strings.ToLower(query)
	seen := map[string]bool{}
	matches := []string{}
	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if queryLower != "" && !strings.Contains(strings.ToLower(entry), queryLower) {
			continue
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		matches = append(matches, entry)
	}
	return matches
}

func CaseInsensitiveMatchRanges(text string, query string) []TextRange {
	if query == "" {
		return nil
	}
	queryLower := strings.ToLower(query)
	if queryLower == "" {
		return nil
	}
	folded := strings.Builder{}
	foldedSpans := []struct {
		FoldedStart int
		FoldedEnd   int
		Original    TextRange
	}{}
	for originalStart, r := range text {
		originalRange := TextRange{Start: originalStart, End: originalStart + utf8.RuneLen(r)}
		lower := unicode.ToLower(r)
		foldedStart := folded.Len()
		folded.WriteRune(lower)
		foldedSpans = append(foldedSpans, struct {
			FoldedStart int
			FoldedEnd   int
			Original    TextRange
		}{
			FoldedStart: foldedStart,
			FoldedEnd:   folded.Len(),
			Original:    originalRange,
		})
	}
	foldedText := folded.String()
	ranges := []TextRange{}
	searchFrom := 0
	for searchFrom <= len(foldedText) {
		relativeStart := strings.Index(foldedText[searchFrom:], queryLower)
		if relativeStart < 0 {
			break
		}
		foldedStart := searchFrom + relativeStart
		foldedEnd := foldedStart + len(queryLower)
		var originalStart *int
		originalEnd := 0
		for _, span := range foldedSpans {
			if span.FoldedEnd <= foldedStart || span.FoldedStart >= foldedEnd {
				continue
			}
			if originalStart == nil {
				start := span.Original.Start
				originalStart = &start
			}
			originalEnd = span.Original.End
		}
		if originalStart != nil {
			ranges = append(ranges, TextRange{Start: *originalStart, End: originalEnd})
		}
		searchFrom = foldedEnd
	}
	return ranges
}

func draftFromHistoryText(original DraftState, text string) DraftState {
	draft := cloneDraftState(original)
	draft.Text = text
	draft.Cursor = len(text)
	return draft
}

func cloneDraftState(draft DraftState) DraftState {
	out := draft
	out.PendingPastes = append([]PendingPaste(nil), draft.PendingPastes...)
	if draft.MentionBindings != nil {
		out.MentionBindings = make(map[uint64]ComposerMentionBinding, len(draft.MentionBindings))
		for key, value := range draft.MentionBindings {
			out.MentionBindings[key] = value
		}
	}
	out.RecentSubmissionMentionBindings = append([]MentionBinding(nil), draft.RecentSubmissionMentionBindings...)
	return out
}
