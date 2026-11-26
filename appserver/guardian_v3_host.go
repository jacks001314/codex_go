package appserver

// Rust parity: codex-rs/ext/guardian-v3/src/host.rs (#62135/#62136/#62309/#62372).
//
// One Guardian v3 root owns a durable evidence board, one intake, and a single
// routing worker: user-message and turn-end wakes coalesce, the worker routes
// durable originals from the tree's last published position, and stopping the
// root joins the worker before the history closes. The app-server host decides
// which threads become roots; this type only owns the per-root lifecycle.

import (
	"context"
	"errors"
	"strings"
	"sync"

	"codex_go/agentboard"
)

// GuardianV3RootOptions binds one root's board, intake and routing pass.
type GuardianV3RootOptions struct {
	// Root is the tree's root thread id.
	Root string
	// Board opens the root's durable evidence board for the routing pass. It is
	// called once, when the root becomes live.
	Board func(ctx context.Context) (agentboard.AgentMessageBoard, error)
	// Reader reads the root's stored original history from the intake cursor.
	Reader agentboard.GuardianEvidenceReader
	// Start is the intake position when the root becomes live: the beginning of
	// a new root's history, or the durable tail of a resumed one.
	Start agentboard.GuardianHistoryPosition
	// Housekeeper carries the routing instructions, batch budget and timeout.
	Housekeeper agentboard.GuardianHousekeeperOptions
	// Decider chooses destinations for one routing batch.
	Decider agentboard.GuardianRoutingDecider
}

// GuardianV3Host owns one routing worker per root. Wakes coalesce, a routing
// failure stops that root's worker, and Close joins every worker.
type GuardianV3Host struct {
	mu    sync.Mutex
	roots map[string]*guardianV3Root
}

// guardianV3Root is one live root: the intake the capture loop fills and the
// worker that routes it.
type guardianV3Root struct {
	root     string
	board    agentboard.AgentMessageBoard
	delivery *agentboard.GuardianLiveDelivery
	queue    *agentboard.GuardianCaptureQueue
	cancel   context.CancelFunc
	done     chan struct{}

	mu  sync.Mutex
	err error
}

// NewGuardianV3Host creates an empty host. No worker starts until a thread
// becomes a live root.
func NewGuardianV3Host() *GuardianV3Host {
	return &GuardianV3Host{roots: map[string]*guardianV3Root{}}
}

// EnsureRoot starts the root's routing worker once and returns the existing one
// afterwards (Rust Host::activate called from the thread-ready contributor).
func (h *GuardianV3Host) EnsureRoot(ctx context.Context, options GuardianV3RootOptions) (*guardianV3Root, error) {
	if h == nil {
		return nil, errors.New("Guardian host is unavailable")
	}
	root := strings.TrimSpace(options.Root)
	if root == "" {
		return nil, errors.New("Guardian root thread is required")
	}
	h.mu.Lock()
	if existing, ok := h.roots[root]; ok {
		h.mu.Unlock()
		return existing, nil
	}
	h.mu.Unlock()

	if options.Board == nil || options.Reader == nil || options.Decider == nil {
		return nil, errors.New("Guardian root requires a board, an evidence reader and a routing decider")
	}
	if start := options.Start; strings.TrimSpace(start.ThreadID) == "" || strings.TrimSpace(start.ThreadID) != root {
		options.Start.ThreadID = root
	}
	board, err := options.Board(ctx)
	if err != nil {
		return nil, err
	}
	if board == nil {
		return nil, errors.New("Guardian root requires an evidence board")
	}
	delivery := agentboard.NewGuardianLiveDelivery(root)
	housekeeper, err := agentboard.NewGuardianHousekeeperWithOptions(board, root, delivery, options.Decider, options.Housekeeper)
	if err != nil {
		return nil, err
	}
	queue, err := agentboard.NewGuardianCaptureQueue(root, options.Start, options.Reader)
	if err != nil {
		return nil, err
	}
	worker, err := agentboard.NewGuardianRootWorker(queue, housekeeper)
	if err != nil {
		queue.Close()
		return nil, err
	}

	workerCtx, cancel := context.WithCancel(context.Background())
	live := &guardianV3Root{root: root, board: board, delivery: delivery, queue: queue, cancel: cancel, done: make(chan struct{})}

	h.mu.Lock()
	if existing, ok := h.roots[root]; ok {
		h.mu.Unlock()
		cancel()
		queue.Close()
		live.closeBoard()
		return existing, nil
	}
	h.roots[root] = live
	h.mu.Unlock()

	go func() {
		defer close(live.done)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			live.recordErr(queue.CaptureOnActivity(workerCtx))
		}()
		go func() {
			defer wg.Done()
			live.recordErr(worker.Run(workerCtx))
		}()
		wg.Wait()
	}()
	return live, nil
}

// Wake asks the root's intake for one capture. Wakes coalesce while one is
// still pending, so a burst of turn ends routes once (Rust host wake channel).
func (h *GuardianV3Host) Wake(root string) bool {
	if h == nil {
		return false
	}
	root = strings.TrimSpace(root)
	h.mu.Lock()
	live := h.roots[root]
	h.mu.Unlock()
	if live == nil {
		return false
	}
	live.queue.Wake()
	return true
}

// Root returns the live root, if any.
func (h *GuardianV3Host) Root(root string) (*guardianV3Root, bool) {
	if h == nil {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	live, ok := h.roots[strings.TrimSpace(root)]
	return live, ok
}

// LastError reports why the root's worker stopped, if it did.
func (h *GuardianV3Host) LastError(root string) error {
	live, ok := h.Root(root)
	if !ok {
		return nil
	}
	return live.LastError()
}

// StopRoot closes the root's intake, cancels its worker and waits for both
// loops to return before forgetting the root (Rust root shutdown joins the
// worker before the history closes).
func (h *GuardianV3Host) StopRoot(ctx context.Context, root string) error {
	if h == nil {
		return nil
	}
	root = strings.TrimSpace(root)
	h.mu.Lock()
	live := h.roots[root]
	delete(h.roots, root)
	h.mu.Unlock()
	if live == nil {
		return nil
	}
	live.queue.Close()
	live.cancel()
	select {
	case <-live.done:
		live.closeBoard()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops every live root.
func (h *GuardianV3Host) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	roots := make([]string, 0, len(h.roots))
	for root := range h.roots {
		roots = append(roots, root)
	}
	h.mu.Unlock()
	for _, root := range roots {
		_ = h.StopRoot(context.Background(), root)
	}
}

// Root is the tree root this worker serves.
func (r *guardianV3Root) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}

// Board is the root's evidence board, the board a reviewer inbox reads from.
func (r *guardianV3Root) Board() agentboard.AgentMessageBoard {
	if r == nil {
		return nil
	}
	return r.board
}

// Delivery is the root's live delivery, which owns every actor inbox.
func (r *guardianV3Root) Delivery() *agentboard.GuardianLiveDelivery {
	if r == nil {
		return nil
	}
	return r.delivery
}

// Position reports the last routed durable position.
func (r *guardianV3Root) Position() agentboard.GuardianHistoryPosition {
	if r == nil || r.queue == nil {
		return agentboard.GuardianHistoryPosition{}
	}
	return r.queue.Position()
}

// LastError reports why the worker stopped, if it did.
func (r *guardianV3Root) LastError() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Done closes when both loops returned.
func (r *guardianV3Root) Done() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.done
}

// closeBoard releases the root's board after the worker joined, so the durable
// store is not closed under a routing pass that is still writing.
func (r *guardianV3Root) closeBoard() {
	if r == nil || r.board == nil {
		return
	}
	if closer, ok := r.board.(interface{ Close() }); ok {
		closer.Close()
	}
	r.board = nil
}

func (r *guardianV3Root) recordErr(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	if r.err == nil {
		r.err = err
	}
	r.mu.Unlock()
}
