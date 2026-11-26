package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type guardianBatchSinkRecorder struct {
	commits    int
	abandons   int
	abandonErr error
}

func (r *guardianBatchSinkRecorder) CommitRoutingBatch() { r.commits++ }

func (r *guardianBatchSinkRecorder) AbandonRoutingBatch(err error) {
	r.abandons++
	r.abandonErr = err
}

// Rust #61799 housekeeper.rs: the sampler that produced a batch learns whether
// it became durable. The root worker commits only after the board writes and
// the routing watermark advanced, so a sampler never retains unsent history.
func TestGuardianRootWorkerCommitsSampledBatchAfterWatermarkLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianHousekeeperFixture(t)
	if err := board.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "evidence", Description: "topic"}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := delivery.Inbox(reviewer).SetSubscription("evidence", true); err != nil {
		t.Fatalf("subscribe live reviewer: %v", err)
	}
	sink := &guardianBatchSinkRecorder{}
	decider := func(_ context.Context, batch GuardianRoutingBatch) (GuardianRoutingDecision, error) {
		routes := make([][]string, len(batch.Sources))
		for index := range routes {
			routes[index] = []string{"evidence"}
		}
		return GuardianRoutingDecision{Routes: routes}, nil
	}
	housekeeper, err := NewGuardianHousekeeperWithOptions(board, root, delivery, decider, GuardianHousekeeperOptions{BatchSink: sink})
	if err != nil {
		t.Fatalf("NewGuardianHousekeeperWithOptions() error = %v", err)
	}
	start := GuardianHistoryPosition{ThreadID: root}
	end := GuardianHistoryPosition{ThreadID: root, EndOrdinalExclusive: 2, EndByteOffset: 20}
	original := json.RawMessage(`{"type":"response_item","payload":{"type":"function_call_output","output":"payload"}}`)
	reader := func(_ context.Context, from GuardianHistoryPosition, _ *GuardianHistoryPosition) (*GuardianEvidenceSnapshot, error) {
		snapshot := &GuardianEvidenceSnapshot{Start: from, End: end}
		for ordinal := uint64(0); ordinal < 2; ordinal++ {
			snapshot.Sources = append(snapshot.Sources, GuardianCapturedEvidence{
				ID:       GuardianEvidenceID{Ordinal: ordinal},
				Original: original,
				End:      GuardianHistoryPosition{ThreadID: root, EndOrdinalExclusive: ordinal + 1, EndByteOffset: (ordinal + 1) * 10},
			})
		}
		return snapshot, nil
	}
	queue, err := NewGuardianCaptureQueue(root, start, reader)
	if err != nil {
		t.Fatalf("NewGuardianCaptureQueue() error = %v", err)
	}
	defer queue.Close()
	worker, err := NewGuardianRootWorker(queue, housekeeper)
	if err != nil {
		t.Fatalf("NewGuardianRootWorker() error = %v", err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(runCtx) }()
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("worker did not stop")
		}
	}()
	if _, err := queue.CatchUp(ctx); err != nil {
		t.Fatalf("CatchUp() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for sink.commits == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sink.commits != 1 {
		t.Fatalf("sampled batch commits = %d, want one after the watermark advanced", sink.commits)
	}
	if sink.abandons != 0 {
		t.Fatalf("successful routing abandoned %d batches", sink.abandons)
	}
}

// A routing failure must abandon the sampled batch, so its history never
// becomes reusable context for the next pass.
func TestGuardianRootWorkerAbandonsFailedBatchLikeRust(t *testing.T) {
	ctx, board, delivery, root, _ := guardianHousekeeperFixture(t)
	sink := &guardianBatchSinkRecorder{}
	failure := errors.New("routing answer rejected")
	decider := func(context.Context, GuardianRoutingBatch) (GuardianRoutingDecision, error) {
		return GuardianRoutingDecision{}, failure
	}
	housekeeper, err := NewGuardianHousekeeperWithOptions(board, root, delivery, decider, GuardianHousekeeperOptions{BatchSink: sink})
	if err != nil {
		t.Fatalf("NewGuardianHousekeeperWithOptions() error = %v", err)
	}
	sources := []GuardianRoutingSource{{ID: GuardianEvidenceID{Ordinal: 0}, Text: `{"payload":1}`}}
	if _, err := housekeeper.RouteNext(ctx, sources); err == nil {
		t.Fatal("failed routing reported no error")
	}
	if sink.abandons != 1 || !errors.Is(sink.abandonErr, failure) {
		t.Fatalf("abandons = %d err = %v, want the routing failure", sink.abandons, sink.abandonErr)
	}
	if sink.commits != 0 {
		t.Fatalf("failed routing committed %d batches", sink.commits)
	}
}
