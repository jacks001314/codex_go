package cloudbackend

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// Mirrors Rust #61481: a queued thread/start whose caller already abandoned it
// is skipped, so it neither replaces the active attachment nor disturbs the
// worker's recovery for the next command.
func TestAbandonedQueuedCloudThreadStartIsSkippedLikeRust(t *testing.T) {
	prewarmed := make(chan struct{}, 1)
	source := &fakeCloud{prewarm: func(context.Context, string, string) (*cloudclient.ThreadActivation, error) {
		prewarmed <- struct{}{}
		return nil, context.Canceled
	}}
	client := ConnectWithOptions(context.Background(), source, json.RawMessage(`{}`), ConnectOptions{})
	defer client.Close()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	pending := &pendingRequest{
		method: appserver.MethodThreadStart,
		params: json.RawMessage(`{"cwd":"/tmp"}`),
		ctx:    canceledCtx,
		cancel: func() {},
		reply:  make(chan requestReply, 1),
	}
	client.requests <- pending

	select {
	case reply := <-pending.reply:
		if reply.err == nil {
			t.Fatal("abandoned thread/start reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not settle the abandoned thread/start")
	}
	select {
	case <-prewarmed:
		t.Fatal("abandoned thread/start still reached the cloud backend")
	default:
	}

	requestCtx, requestCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer requestCancel()
	if _, rpcErr := client.Request(requestCtx, appserver.MethodGetAccount, json.RawMessage(`{}`)); rpcErr != nil {
		t.Fatalf("worker stayed blocked after the abandoned start: %v", rpcErr)
	}
}
