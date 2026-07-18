package bottompane

import (
	"reflect"
	"testing"
)

func TestChatComposerHistoryRecordDedupAndClone(t *testing.T) {
	history := NewChatComposerHistory()
	if history.RecordTextSubmission("") {
		t.Fatal("empty text should not be recorded")
	}
	if !history.RecordTextSubmission("   ") {
		t.Fatal("whitespace text should be preserved like Rust")
	}
	if got := history.LocalEntries()[0].Text; got != "   " {
		t.Fatalf("whitespace entry = %q", got)
	}
	entry := HistoryEntry{
		Text: "hello",
		Attachments: []ComposerAttachment{{
			Kind: AttachmentFile,
			Path: "a.txt",
		}},
		RemoteImageURLs: []string{"https://example.test/a.png"},
		MentionBindings: []MentionBinding{{Sigil: '@', Mention: "sample", Path: "plugin://sample"}},
		PendingPastes:   []HistoryPendingPaste{{Placeholder: "[paste-1]", Content: "large"}},
	}
	if !history.RecordLocalSubmission(entry) {
		t.Fatal("first entry should record")
	}
	if history.RecordLocalSubmission(entry) {
		t.Fatal("adjacent duplicate should not record")
	}
	entries := history.LocalEntries()
	last := len(entries) - 1
	entries[last].Text = "mutated"
	entries[last].Attachments[0].Path = "mutated"
	if got := history.LocalEntries()[last]; got.Text != "hello" || got.Attachments[0].Path != "a.txt" {
		t.Fatalf("LocalEntries clone leaked mutation: %#v", got)
	}
}

func TestChatComposerHistoryNavigationMatchesRustBoundaries(t *testing.T) {
	history := NewChatComposerHistory()
	history.RecordTextSubmission("first")
	history.RecordTextSubmission("second")
	if !history.ShouldHandleNavigation("", 0) {
		t.Fatal("empty composer should navigate history")
	}
	entry, ok := history.NavigateUp()
	if !ok || entry.Text != "second" {
		t.Fatalf("first up = %#v ok=%v", entry, ok)
	}
	if !history.ShouldHandleNavigation("second", len("second")) || !history.ShouldHandleNavigation("second", 0) {
		t.Fatal("recalled text should navigate at line boundaries")
	}
	if history.ShouldHandleNavigation("second", 2) || history.ShouldHandleNavigation("other", 0) {
		t.Fatal("navigation should be blocked for interior cursor or changed text")
	}
	entry, ok = history.NavigateUp()
	if !ok || entry.Text != "first" {
		t.Fatalf("second up = %#v ok=%v", entry, ok)
	}
	if _, ok = history.NavigateUp(); ok {
		t.Fatal("up at oldest should stay at boundary")
	}
	entry, ok = history.NavigateDown()
	if !ok || entry.Text != "second" {
		t.Fatalf("down = %#v ok=%v", entry, ok)
	}
	entry, ok = history.NavigateDown()
	if !ok || entry.Text != "" {
		t.Fatalf("down past newest should return empty entry, got %#v ok=%v", entry, ok)
	}
	if _, ok = history.NavigateDown(); ok {
		t.Fatal("down while not browsing should be ignored")
	}
}

func TestChatComposerHistorySearchUniqueMatchesAndBoundaries(t *testing.T) {
	history := NewChatComposerHistory()
	for _, text := range []string{
		"git status",
		"cargo test -p codex-tui",
		"git status",
		"git diff",
	} {
		history.RecordTextSubmission(text)
	}
	got := history.Search("git", HistorySearchOlder, true)
	if got.Kind != HistorySearchFound || got.Entry.Text != "git diff" {
		t.Fatalf("first search = %#v", got)
	}
	got = history.Search("git", HistorySearchOlder, false)
	if got.Kind != HistorySearchFound || got.Entry.Text != "git status" {
		t.Fatalf("older search = %#v", got)
	}
	got = history.Search("git", HistorySearchOlder, false)
	if got.Kind != HistorySearchAtBoundary {
		t.Fatalf("older boundary = %#v", got)
	}
	got = history.Search("git", HistorySearchNewer, false)
	if got.Kind != HistorySearchFound || got.Entry.Text != "git diff" {
		t.Fatalf("newer search = %#v", got)
	}
	got = history.Search("git", HistorySearchNewer, false)
	if got.Kind != HistorySearchAtBoundary {
		t.Fatalf("newer boundary = %#v", got)
	}
	got = history.Search("missing", HistorySearchOlder, true)
	if got.Kind != HistorySearchNotFound {
		t.Fatalf("missing search = %#v", got)
	}
}

func TestChatComposerHistoryCompatibilityHelpers(t *testing.T) {
	history := NewComposerHistory()
	PushComposerHistory(history, "hello")
	PushComposerHistory(history, "world")
	if got := ComposerHistoryEntries(history); !reflect.DeepEqual(got, []string{"world", "hello"}) {
		t.Fatalf("ComposerHistoryEntries = %#v", got)
	}
	PushComposerHistory(nil, "ignored")
	if ComposerHistoryEntries(nil) != nil {
		t.Fatal("nil history should return nil entries")
	}
}
