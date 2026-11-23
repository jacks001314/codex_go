package cloudbackend

// Rust #62140: a disconnected, loaded Cloud conversation reattaches with its
// replacement reply stream held until the caller refreshes turn and item
// history.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"codex_go/turn"
)

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	return encoded
}

func connectHeldRecoveryTest(t *testing.T, source cloudSource, loginCurrent func() bool) *Client {
	t.Helper()
	client := ConnectWithIdleRecovery(context.Background(), source, nil, IdleRecoveryOptions{LoginCurrent: loginCurrent})
	t.Cleanup(client.Close)
	return client
}

func drainCloudNotifications(t *testing.T, client *Client) {
	t.Helper()
	for {
		select {
		case <-client.Notifications():
		case <-time.After(20 * time.Millisecond):
			return
		}
	}
}

// loadDisconnectedThreadForTest resumes threadID and lets a rejected turn start
// surface the closed reply stream, leaving the conversation loaded but
// disconnected the way recovery expects.
func loadDisconnectedThreadForTest(t *testing.T, client *Client, threadID string, disconnect func()) {
	t.Helper()
	params := mustJSON(t, appserver.ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadResume, params); rpcErr != nil {
		t.Fatalf("resume %s: %#v", threadID, rpcErr)
	}
	disconnect()
	start := mustJSON(t, map[string]string{"threadId": threadID})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodTurnStart, start); rpcErr == nil {
		t.Fatal("turn start reached a closed reply stream")
	}
}

func startTurnAfterRecovery(t *testing.T, client *Client, threadID string) {
	t.Helper()
	if _, rpcErr := client.SubmitUserMessage(context.Background(), turn.TurnStartParams{
		ThreadID:            threadID,
		ClientUserMessageID: "recovery-message",
		Prompt:              "hello",
	}); rpcErr != nil {
		t.Fatalf("committed stream rejected a turn: %#v", rpcErr)
	}
}

// Mirrors Rust #62140: a quiet or busy replacement stays held, recovery history
// is read under the same sign-in, and installation forwards buffered events.
func TestResumeDisconnectedThreadHoldsUntilInstallLikeRust(t *testing.T) {
	initialEvents := make(chan cloudclient.Event)
	initialErrs := make(chan error)
	replacementEvents := make(chan cloudclient.Event, 4)
	replacementErrs := make(chan error)
	var attaches atomic.Int32
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			if attaches.Add(1) == 1 {
				return initialEvents, initialErrs, nil
			}
			return replacementEvents, replacementErrs, nil
		},
		listTurns: func(_ context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
			return cloudclient.HistoryPage[cloudclient.HistoryTurn]{
				ThreadID: request.ThreadID,
				Data: []cloudclient.HistoryTurn{
					{Summary: cloudclient.TurnSummary{ID: "turn-1", Status: cloudclient.TurnCompleted}},
				},
			}, nil
		},
		listItems: func(_ context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
			return cloudclient.HistoryPage[cloudclient.HistoryItem]{ThreadID: request.ThreadID}, nil
		},
		addMessage: func(_ context.Context, request cloudclient.AddUserMessageRequest) (*cloudclient.Admission, error) {
			return &cloudclient.Admission{
				ThreadID: request.ThreadID,
				Outcome: cloudclient.AdmissionOutcome{
					Kind: cloudclient.AdmissionStarted,
					Turn: &cloudclient.TurnSummary{ID: "turn-2", Status: cloudclient.TurnInProgress},
				},
			}, nil
		},
	}
	client := connectHeldRecoveryTest(t, fake, func() bool { return true })
	loadDisconnectedThreadForTest(t, client, "t", func() {
		close(initialEvents)
		close(initialErrs)
	})
	drainCloudNotifications(t, client)

	// A busy replacement carries an event that must not be forwarded before the
	// caller installs the stream.
	replacementEvents <- cloudclient.Event{Kind: cloudclient.EventServerRequest}
	settings, release, rpcErr := client.ResumeDisconnectedThread(
		context.Background(), "t", false, time.Now().Add(8*time.Second))
	if rpcErr != nil || release == nil {
		t.Fatalf("resume disconnected = %#v, %#v", settings, rpcErr)
	}
	if settings == nil || settings.Thread == nil || settings.Thread.ID != "t" {
		t.Fatalf("cached settings = %#v", settings)
	}
	expectNoCloudNotification(t, client, "held stream forwarded an event before install")

	turns := mustJSON(t, appserver.ThreadTurnsListParams{ThreadID: "t", ItemsView: appserver.TurnItemsNotLoaded})
	result, rpcErr := client.Request(context.Background(), appserver.MethodThreadTurnsList, turns)
	if rpcErr != nil || string(result) == "" {
		t.Fatalf("recovery turns = %s, %#v", result, rpcErr)
	}
	items := mustJSON(t, appserver.ThreadItemsListParams{ThreadID: "t"})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadItemsList, items); rpcErr != nil {
		t.Fatalf("recovery items: %#v", rpcErr)
	}

	if rpcErr := release.Install(context.Background()); rpcErr != nil {
		t.Fatalf("install: %#v", rpcErr)
	}
	// The buffered event is the committed stream's first event.
	select {
	case notification := <-client.Notifications():
		if notification.Method != appserver.NotificationWarning {
			t.Fatalf("first notification = %#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("held buffered event was not forwarded after install")
	}
	// Canceling a committed handle must not tear down the live stream.
	release.Cancel()
	startTurnAfterRecovery(t, client, "t")
}

// Mirrors Rust #62140: PAT sessions reopen explicitly, a live attachment has no
// recovery to offer, and an expired deadline is rejected up front.
func TestResumeDisconnectedThreadRejectsUnavailableLikeRust(t *testing.T) {
	pat := connectHeldRecoveryTest(t, &fakeCloud{}, nil)
	if _, _, rpcErr := pat.ResumeDisconnectedThread(context.Background(), "t", false, time.Now().Add(8*time.Second)); rpcErr == nil ||
		rpcErr.Message != cloudRecoveryPATReason {
		t.Fatalf("PAT resume = %#v, want %q", rpcErr, cloudRecoveryPATReason)
	}

	events := make(chan cloudclient.Event)
	errs := make(chan error)
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return events, errs, nil
		},
	}
	client := connectHeldRecoveryTest(t, fake, func() bool { return true })
	params := mustJSON(t, appserver.ThreadResumeParams{ThreadID: "t", ExcludeTurns: true})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadResume, params); rpcErr != nil {
		t.Fatalf("resume: %#v", rpcErr)
	}
	if _, _, rpcErr := client.ResumeDisconnectedThread(context.Background(), "t", false, time.Now().Add(8*time.Second)); rpcErr == nil ||
		rpcErr.Message != cloudRecoveryNoAttachmentReason {
		t.Fatalf("busy resume = %#v, want %q", rpcErr, cloudRecoveryNoAttachmentReason)
	}
	if _, _, rpcErr := client.ResumeDisconnectedThread(context.Background(), "t", false, time.Now().Add(-time.Second)); rpcErr == nil ||
		rpcErr.Message != cloudRecoveryTimeoutReason {
		t.Fatalf("expired deadline = %#v, want %q", rpcErr, cloudRecoveryTimeoutReason)
	}
}

// Mirrors Rust #62140: dropping a hold closes its replacement, and the next
// resume starts immediately instead of waiting for the old attempt to unwind.
func TestResumeDisconnectedThreadCancelThenRetryLikeRust(t *testing.T) {
	initialEvents := make(chan cloudclient.Event)
	initialErrs := make(chan error)
	var attaches atomic.Int32
	canceled := make(chan struct{}, 4)
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(ctx context.Context, _ string) (<-chan cloudclient.Event, <-chan error, error) {
			if attaches.Add(1) == 1 {
				return initialEvents, initialErrs, nil
			}
			events := make(chan cloudclient.Event, 2)
			errs := make(chan error)
			go func() {
				<-ctx.Done()
				canceled <- struct{}{}
			}()
			return events, errs, nil
		},
		addMessage: func(_ context.Context, request cloudclient.AddUserMessageRequest) (*cloudclient.Admission, error) {
			return &cloudclient.Admission{
				ThreadID: request.ThreadID,
				Outcome: cloudclient.AdmissionOutcome{
					Kind: cloudclient.AdmissionStarted,
					Turn: &cloudclient.TurnSummary{ID: "turn-2", Status: cloudclient.TurnInProgress},
				},
			}, nil
		},
	}
	client := connectHeldRecoveryTest(t, fake, func() bool { return true })
	loadDisconnectedThreadForTest(t, client, "t", func() {
		close(initialEvents)
		close(initialErrs)
	})
	drainCloudNotifications(t, client)

	_, release, rpcErr := client.ResumeDisconnectedThread(
		context.Background(), "t", false, time.Now().Add(8*time.Second))
	if rpcErr != nil || release == nil {
		t.Fatalf("first resume = %#v", rpcErr)
	}
	release.Cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("canceled hold did not close its replacement stream")
	}

	_, retry, rpcErr := client.ResumeDisconnectedThread(
		context.Background(), "t", true, time.Now().Add(8*time.Second))
	if rpcErr != nil || retry == nil {
		t.Fatalf("retry after cancel = %#v", rpcErr)
	}
	if rpcErr := retry.Install(context.Background()); rpcErr != nil {
		t.Fatalf("install retry: %#v", rpcErr)
	}
	startTurnAfterRecovery(t, client, "t")
}

// Mirrors Rust #62140: a changed sign-in withholds the fetched page and rejects
// the next history read before it reaches Cloud.
func TestResumeDisconnectedThreadWithholdsPageAfterSignInChangeLikeRust(t *testing.T) {
	initialEvents := make(chan cloudclient.Event)
	initialErrs := make(chan error)
	replacementEvents := make(chan cloudclient.Event, 2)
	replacementErrs := make(chan error)
	var attaches atomic.Int32
	var valid atomic.Bool
	valid.Store(true)
	var itemReads atomic.Int32
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			if attaches.Add(1) == 1 {
				return initialEvents, initialErrs, nil
			}
			return replacementEvents, replacementErrs, nil
		},
		listTurns: func(_ context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
			valid.Store(false)
			return cloudclient.HistoryPage[cloudclient.HistoryTurn]{ThreadID: request.ThreadID}, nil
		},
		listItems: func(_ context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
			itemReads.Add(1)
			return cloudclient.HistoryPage[cloudclient.HistoryItem]{ThreadID: request.ThreadID}, nil
		},
	}
	client := connectHeldRecoveryTest(t, fake, valid.Load)
	loadDisconnectedThreadForTest(t, client, "t", func() {
		close(initialEvents)
		close(initialErrs)
	})
	drainCloudNotifications(t, client)

	_, release, rpcErr := client.ResumeDisconnectedThread(
		context.Background(), "t", true, time.Now().Add(8*time.Second))
	if rpcErr != nil || release == nil {
		t.Fatalf("resume = %#v", rpcErr)
	}
	t.Cleanup(release.Cancel)
	turns := mustJSON(t, appserver.ThreadTurnsListParams{ThreadID: "t", ItemsView: appserver.TurnItemsNotLoaded})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadTurnsList, turns); rpcErr == nil ||
		rpcErr.Message != signInAgainReason {
		t.Fatalf("changed sign-in read = %#v, want %q", rpcErr, signInAgainReason)
	}
	items := mustJSON(t, appserver.ThreadItemsListParams{ThreadID: "t"})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadItemsList, items); rpcErr == nil ||
		rpcErr.Message != signInAgainReason {
		t.Fatalf("next read = %#v, want %q", rpcErr, signInAgainReason)
	}
	if itemReads.Load() != 0 {
		t.Fatalf("item reads = %d, want the changed sign-in rejected before the RPC", itemReads.Load())
	}
}

// Mirrors Rust #62140: a slow history read is bounded by the recovery deadline
// and withheld as a timeout.
func TestResumeDisconnectedThreadBoundsSlowHistoryLikeRust(t *testing.T) {
	initialEvents := make(chan cloudclient.Event)
	initialErrs := make(chan error)
	replacementEvents := make(chan cloudclient.Event, 2)
	replacementErrs := make(chan error)
	var attaches atomic.Int32
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			if attaches.Add(1) == 1 {
				return initialEvents, initialErrs, nil
			}
			return replacementEvents, replacementErrs, nil
		},
		listTurns: func(ctx context.Context, _ cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
			<-ctx.Done()
			return cloudclient.HistoryPage[cloudclient.HistoryTurn]{}, ctx.Err()
		},
	}
	client := connectHeldRecoveryTest(t, fake, func() bool { return true })
	loadDisconnectedThreadForTest(t, client, "t", func() {
		close(initialEvents)
		close(initialErrs)
	})
	drainCloudNotifications(t, client)

	deadline := time.Now().Add(120 * time.Millisecond)
	_, release, rpcErr := client.ResumeDisconnectedThread(context.Background(), "t", true, deadline)
	if rpcErr != nil || release == nil {
		t.Fatalf("resume = %#v", rpcErr)
	}
	turns := mustJSON(t, appserver.ThreadTurnsListParams{ThreadID: "t", ItemsView: appserver.TurnItemsNotLoaded})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadTurnsList, turns); rpcErr == nil ||
		rpcErr.Message != cloudRecoveryTimeoutReason {
		t.Fatalf("slow history = %#v, want %q", rpcErr, cloudRecoveryTimeoutReason)
	}
	if rpcErr := release.Install(context.Background()); rpcErr == nil ||
		rpcErr.Message != cloudRecoveryExpiredReason {
		t.Fatalf("install after deadline = %#v, want %q", rpcErr, cloudRecoveryExpiredReason)
	}
}

// Mirrors Rust #62140: a replacement that ended before installation is never
// committed and its buffered events are not delivered.
func TestResumeDisconnectedThreadDropsStreamLostBeforeInstallLikeRust(t *testing.T) {
	initialEvents := make(chan cloudclient.Event)
	initialErrs := make(chan error)
	replacementEvents := make(chan cloudclient.Event, 2)
	replacementErrs := make(chan error)
	var attaches atomic.Int32
	fake := &fakeCloud{
		activate: preparedActivation,
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			if attaches.Add(1) == 1 {
				return initialEvents, initialErrs, nil
			}
			return replacementEvents, replacementErrs, nil
		},
	}
	client := connectHeldRecoveryTest(t, fake, func() bool { return true })
	loadDisconnectedThreadForTest(t, client, "t", func() {
		close(initialEvents)
		close(initialErrs)
	})
	drainCloudNotifications(t, client)

	_, release, rpcErr := client.ResumeDisconnectedThread(
		context.Background(), "t", false, time.Now().Add(8*time.Second))
	if rpcErr != nil || release == nil {
		t.Fatalf("resume = %#v", rpcErr)
	}
	close(replacementEvents)
	close(replacementErrs)
	if rpcErr := release.Install(context.Background()); rpcErr == nil ||
		rpcErr.Message != cloudRecoveryStreamLostReason {
		t.Fatalf("install lost stream = %#v, want %q", rpcErr, cloudRecoveryStreamLostReason)
	}
	expectNoCloudNotification(t, client, "a lost replacement delivered a buffered event")
}
