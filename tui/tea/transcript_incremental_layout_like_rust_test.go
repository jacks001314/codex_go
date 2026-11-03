package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust's TranscriptView keeps per-cell layouts and only re-lays out the cells
// that changed. The Go transcript resumes its join at the first changed
// message, so the resumed output must stay byte-identical to a full rebuild.
func TestIncrementalTranscriptLayoutMatchesFullRebuild(t *testing.T) {
	state := codextui.NewState(nil)
	state.Messages = []codextui.Message{
		{Role: codextui.RoleUser, Text: "first prompt", RawText: "first prompt"},
		{Role: codextui.RoleAssistant, Text: "## Answer\n\nsome prose that wraps across the width\n\n- one\n- two", RawText: "## Answer"},
		{Role: codextui.RoleUser, Text: "second prompt", RawText: "second prompt"},
		{Role: codextui.RoleSystem, Text: "tool output\nline two", RawText: "tool output"},
	}
	var cache transcriptMessageCache
	render := func(target *transcriptMessageCache, hideTips bool) (string, []string) {
		content, _, _, rows := renderTranscriptLayout(
			target, state, false, 80, "", false, nil, "", false, nil, false, hideTips, "incremental",
		)
		return content, rows
	}
	assertMatches := func(stage string, hideTips bool) {
		t.Helper()
		wantContent, wantRows := render(nil, hideTips)
		gotContent, gotRows := render(&cache, hideTips)
		if gotContent != wantContent {
			t.Fatalf("%s: content diverged\n got=%q\nwant=%q", stage, gotContent, wantContent)
		}
		if strings.Join(gotRows, "\n") != strings.Join(wantRows, "\n") {
			t.Fatalf("%s: rows diverged\n got=%q\nwant=%q", stage, gotRows, wantRows)
		}
	}

	assertMatches("initial", false)

	state.Messages[len(state.Messages)-1].Text += " and a streamed tail that keeps growing"
	state.BumpMessagesRevision()
	assertMatches("streaming tail", false)

	state.Messages = append(state.Messages, codextui.Message{Role: codextui.RoleAssistant, Text: "new answer"})
	state.BumpMessagesRevision()
	assertMatches("appended message", false)

	state.Messages[1].Text = "## Answer\n\nrewritten middle"
	state.BumpMessagesRevision()
	assertMatches("middle mutation", false)

	state.Messages = state.Messages[:len(state.Messages)-1]
	state.BumpMessagesRevision()
	assertMatches("removed tail", false)

	state.Messages = nil
	state.BumpMessagesRevision()
	assertMatches("cleared", false)

	state.Messages = []codextui.Message{{Role: codextui.RoleUser, Text: "rebuilt prompt"}}
	state.BumpMessagesRevision()
	assertMatches("rebuilt after clear", false)

	state.Messages[0].StartupTip = true
	state.BumpMessagesRevision()
	assertMatches("hidden startup tip", true)
}

// Rust re-lays out only the live cell because the cell tells the cache that it
// changed. The Go streaming path reports the mutated tail index the same way,
// and a generic mutation must keep the conservative full-message check.
func TestTranscriptChangedFromHintLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	for index := 0; index < 60; index++ {
		role := codextui.RoleUser
		if index%2 == 1 {
			role = codextui.RoleAssistant
		}
		state.Messages = append(state.Messages, codextui.Message{
			Role:    role,
			Text:    "message body that wraps a little",
			RawText: "message body",
		})
	}
	state.BumpMessagesRevision()
	var cache transcriptMessageCache
	render := func() (string, []string) {
		content, _, _, rows := renderTranscriptLayout(
			&cache, state, false, 80, "", false, nil, "", false, nil, false, false, "hint",
		)
		return content, rows
	}
	render()
	if got := state.MessagesChangedFrom; got != len(state.Messages) {
		t.Fatalf("after a build the consumed hint = %d, want %d", got, len(state.Messages))
	}

	last := len(state.Messages) - 1
	state.Messages[last].Text += " streamed tail"
	state.BumpMessagesRevisionFrom(last)
	if got := state.MessagesChangedFrom; got != last {
		t.Fatalf("tail delta hint = %d, want %d", got, last)
	}
	content, rows := render()
	wantContent, _, _, wantRows := renderTranscriptLayout(
		nil, state, false, 80, "", false, nil, "", false, nil, false, false, "hint",
	)
	if content != wantContent || strings.Join(rows, "\n") != strings.Join(wantRows, "\n") {
		t.Fatal("tail delta with a narrowed hint diverged from a full rebuild")
	}
	if got := state.MessagesChangedFrom; got != len(state.Messages) {
		t.Fatalf("tail delta hint was not consumed: %d", got)
	}

	state.Messages[3].Text = "rewritten middle"
	state.BumpMessagesRevision()
	if got := state.MessagesChangedFrom; got != 0 {
		t.Fatalf("generic bump hint = %d, want 0 (full check)", got)
	}
	state.BumpMessagesRevisionFrom(2)
	if got := state.MessagesChangedFrom; got != 0 {
		t.Fatalf("narrowing after a generic bump = %d, want 0", got)
	}
	content, rows = render()
	wantContent, _, _, wantRows = renderTranscriptLayout(
		nil, state, false, 80, "", false, nil, "", false, nil, false, false, "hint",
	)
	if content != wantContent || strings.Join(rows, "\n") != strings.Join(wantRows, "\n") {
		t.Fatal("middle mutation diverged from a full rebuild")
	}
}
