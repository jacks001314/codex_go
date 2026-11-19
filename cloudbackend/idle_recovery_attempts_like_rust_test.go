package cloudbackend

import (
	"context"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// endIdleStream closes one attachment the way a dropped reply stream does and
// lets the dispatch step observe it.
func endIdleStream(t *testing.T, events chan cloudclient.Event, errs chan error) {
	t.Helper()
	close(events)
	close(errs)
}

// Mirrors Rust #62151: replacements installed in one recovery sequence delay the
// next quiet Attach by 500 ms each.
func TestIdleRecoveryDelaysReplacementsLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		return events, errs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	client.recovery.attempts = 2
	client.recovery.state = idleReconnectOffered
	client.recovery.root = "thread"
	started := time.Now()
	if !client.startIdleRecovery("thread") {
		t.Fatal("startIdleRecovery rejected an offered reopen")
	}
	result := awaitRecoveryResult(t, client)
	if !result.connected {
		t.Fatalf("recovery result = %#v, want a connected stream", result)
	}
	result.cancel()
	// Two installed replacements back the third Attach off by one second.
	if elapsed := time.Since(started); elapsed < 2*idleRecoveryAttemptDelay {
		t.Fatalf("elapsed = %s, want at least %s", elapsed, 2*idleRecoveryAttemptDelay)
	}
}

// Mirrors Rust #62151: after three replacements a recovery request stops the
// loop and keeps the transcript and draft on screen.
func TestIdleRecoveryStopsAfterRepeatedDisconnectsLikeRust(t *testing.T) {
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
	for index := 0; index < idleRecoveryMaxReplacements; index++ {
		client.recovery.attempts = index
		client.recovery.state = idleReconnectOffered
		client.recovery.root = "thread"
		if !client.startIdleRecovery("thread") {
			t.Fatalf("startIdleRecovery rejected replacement %d", index)
		}
		result := awaitRecoveryResult(t, client)
		if !result.connected {
			t.Fatalf("replacement %d = %#v, want a connected stream", index, result)
		}
		client.handleIdleRecoveryResult(result)
	}
	if client.recovery.attempts != idleRecoveryMaxReplacements {
		t.Fatalf("attempts = %d, want %d", client.recovery.attempts, idleRecoveryMaxReplacements)
	}
	client.recovery.state = idleReconnectOffered
	client.recovery.root = "thread"
	if !client.startIdleRecovery("thread") {
		t.Fatal("startIdleRecovery rejected the exhausted recovery request")
	}
	client.handleIdleRecoveryResult(awaitRecoveryResult(t, client))

	if want := 1 + idleRecoveryMaxReplacements; attaches != want {
		t.Fatalf("attach calls = %d, want %d: no Attach once the budget is spent", attaches, want)
	}
	notifications := client.testNotifications()
	var disconnect appserver.ErrorNotification
	found := false
	for _, notification := range notifications {
		if notification.Method != appserver.NotificationError {
			continue
		}
		if params, ok := notification.Params.(appserver.ErrorNotification); ok {
			disconnect = params
			found = true
		}
	}
	if !found || disconnect.Error.Message != recoveryKeepsDisconnectingReason {
		t.Fatalf("notifications = %#v, want the repeated-disconnect message", notifications)
	}
}

// Mirrors Rust #62151: a stream that stays connected long enough clears the
// replacement budget, and so does resetting recovery state.
func TestIdleRecoveryClearsReplacementBudgetLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	events, errs := openStream()
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		return events, errs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.installed = time.Now().Add(-time.Minute)
	client.recovery.attempts = idleRecoveryMaxReplacements
	client.setObservedIdle(false)
	endIdleStream(t, events, errs)
	client.forwardNextNonBlocking()
	if client.recovery.attempts != 0 {
		t.Fatalf("attempts after a stable disconnect = %d, want 0", client.recovery.attempts)
	}

	client.recovery.attempts = idleRecoveryMaxReplacements
	client.resetIdleRecovery()
	if client.recovery.attempts != 0 {
		t.Fatalf("attempts after reset = %d, want 0", client.recovery.attempts)
	}
}
