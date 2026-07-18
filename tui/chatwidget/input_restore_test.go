package chatwidget

import (
	"reflect"
	"testing"

	"codex_go/turn"
)

func TestInputRestoreDrainPendingMessagesOrderAndHistoryMatchRust(t *testing.T) {
	placeholder := "[Image #1]"
	state := InputQueueState{
		RejectedSteersQueue: []UserMessage{NewUserMessage("rejected core")},
		RejectedSteerHistoryRecords: []UserMessageHistoryRecord{
			UserMessageOverrideHistoryRecord("rejected restore"),
		},
		PendingSteers: []PendingSteer{{
			UserMessage: UserMessage{
				Text:         "pending [Image #1]",
				LocalImages:  []string{"pending.png"},
				TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 8, End: 18}, Placeholder: &placeholder}},
			},
			HistoryRecord: UserMessageTextHistoryRecord(),
		}},
		QueuedUserMessages: []QueuedUserMessage{{
			UserMessage: UserMessage{
				Text:         "queued [Image #1]",
				LocalImages:  []string{"queued.png"},
				TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 7, End: 17}, Placeholder: &placeholder}},
			},
			Action: QueuedInputPlain,
		}},
	}
	composer := ThreadComposerState{
		Text:            "draft",
		RemoteImageURLs: []string{"https://example.test/current.png"},
	}

	got, ok := state.DrainPendingMessagesForRestore(composer)

	if !ok {
		t.Fatal("expected pending messages to restore")
	}
	if got.Text != "rejected restore\npending [Image #2]\nqueued [Image #3]\ndraft" {
		t.Fatalf("restored text = %q", got.Text)
	}
	if !reflect.DeepEqual(got.LocalImages, []string{"pending.png", "queued.png"}) {
		t.Fatalf("local images = %#v", got.LocalImages)
	}
	if !reflect.DeepEqual(got.RemoteImageURLs, []string{"https://example.test/current.png"}) {
		t.Fatalf("remote images = %#v", got.RemoteImageURLs)
	}
	if len(state.PendingSteers) != 0 || len(state.RejectedSteersQueue) != 0 || len(state.QueuedUserMessages) != 0 {
		t.Fatalf("queues not drained: %#v", state)
	}
}

func TestInputRestoreDrainPendingMessagesRemapsCollidingPastesMatchRust(t *testing.T) {
	firstPlaceholder := "[Pasted Content 5 chars]"
	state := InputQueueState{
		QueuedUserMessages: []QueuedUserMessage{
			{
				UserMessage: UserMessage{
					Text:         "queued one [Pasted Content 5 chars]",
					TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 11, End: 35}, Placeholder: &firstPlaceholder}},
				},
				Action:        QueuedInputPlain,
				PendingPastes: [][2]string{{firstPlaceholder, "abcde"}},
			},
			{
				UserMessage: UserMessage{
					Text:         "queued two [Pasted Content 5 chars]",
					TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 11, End: 35}, Placeholder: &firstPlaceholder}},
				},
				Action:        QueuedInputPlain,
				PendingPastes: [][2]string{{firstPlaceholder, "vwxyz"}},
			},
		},
	}
	composer := ThreadComposerState{
		Text:          "current [Pasted Content 5 chars]",
		TextElements:  []turn.TextElement{{ByteRange: turn.ByteRange{Start: 8, End: 32}, Placeholder: &firstPlaceholder}},
		PendingPastes: [][2]string{{firstPlaceholder, "12345"}},
	}

	got, ok := state.DrainPendingMessagesForRestore(composer)

	if !ok {
		t.Fatal("expected pending messages to restore")
	}
	wantText := "queued one [Pasted Content 5 chars]\nqueued two [Pasted Content 5 chars] #2\ncurrent [Pasted Content 5 chars] #3"
	if got.Text != wantText {
		t.Fatalf("restored text = %q, want %q", got.Text, wantText)
	}
	wantPastes := [][2]string{
		{"[Pasted Content 5 chars]", "abcde"},
		{"[Pasted Content 5 chars] #2", "vwxyz"},
		{"[Pasted Content 5 chars] #3", "12345"},
	}
	if !reflect.DeepEqual(got.PendingPastes, wantPastes) {
		t.Fatalf("pending pastes = %#v", got.PendingPastes)
	}
}

func TestInputRestoreInterruptedTurnSubmitsPendingSteersImmediatelyMatchRust(t *testing.T) {
	state := InputQueueState{
		SubmitPendingSteersAfterInterrupt: true,
		PendingSteers: []PendingSteer{
			{UserMessage: NewUserMessage("first pending"), HistoryRecord: UserMessageTextHistoryRecord()},
			{UserMessage: NewUserMessage("second pending"), HistoryRecord: UserMessageTextHistoryRecord()},
		},
		QueuedUserMessages: []QueuedUserMessage{
			NewQueuedUserMessage(NewUserMessage("queued draft"), QueuedInputPlain),
		},
	}

	result := state.OnInterruptedTurn(nil, InterruptedTurnRestoreOptions{
		Reason:          TurnAbortInterrupted,
		Composer:        ThreadComposerState{Text: "still editing"},
		InterruptNotice: "Conversation interrupted",
	})

	if result.SubmittedMessage == nil || result.SubmittedMessage.Text != "first pending\nsecond pending" {
		t.Fatalf("submitted message = %#v", result.SubmittedMessage)
	}
	if result.RestoredComposer != nil {
		t.Fatalf("pending steers should submit instead of restoring composer: %#v", result.RestoredComposer)
	}
	if result.NoticeKind != InterruptedTurnNoticeInfo || result.NoticeMessage != "Model interrupted to submit steer instructions." {
		t.Fatalf("notice = %q %q", result.NoticeKind, result.NoticeMessage)
	}
	if state.SubmitPendingSteersAfterInterrupt || len(state.PendingSteers) != 0 {
		t.Fatalf("pending steer flags not cleared: %#v", state)
	}
	if len(state.QueuedUserMessages) != 1 || state.QueuedUserMessages[0].UserMessage.Text != "queued draft" {
		t.Fatalf("queued draft should remain queued: %#v", state.QueuedUserMessages)
	}
	if !reflect.DeepEqual(result.PendingInputPreview.QueuedMessages, []string{"queued draft"}) {
		t.Fatalf("pending preview = %#v", result.PendingInputPreview)
	}
}

func TestInputRestoreInterruptedTurnRestoresPendingQueuedAndComposerMatchRust(t *testing.T) {
	state := InputQueueState{
		PendingSteers: []PendingSteer{
			{UserMessage: NewUserMessage("pending steer"), HistoryRecord: UserMessageTextHistoryRecord()},
		},
		QueuedUserMessages: []QueuedUserMessage{
			NewQueuedUserMessage(NewUserMessage("queued draft"), QueuedInputPlain),
		},
	}

	result := state.OnInterruptedTurn(nil, InterruptedTurnRestoreOptions{
		Reason:          TurnAbortInterrupted,
		Composer:        ThreadComposerState{Text: "still editing"},
		InterruptNotice: "Conversation interrupted",
	})

	if result.SubmittedMessage != nil {
		t.Fatalf("unexpected submitted message = %#v", result.SubmittedMessage)
	}
	if result.RestoredComposer == nil || result.RestoredComposer.Text != "pending steer\nqueued draft\nstill editing" {
		t.Fatalf("restored composer = %#v", result.RestoredComposer)
	}
	if result.NoticeKind != InterruptedTurnNoticeError || result.NoticeMessage != "Conversation interrupted" {
		t.Fatalf("notice = %q %q", result.NoticeKind, result.NoticeMessage)
	}
	if len(state.PendingSteers) != 0 || len(state.QueuedUserMessages) != 0 {
		t.Fatalf("queues should be drained after restore: %#v", state)
	}
}

func TestInputRestoreInterruptedTurnReturnsArmedCancelPromptAndSuppressesNoticeMatchRust(t *testing.T) {
	cancel := CancelEditState{}
	cancel.RecordCancelEditCandidate(NewUserMessage("original prompt"))
	cancel.Arm(true, InputQueueState{}, false)
	state := InputQueueState{}

	result := state.OnInterruptedTurn(&cancel, InterruptedTurnRestoreOptions{
		Reason:          TurnAbortInterrupted,
		InterruptNotice: "Conversation interrupted",
	})

	if result.CancelledPrompt == nil || result.CancelledPrompt.Text != "original prompt" {
		t.Fatalf("cancelled prompt = %#v", result.CancelledPrompt)
	}
	if result.NoticeMessage != "" || result.NoticeKind != "" {
		t.Fatalf("cancel edit should suppress interrupt notice: %#v", result)
	}
	if cancel.Prompt != nil || cancel.Eligible || cancel.Armed {
		t.Fatalf("cancel edit state not cleared: %#v", cancel)
	}
	if !result.FinalizeTurn || !result.RequestRedraw {
		t.Fatalf("interrupted turn should finalize and redraw: %#v", result)
	}
}
