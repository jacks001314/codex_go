package execserver

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Rust #49332 (ed9e5a26a8): a canceled RPC call must leave the pending request
// map immediately, without waiting for further traffic or a disconnect. Rust
// replaced its opportunistic pruning with a PendingRequestGuard that runs on
// Drop; Go removes the registration on every failure and cancellation path in
// Client.call (cancelCall on marshal error, write failure and ctx.Done), so the
// same guarantee holds without a separate guard type.
func TestCancelledCallClearsPendingImmediatelyLikeRust(t *testing.T) {
	conn := newHangingEnvironmentInfoConnection()
	client := newNetworkPolicyTestClient(conn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.call(ctx, MethodEnvironmentInfo, map[string]any{}, nil)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		client.mu.Lock()
		pending := len(client.pending)
		client.mu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("call never registered a pending request")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("call() error = %v, want context.Canceled", err)
	}

	client.mu.Lock()
	pending := len(client.pending)
	client.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending requests after cancellation = %d, want 0", pending)
	}
}
