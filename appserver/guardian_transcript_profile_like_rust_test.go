package appserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/utils"
)

const guardianProfileTestThreadID = "123e4567-e89b-42d3-a456-426614174120"

func guardianProfileTestRouter(t *testing.T, items []session.Item) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(home)
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(guardianProfileTestThreadID),
		SessionID: guardianProfileTestThreadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai"},
		Items:     items,
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
}

// Mirrors Rust #43806 profile retention: the first and latest user messages
// anchor the transcript, the newest 40 non-user entries are retained, omitted
// entries raise the synchronous notice, and every retained entry keeps its
// original position.
func TestGuardianReviewTranscriptAppliesProfileRetentionLikeRust(t *testing.T) {
	now := fixedTime()
	items := []session.Item{{ID: "user-0", Type: "message", Role: "user", Text: "instruction", CreatedAt: now}}
	for index := 1; index <= 50; index++ {
		items = append(items, session.Item{
			ID:        fmt.Sprintf("assistant-%d", index),
			Type:      "message",
			Role:      "assistant",
			Text:      fmt.Sprintf("step %d", index),
			Data:      map[string]any{"phase": "final_answer"},
			CreatedAt: now,
		})
	}
	items = append(items, session.Item{ID: "user-51", Type: "message", Role: "user", Text: "latest", CreatedAt: now})
	router := guardianProfileTestRouter(t, items)

	selection := router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	if selection.OmissionNote != state.GuardianTranscriptOmissionNotice {
		t.Fatalf("omission note = %q, want %q", selection.OmissionNote, state.GuardianTranscriptOmissionNotice)
	}
	if len(selection.Entries) != 42 {
		t.Fatalf("retained entries = %d, want 2 users plus 40 non-user entries", len(selection.Entries))
	}
	if selection.Entries[0].Index != 0 || selection.Entries[0].Text != "instruction" {
		t.Fatalf("first retained entry = %#v, want original user anchor", selection.Entries[0])
	}
	if selection.Entries[1].Index != 11 {
		t.Fatalf("oldest retained assistant index = %d, want 11", selection.Entries[1].Index)
	}
	if last := selection.Entries[len(selection.Entries)-2]; last.Index != 50 || last.Text != "step 50" {
		t.Fatalf("newest retained assistant = %#v", last)
	}
	if last := selection.Entries[len(selection.Entries)-1]; last.Index != 51 || last.Text != "latest" {
		t.Fatalf("latest user entry = %#v", last)
	}
}

// Mirrors Rust's collect_transcript per-entry caps: the profile applies the
// message and tool token budgets before aggregate retention.
func TestGuardianReviewTranscriptAppliesPerEntryCapsLikeRust(t *testing.T) {
	now := fixedTime()
	messageText := strings.Repeat("m", 3*utils.ApproxBytesForTokens(state.MaxMessageEntryTokens))
	toolText := strings.Repeat("t", 3*utils.ApproxBytesForTokens(state.MaxToolEntryTokens))
	router := guardianProfileTestRouter(t, []session.Item{
		{ID: "user", Type: "message", Role: "user", Text: messageText, CreatedAt: now},
		{ID: "tool", Type: "function_call_output", Name: "read", Namespace: "drive", Text: toolText, CreatedAt: now},
	})

	selection := router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	if len(selection.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(selection.Entries))
	}
	if got, limit := len(selection.Entries[0].Text), utils.ApproxBytesForTokens(state.MaxMessageEntryTokens)+64; got > limit {
		t.Fatalf("message entry bytes = %d, want <= %d (marker allowance)", got, limit)
	}
	if got, limit := len(selection.Entries[1].Text), utils.ApproxBytesForTokens(state.MaxToolEntryTokens)+64; got > limit {
		t.Fatalf("tool entry bytes = %d, want <= %d (marker allowance)", got, limit)
	}
	if !strings.Contains(selection.Entries[0].Text, "tokens truncated") {
		t.Fatalf("message entry missing truncation marker: %q", selection.Entries[0].Text)
	}
	if !strings.Contains(selection.Entries[1].Text, "tokens truncated") {
		t.Fatalf("tool entry missing truncation marker: %q", selection.Entries[1].Text)
	}
}

func TestGuardianReviewPromptRendersTranscriptOmissionNoticeLikeRust(t *testing.T) {
	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
		transcriptSelection: func(string) guardianTranscriptSelection {
			return guardianTranscriptSelection{
				Entries: []state.GuardianTranscriptEntry{
					{Author: "user", LineRole: "user", Text: "inspect only"},
					{Author: "assistant", LineRole: "assistant", Text: "step 50"},
				},
				OmissionNote: state.GuardianTranscriptOmissionNotice,
			}
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-profile", "turn-profile", "call-profile", state.Action{
		Type:    "command",
		Command: "ls",
		CWD:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved", decision)
	}
	if captured == nil {
		t.Fatal("reviewer model did not receive a request")
	}
	if !strings.Contains(captured.Prompt, "Recent transcript:\n- user: inspect only\n- assistant: step 50\n"+state.GuardianTranscriptOmissionNotice) {
		t.Fatalf("prompt missing transcript omission notice:\n%s", captured.Prompt)
	}
}

// Mirrors Rust #41422's independent source flags: tool calls, tool outputs, and
// reasoning are configurable per request without changing conversation order.
func TestGuardianReviewTranscriptOptionalSourcesLikeRust(t *testing.T) {
	now := fixedTime()
	router := guardianProfileTestRouter(t, []session.Item{
		{ID: "call", Type: "function_call", Role: "assistant", Name: "shell", CallID: "call-1", Text: `{"cmd":"ls"}`, CreatedAt: now},
		{ID: "output", Type: "tool_output", CallID: "call-1", Text: "ok", CreatedAt: now},
		{ID: "reasoning", Type: "reasoning", Role: "assistant", Text: "thinking", CreatedAt: now},
	})

	defaults := router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	if len(defaults.Entries) != 2 {
		t.Fatalf("default entries = %#v, want tool call and output without reasoning", defaults.Entries)
	}

	profile := state.SynchronousContextProfile()
	profile.Transcript.Options.IncludeToolCalls = false
	profile.Transcript.Options.IncludeToolOutputs = false
	profile.Transcript.Options.IncludeReasoning = true
	optional := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, profile)
	if len(optional.Entries) != 1 || optional.Entries[0].Text != "thinking" {
		t.Fatalf("optional entries = %#v, want reasoning only", optional.Entries)
	}
}
