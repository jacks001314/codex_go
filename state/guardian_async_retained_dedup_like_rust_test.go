package state

import (
	"strings"
	"testing"

	"codex_go/retainedctx"
)

func asyncRetainedDedupContext(t *testing.T, instruction string) (*retainedctx.RetainedContext, *retainedctx.RetainedSource) {
	t.Helper()
	context := &retainedctx.RetainedContext{}
	source := context.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("user-1"),
		Text:      instruction,
		Complete:  true,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(0)))
	if source == nil {
		t.Fatal("the retained instruction has no captured source")
	}
	context.RecordAssistantMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("assistant-1"),
		Text:      "Understood.",
		Complete:  true,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(1)))
	return context, source
}

// Mirrors Rust #48110: an asynchronous sample omits a retained instruction
// whose complete source revision its own transcript already carries, keeps the
// ordering banners, and still delivers the assistant context.
func TestAsyncGuardianDeduplicatesTranscriptDeliveredInstructionsLikeRust(t *testing.T) {
	instruction := "Keep staging private."
	context, source := asyncRetainedDedupContext(t, instruction)
	action := Action{Type: "command", Command: "ls", CWD: "/repo"}
	entries := []GuardianTranscriptEntry{{
		Author:              "user",
		LineRole:            "user",
		Index:               0,
		RetainedSourceOrder: "0",
		RetainedSource:      source,
		Async:               true,
		Text:                instruction,
	}}
	parts, err := BuildPromptPartsWithOptions(action, entries, BuildPromptOptions{
		Presentation:     ActionPresentationAsync,
		TranscriptFormat: GuardianTranscriptFormatLine,
		RetainedContext:  context,
	})
	if err != nil {
		t.Fatalf("BuildPromptPartsWithOptions() error = %v", err)
	}
	if len(parts) != 1 || len(parts[0].Native) != 0 {
		t.Fatalf("parts = %#v, want one text block", parts)
	}
	prompt := parts[0].Text
	if got := strings.Count(prompt, "user: "+instruction); got != 1 {
		t.Fatalf("the instruction appears %d times, want only the transcript copy:\n%s", got, prompt)
	}
	if !strings.Contains(prompt, retainedUserInstructionsStart+"\n"+retainedUserInstructionsEnd) {
		t.Fatalf("the ordering banners were dropped:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- [0] Retained source order: 0\nuser: "+instruction) {
		t.Fatalf("the transcript lost its retained source order label:\n%s", prompt)
	}
	if !strings.Contains(prompt, retainedAssistantContextStart) || !strings.Contains(prompt, "assistant: Understood.") {
		t.Fatalf("the assistant context section was dropped:\n%s", prompt)
	}
}

// Each asynchronous sample is self-contained: a request whose transcript has no
// source proof must redeliver the retained original, even after an earlier
// sample deduplicated it (#48110).
func TestAsyncGuardianDedupIsRequestLocalLikeRust(t *testing.T) {
	instruction := "Inspect staging only."
	context, source := asyncRetainedDedupContext(t, instruction)
	action := Action{Type: "command", Command: "ls", CWD: "/repo"}
	first, err := BuildPromptPartsWithOptions(action, []GuardianTranscriptEntry{{
		Author:              "user",
		LineRole:            "user",
		Index:               0,
		RetainedSourceOrder: "0",
		RetainedSource:      source,
		Async:               true,
		Text:                instruction,
	}}, BuildPromptOptions{Presentation: ActionPresentationAsync, RetainedContext: context})
	if err != nil {
		t.Fatalf("first sample error = %v", err)
	}
	if got := strings.Count(first[0].Text, "user: "+instruction); got != 1 {
		t.Fatalf("first sample instruction count = %d\n%s", got, first[0].Text)
	}
	second, err := BuildPromptPartsWithOptions(action, []GuardianTranscriptEntry{{
		Author:   "user",
		LineRole: "user",
		Index:    0,
		Async:    true,
		Text:     instruction,
	}}, BuildPromptOptions{Presentation: ActionPresentationAsync, RetainedContext: context})
	if err != nil {
		t.Fatalf("second sample error = %v", err)
	}
	if got := strings.Count(second[0].Text, "user: "+instruction); got != 2 {
		t.Fatalf("second sample instruction count = %d, want the retained original and the unproven transcript copy:\n%s", got, second[0].Text)
	}
	if !strings.Contains(second[0].Text, retainedUserInstructionsStart) {
		t.Fatalf("second sample lost the ordering banners:\n%s", second[0].Text)
	}
}

// A copy that is not a complete source revision cannot suppress its original;
// the retained section must still carry the instruction (#48110).
func TestAsyncGuardianPreservesInstructionsWithoutCompleteSourceProofLikeRust(t *testing.T) {
	instruction := "Never publish artifacts."
	context, completeSource := asyncRetainedDedupContext(t, instruction)
	// The retained original stays complete; only the transcript copy's host
	// metadata marks it incomplete, so the original must be redelivered.
	incomplete := *completeSource
	incomplete.Complete = false
	parts, err := BuildPromptPartsWithOptions(
		Action{Type: "command", Command: "ls", CWD: "/repo"},
		[]GuardianTranscriptEntry{{
			Author:              "user",
			LineRole:            "user",
			Index:               0,
			RetainedSourceOrder: "0",
			RetainedSource:      &incomplete,
			Async:               true,
			Text:                instruction,
		}},
		BuildPromptOptions{Presentation: ActionPresentationAsync, RetainedContext: context},
	)
	if err != nil {
		t.Fatalf("BuildPromptPartsWithOptions() error = %v", err)
	}
	if got := strings.Count(parts[0].Text, "user: "+instruction); got != 2 {
		t.Fatalf("instruction count = %d, want the preserved original plus the transcript copy:\n%s", got, parts[0].Text)
	}
}

// Async transcript records terminate with an extra newline, including the
// retained-source-order rendering (#48110).
func TestAsyncGuardianTranscriptRetainedOrderSuffixLikeRust(t *testing.T) {
	entry := GuardianTranscriptEntry{
		Author:              "user",
		LineRole:            "user",
		Index:               0,
		RetainedSourceOrder: "0",
		Async:               true,
		Text:                "Inspect only.",
	}
	if got, want := entry.Render(GuardianTranscriptFormatLine), "[0] Retained source order: 0\nuser: Inspect only.\n\n"; got != want {
		t.Fatalf("async retained line = %q, want %q", got, want)
	}
	entry.Async = false
	if got, want := entry.Render(GuardianTranscriptFormatLine), "[0] Retained source order: 0\nuser: Inspect only.\n"; got != want {
		t.Fatalf("sync retained line = %q, want %q", got, want)
	}
}

// Only delivered text entries with a source prove delivery; native ciphertext
// and blank entries never suppress a retained original (#48110).
func TestGuardianTranscriptRetainedSourcesRequireDeliveredProofLikeRust(t *testing.T) {
	source := retainedctx.RetainedSource{
		ID:       retainedctx.RetainedSourceID{MessageID: "user-1", TurnID: "turn-1", Role: retainedctx.RetainedSourceRoleUser},
		Revision: retainedctx.NewResponseItemID("retained"),
		Complete: true,
	}
	entries := []GuardianTranscriptEntry{
		{Author: "user", Text: "delivered", RetainedSource: &source},
		{Author: "user", Text: "ciphertext", Native: []byte(`{"type":"agent_message"}`), RetainedSource: &source},
		{Author: "user", Text: "  ", RetainedSource: &source},
	}
	if got := GuardianTranscriptRetainedSources(entries); len(got) != 1 || got[0] != source {
		t.Fatalf("delivered sources = %#v, want only the delivered text entry", got)
	}
}
