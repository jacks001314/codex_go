package cloudbackend

// Rust #61490: a Cloud task selection keeps the displayed conversation, its
// draft, and its reply stream until the replacement attachment and its history
// are ready.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

func preparedActivation(_ context.Context, threadID string) (*cloudclient.ThreadActivation, error) {
	return &cloudclient.ThreadActivation{
		Thread:         cloudclient.ThreadSnapshot{ID: threadID, Status: map[string]any{"type": "idle"}},
		Model:          "gpt-5",
		ModelProvider:  "openai",
		CWD:            "/repo",
		ApprovalPolicy: "on-request",
	}, nil
}

func resumeThreadForTest(t *testing.T, client *Client, threadID string) {
	t.Helper()
	params, err := json.Marshal(appserver.ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true})
	if err != nil {
		t.Fatalf("marshal resume: %v", err)
	}
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadResume, params); rpcErr != nil {
		t.Fatalf("resume %s: %#v", threadID, rpcErr)
	}
}

func expectNoCloudNotification(t *testing.T, client *Client, message string) {
	t.Helper()
	select {
	case notification := <-client.Notifications():
		t.Fatalf("%s: %#v", message, notification)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestPreparedResumeKeepsCurrentAttachmentUntilCommitLikeRust(t *testing.T) {
	oldEvents := make(chan cloudclient.Event)
	oldErrs := make(chan error)
	// Two buffered events arrive before the commit; both must be replayed after
	// the replacement becomes active.
	newEvents := make(chan cloudclient.Event, 2)
	newEvents <- cloudclient.Event{Kind: cloudclient.EventServerRequest}
	newEvents <- cloudclient.Event{Kind: cloudclient.EventServerRequest}
	newErrs := make(chan error)
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(_ context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error) {
			if threadID == "old" {
				return oldEvents, oldErrs, nil
			}
			return newEvents, newErrs, nil
		},
		listTurns: func(_ context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
			return cloudclient.HistoryPage[cloudclient.HistoryTurn]{
				ThreadID: request.ThreadID,
				Data:     []cloudclient.HistoryTurn{{Summary: cloudclient.TurnSummary{ID: "turn-1", Status: cloudclient.TurnCompleted}}},
			}, nil
		},
	}
	client := Connect(context.Background(), fake, nil)
	defer client.Close()
	ctx := context.Background()
	resumeThreadForTest(t, client, "old")

	settings, prepared, rpcErr := client.PrepareThreadResume(ctx, appserver.ThreadResumeParams{ThreadID: "new", ExcludeTurns: true})
	if rpcErr != nil || prepared == nil {
		t.Fatalf("prepare = %#v, %#v", settings, rpcErr)
	}
	if settings.Thread == nil || settings.Thread.ID != "new" {
		t.Fatalf("prepared settings = %#v", settings)
	}
	// The displayed conversation still owns the attachment.
	if err := client.admitTurn("old"); err != nil {
		t.Fatalf("old attachment lost while preparing: %v", err)
	}
	if err := client.admitTurn("new"); err == nil {
		t.Fatal("prepared attachment admitted a turn before commit")
	}
	expectNoCloudNotification(t, client, "preparation published a notification early")

	// The scoped handle serves only the prepared conversation's history.
	result, rpcErr := prepared.Request(ctx, appserver.MethodThreadTurnsList, appserver.ThreadTurnsListParams{ThreadID: "new", ItemsView: appserver.TurnItemsNotLoaded})
	if rpcErr != nil || !strings.Contains(string(result), `"turn-1"`) {
		t.Fatalf("scoped turns = %s, %#v", result, rpcErr)
	}
	if _, rpcErr := prepared.Request(ctx, appserver.MethodThreadTurnsList, appserver.ThreadTurnsListParams{ThreadID: "old", ItemsView: appserver.TurnItemsNotLoaded}); rpcErr == nil {
		t.Fatal("scoped handle read another conversation")
	}
	if _, rpcErr := prepared.Request(ctx, appserver.MethodThreadRead, appserver.ThreadReadParams{ThreadID: "new"}); rpcErr == nil {
		t.Fatal("scoped handle served a non-history request")
	}

	if rpcErr := prepared.Commit(ctx); rpcErr != nil {
		t.Fatalf("commit: %#v", rpcErr)
	}
	if err := client.admitTurn("new"); err != nil {
		t.Fatalf("committed attachment rejected a turn: %v", err)
	}
	if err := client.admitTurn("old"); err == nil {
		t.Fatal("previous attachment survived the commit")
	}
	loaded, rpcErr := client.Request(ctx, appserver.MethodThreadLoadedList, nil)
	if rpcErr != nil || !strings.Contains(string(loaded), `"new"`) || strings.Contains(string(loaded), `"old"`) {
		t.Fatalf("loaded list = %s, %#v", loaded, rpcErr)
	}
	read, rpcErr := client.Request(ctx, appserver.MethodThreadRead, json.RawMessage(`{"threadId":"new"}`))
	if rpcErr != nil || !strings.Contains(string(read), `"readState":null`) {
		t.Fatalf("read after commit = %s, %#v", read, rpcErr)
	}
	// The buffered prefix is replayed once the attachment is installed.
	for index := 0; index < 2; index++ {
		select {
		case notification := <-client.Notifications():
			if notification.Method != appserver.NotificationWarning {
				t.Fatalf("notification %d = %#v", index, notification)
			}
		case <-time.After(time.Second):
			t.Fatalf("buffered event %d was not replayed", index)
		}
	}
}

func TestFailedPreparedResumeKeepsCurrentAttachmentLikeRust(t *testing.T) {
	oldEvents := make(chan cloudclient.Event)
	oldErrs := make(chan error)
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(_ context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error) {
			if threadID == "old" {
				return oldEvents, oldErrs, nil
			}
			events := make(chan cloudclient.Event)
			close(events)
			errs := make(chan error)
			close(errs)
			return events, errs, nil
		},
	}
	client := Connect(context.Background(), fake, nil)
	defer client.Close()
	resumeThreadForTest(t, client, "old")

	_, prepared, rpcErr := client.PrepareThreadResume(context.Background(), appserver.ThreadResumeParams{ThreadID: "new", ExcludeTurns: true})
	if rpcErr == nil || prepared != nil {
		t.Fatalf("closed replacement was accepted: %#v", prepared)
	}
	if err := client.admitTurn("old"); err != nil {
		t.Fatalf("failed preparation displaced the current attachment: %v", err)
	}
	if err := client.admitTurn("new"); err == nil {
		t.Fatal("failed preparation left an attachment")
	}
}

func TestDiscardedPreparedResumeKeepsCurrentAttachmentLikeRust(t *testing.T) {
	oldEvents := make(chan cloudclient.Event)
	oldErrs := make(chan error)
	newCancelled := make(chan struct{})
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(ctx context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error) {
			if threadID == "old" {
				return oldEvents, oldErrs, nil
			}
			events := make(chan cloudclient.Event)
			errs := make(chan error)
			go func() {
				<-ctx.Done()
				close(newCancelled)
			}()
			return events, errs, nil
		},
	}
	client := Connect(context.Background(), fake, nil)
	defer client.Close()
	resumeThreadForTest(t, client, "old")

	_, prepared, rpcErr := client.PrepareThreadResume(context.Background(), appserver.ThreadResumeParams{ThreadID: "new", ExcludeTurns: true})
	if rpcErr != nil || prepared == nil {
		t.Fatalf("prepare = %#v", rpcErr)
	}
	prepared.Discard()
	select {
	case <-newCancelled:
	case <-time.After(time.Second):
		t.Fatal("discarded preparation kept its stream open")
	}
	if err := client.admitTurn("old"); err != nil {
		t.Fatalf("discard displaced the current attachment: %v", err)
	}
	if err := client.admitTurn("new"); err == nil {
		t.Fatal("discarded preparation left an attachment")
	}
}

func TestPreparedHistoryWarningWaitsForCommitLikeRust(t *testing.T) {
	oldEvents := make(chan cloudclient.Event)
	oldErrs := make(chan error)
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(_ context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error) {
			if threadID == "old" {
				return oldEvents, oldErrs, nil
			}
			return make(chan cloudclient.Event), make(chan error), nil
		},
		listItems: func(_ context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
			// The single entry has no displayable summary, so the page emits the
			// partial-history warning.
			return cloudclient.HistoryPage[cloudclient.HistoryItem]{
				ThreadID: request.ThreadID,
				Data:     []cloudclient.HistoryItem{{TurnID: "turn-1"}},
			}, nil
		},
	}
	client := Connect(context.Background(), fake, nil)
	defer client.Close()
	resumeThreadForTest(t, client, "old")

	_, prepared, rpcErr := client.PrepareThreadResume(context.Background(), appserver.ThreadResumeParams{ThreadID: "new", ExcludeTurns: true})
	if rpcErr != nil || prepared == nil {
		t.Fatalf("prepare = %#v", rpcErr)
	}
	if _, rpcErr := prepared.Request(context.Background(), appserver.MethodThreadItemsList, appserver.ThreadItemsListParams{ThreadID: "new"}); rpcErr != nil {
		t.Fatalf("scoped items: %#v", rpcErr)
	}
	expectNoCloudNotification(t, client, "prepared history warning was published before commit")
	if rpcErr := prepared.Commit(context.Background()); rpcErr != nil {
		t.Fatalf("commit: %#v", rpcErr)
	}
	select {
	case notification := <-client.Notifications():
		if notification.Method != appserver.NotificationWarning {
			t.Fatalf("commit notification = %#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("committed history warning was not published")
	}
}