package retainedctx

import (
	"fmt"
	"reflect"
	"testing"
)

func liveInstruction(text string) RetainedUserMessage {
	return RetainedUserMessage{TurnID: "turn-1", Text: text, Complete: false}
}

func userMessageDetails(entries []OrderedEntry) []struct {
	Order RetainedContextOrder
	Text  string
} {
	out := make([]struct {
		Order RetainedContextOrder
		Text  string
	}, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Entry.UserMessage != nil:
			out = append(out, struct {
				Order RetainedContextOrder
				Text  string
			}{entry.Order, entry.Entry.UserMessage.Text})
		case entry.Entry.AssistantMessage != nil:
			out = append(out, struct {
				Order RetainedContextOrder
				Text  string
			}{entry.Order, entry.Entry.AssistantMessage.Text})
		case entry.Entry.VerifiedAnswer != nil:
			out = append(out, struct {
				Order RetainedContextOrder
				Text  string
			}{entry.Order, entry.Entry.VerifiedAnswer.Questions[0].Answer})
		}
	}
	return out
}

func TestRecoveryPreservesSourceIdentityAcceptanceOrderAndCheckpointGaps(t *testing.T) {
	initial := liveInstruction("Inspect the deployment.")
	retainedExcerpt := liveInstruction("")
	retainedExcerpt.MessageID = stringPtr("retained-message")
	original := retainedExcerpt
	original.Text = "Deploy the reviewed change."

	retained := &RetainedContext{}
	retained.RecordUserMessage(initial, LocalInputSource(uintPtr(0)))
	retained.RecordUserMessage(retainedExcerpt, LocalInputSource(uintPtr(1)))
	retained.Record(RetainedContextEvent{
		Answer: VerifiedAnswer{
			TurnID: "answer-turn",
			CallID: "publish-question",
			Questions: []VerifiedQuestionAnswer{{
				Question: "Publish?",
				Answer:   "Never publicly.",
			}},
		},
		AcceptanceOrder: uintPtr(3),
	})
	retained.MarkUserMessagesIncomplete()
	checkpoint := retained.Clone()

	reconciled := NewReconciledRetainedContext(retained, []LiveUserMessage{
		// Existing identities match before requiring an order, including an omitted excerpt.
		{Message: initial},
		{Message: original},
		{Order: uintPtr(4), Message: liveInstruction("Do not deploy after all.")},
		// This steer arrived before the checkpoint-only answer but was recorded later.
		{Order: uintPtr(2), Message: liveInstruction("Make the deployment public.")},
		// A second identical message is a distinct source once the retained one matched.
		{Order: uintPtr(5), Message: initial},
	})

	wantEntries := []struct {
		Order RetainedContextOrder
		Text  string
	}{
		{RetainedContextOrder{Order: 0}, "Inspect the deployment."},
		{RetainedContextOrder{Order: 1}, ""},
		{RetainedContextOrder{Order: 2}, "Make the deployment public."},
		{RetainedContextOrder{Order: 3}, "Never publicly."},
		{RetainedContextOrder{Order: 4}, "Do not deploy after all."},
		{RetainedContextOrder{Order: 5}, "Inspect the deployment."},
	}
	if got := userMessageDetails(reconciled.OrderedEntries()); !reflect.DeepEqual(got, wantEntries) {
		t.Fatalf("reconciled entries = %#v, want %#v", got, wantEntries)
	}
	if !reconciled.MissingUserMessages {
		t.Fatal("a retained checkpoint gap must stay flagged")
	}
	legacyInstruction := liveInstruction("Only publish to staging.")
	unmatched := reconciled.UnmatchedUserMessages([]RetainedUserMessage{
		initial,
		original,
		liveInstruction("Do not deploy after all."),
		legacyInstruction,
	})
	if !reflect.DeepEqual(unmatched, []RetainedUserMessage{legacyInstruction}) {
		t.Fatalf("unmatched = %#v, want only the legacy instruction", unmatched)
	}
	if !reflect.DeepEqual(retained, checkpoint) {
		t.Fatal("reconciliation must not change the checkpoint")
	}
}

func TestRecoveryMarksMissingAndConflictingOrdersIncomplete(t *testing.T) {
	retained := &RetainedContext{}
	retained.RecordUserMessage(liveInstruction("Inspect the deployment."), LocalInputSource(uintPtr(1)))
	if retained.HasMissingUserMessages() {
		t.Fatal("a fresh retained instruction must not be flagged missing")
	}
	checkpoint := retained.Clone()

	for _, invalidOrder := range []*uint64{nil, uintPtr(1), uintPtr(2)} {
		reconciled := NewReconciledRetainedContext(retained, []LiveUserMessage{
			{Order: uintPtr(2), Message: liveInstruction("Do not deploy.")},
			{Order: invalidOrder, Message: liveInstruction("Invalid-order instruction.")},
		})
		wantEntries := []struct {
			Order RetainedContextOrder
			Text  string
		}{
			{RetainedContextOrder{Order: 1}, "Inspect the deployment."},
			{RetainedContextOrder{Order: 2}, "Do not deploy."},
		}
		if got := userMessageDetails(reconciled.OrderedEntries()); !reflect.DeepEqual(got, wantEntries) {
			t.Fatalf("invalid order %v: entries = %#v, want %#v", invalidOrder, got, wantEntries)
		}
		if !reconciled.MissingUserMessages {
			t.Fatalf("invalid order %v: recovery must be flagged incomplete", invalidOrder)
		}
	}
	if !reflect.DeepEqual(retained, checkpoint) {
		t.Fatal("reconciliation must not change the checkpoint")
	}
}

func TestHeartbeatVersionsSurviveRetentionRestoreAndReconciliation(t *testing.T) {
	messages := make([]RetainedUserMessage, 0, 35)
	for index := 0; index < 35; index++ {
		instructions := "Monitor only."
		origin := UserInputOriginHeartbeat
		switch index {
		case 1:
			instructions, origin = "Create a worktree.", UserInputOriginUser
		case 31:
			instructions = "Stop monitoring."
		case 33:
			instructions, origin = "Monitor only.", UserInputOriginUser
		}
		messages = append(messages, RetainedUserMessage{
			TurnID:    fmt.Sprintf("turn-%d", index),
			MessageID: stringPtr(fmt.Sprintf("message-%d", index)),
			Text: fmt.Sprintf(
				"<heartbeat>\n  <automation_id>monitor</automation_id>\n  <current_time_iso>2026-09-23T00:%02d:00Z</current_time_iso>\n  <instructions>\n%s\n  </instructions>\n</heartbeat>\n",
				index, instructions),
			Complete: true,
			Origin:   origin,
		})
	}
	retained := &RetainedContext{}
	for index, message := range messages {
		if index == 30 {
			checkpoint := roundTripContext(t, retained)
			retained = &RetainedContext{}
			retained.Restore(checkpoint, nil)
		}
		retained.RecordUserMessage(message, LocalInputSource(uintPtr(uint64(index))))
	}
	expectedIndexes := []int{0, 1, 31, 32, 33}
	expected := make([]struct {
		Order RetainedContextOrder
		Text  string
	}, 0, len(expectedIndexes))
	for _, index := range expectedIndexes {
		expected = append(expected, struct {
			Order RetainedContextOrder
			Text  string
		}{RetainedContextOrder{Order: uint64(index)}, messages[index].Text})
	}
	// Check storage before reconciliation can recover or coalesce anything.
	if got := userMessageDetails(retained.OrderedEntries()); !reflect.DeepEqual(got, expected) {
		t.Fatalf("retained entries = %#v, want %#v", got, expected)
	}
	if !retained.UserMessagesComplete() {
		t.Fatal("every stored heartbeat version is complete")
	}
	for _, missing := range []bool{false, true} {
		if missing {
			retained.MarkUserMessagesIncomplete()
		}
		checkpoint := retained.Clone()
		live := make([]LiveUserMessage, 0, len(messages))
		// Reverse delivery order to exercise sorting across recovered and retained versions.
		for index := len(messages) - 1; index >= 0; index-- {
			message := messages[index]
			message.Complete = false
			live = append(live, LiveUserMessage{Order: uintPtr(uint64(index)), Message: message})
		}
		reconciled := NewReconciledRetainedContext(retained, live)
		if got := userMessageDetails(reconciled.OrderedEntries()); !reflect.DeepEqual(got, expected) {
			t.Fatalf("missing=%v: reconciled entries = %#v, want %#v", missing, got, expected)
		}
		if reconciled.LatestUserTurnID == nil || *reconciled.LatestUserTurnID != "turn-34" {
			t.Fatalf("missing=%v: latest user turn = %v, want turn-34", missing, reconciled.LatestUserTurnID)
		}
		if reconciled.MissingUserMessages != missing {
			t.Fatalf("missing=%v: flagged missing = %v", missing, reconciled.MissingUserMessages)
		}
		if !reflect.DeepEqual(retained, checkpoint) {
			t.Fatalf("missing=%v: reconciliation must not change the checkpoint", missing)
		}
	}
}
