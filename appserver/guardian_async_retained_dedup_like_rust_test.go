package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

func guardianAsyncRetainedUserItem(id, turnID, text string, createdAt time.Time) session.Item {
	raw, _ := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []map[string]any{{"type": "input_text", "text": text}},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []string{"user.text"},
		},
	})
	return session.Item{
		ID:        id,
		Type:      "message",
		Role:      "user",
		Text:      text,
		Raw:       raw,
		Metadata:  map[string]any{"turnId": turnID},
		CreatedAt: createdAt,
	}
}

// Mirrors Rust #48110: a complete retained source appears as a source-order
// labelled user entry in both profiles, the asynchronous record gains its extra
// newline, and the async prompt omits the retained original the transcript
// already carries while keeping the ordering banners.
func TestGuardianAsyncTranscriptCarriesRetainedSourceOrderLikeRust(t *testing.T) {
	instruction := "Keep staging private."
	router := guardianProfileTestRouter(t, []session.Item{
		guardianAsyncRetainedUserItem("user-1", "turn-1", instruction, fixedTime()),
	})
	synchronous := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, state.SynchronousContextProfile())
	if len(synchronous.Entries) != 1 {
		t.Fatalf("synchronous entries = %#v", synchronous.Entries)
	}
	syncEntry := synchronous.Entries[0]
	if syncEntry.RetainedSourceOrder != "0" || syncEntry.RetainedSource == nil || !syncEntry.RetainedSource.Complete {
		t.Fatalf("synchronous entry = %#v, want a complete retained source with label 0", syncEntry)
	}
	if got, want := syncEntry.Render(state.GuardianTranscriptFormatLine), "[0] Retained source order: 0\nuser: "+instruction+"\n"; got != want {
		t.Fatalf("synchronous line = %q, want %q", got, want)
	}

	asynchronous := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, state.AsynchronousContextProfile())
	if len(asynchronous.Entries) != 1 {
		t.Fatalf("asynchronous entries = %#v", asynchronous.Entries)
	}
	asyncEntry := asynchronous.Entries[0]
	if !asyncEntry.Async || asyncEntry.RetainedSourceOrder != "0" || asyncEntry.RetainedSource == nil {
		t.Fatalf("asynchronous entry = %#v, want a labelled async retained source", asyncEntry)
	}
	if got, want := asyncEntry.Render(state.GuardianTranscriptFormatLine), "[0] Retained source order: 0\nuser: "+instruction+"\n\n"; got != want {
		t.Fatalf("asynchronous line = %q, want %q", got, want)
	}

	retained := router.retainedContextForThread(guardianProfileTestThreadID)
	if retained == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	parts, err := state.BuildPromptPartsWithOptions(
		state.Action{Type: "command", Command: "ls", CWD: "/repo"},
		asynchronous.Entries,
		state.BuildPromptOptions{
			Presentation:     state.ActionPresentationAsync,
			TranscriptFormat: state.GuardianTranscriptFormatLine,
			RetainedContext:  retained,
		},
	)
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
	if !strings.Contains(prompt, "- [0] Retained source order: 0\nuser: "+instruction) {
		t.Fatalf("the transcript lost its retained source order label:\n%s", prompt)
	}
	if !strings.Contains(prompt, ">>> RETAINED USER INSTRUCTIONS START") || !strings.Contains(prompt, ">>> RETAINED USER INSTRUCTIONS END") {
		t.Fatalf("the ordering banners were dropped:\n%s", prompt)
	}
}

// Rust #48110 budget protection: an async copy whose retained-source-order
// rendering exceeds the instruction budget cannot prove delivery. The sync
// profile renders without that async gate, and the retained section reports the
// original as unavailable instead of pretending the transcript carried it.
func TestGuardianAsyncTranscriptRetainedProofHonorsBudgetLikeRust(t *testing.T) {
	text := strings.Repeat("x", state.RetainedInstructionByteBudget()-10)
	router := guardianProfileTestRouter(t, []session.Item{
		guardianAsyncRetainedUserItem("user-1", "turn-1", text, fixedTime()),
	})
	synchronous := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, state.SynchronousContextProfile())
	if len(synchronous.Entries) != 1 || synchronous.Entries[0].RetainedSource == nil {
		t.Fatalf("synchronous entry = %#v, want the unbounded sync label", synchronous.Entries)
	}
	asynchronous := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, state.AsynchronousContextProfile())
	if len(asynchronous.Entries) != 1 || asynchronous.Entries[0].RetainedSource != nil || asynchronous.Entries[0].RetainedSourceOrder != "" {
		t.Fatalf("asynchronous entry = %#v, want no source proof over budget", asynchronous.Entries)
	}
	retained := router.retainedContextForThread(guardianProfileTestThreadID)
	parts, err := state.BuildPromptPartsWithOptions(
		state.Action{Type: "command", Command: "ls", CWD: "/repo"},
		asynchronous.Entries,
		state.BuildPromptOptions{Presentation: state.ActionPresentationAsync, RetainedContext: retained},
	)
	if err != nil {
		t.Fatalf("BuildPromptPartsWithOptions() error = %v", err)
	}
	if !strings.Contains(parts[0].Text, "Host notice: some retained user instructions are unavailable within the evidence budget.") {
		t.Fatalf("the over-budget original was not reported as unavailable:\n%s", parts[0].Text)
	}
}
