package chatwidget

import (
	"reflect"
	"testing"
)

func TestInputQueuePreviewKeepsQueueCategoriesSeparate(t *testing.T) {
	state := InputQueueState{}
	state.QueuedUserMessages = append(state.QueuedUserMessages, NewQueuedUserMessage(NewUserMessage("queued"), QueuedInputPlain))
	state.RejectedSteersQueue = append(state.RejectedSteersQueue, NewUserMessage("rejected"))
	state.PendingSteers = append(state.PendingSteers, PendingSteer{
		UserMessage:   NewUserMessage("pending"),
		HistoryRecord: UserMessageTextHistoryRecord(),
		CompareKey: PendingSteerCompareKey{
			Message: "pending",
		},
	})

	got := state.Preview()
	want := PendingInputPreview{
		QueuedMessages: []string{"queued"},
		PendingSteers:  []string{"pending"},
		RejectedSteers: []string{"rejected"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Preview() = %#v, want %#v", got, want)
	}
}

func TestInputQueuePreviewUsesHistoryOverrides(t *testing.T) {
	state := InputQueueState{}
	state.QueuedUserMessages = append(state.QueuedUserMessages, NewQueuedUserMessage(NewUserMessage("core queued"), QueuedInputPlain))
	state.QueuedUserMessageHistoryRecords = append(state.QueuedUserMessageHistoryRecords, UserMessageOverrideHistoryRecord("history queued"))
	state.RejectedSteersQueue = append(state.RejectedSteersQueue, NewUserMessage("core rejected"))
	state.RejectedSteerHistoryRecords = append(state.RejectedSteerHistoryRecords, UserMessageOverrideHistoryRecord("history rejected"))
	state.PendingSteers = append(state.PendingSteers, PendingSteer{
		UserMessage:   NewUserMessage("core pending"),
		HistoryRecord: UserMessageOverrideHistoryRecord("history pending"),
	})

	got := state.Preview()
	want := PendingInputPreview{
		QueuedMessages: []string{"history queued"},
		PendingSteers:  []string{"history pending"},
		RejectedSteers: []string{"history rejected"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Preview() = %#v, want %#v", got, want)
	}
}

func TestInputQueueClearResetsAllInputQueues(t *testing.T) {
	state := InputQueueState{}
	state.QueuedUserMessages = append(state.QueuedUserMessages, NewQueuedUserMessage(NewUserMessage("queued"), QueuedInputPlain))
	state.QueuedUserMessageHistoryRecords = append(state.QueuedUserMessageHistoryRecords, UserMessageTextHistoryRecord())
	state.RejectedSteersQueue = append(state.RejectedSteersQueue, NewUserMessage("rejected"))
	state.RejectedSteerHistoryRecords = append(state.RejectedSteerHistoryRecords, UserMessageTextHistoryRecord())
	state.PendingSteers = append(state.PendingSteers, PendingSteer{UserMessage: NewUserMessage("pending")})
	state.UserTurnPendingStart = true
	state.SubmitPendingSteersAfterInterrupt = true
	state.SuppressQueueAutosend = true

	state.Clear()

	if len(state.QueuedUserMessages) != 0 ||
		len(state.QueuedUserMessageHistoryRecords) != 0 ||
		state.UserTurnPendingStart ||
		len(state.RejectedSteersQueue) != 0 ||
		len(state.RejectedSteerHistoryRecords) != 0 ||
		len(state.PendingSteers) != 0 ||
		state.SubmitPendingSteersAfterInterrupt {
		t.Fatalf("Clear() left queue state = %#v", state)
	}
	if !state.SuppressQueueAutosend {
		t.Fatal("Clear() should preserve SuppressQueueAutosend like Rust")
	}
}

func TestInputQueueHasQueuedFollowUpMessages(t *testing.T) {
	var state InputQueueState
	if state.HasQueuedFollowUpMessages() {
		t.Fatal("empty state should not have follow-up messages")
	}
	state.PendingSteers = append(state.PendingSteers, PendingSteer{UserMessage: NewUserMessage("pending")})
	if state.HasQueuedFollowUpMessages() {
		t.Fatal("pending steers are already submitted and should not count as queued follow-ups")
	}
	state.QueuedUserMessages = append(state.QueuedUserMessages, NewQueuedUserMessage(NewUserMessage("queued"), QueuedInputPlain))
	if !state.HasQueuedFollowUpMessages() {
		t.Fatal("queued message should count as follow-up")
	}
}
