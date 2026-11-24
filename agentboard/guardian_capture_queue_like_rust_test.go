package agentboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func guardianCaptureSnapshot(start, end GuardianHistoryPosition, ordinals ...uint64) *GuardianEvidenceSnapshot {
	snapshot := &GuardianEvidenceSnapshot{Start: start, End: end}
	for index, ordinal := range ordinals {
		snapshot.Sources = append(snapshot.Sources, GuardianCapturedEvidence{
			ID:       GuardianEvidenceID{Ordinal: ordinal},
			Original: json.RawMessage(`{"type":"response_item","payload":{"type":"function_call_output","output":"evidence"}}`),
			End: GuardianHistoryPosition{
				ThreadID:            start.ThreadID,
				EndOrdinalExclusive: ordinal + 1,
				EndByteOffset:       uint64(index+1) * 10,
			},
		})
	}
	return snapshot
}

func guardianCaptureReader(snapshots ...*GuardianEvidenceSnapshot) (GuardianEvidenceReader, *[]GuardianHistoryPosition) {
	var reads []GuardianHistoryPosition
	return func(_ context.Context, start GuardianHistoryPosition, through *GuardianHistoryPosition) (*GuardianEvidenceSnapshot, error) {
		reads = append(reads, start)
		snapshot := snapshots[0]
		snapshots = snapshots[1:]
		if through != nil {
			snapshot.End = *through
		}
		return snapshot, nil
	}, &reads
}

// Rust #62135 RootEvidence::enqueue: one capture may wait for the routing
// worker, and a second read is acknowledged only once the worker takes the
// first, so the intake cannot run ahead of the housekeeper.
func TestGuardianCaptureQueueAcknowledgesOnlyWhenAWorkerTakesTheCaptureLikeRust(t *testing.T) {
	start := GuardianHistoryPosition{ThreadID: "root"}
	first := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2, EndByteOffset: 20}
	second := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 30}
	reads := make(chan GuardianHistoryPosition, 4)
	reader := func(_ context.Context, from GuardianHistoryPosition, _ *GuardianHistoryPosition) (*GuardianEvidenceSnapshot, error) {
		reads <- from
		if from == start {
			return guardianCaptureSnapshot(start, first, 0, 1), nil
		}
		return guardianCaptureSnapshot(first, second, 2), nil
	}
	queue, err := NewGuardianCaptureQueue("root", start, reader)
	if err != nil {
		t.Fatalf("NewGuardianCaptureQueue() error = %v", err)
	}
	defer queue.Close()
	ctx := context.Background()

	if position, err := queue.Enqueue(ctx, nil); err != nil || position != first {
		t.Fatalf("Enqueue(first) = %#v, %v; want %#v", position, err, first)
	}
	if got := queue.Position(); got != first {
		t.Fatalf("cursor after first capture = %#v, want %#v", got, first)
	}
	if from := <-reads; from != start {
		t.Fatalf("first read started at %#v, want %#v", from, start)
	}

	enqueued := make(chan GuardianHistoryPosition, 1)
	enqueueErr := make(chan error, 1)
	go func() {
		position, err := queue.Enqueue(ctx, nil)
		enqueued <- position
		enqueueErr <- err
	}()

	// The second read ran, but its acknowledgement waits for the worker.
	select {
	case from := <-reads:
		if from != first {
			t.Fatalf("second read started at %#v, want %#v", from, first)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("second read did not run")
	}
	select {
	case err := <-enqueueErr:
		t.Fatalf("second Enqueue() returned before a worker took the first capture: %v", err)
	case position := <-enqueued:
		t.Fatalf("second Enqueue() returned %#v before a worker took the first capture", position)
	case <-time.After(50 * time.Millisecond):
	}
	if got := queue.Position(); got != first {
		t.Fatalf("cursor while the second capture waits = %#v, want %#v", got, first)
	}

	capture, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if len(capture.Snapshot().Sources) != 2 {
		t.Fatalf("capture sources = %d, want 2", len(capture.Snapshot().Sources))
	}
	select {
	case err := <-enqueueErr:
		if err != nil {
			t.Fatalf("second Enqueue() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("second Enqueue() stayed blocked after a worker took the first capture")
	}
	if got := <-enqueued; got != second {
		t.Fatalf("second Enqueue() position = %#v, want %#v", got, second)
	}
	if got := queue.Position(); got != second {
		t.Fatalf("cursor after the worker took the first capture = %#v, want %#v", got, second)
	}
	next, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next(second) error = %v", err)
	}
	if len(next.Snapshot().Sources) != 1 || next.Snapshot().Sources[0].ID.Ordinal != 2 {
		t.Fatalf("second capture = %#v", next.Snapshot().Sources)
	}
}

// A cutoff the writer already routed needs no read, and a cutoff inside the
// acknowledged range is reported as covered.
func TestGuardianCaptureQueueSkipsCoveredCutoffsLikeRust(t *testing.T) {
	start := GuardianHistoryPosition{ThreadID: "root"}
	end := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2, EndByteOffset: 20}
	reader, reads := guardianCaptureReader(guardianCaptureSnapshot(start, end, 0, 1))
	queue, err := NewGuardianCaptureQueue("root", start, reader)
	if err != nil {
		t.Fatalf("NewGuardianCaptureQueue() error = %v", err)
	}
	defer queue.Close()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, nil); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	capture, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if err := capture.Finish(); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	position, err := queue.Enqueue(ctx, &end)
	if err != nil {
		t.Fatalf("Enqueue(covered) error = %v", err)
	}
	if position != end {
		t.Fatalf("Enqueue(covered) position = %#v, want %#v", position, end)
	}
	if len(*reads) != 1 {
		t.Fatalf("reads = %v, want only the first capture", *reads)
	}
	other := GuardianHistoryPosition{ThreadID: "other", EndOrdinalExclusive: 1, EndByteOffset: 10}
	if _, err := queue.Enqueue(ctx, &other); err == nil || !strings.Contains(err.Error(), "another root") {
		t.Fatalf("Enqueue(foreign cutoff) error = %v", err)
	}
}

// A catch-up acknowledgement resolves only after the capture reached its
// exclusive boundary, even when the capture selected no evidence.
func TestGuardianCaptureQueueAcknowledgesCatchUpAfterRoutingLikeRust(t *testing.T) {
	start := GuardianHistoryPosition{ThreadID: "root"}
	empty := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 1, EndByteOffset: 5}
	end := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2, EndByteOffset: 20}
	reader, _ := guardianCaptureReader(guardianCaptureSnapshot(start, empty), guardianCaptureSnapshot(empty, end, 1))
	queue, err := NewGuardianCaptureQueue("root", start, reader)
	if err != nil {
		t.Fatalf("NewGuardianCaptureQueue() error = %v", err)
	}
	defer queue.Close()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, nil); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	first, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if len(first.Snapshot().Sources) != 0 {
		t.Fatalf("first capture sources = %d, want none", len(first.Snapshot().Sources))
	}

	acked := make(chan GuardianHistoryPosition, 1)
	go func() {
		position, _ := queue.Routed().WaitUntilRouted(ctx, empty)
		acked <- position
	}()
	select {
	case position := <-acked:
		t.Fatalf("catch-up resolved at %#v before routing finished", position)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Finish(); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	select {
	case position := <-acked:
		if position != empty {
			t.Fatalf("catch-up ack = %#v, want %#v", position, empty)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("catch-up did not resolve after the capture finished")
	}
	if _, err := queue.Enqueue(ctx, nil); err != nil {
		t.Fatalf("Enqueue() after catch-up error = %v", err)
	}
	second, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if len(second.Snapshot().Sources) != 1 || second.Snapshot().Sources[0].ID.Ordinal != 1 {
		t.Fatalf("second capture = %#v, want the record after the catch-up", second.Snapshot().Sources)
	}
}

// Stored evidence the intake can never accept latches, so later captures and
// catch-ups fail with the same reason instead of retrying (Rust
// RootEvidence::ensure_active).
func TestGuardianCaptureQueueLatchesRejectedEvidenceLikeRust(t *testing.T) {
	start := GuardianHistoryPosition{ThreadID: "root"}
	reader := func(context.Context, GuardianHistoryPosition, *GuardianHistoryPosition) (*GuardianEvidenceSnapshot, error) {
		return nil, &GuardianEvidenceRejection{Reason: "evidence history was replaced"}
	}
	queue, err := NewGuardianCaptureQueue("root", start, reader)
	if err != nil {
		t.Fatalf("NewGuardianCaptureQueue() error = %v", err)
	}
	defer queue.Close()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, nil); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("Enqueue() error = %v", err)
	}
	through := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 1, EndByteOffset: 10}
	if _, err := queue.Enqueue(ctx, &through); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("Enqueue(cutoff) after rejection error = %v", err)
	}
}

// Rust #62135 host.rs RootWorker: each capture is routed through the board in
// source order, the watermark follows the published batches, and the capture is
// finished at its exclusive boundary.
func TestGuardianRootWorkerRoutesCapturesLikeRust(t *testing.T) {
	ctx, board, delivery, root, reviewer := guardianHousekeeperFixture(t)
	if err := board.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: "evidence", Description: "topic"}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := delivery.Inbox(reviewer).SetSubscription("evidence", true); err != nil {
		t.Fatalf("subscribe live reviewer: %v", err)
	}
	decider := func(_ context.Context, batch GuardianRoutingBatch) (GuardianRoutingDecision, error) {
		routes := make([][]string, len(batch.Sources))
		for index := range routes {
			routes[index] = []string{"evidence"}
		}
		return GuardianRoutingDecision{Routes: routes}, nil
	}
	housekeeper, err := NewGuardianHousekeeper(board, root, delivery, decider)
	if err != nil {
		t.Fatalf("NewGuardianHousekeeper() error = %v", err)
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
		case err := <-done:
			if err != nil {
				t.Fatalf("worker stopped with %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("worker did not stop")
		}
	}()

	if position, err := queue.CatchUp(ctx); err != nil || position != end {
		t.Fatalf("CatchUp() = %#v, %v; want %#v", position, err, end)
	}
	queued := delivery.Inbox(reviewer).QueuedPosts()
	if len(queued) != 2 || queued[0].Ordinal != 0 || queued[1].Ordinal != 1 {
		t.Fatalf("queued evidence = %#v, want both routed originals", queued)
	}
	posts, err := board.SearchPosts(ctx, root, PostQuery{Query: guardianHousekeeperText("payload"), Page: PageRequest{Limit: 10}})
	if err != nil {
		t.Fatalf("SearchPosts() error = %v", err)
	}
	if len(posts.Results) != 2 {
		t.Fatalf("published posts = %d, want 2", len(posts.Results))
	}
}
