package cloudbackend

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"google.golang.org/grpc/codes"
)

// Mirrors Rust #61599 blocked_recovery: missing conversations, lost access, and
// changed sign-in have dedicated disconnect messages.
func TestBlockedCloudRecoveryReasonsLikeRust(t *testing.T) {
	cases := []struct {
		code codes.Code
		want string
	}{
		{codes.NotFound, "Cloud could not find this conversation."},
		{codes.PermissionDenied, "Your account no longer has access to this cloud conversation."},
		{codes.Unauthenticated, signInAgainReason},
		{codes.InvalidArgument, ""},
		{codes.Unavailable, ""},
	}
	for _, tc := range cases {
		err := &cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: tc.code}}
		if got := blockedCloudRecoveryReason(err); got != tc.want {
			t.Errorf("blockedCloudRecoveryReason(%v) = %q, want %q", tc.code, got, tc.want)
		}
	}
	if got := recoveryFailureReason(&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.InvalidArgument}}); got != recoveryFailedReason {
		t.Errorf("generic recovery reason = %q, want %q", got, recoveryFailedReason)
	}
}

// Mirrors Rust #61599: a reply stream that ends with access denied reports that
// instead of the generic reopen hint.
func TestStreamEndedReportsAccessDeniedLikeRust(t *testing.T) {
	events, errs := openStream()
	fake := &fakeCloud{attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		return events, errs, nil
	}}
	client := newTestClient(fake)
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	active := client.active
	client.setObservedIdle(false)
	client.streamEnded(active, &cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.PermissionDenied}})

	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != appserver.NotificationError {
		t.Fatalf("notifications = %#v, want one disconnect error", notifications)
	}
	payload, ok := notifications[0].Params.(appserver.ErrorNotification)
	if !ok || payload.Error.Message != "Your account no longer has access to this cloud conversation." {
		t.Fatalf("disconnect payload = %#v", notifications[0].Params)
	}
}

// Mirrors Rust #61599 accepted idle reconnect: a login change after the Attach
// succeeded is reported as the sign-in message, not the generic loss.
func TestIdleRecoveryLoginChangeReportsSignInLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		return events, errs, nil
	}
	valid := true
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return valid }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.setObservedIdle(true)
	valid = false
	close(events)
	close(errs)
	client.forwardNextNonBlocking()
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))

	notifications := client.testNotifications()
	if len(notifications) != 2 || notifications[1].Method != appserver.NotificationError {
		t.Fatalf("notifications = %#v, want held input then disconnect error", notifications)
	}
	payload, ok := notifications[1].Params.(appserver.ErrorNotification)
	if !ok || payload.Error.Message != signInAgainReason {
		t.Fatalf("sign-in disconnect payload = %#v", notifications[1].Params)
	}
}

// Mirrors Rust #61599: dropping the caller cancels an in-flight turns/items
// read so it does not keep the native worker occupied.
func TestAbandonedCloudReadCancelsRequestContextLikeRust(t *testing.T) {
	for _, method := range []appserver.Method{appserver.MethodThreadTurnsList, appserver.MethodThreadItemsList} {
		t.Run(string(method), func(t *testing.T) {
			started := make(chan struct{}, 1)
			canceled := make(chan struct{}, 1)
			var once sync.Once
			blockingRead := func(ctx context.Context) error {
				once.Do(func() { started <- struct{}{} })
				select {
				case <-ctx.Done():
					canceled <- struct{}{}
					return ctx.Err()
				case <-time.After(5 * time.Second):
					return nil
				}
			}
			fake := &fakeCloud{}
			fake.listTurns = func(ctx context.Context, _ cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
				return cloudclient.HistoryPage[cloudclient.HistoryTurn]{}, blockingRead(ctx)
			}
			fake.listItems = func(ctx context.Context, _ cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
				return cloudclient.HistoryPage[cloudclient.HistoryItem]{}, blockingRead(ctx)
			}
			client := Connect(context.Background(), fake, nil)
			t.Cleanup(client.Close)

			ctx, cancel := context.WithCancel(context.Background())
			requestDone := make(chan struct{})
			go func() {
				defer close(requestDone)
				_, _ = client.Request(ctx, method, json.RawMessage(`{"threadId":"t","itemsView":"notLoaded"}`))
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("read never reached the cloud source")
			}
			cancel()
			select {
			case <-requestDone:
			case <-time.After(5 * time.Second):
				t.Fatal("abandoned read did not return")
			}
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				t.Fatal("abandoned read did not cancel the native request")
			}
		})
	}
}
