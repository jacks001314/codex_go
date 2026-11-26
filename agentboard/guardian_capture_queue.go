package agentboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// GuardianEvidenceReader reads a root's stored original evidence from start.
// through nil means "flush pending history and read everything after start"
// (Rust ThreadStore::flush_thread plus OriginalHistoryRequest::After); a
// non-nil through reads exactly that durable range and must report it as the
// capture's end (OriginalHistoryRequest::Range). The reader reports stored
// evidence the intake can never accept as *GuardianEvidenceRejection.
type GuardianEvidenceReader func(ctx context.Context, start GuardianHistoryPosition, through *GuardianHistoryPosition) (*GuardianEvidenceSnapshot, error)

// GuardianEvidenceRejection marks stored history the intake refuses for the
// life of the root (Rust RootEvidence::rejection). Every later capture and
// catch-up fails with the same reason instead of retrying.
type GuardianEvidenceRejection struct {
	Reason string
}

func (e *GuardianEvidenceRejection) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "Guardian intake rejected stored evidence"
	}
	return e.Reason
}

// GuardianEvidenceCapture is one read snapshot waiting for the housekeeper. The
// routing watermark advances as the capture is published, and finishing it
// covers the whole capture, including records evidence selection excluded.
type GuardianEvidenceCapture struct {
	queue    *GuardianCaptureQueue
	snapshot *GuardianEvidenceSnapshot
}

// Snapshot is the immutable capture this routing work was read from.
func (c *GuardianEvidenceCapture) Snapshot() *GuardianEvidenceSnapshot {
	if c == nil {
		return nil
	}
	return c.snapshot
}

// Sources renders the selected originals as routing input in source order.
func (c *GuardianEvidenceCapture) Sources() ([]GuardianRoutingSource, error) {
	if c == nil || c.snapshot == nil {
		return nil, errors.New("Guardian evidence capture is unavailable")
	}
	texts, err := c.snapshot.EncodedSources()
	if err != nil {
		return nil, err
	}
	sources := make([]GuardianRoutingSource, 0, len(texts))
	for index, text := range texts {
		sources = append(sources, GuardianRoutingSource{ID: c.snapshot.Sources[index].ID, Text: text})
	}
	return sources, nil
}

// RoutedThrough advances the root's routing watermark after the sole writer
// published every delivery through position (Rust Capture::routed_through).
func (c *GuardianEvidenceCapture) RoutedThrough(position GuardianHistoryPosition) error {
	if c == nil || c.queue == nil {
		return errors.New("Guardian evidence capture is unavailable")
	}
	return c.queue.routed.RoutedThrough(position)
}

// Finish completes the capture at its exclusive durable boundary, so a capture
// that selected nothing still acknowledges the history it read (Rust
// Capture::finish).
func (c *GuardianEvidenceCapture) Finish() error {
	if c == nil || c.snapshot == nil {
		return errors.New("Guardian evidence capture is unavailable")
	}
	return c.RoutedThrough(c.snapshot.Cutoff())
}

// GuardianCaptureQueue serializes the root's evidence reads and hands each
// capture to the single routing worker. It mirrors Rust guardian-v3
// RootEvidence: one queued capture at a time, coalesced activity wakes, the
// intake cursor advances only once a worker can take the capture, and a
// rejected history stops the intake for good.
type GuardianCaptureQueue struct {
	root   string
	reader GuardianEvidenceReader
	routed *GuardianRoutedEvidence

	// slot is Rust's mpsc reserve: it holds one token while no capture is
	// queued, so an enqueue cannot acknowledge history nobody will route.
	slot    chan struct{}
	pending chan *GuardianEvidenceCapture
	wake    chan struct{}
	stop    chan struct{}

	mu       sync.Mutex
	cursor   *GuardianEvidenceCursor
	rejected string
	lastErr  error

	closeOnce sync.Once
}

// NewGuardianCaptureQueue opens the root's intake at start. reader is required;
// a host supplies it from its paginated thread store.
func NewGuardianCaptureQueue(root string, start GuardianHistoryPosition, reader GuardianEvidenceReader) (*GuardianCaptureQueue, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("Guardian capture queue requires a root thread")
	}
	if reader == nil {
		return nil, errors.New("Guardian capture queue requires an evidence reader")
	}
	start.ThreadID = root
	routed, err := NewGuardianRoutedEvidence(start)
	if err != nil {
		return nil, err
	}
	queue := &GuardianCaptureQueue{
		root:    root,
		reader:  reader,
		routed:  routed,
		slot:    make(chan struct{}, 1),
		pending: make(chan *GuardianEvidenceCapture, 1),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		cursor:  NewGuardianEvidenceCursor(root),
	}
	queue.cursor.position = start
	queue.slot <- struct{}{}
	return queue, nil
}

// Root is the thread whose history this intake reads.
func (q *GuardianCaptureQueue) Root() string {
	if q == nil {
		return ""
	}
	return q.root
}

// Routed is the watermark a catch-up request waits on.
func (q *GuardianCaptureQueue) Routed() *GuardianRoutedEvidence {
	if q == nil {
		return nil
	}
	return q.routed
}

// Position is the acknowledged intake position: history the housekeeper is
// already committed to reading.
func (q *GuardianCaptureQueue) Position() GuardianHistoryPosition {
	if q == nil || q.cursor == nil {
		return GuardianHistoryPosition{}
	}
	return q.cursor.Position()
}

// LastError is the most recent transient capture failure, for diagnostics.
func (q *GuardianCaptureQueue) LastError() error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.lastErr
}

// Wake requests one capture, coalescing with a wake that is still pending.
func (q *GuardianCaptureQueue) Wake() {
	if q == nil {
		return
	}
	select {
	case q.wake <- struct{}{}:
	case <-q.stop:
	default:
	}
}

// Close ends the intake: pending and later enqueues fail, waiters on the routing
// watermark are released, and the capture loop returns.
func (q *GuardianCaptureQueue) Close() {
	if q == nil {
		return
	}
	q.closeOnce.Do(func() {
		close(q.stop)
		q.routed.Stop(nil)
	})
}

// Enqueue reads the history after the acknowledged cursor and queues one
// capture for routing. A cutoff limits the read to that durable range and is
// returned unchanged when the range was already routed; the returned position
// is the capture's exclusive end. The cursor only advances once a worker can
// take the capture, so a cancelled enqueue leaves the intake unchanged (Rust
// RootEvidence::enqueue).
func (q *GuardianCaptureQueue) Enqueue(ctx context.Context, cutoff *GuardianHistoryPosition) (GuardianHistoryPosition, error) {
	if q == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian capture queue is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var through GuardianHistoryPosition
	if cutoff != nil {
		through = *cutoff
		through.ThreadID = strings.TrimSpace(through.ThreadID)
		if through.ThreadID != q.root {
			return GuardianHistoryPosition{}, errors.New("Guardian cutoff belongs to another root")
		}
		// The writer may already have routed this cutoff, so no capture is owed.
		if covered, err := q.routed.Position().Covers(through); err != nil {
			return GuardianHistoryPosition{}, err
		} else if covered {
			return through, nil
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if strings.TrimSpace(q.rejected) != "" {
		return GuardianHistoryPosition{}, &GuardianEvidenceRejection{Reason: q.rejected}
	}
	select {
	case <-q.stop:
		return GuardianHistoryPosition{}, errors.New("Guardian housekeeper stopped")
	default:
	}
	position := q.cursor.Position()
	readThrough := (*GuardianHistoryPosition)(nil)
	if cutoff != nil {
		if covered, err := position.Covers(through); err != nil {
			return GuardianHistoryPosition{}, err
		} else if covered {
			return through, nil
		}
		readThrough = &through
	}
	snapshot, err := q.reader(ctx, position, readThrough)
	if err != nil {
		q.lastErr = err
		if rejection, ok := err.(*GuardianEvidenceRejection); ok {
			q.rejected = rejection.Error()
		}
		return GuardianHistoryPosition{}, err
	}
	if err := q.validateSnapshot(snapshot, position, readThrough); err != nil {
		q.lastErr = err
		q.rejected = err.Error()
		return GuardianHistoryPosition{}, &GuardianEvidenceRejection{Reason: err.Error()}
	}
	end := snapshot.Cutoff()
	if !snapshot.SourceAdvanced() {
		return end, nil
	}
	// Reserve the single capture slot before acknowledging, so cancellation
	// while routing is busy leaves the cursor unchanged.
	select {
	case <-q.slot:
	case <-ctx.Done():
		return GuardianHistoryPosition{}, ctx.Err()
	case <-q.stop:
		return GuardianHistoryPosition{}, errors.New("Guardian housekeeper stopped")
	}
	pending, err := q.cursor.Pending(snapshot)
	if err != nil {
		q.releaseSlot()
		return GuardianHistoryPosition{}, err
	}
	if err := q.cursor.Acknowledge(snapshot, len(pending)); err != nil {
		q.releaseSlot()
		return GuardianHistoryPosition{}, err
	}
	capture := &GuardianEvidenceCapture{queue: q, snapshot: snapshot}
	select {
	case q.pending <- capture:
	case <-q.stop:
		q.releaseSlot()
		return GuardianHistoryPosition{}, errors.New("Guardian housekeeper stopped")
	}
	return end, nil
}

// CatchUp submits a capture through the current durable tail and returns its
// acknowledgement once the housekeeper routed through it (Rust
// RootEvidence::request_catch_up). The acknowledgement is a lower bound: later
// board writes stay visible.
func (q *GuardianCaptureQueue) CatchUp(ctx context.Context) (GuardianHistoryPosition, error) {
	return q.RequestThrough(ctx, nil)
}

// RequestThrough submits routing work through cutoff - or through the durable
// tail when cutoff is nil - and waits for the routing watermark to cover it
// (Rust RootEvidence::request_through).
func (q *GuardianCaptureQueue) RequestThrough(ctx context.Context, cutoff *GuardianHistoryPosition) (GuardianHistoryPosition, error) {
	if q == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian capture queue is unavailable")
	}
	target, err := q.Enqueue(ctx, cutoff)
	if err != nil {
		return GuardianHistoryPosition{}, err
	}
	return q.routed.WaitUntilRouted(ctx, target)
}

// Next returns the capture waiting for the routing worker, or fails when ctx
// ends or the intake closes. Taking the capture frees the slot for the next
// enqueue.
func (q *GuardianCaptureQueue) Next(ctx context.Context) (*GuardianEvidenceCapture, error) {
	if q == nil {
		return nil, errors.New("Guardian capture queue is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case capture := <-q.pending:
		q.releaseSlot()
		return capture, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-q.stop:
		return nil, errors.New("Guardian housekeeper stopped")
	}
}

// CaptureOnActivity reads a capture for every coalesced wake and queues it for
// routing. A transient read failure is retained in LastError and the loop waits
// for the next wake; a rejected history stops the loop (Rust
// RootEvidence::capture_on_activity).
func (q *GuardianCaptureQueue) CaptureOnActivity(ctx context.Context) error {
	if q == nil {
		return errors.New("Guardian capture queue is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-q.stop:
			return nil
		case <-q.wake:
		}
		if _, err := q.Enqueue(ctx, nil); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			q.mu.Lock()
			q.lastErr = err
			rejected := strings.TrimSpace(q.rejected) != ""
			q.mu.Unlock()
			if rejected {
				return err
			}
		}
	}
}

func (q *GuardianCaptureQueue) releaseSlot() {
	select {
	case q.slot <- struct{}{}:
	default:
	}
}

func (q *GuardianCaptureQueue) validateSnapshot(snapshot *GuardianEvidenceSnapshot, position GuardianHistoryPosition, through *GuardianHistoryPosition) error {
	if snapshot == nil {
		return errors.New("Guardian evidence read returned no capture")
	}
	if snapshot.Start != position {
		return errors.New("Guardian evidence read did not begin at the intake cursor")
	}
	if strings.TrimSpace(snapshot.End.ThreadID) != q.root {
		return errors.New("Guardian evidence read did not end at the root")
	}
	if through != nil && snapshot.End != *through {
		return errors.New("Guardian evidence range read returned a different boundary")
	}
	return nil
}

// GuardianRootWorker routes each capture this root's intake produces: it runs
// the housekeeper over the selected originals in source order, advances the
// routing watermark after each published batch, and finishes the capture at its
// exclusive boundary (Rust host.rs RootWorker).
type GuardianRootWorker struct {
	queue       *GuardianCaptureQueue
	housekeeper *GuardianHousekeeper
}

// NewGuardianRootWorker binds one intake to the housekeeper that routes it.
func NewGuardianRootWorker(queue *GuardianCaptureQueue, housekeeper *GuardianHousekeeper) (*GuardianRootWorker, error) {
	if queue == nil {
		return nil, errors.New("Guardian root worker requires an intake")
	}
	if housekeeper == nil {
		return nil, errors.New("Guardian root worker requires a housekeeper")
	}
	if housekeeper.Owner() != queue.Root() {
		return nil, errors.New("Guardian root worker root mismatch")
	}
	return &GuardianRootWorker{queue: queue, housekeeper: housekeeper}, nil
}

// RouteCapture publishes every selected original of one capture. The watermark
// trails the routing work, so a catch-up resolves only after the batch reached
// the board.
func (w *GuardianRootWorker) RouteCapture(ctx context.Context, capture *GuardianEvidenceCapture) error {
	if w == nil || w.housekeeper == nil {
		return errors.New("Guardian root worker is unavailable")
	}
	if capture == nil || capture.snapshot == nil {
		return errors.New("Guardian evidence capture is unavailable")
	}
	sources, err := capture.Sources()
	if err != nil {
		return err
	}
	for index := 0; index < len(sources); {
		// A failed batch abandons its sampler history inside RouteNext.
		routed, err := w.housekeeper.RouteNext(ctx, sources[index:])
		if err != nil {
			return err
		}
		if routed <= 0 || routed > len(sources)-index {
			err := fmt.Errorf("housekeeper routed %d of %d evidence records", routed, len(sources)-index)
			w.housekeeper.AbandonBatch(err)
			return err
		}
		index += routed
		if err := capture.RoutedThrough(capture.snapshot.Sources[index-1].End); err != nil {
			w.housekeeper.AbandonBatch(err)
			return err
		}
		// Board writes and the routing watermark are both durable now, so the
		// sampler may retain this batch's history for the next request.
		w.housekeeper.CommitBatch()
	}
	return capture.Finish()
}

// Run routes captures until ctx ends, the intake closes, or routing fails. A
// routing failure stops the worker because a batch can be interrupted after its
// first board write.
func (w *GuardianRootWorker) Run(ctx context.Context) error {
	if w == nil || w.queue == nil {
		return errors.New("Guardian root worker is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		capture, err := w.queue.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := w.RouteCapture(ctx, capture); err != nil {
			return err
		}
	}
}
