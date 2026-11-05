package compact

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func pendingAgentMailItem(t *testing.T, id string, sequence uint64, encrypted string) Item {
	t.Helper()
	item := structuredAgentMessageItem(t, id, "Message Type: MESSAGE\nTask name: /root\nSender: /root/worker\nPayload:\n", encrypted)
	item.HistorySequence = sequence
	return item
}

func TestCompactLocallyPreservesPendingAgentMailBeforeLaterInput(t *testing.T) {
	pending := pendingAgentMailItem(t, "pending-mail", 3, "worker result")
	request := &Request{
		ThreadID: "thread-pending-local",
		Trigger:  TriggerAuto,
		Reason:   ReasonTokenLimit,
		Phase:    PhaseMidTurn,
		History: []Item{
			{ID: "old-user", Type: "message", Role: "user", Kind: "user_message", Text: "start", HistorySequence: 1, SampledThroughSequence: 2},
			{ID: "old-assistant", Type: "assistant_message", Role: "assistant", Text: "working", HistorySequence: 2, SampledThroughSequence: 2},
			pending,
			{ID: "later-user", Type: "message", Role: "user", Kind: "user_message", Text: "continue", HistorySequence: 4},
		},
	}
	result, err := CompactLocally(request, 1000, nil, false)
	if err != nil {
		t.Fatalf("CompactLocally() error = %v", err)
	}
	if got, want := pendingMailHistoryIDs(result.NewHistory), "old-user,compaction-summary,pending-mail,later-user"; got != want {
		t.Fatalf("history ids = %s, want %s: %+v", got, want, result.NewHistory)
	}
}

func TestCompactRemotelyKeepsPendingAgentMailAcrossRepeatedCompaction(t *testing.T) {
	pending := pendingAgentMailItem(t, "pending-mail", 3, "worker result")
	history := []Item{
		{ID: "old-user", Type: "message", Role: "user", Kind: "user_message", Text: "start", HistorySequence: 1, SampledThroughSequence: 2},
		{ID: "old-assistant", Type: "assistant_message", Role: "assistant", Text: "working", HistorySequence: 2, SampledThroughSequence: 2},
		pending,
		{ID: "later-user", Type: "message", Role: "user", Kind: "user_message", Text: "continue", HistorySequence: 4},
	}
	calls := 0
	runner := remoteRunnerFunc(func(context.Context, *Request) (*Result, error) {
		calls++
		return &Result{
			Status:  StatusCompleted,
			Summary: "remote summary",
			NewHistory: []Item{{
				ID: "checkpoint-" + strconv.Itoa(calls), Type: "context_compaction",
			}},
		}, nil
	})
	first, err := CompactRemotely(context.Background(), &Request{
		ThreadID: "thread-pending-remote",
		Trigger:  TriggerAuto,
		Reason:   ReasonTokenLimit,
		Phase:    PhaseMidTurn,
		History:  history,
	}, &RemoteOptions{Runner: runner})
	if err != nil {
		t.Fatalf("first CompactRemotely() error = %v", err)
	}
	if got, want := pendingMailHistoryIDs(first.NewHistory), "old-user,checkpoint-1,pending-mail,later-user"; got != want {
		t.Fatalf("first history ids = %s, want %s: %+v", got, want, first.NewHistory)
	}
	second, err := CompactRemotely(context.Background(), &Request{
		ThreadID: "thread-pending-remote",
		Trigger:  TriggerAuto,
		Reason:   ReasonTokenLimit,
		Phase:    PhaseMidTurn,
		History:  first.NewHistory,
	}, &RemoteOptions{Runner: runner})
	if err != nil {
		t.Fatalf("second CompactRemotely() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("remote runner calls = %d, want 2", calls)
	}
	if got, want := pendingMailHistoryIDs(second.NewHistory), "old-user,checkpoint-2,pending-mail,later-user"; got != want {
		t.Fatalf("second history ids = %s, want %s: %+v", got, want, second.NewHistory)
	}
}

func TestCompactionDropsOversizedPendingAgentMail(t *testing.T) {
	oversized := pendingAgentMailItem(t, "oversized", 3, strings.Repeat("x", 80_000))
	small := pendingAgentMailItem(t, "small", 4, "fits")
	result, err := CompactLocally(&Request{
		ThreadID: "thread-oversized-pending",
		Trigger:  TriggerAuto,
		Reason:   ReasonTokenLimit,
		Phase:    PhaseMidTurn,
		History: []Item{
			{ID: "old-assistant", Type: "assistant_message", Role: "assistant", Text: "working", HistorySequence: 2, SampledThroughSequence: 2},
			oversized,
			small,
		},
	}, 1000, nil, false)
	if err != nil {
		t.Fatalf("CompactLocally() error = %v", err)
	}
	if got, want := pendingMailHistoryIDs(result.NewHistory), "compaction-summary,small"; got != want {
		t.Fatalf("history ids = %s, want %s: %+v", got, want, result.NewHistory)
	}
}

func pendingMailHistoryIDs(items []Item) string {
	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}
	return strings.Join(ids, ",")
}
