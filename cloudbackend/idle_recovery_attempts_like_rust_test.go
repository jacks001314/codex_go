package cloudbackend

import (
	"context"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"google.golang.org/grpc/codes"
)

// endIdleStream closes one attachment the way a dropped reply stream does and
// lets the dispatch step observe it.
func endIdleStream(t *testing.T, events chan cloudclient.Event, errs chan error) {
	t.Helper()
	close(events)
	close(errs)
}

// Mirrors Rust #63058 recovery_backoff: the first replacement is immediate,
// then the delay doubles from 500 ms and caps at 30 s.
func TestIdleRecoveryBackoffDoublesAndCapsLikeRust(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 0},
		{1, 500 * time.Millisecond},
		{2, time.Second},
		{3, 2 * time.Second},
		{4, 4 * time.Second},
		{5, 8 * time.Second},
		{6, 16 * time.Second},
		{7, 30 * time.Second},
		{12, 30 * time.Second},
	}
	for _, tc := range cases {
		if got := idleRecoveryBackoff(tc.attempts); got != tc.want {
			t.Fatalf("idleRecoveryBackoff(%d) = %s, want %s", tc.attempts, got, tc.want)
		}
	}
}

// Mirrors Rust #63058: replacements back off exponentially before the attach
// budget starts.
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
	// Two attempts behind us back the next Attach off by one second.
	if elapsed := time.Since(started); elapsed < idleRecoveryBackoff(2) {
		t.Fatalf("elapsed = %s, want at least %s", elapsed, idleRecoveryBackoff(2))
	}
}

// Mirrors Rust #63058: recovery stays retryable after many replacements; there
// is no fixed replacement budget that reports the conversation as stuck.
func TestIdleRecoveryStaysRetryableAfterRepeatedReplacementsLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		events, errs := openStream()
		return events, errs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	for attempt := 0; attempt < 4; attempt++ {
		client.recovery.state = idleReconnectOffered
		client.recovery.root = "thread"
		if !client.startIdleRecovery("thread") {
			t.Fatalf("startIdleRecovery rejected replacement %d", attempt)
		}
		result := awaitRecoveryResult(t, client)
		if !result.connected {
			t.Fatalf("replacement %d = %#v, want a connected stream", attempt, result)
		}
		client.handleIdleRecoveryResult(result)
	}
	if client.recovery.attempts != 4 {
		t.Fatalf("attempts = %d, want 4", client.recovery.attempts)
	}
	// A replacement past the old three-replacement budget still attaches.
	client.recovery.state = idleReconnectOffered
	client.recovery.root = "thread"
	if !client.startIdleRecovery("thread") {
		t.Fatal("startIdleRecovery rejected a replacement past the old budget")
	}
	result := awaitRecoveryResult(t, client)
	if !result.connected {
		t.Fatalf("fifth replacement = %#v, want a connected stream", result)
	}
	result.cancel()
	if attaches != 6 {
		t.Fatalf("attach calls = %d, want 6: every replacement retries", attaches)
	}
	for _, notification := range client.testNotifications() {
		if notification.Method != appserver.NotificationError {
			continue
		}
		if params, ok := notification.Params.(appserver.ErrorNotification); ok && params.Error.Message == "The cloud reply stream keeps disconnecting. Your transcript and draft are still here." {
			t.Fatalf("recovery reported the retired fixed-budget message: %#v", params)
		}
	}
}

// Mirrors Rust #63058: an episode whose attempts all failed transiently stays
// retryable, so a later quiet reopen still attaches instead of being disabled.
func TestIdleRecoveryTransientFailureStaysRetryableLikeRust(t *testing.T) {
	fake := &fakeCloud{}
	attaches := 0
	fake.attach = func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
		attaches++
		if attaches > 1 && attaches <= 1+len(idleRecoveryDelays) {
			return nil, nil, &cloudclient.RpcError{Status: cloudclient.RpcStatus{Code: codes.Unavailable}}
		}
		events, errs := openStream()
		return events, errs, nil
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return true }})
	if err := client.replaceAttachment(context.Background(), "thread"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.recovery.state = idleReconnectOffered
	client.recovery.root = "thread"
	if !client.startIdleRecovery("thread") {
		t.Fatal("startIdleRecovery rejected an offered reopen")
	}
	result := awaitRecoveryResult(t, client)
	if result.connected || !result.transient {
		t.Fatalf("transient episode = %#v, want a retryable failure", result)
	}
	client.handleIdleRecoveryResult(result)
	if client.recovery.state != idleReconnectInactive {
		t.Fatalf("recovery state = %v, want inactive after the loss is reported", client.recovery.state)
	}
	// The loss message says the conversation can be reopened again.
	found := false
	for _, notification := range client.testNotifications() {
		if notification.Method != appserver.NotificationError {
			continue
		}
		if params, ok := notification.Params.(appserver.ErrorNotification); ok && params.Error.Message == recoveryTransientReason {
			found = true
		}
	}
	if !found {
		t.Fatalf("notifications = %#v, want the retryable loss message", client.testNotifications())
	}
	// A later quiet reopen is accepted and attaches again.
	client.recovery.attempts = 1
	if !client.offerIdleReconnect("thread") {
		t.Fatal("offerIdleReconnect was not accepted after a transient episode")
	}
	retry := awaitRecoveryResult(t, client)
	if !retry.connected {
		t.Fatalf("retry = %#v, want a connected stream", retry)
	}
	retry.cancel()
}

// Mirrors Rust #63058: a stream that stays connected long enough clears the
// backoff, and so does resetting recovery state.
func TestIdleRecoveryClearsBackoffLikeRust(t *testing.T) {
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
	client.recovery.attempts = 6
	client.setObservedIdle(false)
	endIdleStream(t, events, errs)
	client.forwardNextNonBlocking()
	if client.recovery.attempts != 0 {
		t.Fatalf("attempts after a stable disconnect = %d, want 0", client.recovery.attempts)
	}

	client.recovery.attempts = 6
	client.resetIdleRecovery()
	if client.recovery.attempts != 0 {
		t.Fatalf("attempts after reset = %d, want 0", client.recovery.attempts)
	}
}
