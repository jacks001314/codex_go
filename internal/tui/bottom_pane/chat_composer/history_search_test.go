package chatcomposer

import (
	"reflect"
	"testing"
)

func TestHistorySearchSessionQueryNavigationAndHighlightsMatchRustCore(t *testing.T) {
	original := *NewDraftState()
	original.SetText("draft")
	original.Cursor = 2
	original.AddPendingPaste("[paste]", "payload")
	session := BeginHistorySearch(original, []string{
		"git old",
		"cargo test",
		"git old",
		"git new",
	})

	if !session.Active || session.Status != HistorySearchIdle || session.PreviewDraft.Text != "draft" || session.PreviewDraft.Cursor != 2 {
		t.Fatalf("new session = %#v", session)
	}
	if line, ok := session.FooterLine(); !ok || line != "reverse-i-search: " {
		t.Fatalf("idle footer = %q ok=%v", line, ok)
	}

	draft, result := session.UpdateQuery("git")
	if result.Kind != HistorySearchResultFound || result.Entry != "git new" {
		t.Fatalf("query result = %#v", result)
	}
	if draft.Text != "git new" || draft.Cursor != len("git new") || session.Status != HistorySearchMatch {
		t.Fatalf("query draft/status = %#v status=%s", draft, session.Status)
	}
	if got := session.HighlightRanges(); !reflect.DeepEqual(got, []TextRange{{Start: 0, End: 3}}) {
		t.Fatalf("highlight ranges = %#v", got)
	}

	draft, result = session.SearchInDirection(HistorySearchOlder)
	if result.Kind != HistorySearchResultFound || result.Entry != "git old" || draft.Text != "git old" {
		t.Fatalf("older result = %#v draft=%#v", result, draft)
	}
	draft, result = session.SearchInDirection(HistorySearchOlder)
	if result.Kind != HistorySearchResultAtBoundary || draft.Text != "git old" || session.Status != HistorySearchMatch {
		t.Fatalf("older boundary result = %#v draft=%#v status=%s", result, draft, session.Status)
	}
	draft, result = session.SearchInDirection(HistorySearchNewer)
	if result.Kind != HistorySearchResultFound || result.Entry != "git new" || draft.Text != "git new" {
		t.Fatalf("newer result = %#v draft=%#v", result, draft)
	}
}

func TestHistorySearchSessionCancelAcceptNoMatchAndQueryEditingMatchRustCore(t *testing.T) {
	original := *NewDraftState()
	original.SetText("draft")
	original.Cursor = 2
	session := BeginHistorySearch(original, []string{"git status", "cargo test"})

	draft, result := session.UpdateQuery("zzz")
	if result.Kind != HistorySearchResultNotFound || session.Status != HistorySearchNoMatch || draft.Text != "draft" || draft.Cursor != 2 {
		t.Fatalf("no match = result %#v draft %#v status %s", result, draft, session.Status)
	}
	if line, ok := session.FooterLine(); !ok || line != "reverse-i-search: zzz  no match" {
		t.Fatalf("no-match footer = %q ok=%v", line, ok)
	}

	draft, result = session.BackspaceQuery()
	if result.Kind != HistorySearchResultNotFound || session.Query != "zz" || draft.Text != "draft" {
		t.Fatalf("backspace no-match = result %#v query %q draft %#v", result, session.Query, draft)
	}
	draft, result = session.ClearQuery()
	if result.Kind != HistorySearchResultNotFound || session.Query != "" || session.Status != HistorySearchIdle || draft.Text != "draft" {
		t.Fatalf("clear query = result %#v query %q status %s draft %#v", result, session.Query, session.Status, draft)
	}

	draft, result = session.UpdateQuery("cargo")
	if result.Kind != HistorySearchResultFound || draft.Text != "cargo test" {
		t.Fatalf("cargo result = %#v draft=%#v", result, draft)
	}
	accepted, ok := session.Accept()
	if !ok || session.Active || accepted.Text != "cargo test" || accepted.Cursor != len("cargo test") {
		t.Fatalf("accepted = %#v ok=%v active=%v", accepted, ok, session.Active)
	}
	if line, ok := session.FooterLine(); ok || line != "" {
		t.Fatalf("inactive footer = %q ok=%v", line, ok)
	}

	cancelSession := BeginHistorySearch(original, []string{"git status"})
	draft, result = cancelSession.AppendQueryRune('g')
	if result.Kind != HistorySearchResultFound || draft.Text != "git status" {
		t.Fatalf("append query = result %#v draft %#v", result, draft)
	}
	restored := cancelSession.Cancel()
	if cancelSession.Active || restored.Text != "draft" || restored.Cursor != 2 {
		t.Fatalf("cancel restored = %#v active=%v", restored, cancelSession.Active)
	}
}

func TestHistorySearchFooterCursorCompatibilityAndCaseInsensitiveRangesMatchRustCore(t *testing.T) {
	entries := []string{"git status", "cargo test", "git status", "Git commit"}
	if got := SearchHistory(entries, "git"); !reflect.DeepEqual(got, []string{"Git commit", "git status"}) {
		t.Fatalf("SearchHistory = %#v", got)
	}

	session := BeginHistorySearch(DraftState{}, entries)
	session.UpdateQuery("Git")
	if line, ok := session.FooterLine(); !ok || line != "reverse-i-search: Git  enter accept | esc cancel" {
		t.Fatalf("match footer = %q ok=%v", line, ok)
	}
	if col, ok := session.CursorColumn(2, 80); !ok || col != 2+len("reverse-i-search: ")+len("Git") {
		t.Fatalf("cursor column = %d ok=%v", col, ok)
	}
	session.UpdateQuery("你")
	if col, ok := session.CursorColumn(2, 80); !ok || col != 2+len("reverse-i-search: ")+2 {
		t.Fatalf("wide cursor column = %d ok=%v", col, ok)
	}
	if col, ok := session.CursorColumn(2, 10); !ok || col != 9 {
		t.Fatalf("clamped cursor column = %d ok=%v", col, ok)
	}

	if got := CaseInsensitiveMatchRanges("git status GIT", "GIT"); !reflect.DeepEqual(got, []TextRange{{Start: 0, End: 3}, {Start: 11, End: 14}}) {
		t.Fatalf("case ranges = %#v", got)
	}
	if got := CaseInsensitiveMatchRanges("a\u0130 i", "i"); !reflect.DeepEqual(got, []TextRange{{Start: 1, End: 3}, {Start: 4, End: 5}}) {
		t.Fatalf("turkish dotted-I ranges = %#v", got)
	}
	if got := CaseInsensitiveMatchRanges("你好Git", "git"); !reflect.DeepEqual(got, []TextRange{{Start: len("你好"), End: len("你好Git")}}) {
		t.Fatalf("unicode case ranges = %#v", got)
	}

	session.ApplyResult(HistorySearchResult{Kind: HistorySearchResultPending})
	if session.Status != HistorySearchSearching {
		t.Fatalf("pending status = %s", session.Status)
	}
}
