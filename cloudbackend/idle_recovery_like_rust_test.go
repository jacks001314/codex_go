package cloudbackend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"google.golang.org/grpc/codes"
)

// newRecoveryTestClient builds a backend with the recovery plumbing a real
// Connect installs, so the idle state machine runs without the dispatch loop.
func newRecoveryTestClient(source cloudSource, options IdleRecoveryOptions) *Client {
	client := &Client{
		source:          source,
		status:          map[string]any{},
		outCh:           make(chan Notification, 16),
		closed:          make(chan struct{}),
		runCtx:          context.Background(),
		recoveryOptions: options,
		recoveryResults: make(chan idleRecoveryResult, 1),
	}
	client.outCond = sync.NewCond(&client.outMu)
	return client
}

func awaitRecoveryResult(t *testing.T, client *Client) idleRecoveryResult {
	t.Helper()
	select {
	case result := <-client.recoveryResults:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("idle recovery did not finish")
		return idleRecoveryResult{}
	}
}

func openStream() (chan cloudclient.Event, chan error) {
	return make(chan cloudclient.Event, 4), make(chan error, 1)
}

func serverRequestEvent() cloudclient.Event {
	return cloudclient.Event{Kind: cloudclient.EventServerRequest, Payload: []byte{0x08, 0x01}}
}

// Mirrors Rust #60969 idle::transient: only a clean end, a timeout, and the
// retryable gRPC codes are worth another Attach attempt.
func TestIdleRecoveryTransientClassificationLikeRust(t *testing.T) {
	retryable := []error{
		nil,
		cloudclient.ErrTimeout,
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.Canceled}},
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.Unknown}},
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.DeadlineExceeded}},
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.Internal}},
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.Unavailable}},
	}
	for _, err := range retryable {
		if !transientCloudError(err) {
			t.Errorf("transientCloudError(%v) = false, want true", err)
		}
	}
	permanent := []error{
		cloudclient.ErrInvalidResponse,
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.InvalidArgument}},
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.PermissionDenied}},
		&cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.Unauthenticated}},
		errors.New("boom"),
	}
	for _, err := range permanent {
		if transientCloudError(err) {
			t.Errorf("transientCloudError(%v) = true, want false", err)
		}
	}
}

// Mirrors Rust #60969: a displayed idle conversation whose stream ends is
// reopened quietly, the buffered first event is kept, and the queued loss is
// superseded instead of surfacing.
func TestIdleRecoveryReopensIdleDisplayedStreamLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	initialEvents, initialErrs := openStream()
	recoveredEvents, recoveredErrs := openStream()
	recoveredEvents <- serverRequestEvent()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		if attaches == 1 {
			return initialEvents, initialErrs, nil
		}
		return recoveredEvents, recoveredErrs, nil
	}
	reconnected := make(chan string, 1)
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{
		LoginCurrent:  func() bool { return true },
		OnReconnected: func(threadID string) { reconnected <- threadID },
	})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// A stream that has stayed open proves it is not actively failing.
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.setObservedIdle(true)
	close(initialEvents)
	close(initialErrs)
	client.forwardNextNonBlocking()
	held := client.testNotifications()
	if len(held) != 1 || held[0].Method != NotificationCloudStreamDisconnected {
		t.Fatalf("notifications before reconnect = %#v, want the input hold", held)
	}
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))

	if attaches != 2 {
		t.Fatalf("attach calls = %d, want one quiet reopen", attaches)
	}

	if client.active == nil || client.active.threadID != "thread" {
		t.Fatalf("active attachment = %#v, want the recovered stream", client.active)
	}
	select {
	case threadID := <-reconnected:
		if threadID != "thread" {
			t.Fatalf("reconnected thread = %q", threadID)
		}
	default:
		t.Fatal("reconnected event was not reported")
	}
	notifications := client.testNotifications()
	methods := map[appserver.NotificationMethod]bool{}
	for _, notification := range notifications {
		methods[notification.Method] = true
	}
	if len(notifications) != 2 || !methods[NotificationCloudStreamReconnected] || !methods[appserver.NotificationWarning] {
		t.Fatalf("notifications = %#v, want the release plus the buffered first event", notifications)
	}
	// The recovered stream keeps forwarding.
	recoveredEvents <- serverRequestEvent()
	client.forwardNextNonBlocking()
	if notifications := client.testNotifications(); len(notifications) != 1 {
		t.Fatalf("notifications after recovery = %#v", notifications)
	}
}

// Mirrors Rust #60969: a conversation that is not idle keeps the ordinary
// explicit-reopen behavior and reports the loss.
func TestIdleRecoveryLeavesBusyStreamExplicitLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		return events, errs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.setObservedIdle(false)
	close(events)
	close(errs)
	client.forwardNextNonBlocking()

	if attaches != 1 {
		t.Fatalf("attach calls = %d, want no quiet reopen while busy", attaches)
	}
	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != appserver.NotificationError {
		t.Fatalf("notifications = %#v, want the disconnect error", notifications)
	}
}

// Mirrors Rust #60969: a stream that died right after it was installed is not
// reopened quietly.
func TestIdleRecoverySkipsFreshStreamLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		return events, errs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.setObservedIdle(true)
	close(events)
	close(errs)
	client.forwardNextNonBlocking()

	if attaches != 1 {
		t.Fatalf("attach calls = %d, want no reopen for an unstable stream", attaches)
	}
	if notifications := client.testNotifications(); len(notifications) != 1 {
		t.Fatalf("notifications = %#v, want the disconnect error", notifications)
	}
}

// Mirrors Rust #60969: every attempt rechecks the stored credentials, and a
// changed login abandons the episode and reports the loss.
func TestIdleRecoveryAbortsWhenCredentialsChangeLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
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

	if attaches != 1 {
		t.Fatalf("attach calls = %d, want no Attach with stale credentials", attaches)
	}
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))
	notifications := client.testNotifications()
	if len(notifications) != 2 || notifications[0].Method != NotificationCloudStreamDisconnected ||
		notifications[1].Method != appserver.NotificationError {
		t.Fatalf("notifications = %#v, want the held input then the disconnect error", notifications)
	}
}

// Mirrors Rust #60969: a transient Attach failure uses the retry budget.
func TestIdleRecoveryRetriesTransientAttachLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	recoveredEvents, recoveredErrs := openStream()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		switch attaches {
		case 1:
			return events, errs, nil
		case 2:
			return nil, nil, &cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.Unavailable}}
		default:
			return recoveredEvents, recoveredErrs, nil
		}
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.setObservedIdle(true)
	close(events)
	close(errs)
	client.forwardNextNonBlocking()
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))

	if attaches != 3 {
		t.Fatalf("attach calls = %d, want one retry after the transient failure", attaches)
	}
	if client.active == nil || client.active.threadID != "thread" {
		t.Fatalf("active attachment = %#v, want the recovered stream", client.active)
	}
}

// Mirrors Rust #60969: a non-transient failure ends the episode, and the held
// back loss is reported.
func TestIdleRecoveryReportsLossOnPermanentFailureLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		if attaches > 1 {
			return nil, nil, &cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.PermissionDenied}}
		}
		return events, errs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.setObservedIdle(true)
	close(events)
	close(errs)
	client.forwardNextNonBlocking()
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))

	if attaches != 2 {
		t.Fatalf("attach calls = %d, want one non-retried attempt", attaches)
	}
	if client.recovery.state != idleReconnectInactive {
		t.Fatalf("recovery state = %v, want inactive", client.recovery.state)
	}
	notifications := client.testNotifications()
	if len(notifications) != 2 || notifications[0].Method != NotificationCloudStreamDisconnected ||
		notifications[1].Method != appserver.NotificationError {
		t.Fatalf("notifications = %#v, want the held input then the disconnect error", notifications)
	}
}

// Mirrors Rust #60969: a hidden view keeps the offer until the client accepts
// it, and the explicit acceptance opens the quiet Attach.
func TestIdleRecoveryAcceptsOfferedStreamExplicitlyLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	recoveredEvents, recoveredErrs := openStream()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		if attaches == 1 {
			return events, errs, nil
		}
		return recoveredEvents, recoveredErrs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{
		LoginCurrent: func() bool { return true },
		Displayed:    func(string) bool { return false },
	})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.setObservedIdle(true)
	close(events)
	close(errs)
	client.forwardNextNonBlocking()

	if attaches != 1 {
		t.Fatalf("attach calls = %d, want the offer withheld from a hidden view", attaches)
	}
	if notifications := client.testNotifications(); len(notifications) != 1 {
		t.Fatalf("notifications = %#v, want the loss while the offer is pending", notifications)
	}
	if !client.ReconnectIdleThread("thread") {
		t.Fatal("explicit acceptance was rejected for an offered conversation")
	}
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))
	if client.active == nil || client.active.threadID != "thread" {
		t.Fatalf("active attachment = %#v, want the accepted quiet reopen", client.active)
	}
}

// Mirrors Rust #60969: an explicit reopen is the escape hatch that releases
// input held while a quiet reopen kept failing.
func TestIdleRecoveryReleasesHeldInputOnExplicitReopenLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	initialEvents, initialErrs := openStream()
	reopenedEvents, reopenedErrs := openStream()
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		if attaches == 1 {
			return initialEvents, initialErrs, nil
		}
		return reopenedEvents, reopenedErrs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return false }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.setObservedIdle(true)
	close(initialEvents)
	close(initialErrs)
	client.forwardNextNonBlocking()
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))
	client.testNotifications()

	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("explicit reopen: %v", err)
	}
	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != NotificationCloudStreamReconnected {
		t.Fatalf("notifications = %#v, want the explicit-reopen release", notifications)
	}
}
