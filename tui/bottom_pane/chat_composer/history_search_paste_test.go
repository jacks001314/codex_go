package chatcomposer

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/history_search_paste_tests.rs.
// Pasted text edits the active history query while preserving the original draft.

func historySearchWithDraft(entries []string) *HistorySearchSession {
	original := *NewDraftState()
	original.SetText("draft")
	return BeginHistorySearch(original, entries)
}

func TestHistorySearchPasteAppendsQueryAndAcceptsMatch(t *testing.T) {
	// Newest first: "git log" is the most recently submitted entry.
	session := historySearchWithDraft([]string{"git status", "git log"})

	draft, result := session.AppendPastedQuery("git")
	if result.Kind != HistorySearchResultFound || result.Entry != "git log" || draft.Text != "git log" {
		t.Fatalf("first paste = result %#v draft %#v", result, draft)
	}

	// Rust types the separating space before pasting the rest of the query.
	draft, result = session.AppendPastedQuery(" status")
	if session.Query != "git status" {
		t.Fatalf("query = %q, want %q", session.Query, "git status")
	}
	if result.Kind != HistorySearchResultFound || result.Entry != "git status" || draft.Text != "git status" {
		t.Fatalf("second paste = result %#v draft %#v", result, draft)
	}
	if line, ok := session.FooterLine(); !ok || line != "reverse-i-search: git status  enter accept | esc cancel" {
		t.Fatalf("footer = %q ok=%v", line, ok)
	}

	accepted, ok := session.Accept()
	if !ok || session.Active || accepted.Text != "git status" {
		t.Fatalf("accepted = %#v ok=%v active=%v", accepted, ok, session.Active)
	}
}

func TestHistorySearchLargePasteClampsCursor(t *testing.T) {
	session := historySearchWithDraft([]string{"git log"})
	// Rust's case pastes `u16::MAX + 1` characters.
	session.AppendPastedQuery(strings.Repeat("x", 1<<16))

	if col, ok := session.CursorColumn(0, 80); !ok || col != 79 {
		t.Fatalf("cursor column = %d ok=%v, want 79", col, ok)
	}
}

func TestHistorySearchEmptyPastePreservesSelectedMatch(t *testing.T) {
	session := historySearchWithDraft([]string{"git status", "git log"})
	session.AppendPastedQuery("git")
	if session.PreviewDraft.Text != "git log" {
		t.Fatalf("preview = %q, want %q", session.PreviewDraft.Text, "git log")
	}
	// Repeat the search to move to the older unique match.
	session.SearchInDirection(HistorySearchOlder)
	if session.PreviewDraft.Text != "git status" {
		t.Fatalf("moved preview = %q, want %q", session.PreviewDraft.Text, "git status")
	}
	selectedDraft := session.PreviewDraft
	selectedIndex := session.index

	for _, pasted := range []string{"", "\x1b[31m\x1b[0m"} {
		draft, result := session.AppendPastedQuery(pasted)
		if draft.Text != selectedDraft.Text {
			t.Fatalf("paste %q preview = %q, want %q", pasted, draft.Text, selectedDraft.Text)
		}
		if session.Query != "git" || session.index != selectedIndex {
			t.Fatalf("paste %q query/index = %q/%d, want %q/%d", pasted, session.Query, session.index, "git", selectedIndex)
		}
		if result.Kind != HistorySearchResultFound || result.Entry != "git status" {
			t.Fatalf("paste %q result = %#v", pasted, result)
		}
	}
}

func TestHistorySearchPasteShowsSeparatorsAndMatchesOriginalQuery(t *testing.T) {
	const query = "foo\nbar\tbaz"
	session := historySearchWithDraft([]string{"foobarbaz", "foo\u21b5bar\u21e5baz"})

	draft, result := session.AppendPastedQuery(query)
	if result.Kind != HistorySearchResultNotFound {
		t.Fatalf("separator paste result = %#v, want not found", result)
	}
	if session.Query != query {
		t.Fatalf("query = %q, want %q", session.Query, query)
	}
	if draft.Text != "draft" {
		t.Fatalf("preview = %q, want %q", draft.Text, "draft")
	}
	if line, ok := session.FooterLine(); !ok || line != "reverse-i-search: foo\u21b5bar\u21e5baz  no match" {
		t.Fatalf("footer = %q ok=%v", line, ok)
	}
	if col, ok := session.CursorColumn(2, 80); !ok || col != 31 {
		t.Fatalf("cursor column = %d ok=%v, want 31", col, ok)
	}

	// The original query text matches once it is a history entry itself.
	reopened := historySearchWithDraft([]string{"foobarbaz", query})
	reopened.AppendPastedQuery(query)
	if reopened.PreviewDraft.Text != query {
		t.Fatalf("matched preview = %q, want %q", reopened.PreviewDraft.Text, query)
	}
	accepted, ok := reopened.Accept()
	if !ok || reopened.Active || accepted.Text != query {
		t.Fatalf("accepted = %#v ok=%v active=%v", accepted, ok, reopened.Active)
	}
}

func TestHistorySearchPastePreservesOriginalDraftOnMissAndCancel(t *testing.T) {
	original := *NewDraftState()
	original.SetText(strings.Repeat("x", LargePasteCharThreshold+1))
	original.Cursor = 2
	session := BeginHistorySearch(original, []string{"git status", "git log"})

	session.AppendPastedQuery("git")
	if session.PreviewDraft.Text != "git log" {
		t.Fatalf("preview = %q, want %q", session.PreviewDraft.Text, "git log")
	}

	session.AppendPastedQuery(" missing")
	if session.Query != "git missing" {
		t.Fatalf("query = %q, want %q", session.Query, "git missing")
	}
	if session.PreviewDraft.Text != original.Text || session.PreviewDraft.Cursor != original.Cursor {
		t.Fatalf("miss preview = %#v, want original draft", session.PreviewDraft)
	}

	restored := session.Cancel()
	if session.Active || restored.Text != original.Text || restored.Cursor != original.Cursor {
		t.Fatalf("cancel = %#v active=%v, want original draft", restored, session.Active)
	}
}

func TestHistorySearchPasteUsesFullSanitizedText(t *testing.T) {
	largePaste := strings.Repeat("x", LargePasteCharThreshold+1)
	for _, testCase := range []struct {
		pasted string
		query  string
	}{
		{pasted: `D:\tmp\history.png`, query: `D:\tmp\history.png`},
		{pasted: largePaste, query: largePaste},
		{pasted: "\u00e9\r\n\u4e2d\r\x1b[31mtext\x1b[0m", query: "\u00e9\n\u4e2d\ntext"},
	} {
		session := historySearchWithDraft([]string{testCase.query})
		draft, result := session.AppendPastedQuery(testCase.pasted)
		if session.Query != testCase.query {
			t.Fatalf("paste %q query = %q, want %q", testCase.pasted, session.Query, testCase.query)
		}
		if result.Kind != HistorySearchResultFound || draft.Text != testCase.query {
			t.Fatalf("paste %q = result %#v draft %#v", testCase.pasted, result, draft)
		}
		if len(draft.PendingPastes) != 0 {
			t.Fatalf("paste %q kept pending pastes = %#v", testCase.pasted, draft.PendingPastes)
		}
	}
}
