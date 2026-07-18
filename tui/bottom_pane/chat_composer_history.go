package bottompane

import "strings"

// Rust parity subset: codex-rs/tui/src/bottom_pane/chat_composer_history.rs.

type HistoryEntry struct {
	Text            string
	Attachments     []ComposerAttachment
	RemoteImageURLs []string
	MentionBindings []MentionBinding
	PendingPastes   []HistoryPendingPaste
}

type MentionBinding struct {
	Sigil   rune
	Mention string
	Path    string
}

type HistoryPendingPaste struct {
	Placeholder string
	Content     string
}

func NewHistoryEntry(text string) HistoryEntry {
	return HistoryEntry{Text: text}
}

func (e HistoryEntry) IsEmpty() bool {
	return e.Text == "" &&
		len(e.Attachments) == 0 &&
		len(e.RemoteImageURLs) == 0 &&
		len(e.MentionBindings) == 0 &&
		len(e.PendingPastes) == 0
}

type HistorySearchDirection string

const (
	HistorySearchOlder HistorySearchDirection = "older"
	HistorySearchNewer HistorySearchDirection = "newer"
)

type HistorySearchResultKind string

const (
	HistorySearchFound      HistorySearchResultKind = "found"
	HistorySearchAtBoundary HistorySearchResultKind = "at_boundary"
	HistorySearchNotFound   HistorySearchResultKind = "not_found"
)

type HistorySearchResult struct {
	Kind  HistorySearchResultKind
	Entry HistoryEntry
}

type ChatComposerHistory struct {
	localHistory    []HistoryEntry
	historyCursor   int
	lastHistoryText *string
	search          *historySearchState
}

type historySearchState struct {
	Query   string
	Matches []HistoryEntry
	Index   int
}

func NewChatComposerHistory() *ChatComposerHistory {
	return &ChatComposerHistory{historyCursor: -1}
}

func (h *ChatComposerHistory) RecordLocalSubmission(entry HistoryEntry) bool {
	if h == nil || entry.IsEmpty() {
		return false
	}
	h.ResetNavigation()
	if len(h.localHistory) > 0 && historyEntriesEqual(h.localHistory[len(h.localHistory)-1], entry) {
		return false
	}
	h.localHistory = append(h.localHistory, cloneHistoryEntry(entry))
	return true
}

func (h *ChatComposerHistory) RecordTextSubmission(text string) bool {
	return h.RecordLocalSubmission(NewHistoryEntry(text))
}

func (h *ChatComposerHistory) LocalEntries() []HistoryEntry {
	if h == nil {
		return nil
	}
	out := make([]HistoryEntry, len(h.localHistory))
	for i := range h.localHistory {
		out[i] = cloneHistoryEntry(h.localHistory[i])
	}
	return out
}

func (h *ChatComposerHistory) EntriesNewestFirst() []HistoryEntry {
	if h == nil {
		return nil
	}
	out := make([]HistoryEntry, 0, len(h.localHistory))
	for i := len(h.localHistory) - 1; i >= 0; i-- {
		out = append(out, cloneHistoryEntry(h.localHistory[i]))
	}
	return out
}

func (h *ChatComposerHistory) ResetNavigation() {
	if h == nil {
		return
	}
	h.historyCursor = -1
	h.lastHistoryText = nil
	h.search = nil
}

func (h *ChatComposerHistory) ResetSearch() {
	if h != nil {
		h.search = nil
	}
}

func (h *ChatComposerHistory) ShouldHandleNavigation(text string, cursor int) bool {
	if h == nil || len(h.localHistory) == 0 {
		return false
	}
	if text == "" {
		return true
	}
	if cursor != 0 && cursor != len(text) {
		return false
	}
	return h.lastHistoryText != nil && *h.lastHistoryText == text
}

func (h *ChatComposerHistory) NavigateUp() (HistoryEntry, bool) {
	if h == nil || len(h.localHistory) == 0 {
		return HistoryEntry{}, false
	}
	h.search = nil
	if h.historyCursor < 0 || h.historyCursor > len(h.localHistory) {
		h.historyCursor = len(h.localHistory)
	}
	if h.historyCursor == 0 {
		return HistoryEntry{}, false
	}
	h.historyCursor--
	return h.entryAtCursor()
}

func (h *ChatComposerHistory) NavigateDown() (HistoryEntry, bool) {
	if h == nil || len(h.localHistory) == 0 || h.historyCursor < 0 {
		return HistoryEntry{}, false
	}
	h.search = nil
	if h.historyCursor+1 >= len(h.localHistory) {
		h.historyCursor = -1
		h.lastHistoryText = nil
		return HistoryEntry{}, true
	}
	h.historyCursor++
	return h.entryAtCursor()
}

func (h *ChatComposerHistory) Search(query string, direction HistorySearchDirection, restart bool) HistorySearchResult {
	if h == nil {
		return HistorySearchResult{Kind: HistorySearchNotFound}
	}
	query = strings.ToLower(query)
	if restart || h.search == nil || h.search.Query != query {
		matches := h.uniqueMatches(query)
		if len(matches) == 0 {
			h.search = &historySearchState{Query: query, Matches: nil, Index: -1}
			return HistorySearchResult{Kind: HistorySearchNotFound}
		}
		h.search = &historySearchState{Query: query, Matches: matches, Index: 0}
		return HistorySearchResult{Kind: HistorySearchFound, Entry: cloneHistoryEntry(matches[0])}
	}
	if len(h.search.Matches) == 0 || h.search.Index < 0 {
		return HistorySearchResult{Kind: HistorySearchNotFound}
	}
	switch direction {
	case HistorySearchNewer:
		if h.search.Index == 0 {
			return HistorySearchResult{Kind: HistorySearchAtBoundary}
		}
		h.search.Index--
	default:
		if h.search.Index+1 >= len(h.search.Matches) {
			return HistorySearchResult{Kind: HistorySearchAtBoundary}
		}
		h.search.Index++
	}
	return HistorySearchResult{Kind: HistorySearchFound, Entry: cloneHistoryEntry(h.search.Matches[h.search.Index])}
}

func (h *ChatComposerHistory) entryAtCursor() (HistoryEntry, bool) {
	if h.historyCursor < 0 || h.historyCursor >= len(h.localHistory) {
		return HistoryEntry{}, false
	}
	entry := cloneHistoryEntry(h.localHistory[h.historyCursor])
	h.lastHistoryText = &entry.Text
	return entry, true
}

func (h *ChatComposerHistory) uniqueMatches(queryLower string) []HistoryEntry {
	seen := map[string]bool{}
	matches := []HistoryEntry{}
	for i := len(h.localHistory) - 1; i >= 0; i-- {
		entry := h.localHistory[i]
		if queryLower != "" && !strings.Contains(strings.ToLower(entry.Text), queryLower) {
			continue
		}
		if seen[entry.Text] {
			continue
		}
		seen[entry.Text] = true
		matches = append(matches, cloneHistoryEntry(entry))
	}
	return matches
}

func ComposerHistoryEntries(history *ComposerHistory) []string {
	if history == nil {
		return nil
	}
	return history.Search("")
}

func PushComposerHistory(history *ComposerHistory, entry string) {
	if history == nil {
		return
	}
	history.Add(entry)
}

func cloneHistoryEntry(entry HistoryEntry) HistoryEntry {
	out := entry
	out.Attachments = append([]ComposerAttachment(nil), entry.Attachments...)
	out.RemoteImageURLs = append([]string(nil), entry.RemoteImageURLs...)
	out.MentionBindings = append([]MentionBinding(nil), entry.MentionBindings...)
	out.PendingPastes = append([]HistoryPendingPaste(nil), entry.PendingPastes...)
	return out
}

func historyEntriesEqual(a HistoryEntry, b HistoryEntry) bool {
	if a.Text != b.Text || len(a.Attachments) != len(b.Attachments) || len(a.RemoteImageURLs) != len(b.RemoteImageURLs) || len(a.MentionBindings) != len(b.MentionBindings) || len(a.PendingPastes) != len(b.PendingPastes) {
		return false
	}
	for i := range a.Attachments {
		if a.Attachments[i] != b.Attachments[i] {
			return false
		}
	}
	for i := range a.RemoteImageURLs {
		if a.RemoteImageURLs[i] != b.RemoteImageURLs[i] {
			return false
		}
	}
	for i := range a.MentionBindings {
		if a.MentionBindings[i] != b.MentionBindings[i] {
			return false
		}
	}
	for i := range a.PendingPastes {
		if a.PendingPastes[i] != b.PendingPastes[i] {
			return false
		}
	}
	return true
}
