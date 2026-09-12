package bottompane

import (
	"fmt"
	"strings"
	"testing"

	"codex_go/tui"
	"codex_go/turn"
)

func taskMentionTextInput(text string, elements ...turn.TextElement) turn.TurnUserInput {
	return turn.TurnUserInput{Type: "text", Text: text, TextElements: elements}
}

func placeholderElement(start int, end int, placeholder string) turn.TextElement {
	value := placeholder
	return turn.TextElement{ByteRange: turn.ByteRange{Start: uint(start), End: uint(end)}, Placeholder: &value}
}

// TestTaskMentionHistoryRoundTripsLikeRust mirrors Rust
// task_mentions_tests::task_mention_history_round_trips_multiword_and_escaped_titles.
func TestTaskMentionHistoryRoundTripsLikeRust(t *testing.T) {
	title := "Fix ]( parser \\ safely"
	text := "Inspect x@" + title + " next"
	mention := tui.LinkedMention{Sigil: '@', Mention: title, Path: "thread://task-123"}
	encoded := tui.EncodeHistoryMentionsAtElements(text, []tui.LinkedMention{mention}, []turn.TextElement{{
		ByteRange: turn.ByteRange{Start: uint(len("Inspect x")), End: uint(len("Inspect x@") + len(title))},
	}})
	want := "Inspect x[@Fix \\]\\( parser \\\\ safely](thread://task-123) next"
	if encoded != want {
		t.Fatalf("encoded = %q, want %q", encoded, want)
	}
	decoded := tui.DecodeHistoryMentionsWithAtMentions(encoded, true)
	if decoded.Text != text {
		t.Fatalf("decoded text = %q, want %q", decoded.Text, text)
	}
	if len(decoded.Mentions) != 1 || decoded.Mentions[0] != mention {
		t.Fatalf("decoded mentions = %#v, want [%#v]", decoded.Mentions, mention)
	}
	if len(decoded.TaskMentionRanges) != 1 {
		t.Fatalf("task mention ranges = %#v, want one", decoded.TaskMentionRanges)
	}
	rangeStart, rangeEnd := decoded.TaskMentionRanges[0][0], decoded.TaskMentionRanges[0][1]
	if rangeStart < 0 || rangeEnd > len(decoded.Text) || decoded.Text[rangeStart:rangeEnd] != "@"+title {
		t.Fatalf("task mention range = %v over %q", decoded.TaskMentionRanges[0], decoded.Text)
	}

	tooLong := "[@" + strings.Repeat("x", tui.MaxTaskTitleChars+1) + "](thread://task-123)"
	if _, _, _, ok := tui.ParseTaskLink(tooLong, 0); ok {
		t.Fatalf("over-long title parsed: %q", tooLong)
	}
	for _, path := range []string{"thread://", "thread://../settings", "thread://task?target", "thread://" + strings.Repeat("x", 65)} {
		if _, ok := tui.ValidThreadPath(path); ok {
			t.Fatalf("ValidThreadPath(%q) = true, want false", path)
		}
	}
	malformed := "[@é](thread://task-123)"
	for _, end := range []int{3, len(malformed) + 1} {
		decodedText, decodedElements := DecodeTaskLinks(malformed, []turn.TextElement{{
			ByteRange: turn.ByteRange{Start: 0, End: uint(end)},
		}})
		if decodedText != malformed || len(decodedElements) != 0 {
			t.Fatalf("malformed decode(end=%d) = %q %#v, want unchanged text and no elements", end, decodedText, decodedElements)
		}
	}
	literal := "[@actual](thread://task-456)"
	decodedText, _ := DecodeTaskLinks(literal+" "+literal, []turn.TextElement{
		{ByteRange: turn.ByteRange{Start: 0, End: uint(len(literal))}},
		placeholderElement(len(literal)+1, len(literal)*2+1, "@actual"),
	})
	if decodedText != literal+" @actual" {
		t.Fatalf("literal decode = %q, want %q", decodedText, literal+" @actual")
	}
}

// TestTaskReferenceContextMergesWithIDEContextLikeRust mirrors Rust
// task_mentions_tests::task_reference_context_deduplicates_and_merges_with_ide_context.
func TestTaskReferenceContextMergesWithIDEContextLikeRust(t *testing.T) {
	title := "Review the migration"
	visible := "Compare plain @" + title + " with plugin @" + title + " and selected @" + title + "x"
	binding := MentionBinding{Sigil: '@', Mention: title, Path: "thread://task-123"}
	text := "# IDE: @" + title + "\n" + tui.TaskMentionRequestHeading + "\n" + visible
	pluginStart := strings.Index(text, "plugin @") + len("plugin ")
	selectedStart := strings.LastIndex(text, "@"+title)
	items := []turn.TurnUserInput{taskMentionTextInput(text,
		placeholderElement(pluginStart, pluginStart+len(title)+1, "@"+title),
		placeholderElement(selectedStart, selectedStart+len(title)+1, "@"+title),
	)}
	ApplyTaskReferences(items, []MentionBinding{
		{Sigil: '@', Mention: title, Path: "plugin://sample@test"},
		binding,
		binding,
	}, "")
	got := items[0].Text
	if !strings.HasPrefix(got, "# IDE: @"+title+"\n## Referenced chats") {
		t.Fatalf("merged text = %q", got)
	}
	if strings.Count(got, tui.TaskMentionRequestHeading) != 1 {
		t.Fatalf("request heading count = %d in %q", strings.Count(got, tui.TaskMentionRequestHeading), got)
	}
	if strings.Count(got, `"threadId":"task-123"`) != 1 {
		t.Fatalf("thread reference count = %d in %q", strings.Count(got, `"threadId":"task-123"`), got)
	}
	if !strings.Contains(got, "MUST call `read_thread`") {
		t.Fatalf("missing read_thread guidance: %q", got)
	}
	wantTail := "Compare plain @" + title + " with plugin @" + title + " and selected [@" + title + "](thread://task-123)x"
	if !strings.HasSuffix(got, wantTail) {
		t.Fatalf("tail = %q, want suffix %q", got, wantTail)
	}

	for _, idLen := range []int{36, 64} {
		bindings := make([]MentionBinding, 0, tui.MaxReferencedTasks)
		for index := 0; index < tui.MaxReferencedTasks; index++ {
			threadID := fmt.Sprintf("%0*d", idLen, index)
			bindings = append(bindings, MentionBinding{Sigil: '@', Mention: "task", Path: "thread://" + threadID})
		}
		item := []turn.TurnUserInput{taskMentionTextInput("")}
		ApplyTaskReferences(item, bindings, "")
		gotRefs := strings.Count(item[0].Text, `"threadId":`)
		wantRefs := tui.MaxReferencedTasks
		if budget := tui.MaxReferencedThreadIDBytes / idLen; budget < wantRefs {
			wantRefs = budget
		}
		if gotRefs != wantRefs {
			t.Fatalf("id_len=%d references = %d, want %d", idLen, gotRefs, wantRefs)
		}
	}
}

// TestTaskReferenceHeadingInsideTitleLikeRust mirrors Rust
// task_mentions_tests::task_reference_heading_inside_selected_title_is_not_a_context_boundary.
func TestTaskReferenceHeadingInsideTitleLikeRust(t *testing.T) {
	title := "Review " + tui.TaskMentionRequestHeading + " carefully"
	items := []turn.TurnUserInput{taskMentionTextInput("@"+title, turn.TextElement{
		ByteRange: turn.ByteRange{Start: 0, End: uint(len(title) + 1)},
	})}
	ApplyTaskReferences(items, []MentionBinding{{Sigil: '@', Mention: title, Path: "thread://task-123"}}, "")
	got := items[0].Text
	if !strings.HasPrefix(got, "## Referenced chats with Codex:") {
		t.Fatalf("text = %q", got)
	}
	if !strings.HasSuffix(got, "[@"+title+"](thread://task-123)") {
		t.Fatalf("tail = %q", got)
	}
}
