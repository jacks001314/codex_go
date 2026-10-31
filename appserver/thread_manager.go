package appserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/attachmentstore"
	"codex_go/rollout"
	"codex_go/runtimeutil"
	"codex_go/session"
	"codex_go/turn"
)

// ThreadManager owns app-server state whose lifetime is scoped to a loaded
// thread. Router and RuntimeRouter share one instance so writer ownership,
// active turns, subscriptions, diffs, status, and terminal delivery cannot
// drift into independent registries.
type ThreadManager struct {
	turnsMu sync.Mutex
	active  map[string]*activeRuntimeTurn
	diffs   map[string]*runtimeutil.DiffTracker
	// capabilityDiscoveryV2 is retained for idle callers and replaced when a
	// new turn starts (Rust #53736 thread extension data).
	capabilityDiscoveryV2 map[string]*runtimeCapabilityDiscoveryV2Cache
	asyncCompaction       map[string]*asyncCompactionState
	closing               bool
	// paused closes turn admission while a runtime-wide change (a workspace
	// switch) replaces credentials. Rust #57514 TurnAdmission.pause.
	paused bool
	turnWG sync.WaitGroup

	ephemeralMu sync.RWMutex
	ephemeral   map[string]*session.Record

	subscriptionsMu sync.Mutex
	subscriptions   map[string]map[string]struct{}

	liveThreadsMu sync.Mutex
	liveThreads   map[session.ThreadID]*managedLiveThread
	// retainedHook runs after a live thread is retained, outside liveThreadsMu,
	// so the runtime can arm its idle lifecycle without re-entering the lock.
	retainedHook func(string)

	// providerRoutes retains the model provider route each live thread started
	// with, so managed policy changes can be detected against it (Rust #44944
	// keeps the thread's resolved Config for the same purpose).
	providerRoutesMu sync.Mutex
	providerRoutes   map[session.ThreadID]threadModelProviderRoute

	terminalMu sync.Mutex
	terminals  map[string]struct{}

	statusMu sync.Mutex
	status   *ThreadStatusManager

	attachmentStoreMu sync.RWMutex
	attachmentStore   attachmentstore.Store
}

// ErrServerDraining is returned when graceful shutdown rejects new work. It
// keeps JSON-RPC invalid-request code -32600 and exposes the structured reason
// clients use to distinguish shutdown from ordinary invalid requests.
var ErrServerDraining = serverDrainingError{}

type serverDrainingError struct{}

func (serverDrainingError) Error() string {
	return "Server is draining; retry after reconnecting"
}

func (serverDrainingError) Unwrap() error {
	return session.ErrConflict
}

func (serverDrainingError) JSONRPCErrorData() map[string]any {
	return map[string]any{"reason": "serverShuttingDown"}
}

func NewThreadManager(status *ThreadStatusManager) *ThreadManager {
	if status == nil {
		status = NewThreadStatusManager()
	}
	return &ThreadManager{
		active:                map[string]*activeRuntimeTurn{},
		diffs:                 map[string]*runtimeutil.DiffTracker{},
		capabilityDiscoveryV2: map[string]*runtimeCapabilityDiscoveryV2Cache{},
		asyncCompaction:       map[string]*asyncCompactionState{},
		ephemeral:             map[string]*session.Record{},
		subscriptions:         map[string]map[string]struct{}{},
		liveThreads:           map[session.ThreadID]*managedLiveThread{},
		providerRoutes:        map[session.ThreadID]threadModelProviderRoute{},
		terminals:             map[string]struct{}{},
		status:                status,
		attachmentStore:       attachmentstore.InlineStore{},
	}
}

// SetAttachmentStore injects the storage-neutral attachment backend used by
// this thread manager. A nil store restores the inline passthrough default.
func (m *ThreadManager) SetAttachmentStore(store attachmentstore.Store) {
	if m == nil {
		return
	}
	m.attachmentStoreMu.Lock()
	m.attachmentStore = attachmentstore.StoreOrDefault(store)
	m.attachmentStoreMu.Unlock()
}

// AttachmentStore returns the injected attachment backend, or the inline
// passthrough store when no backend was configured.
func (m *ThreadManager) AttachmentStore() attachmentstore.Store {
	if m == nil {
		return attachmentstore.InlineStore{}
	}
	m.attachmentStoreMu.RLock()
	store := m.attachmentStore
	m.attachmentStoreMu.RUnlock()
	return attachmentstore.StoreOrDefault(store)
}

func (m *ThreadManager) SetStatusManager(status *ThreadStatusManager) {
	if m == nil || status == nil {
		return
	}
	m.statusMu.Lock()
	m.status = status
	m.statusMu.Unlock()
}

func (m *ThreadManager) StatusManager() *ThreadStatusManager {
	if m == nil {
		return NewThreadStatusManager()
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.status == nil {
		m.status = NewThreadStatusManager()
	}
	return m.status
}

func (m *ThreadManager) RetainLiveThread(store *session.Store, record *session.Record) error {
	if m == nil || record == nil {
		return nil
	}
	m.liveThreadsMu.Lock()
	if _, ok := m.liveThreads[record.ID]; ok {
		m.liveThreadsMu.Unlock()
		return nil
	}
	guard, err := session.OpenLiveThread(store, record, threadUsesPaginatedHistory(record))
	if err != nil {
		m.liveThreadsMu.Unlock()
		return writerOwnershipError(err)
	}
	defer func() { _ = guard.Discard() }()
	m.liveThreads[record.ID] = &managedLiveThread{persistence: guard.Commit()}
	hook := m.retainedHook
	m.liveThreadsMu.Unlock()
	if hook != nil {
		hook(string(record.ID))
	}
	return nil
}

// LiveThreadRetention owns a persistence acquisition until initialization
// decides whether the thread really became live (Rust #45487
// LiveThreadInitGuard).
//
// Rust keeps the guard for the whole session initialization and only commits
// once it succeeds, so a failure after the writer was installed discards the
// writer instead of leaving it owned by a thread the client never received.
type LiveThreadRetention struct {
	manager *ThreadManager
	record  *session.Record
	guard   *session.LiveThreadInitGuard
	done    bool
}

// ReadRecord reads through the writer acquired by this resume. Writes committed
// by a previous writer before ownership transferred are visible here, so callers
// can restore from authoritative history instead of their pre-acquisition
// snapshot (Rust #49599).
func (retention *LiveThreadRetention) ReadRecord(includeArchived bool, includeHistory bool) (*session.Record, error) {
	if retention == nil || retention.done || retention.guard == nil || retention.guard.LiveThread() == nil {
		return nil, fmt.Errorf("%w: resume persistence is not held", session.ErrConflict)
	}
	return retention.guard.LiveThread().Read(includeArchived, includeHistory)
}

// AcquireLiveThreadRetention opens persistence for a thread that is about to
// become live. It returns nil when the thread is already retained.
func (m *ThreadManager) AcquireLiveThreadRetention(store *session.Store, record *session.Record) (*LiveThreadRetention, error) {
	if m == nil || record == nil {
		return nil, nil
	}
	m.liveThreadsMu.Lock()
	defer m.liveThreadsMu.Unlock()
	if _, ok := m.liveThreads[record.ID]; ok {
		return nil, nil
	}
	guard, err := session.OpenLiveThread(store, record, threadUsesPaginatedHistory(record))
	if err != nil {
		return nil, writerOwnershipError(err)
	}
	return &LiveThreadRetention{manager: m, record: record, guard: guard}, nil
}

// Commit registers the acquired thread as live and hands its writer to the
// manager.
func (retention *LiveThreadRetention) Commit() {
	if retention == nil || retention.done {
		return
	}
	retention.done = true
	manager := retention.manager
	threadID := retention.record.ID
	manager.liveThreadsMu.Lock()
	if _, ok := manager.liveThreads[threadID]; ok {
		manager.liveThreadsMu.Unlock()
		_ = retention.guard.Discard()
		return
	}
	manager.liveThreads[threadID] = &managedLiveThread{persistence: retention.guard.Commit()}
	hook := manager.retainedHook
	manager.liveThreadsMu.Unlock()
	if hook != nil {
		hook(string(threadID))
	}
}

// Discard releases the acquisition without retaining the thread.
func (retention *LiveThreadRetention) Discard() error {
	if retention == nil || retention.done {
		return nil
	}
	retention.done = true
	return retention.guard.Discard()
}

// SetThreadRetainedHook installs the callback that arms idle eviction after a
// thread becomes live (Rust #52081: persisted children created after a client
// disconnect still receive lifecycle listeners).
func (m *ThreadManager) SetThreadRetainedHook(hook func(string)) {
	if m == nil {
		return
	}
	m.liveThreadsMu.Lock()
	m.retainedHook = hook
	m.liveThreadsMu.Unlock()
}

func (m *ThreadManager) AcquireLifecycleWriters(store *session.Store, threadIDs []session.ThreadID) ([]*session.WriterLock, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: thread manager is nil", ErrInvalidRequest)
	}
	m.liveThreadsMu.Lock()
	defer m.liveThreadsMu.Unlock()
	missing := make([]session.ThreadID, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if liveThread := m.liveThreads[threadID]; liveThread == nil || !liveThread.persistence.OwnsWriter() {
			missing = append(missing, threadID)
		}
	}
	locks, err := store.AcquireWriters(missing)
	if err != nil {
		return nil, writerOwnershipError(err)
	}
	return locks, nil
}

func (m *ThreadManager) ReleaseLiveThreads(threadIDs []session.ThreadID) {
	if m == nil {
		return
	}
	m.turnsMu.Lock()
	for _, threadID := range threadIDs {
		delete(m.capabilityDiscoveryV2, string(threadID))
	}
	m.turnsMu.Unlock()
	for _, threadID := range threadIDs {
		m.shutdownAsyncCompaction(string(threadID))
	}
	m.liveThreadsMu.Lock()
	liveThreads := make([]*managedLiveThread, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if liveThread := m.liveThreads[threadID]; liveThread != nil {
			liveThreads = append(liveThreads, liveThread)
			delete(m.liveThreads, threadID)
		}
	}
	m.liveThreadsMu.Unlock()
	m.clearModelProviderRoutes(threadIDs)
	for i := len(liveThreads) - 1; i >= 0; i-- {
		_ = liveThreads[i].Close()
	}
}

func (m *ThreadManager) CloseLiveThreads() error {
	if m == nil {
		return nil
	}
	m.liveThreadsMu.Lock()
	threadIDs := make([]session.ThreadID, 0, len(m.liveThreads))
	for threadID := range m.liveThreads {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Slice(threadIDs, func(i, j int) bool { return threadIDs[i] < threadIDs[j] })
	liveThreads := make([]*managedLiveThread, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		liveThreads = append(liveThreads, m.liveThreads[threadID])
		delete(m.liveThreads, threadID)
	}
	m.liveThreadsMu.Unlock()
	m.turnsMu.Lock()
	for _, threadID := range threadIDs {
		delete(m.capabilityDiscoveryV2, string(threadID))
	}
	m.turnsMu.Unlock()
	for _, threadID := range threadIDs {
		m.shutdownAsyncCompaction(string(threadID))
	}
	m.clearModelProviderRoutes(threadIDs)
	var closeErr error
	for _, liveThread := range liveThreads {
		if err := liveThread.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	return closeErr
}

// SetModelProviderRoute records the provider route a live thread started with.
func (m *ThreadManager) SetModelProviderRoute(threadID session.ThreadID, route threadModelProviderRoute) {
	if m == nil || strings.TrimSpace(string(threadID)) == "" {
		return
	}
	m.providerRoutesMu.Lock()
	if m.providerRoutes == nil {
		m.providerRoutes = map[session.ThreadID]threadModelProviderRoute{}
	}
	m.providerRoutes[threadID] = route
	m.providerRoutesMu.Unlock()
}

// ModelProviderRoute returns the retained provider route for a live thread.
func (m *ThreadManager) ModelProviderRoute(threadID session.ThreadID) (threadModelProviderRoute, bool) {
	if m == nil {
		return threadModelProviderRoute{}, false
	}
	m.providerRoutesMu.Lock()
	defer m.providerRoutesMu.Unlock()
	route, ok := m.providerRoutes[threadID]
	return route, ok
}

func (m *ThreadManager) clearModelProviderRoutes(threadIDs []session.ThreadID) {
	if m == nil || len(threadIDs) == 0 {
		return
	}
	m.providerRoutesMu.Lock()
	for _, threadID := range threadIDs {
		delete(m.providerRoutes, threadID)
	}
	m.providerRoutesMu.Unlock()
}

func (m *ThreadManager) LiveThreadCount() int {
	if m == nil {
		return 0
	}
	m.liveThreadsMu.Lock()
	defer m.liveThreadsMu.Unlock()
	return len(m.liveThreads)
}

func (m *ThreadManager) LiveThread(threadID session.ThreadID) *session.LiveThread {
	if m == nil || strings.TrimSpace(string(threadID)) == "" {
		return nil
	}
	m.liveThreadsMu.Lock()
	defer m.liveThreadsMu.Unlock()
	liveThread := m.liveThreads[threadID]
	if liveThread == nil {
		return nil
	}
	return liveThread.persistence
}

// HasLiveThread reports whether a thread is currently loaded in the manager,
// independent of whether its persistence writer has been opened (Rust's
// thread manager tracks the loaded thread itself).
func (m *ThreadManager) HasLiveThread(threadID session.ThreadID) bool {
	if m == nil || strings.TrimSpace(string(threadID)) == "" {
		return false
	}
	m.liveThreadsMu.Lock()
	defer m.liveThreadsMu.Unlock()
	return m.liveThreads[threadID] != nil
}

func (m *ThreadManager) WithRolloutRecorder(threadID session.ThreadID, open func() (*rollout.Recorder, error), apply func(*rollout.Recorder) error) (bool, error) {
	if m == nil || strings.TrimSpace(string(threadID)) == "" {
		return false, nil
	}
	m.liveThreadsMu.Lock()
	liveThread := m.liveThreads[threadID]
	m.liveThreadsMu.Unlock()
	if liveThread == nil {
		return false, nil
	}
	return true, liveThread.withRolloutRecorder(open, apply)
}

type managedLiveThread struct {
	persistence *session.LiveThread
	rolloutMu   sync.Mutex
	closed      bool
}

func (t *managedLiveThread) withRolloutRecorder(open func() (*rollout.Recorder, error), apply func(*rollout.Recorder) error) error {
	if t == nil {
		return fmt.Errorf("%w: managed live thread is nil", session.ErrConflict)
	}
	t.rolloutMu.Lock()
	defer t.rolloutMu.Unlock()
	if t.closed {
		return fmt.Errorf("%w: managed live thread is closed", session.ErrConflict)
	}
	if open == nil {
		return nil
	}
	recorder, err := open()
	if err != nil || recorder == nil {
		return err
	}
	if apply == nil {
		return recorder.Close()
	}
	applyErr := apply(recorder)
	flushErr := recorder.Flush()
	closeErr := recorder.Close()
	if applyErr != nil {
		return applyErr
	}
	if flushErr != nil {
		return flushErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func (t *managedLiveThread) Close() error {
	if t == nil {
		return nil
	}
	t.rolloutMu.Lock()
	if t.closed {
		t.rolloutMu.Unlock()
		return nil
	}
	t.closed = true
	t.rolloutMu.Unlock()

	var closeErr error
	if t.persistence != nil {
		if err := t.persistence.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	return closeErr
}

func (m *ThreadManager) ReserveTurn(threadID string) error {
	if m == nil {
		return fmt.Errorf("%w: thread manager is nil", ErrInvalidRequest)
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	if m.closing {
		return ErrServerDraining
	}
	if m.paused {
		return jsonRPCInvalidRequest("Workspace change in progress; retry when it completes")
	}
	if active := m.active[threadID]; active != nil {
		return fmt.Errorf("%w: thread %s already has active turn %s", session.ErrConflict, threadID, active.TurnID)
	}
	m.replaceCapabilityDiscoveryV2CacheLocked(threadID)
	m.active[threadID] = &activeRuntimeTurn{ThreadID: threadID}
	return nil
}

func (m *ThreadManager) RegisterTurn(threadID string, turnID string, cancel context.CancelFunc, startedAtMS int64, params *turn.TurnStartParams) error {
	return m.registerTurn(threadID, turnID, cancel, startedAtMS, params, false)
}

func (m *ThreadManager) RegisterTrackedTurn(threadID string, turnID string, cancel context.CancelFunc, startedAtMS int64, params *turn.TurnStartParams) error {
	return m.registerTurn(threadID, turnID, cancel, startedAtMS, params, true)
}

func (m *ThreadManager) registerTurn(threadID string, turnID string, cancel context.CancelFunc, startedAtMS int64, params *turn.TurnStartParams, tracked bool) error {
	if m == nil {
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("%w: thread manager is nil", ErrInvalidRequest)
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	if m.closing {
		if cancel != nil {
			cancel()
		}
		return ErrServerDraining
	}
	if m.paused {
		if cancel != nil {
			cancel()
		}
		return jsonRPCInvalidRequest("Workspace change in progress; retry when it completes")
	}
	if active := m.active[threadID]; active != nil {
		if active.TurnID == "" {
			active.TurnID = turnID
			active.Cancel = cancel
			active.StartedAtMS = startedAtMS
			active.Params = cloneTurnStartParams(params)
			active.Timing = turn.NewTimingState()
			m.ensureDiffLocked(threadID, turnID)
			if tracked {
				m.turnWG.Add(1)
			}
			return nil
		}
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("%w: thread %s already has active turn %s", session.ErrConflict, threadID, active.TurnID)
	}
	m.replaceCapabilityDiscoveryV2CacheLocked(threadID)
	m.active[threadID] = &activeRuntimeTurn{
		ThreadID: threadID, TurnID: turnID, Cancel: cancel, StartedAtMS: startedAtMS,
		Params: cloneTurnStartParams(params), Timing: turn.NewTimingState(),
	}
	m.ensureDiffLocked(threadID, turnID)
	if tracked {
		m.turnWG.Add(1)
	}
	return nil
}

func (m *ThreadManager) TurnWorkerDone() {
	if m != nil {
		m.turnWG.Done()
	}
}

func (m *ThreadManager) capabilityDiscoveryV2Cache(threadID string) *runtimeCapabilityDiscoveryV2Cache {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	if m.capabilityDiscoveryV2 == nil {
		m.capabilityDiscoveryV2 = map[string]*runtimeCapabilityDiscoveryV2Cache{}
	}
	cache := m.capabilityDiscoveryV2[threadID]
	if cache == nil {
		cache = newRuntimeCapabilityDiscoveryV2Cache()
		m.capabilityDiscoveryV2[threadID] = cache
	}
	return cache
}

func (m *ThreadManager) replaceCapabilityDiscoveryV2CacheLocked(threadID string) {
	if m.capabilityDiscoveryV2 == nil {
		m.capabilityDiscoveryV2 = map[string]*runtimeCapabilityDiscoveryV2Cache{}
	}
	m.capabilityDiscoveryV2[threadID] = newRuntimeCapabilityDiscoveryV2Cache()
}

// BeginShutdown atomically prevents new turns and takes ownership of every
// turn that has not begun terminal processing yet. Workers that already
// consumed their turn remain covered by WaitForTurnWorkers.
func (m *ThreadManager) BeginShutdown() []*activeRuntimeTurn {
	if m == nil {
		return nil
	}
	m.turnsMu.Lock()
	m.closing = true
	active := make([]*activeRuntimeTurn, 0, len(m.active))
	for threadID, runtimeTurn := range m.active {
		if clone := cloneActiveRuntimeTurn(runtimeTurn); clone != nil {
			active = append(active, clone)
		}
		delete(m.active, threadID)
		if runtimeTurn != nil && runtimeTurn.TurnID != "" {
			delete(m.diffs, activeTurnDiffKey(threadID, runtimeTurn.TurnID))
		}
	}
	m.turnsMu.Unlock()
	sort.Slice(active, func(i, j int) bool {
		if active[i].ThreadID == active[j].ThreadID {
			return active[i].TurnID < active[j].TurnID
		}
		return active[i].ThreadID < active[j].ThreadID
	})
	return active
}

func (m *ThreadManager) WaitForTurnWorkers(ctx context.Context) error {
	if m == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		m.turnWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *ThreadManager) IsClosing() bool {
	if m == nil {
		return true
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	return m.closing
}

// BeginDraining closes turn admission without taking or cancelling active
// turns. Shutdown uses it before waiting for in-flight requests so automatic
// continuations cannot start new work while the connection drains; a later
// BeginShutdown still owns and interrupts the remaining turns (Rust #43959).
func (m *ThreadManager) BeginDraining() {
	if m == nil {
		return
	}
	m.turnsMu.Lock()
	m.closing = true
	m.turnsMu.Unlock()
}

// TurnAdmissionPause keeps turn admission closed until Release is called. A
// dropped pause (released exactly once) restores the previous state.
type TurnAdmissionPause struct {
	manager *ThreadManager
	once    sync.Once
}

// Release allows new turns to start again.
func (p *TurnAdmissionPause) Release() {
	if p == nil || p.manager == nil {
		return
	}
	p.once.Do(func() {
		p.manager.turnsMu.Lock()
		p.manager.paused = false
		p.manager.turnsMu.Unlock()
	})
}

// PauseTurnAdmission prevents new turns from starting and waits up to ten
// seconds for the active turns to drain (Rust #57514 TurnAdmission::pause).
func (m *ThreadManager) PauseTurnAdmission(ctx context.Context) (*TurnAdmissionPause, error) {
	if m == nil {
		return nil, jsonRPCInvalidRequest("Turn admission is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.turnsMu.Lock()
	if m.closing || m.paused {
		m.turnsMu.Unlock()
		return nil, jsonRPCInvalidRequest("Runtime changes are already paused")
	}
	m.paused = true
	m.turnsMu.Unlock()
	pause := &TurnAdmissionPause{manager: m}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.turnsMu.Lock()
		active := len(m.active)
		m.turnsMu.Unlock()
		if active == 0 {
			return pause, nil
		}
		select {
		case <-ctx.Done():
			pause.Release()
			return nil, jsonRPCInvalidRequest("Turn admission is unavailable")
		case <-deadline.C:
			pause.Release()
			return nil, jsonRPCInvalidRequest("Work is still starting; retry the workspace switch")
		case <-ticker.C:
		}
	}
}

// LiveThreadIDs lists the threads whose runtime is currently loaded.
func (m *ThreadManager) LiveThreadIDs() []session.ThreadID {
	if m == nil {
		return nil
	}
	m.liveThreadsMu.Lock()
	defer m.liveThreadsMu.Unlock()
	ids := make([]session.ThreadID, 0, len(m.liveThreads))
	for threadID := range m.liveThreads {
		ids = append(ids, threadID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (m *ThreadManager) UpdateTurn(threadID string, turnID string, apply func(*activeRuntimeTurn)) bool {
	if m == nil {
		return false
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	active := m.active[threadID]
	if active == nil || active.TurnID != turnID {
		return false
	}
	apply(active)
	return true
}

func (m *ThreadManager) ActiveTurn(threadID string) *activeRuntimeTurn {
	if m == nil {
		return nil
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	return cloneActiveRuntimeTurn(m.active[strings.TrimSpace(threadID)])
}

// ActiveTurnID mirrors Rust #46305 (`ThreadState::active_turn_id`): callers
// that only need the active turn's identity must not pay for a turn snapshot,
// which clones the turn's start parameters and inputs.
func (m *ThreadManager) ActiveTurnID(threadID string) string {
	if m == nil {
		return ""
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	active := m.active[strings.TrimSpace(threadID)]
	if active == nil {
		return ""
	}
	return active.TurnID
}

func (m *ThreadManager) ActiveTurns() []*activeRuntimeTurn {
	if m == nil {
		return nil
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	out := make([]*activeRuntimeTurn, 0, len(m.active))
	for _, active := range m.active {
		if clone := cloneActiveRuntimeTurn(active); clone != nil {
			out = append(out, clone)
		}
	}
	return out
}

func (m *ThreadManager) ConsumeTurn(threadID string, turnID string, clearDiff bool) (*activeRuntimeTurn, bool) {
	if m == nil {
		return nil, false
	}
	m.turnsMu.Lock()
	active := m.active[threadID]
	if active == nil || active.TurnID != turnID {
		m.turnsMu.Unlock()
		return nil, false
	}
	delete(m.active, threadID)
	if clearDiff {
		delete(m.diffs, activeTurnDiffKey(threadID, active.TurnID))
	}
	m.turnsMu.Unlock()
	return active, true
}

// ConsumeTurnTracked atomically transfers an active turn to a terminal worker.
// Shutdown either consumes the turn first or observes the worker in turnWG.
func (m *ThreadManager) ConsumeTurnTracked(threadID string, turnID string, clearDiff bool) (*activeRuntimeTurn, bool) {
	if m == nil {
		return nil, false
	}
	m.turnsMu.Lock()
	active := m.active[threadID]
	if active == nil || active.TurnID != turnID {
		m.turnsMu.Unlock()
		return nil, false
	}
	delete(m.active, threadID)
	if clearDiff {
		delete(m.diffs, activeTurnDiffKey(threadID, active.TurnID))
	}
	m.turnWG.Add(1)
	m.turnsMu.Unlock()
	return active, true
}

// ConsumeTurnIf atomically takes the active turn only when accept approves it.
// Automatic wake reservations use it so an interrupt and the post-discovery
// revalidation cannot both consume the same reservation.
func (m *ThreadManager) ConsumeTurnIf(threadID string, turnID string, clearDiff bool, accept func(*activeRuntimeTurn) bool) (*activeRuntimeTurn, bool) {
	if m == nil || accept == nil {
		return nil, false
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	active := m.active[threadID]
	if active == nil || active.TurnID != turnID || !accept(active) {
		return nil, false
	}
	delete(m.active, threadID)
	if clearDiff {
		delete(m.diffs, activeTurnDiffKey(threadID, active.TurnID))
	}
	return active, true
}

func (m *ThreadManager) DiffTracker(threadID string, turnID string) *runtimeutil.DiffTracker {
	if m == nil {
		return nil
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	return m.ensureDiffLocked(threadID, turnID)
}

func (m *ThreadManager) DiffSnapshot(threadID string, turnID string) string {
	if m == nil {
		return ""
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	tracker := m.diffs[activeTurnDiffKey(threadID, turnID)]
	if tracker == nil || tracker.UnifiedDiff() == nil {
		return ""
	}
	return *tracker.UnifiedDiff()
}

func (m *ThreadManager) ClearDiff(threadID string, turnID string) {
	if m == nil {
		return
	}
	m.turnsMu.Lock()
	delete(m.diffs, activeTurnDiffKey(threadID, turnID))
	m.turnsMu.Unlock()
}

func (m *ThreadManager) ensureDiffLocked(threadID string, turnID string) *runtimeutil.DiffTracker {
	key := activeTurnDiffKey(threadID, turnID)
	tracker := m.diffs[key]
	if tracker == nil {
		tracker = runtimeutil.NewDiffTracker()
		m.diffs[key] = tracker
	}
	return tracker
}

func (m *ThreadManager) Subscribe(threadID string, connectionID string) {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	m.subscriptionsMu.Lock()
	defer m.subscriptionsMu.Unlock()
	connections := m.subscriptions[threadID]
	if connections == nil {
		connections = map[string]struct{}{}
		m.subscriptions[threadID] = connections
	}
	connections[connectionID] = struct{}{}
}

func (m *ThreadManager) Subscribers(threadID string) []string {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	m.subscriptionsMu.Lock()
	defer m.subscriptionsMu.Unlock()
	connections := m.subscriptions[threadID]
	out := make([]string, 0, len(connections))
	for connectionID := range connections {
		out = append(out, connectionID)
	}
	sort.Strings(out)
	return out
}

func (m *ThreadManager) Unsubscribe(threadID string, connectionID string) bool {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return false
	}
	connectionID = normalizeConnectionID(connectionID)
	m.subscriptionsMu.Lock()
	defer m.subscriptionsMu.Unlock()
	connections := m.subscriptions[threadID]
	if _, ok := connections[connectionID]; !ok {
		return false
	}
	delete(connections, connectionID)
	if len(connections) == 0 {
		delete(m.subscriptions, threadID)
	}
	return true
}

func (m *ThreadManager) ClearConnection(connectionID string) {
	if m == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	m.subscriptionsMu.Lock()
	defer m.subscriptionsMu.Unlock()
	for threadID, connections := range m.subscriptions {
		delete(connections, connectionID)
		if len(connections) == 0 {
			delete(m.subscriptions, threadID)
		}
	}
}

// ThreadsForConnection returns the threads a connection is currently
// subscribed to, so callers can schedule idle work after clearing it.
func (m *ThreadManager) ThreadsForConnection(connectionID string) []string {
	if m == nil {
		return nil
	}
	connectionID = normalizeConnectionID(connectionID)
	m.subscriptionsMu.Lock()
	defer m.subscriptionsMu.Unlock()
	threads := make([]string, 0)
	for threadID, connections := range m.subscriptions {
		if _, ok := connections[connectionID]; ok {
			threads = append(threads, threadID)
		}
	}
	sort.Strings(threads)
	return threads
}

func (m *ThreadManager) ClearThread(threadID string) {
	if m == nil {
		return
	}
	m.subscriptionsMu.Lock()
	delete(m.subscriptions, strings.TrimSpace(threadID))
	m.subscriptionsMu.Unlock()
}

func (m *ThreadManager) ClearSubscriptions() {
	if m == nil {
		return
	}
	m.subscriptionsMu.Lock()
	clear(m.subscriptions)
	m.subscriptionsMu.Unlock()
}

func (m *ThreadManager) EphemeralRecord(threadID session.ThreadID, includeHistory bool) (*session.Record, bool) {
	if m == nil || strings.TrimSpace(string(threadID)) == "" {
		return nil, false
	}
	m.ephemeralMu.RLock()
	record, ok := m.ephemeral[string(threadID)]
	if !ok || record == nil {
		m.ephemeralMu.RUnlock()
		return nil, false
	}
	// Clone while the read lock is still held. The map entry points at mutable
	// record state that AppendEphemeralItems updates under the write lock; only
	// protecting the map lookup leaves the deep copy racing with those writes.
	clone := cloneRuntimeSessionRecord(record)
	m.ephemeralMu.RUnlock()
	if !includeHistory {
		clone.Items = nil
	}
	return clone, true
}

func (m *ThreadManager) SaveEphemeralRecord(record *session.Record) bool {
	if m == nil || record == nil || strings.TrimSpace(string(record.ID)) == "" || !runtimeRecordEphemeral(record) {
		return false
	}
	clone := cloneRuntimeSessionRecord(record)
	clone.Metadata.Extra = ensureRecordExtra(clone.Metadata.Extra)
	clone.Metadata.Extra["ephemeral"] = true
	m.ephemeralMu.Lock()
	if existing := m.ephemeral[string(clone.ID)]; existing != nil && len(clone.Items) == 0 && len(existing.Items) > 0 {
		clone.Items = cloneRuntimeSessionItems(existing.Items)
	}
	m.ephemeral[string(clone.ID)] = clone
	m.ephemeralMu.Unlock()
	return true
}

func (m *ThreadManager) DeleteEphemeralRecord(threadID session.ThreadID) bool {
	if m == nil || strings.TrimSpace(string(threadID)) == "" {
		return false
	}
	m.ephemeralMu.Lock()
	defer m.ephemeralMu.Unlock()
	if _, ok := m.ephemeral[string(threadID)]; !ok {
		return false
	}
	delete(m.ephemeral, string(threadID))
	return true
}

func (m *ThreadManager) AppendEphemeralItems(threadID session.ThreadID, items []session.Item) (*session.Record, bool) {
	if m == nil || strings.TrimSpace(string(threadID)) == "" {
		return nil, false
	}
	m.ephemeralMu.Lock()
	defer m.ephemeralMu.Unlock()
	record, ok := m.ephemeral[string(threadID)]
	if !ok || record == nil {
		return nil, false
	}
	now := time.Now().UTC()
	for i := range items {
		item := items[i]
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		record.Items = append(record.Items, cloneRuntimeSessionItem(item))
		record.UpdatedAt = item.CreatedAt
		record.RecencyAt = item.CreatedAt
		if strings.TrimSpace(record.Preview) == "" {
			// Rust #50462: prefer the delegated-task preview so an ephemeral
			// delegated thread is discoverable before any user follow-up.
			if preview, ok := session.DelegatedItemPreview(&item); ok {
				record.Preview = preview
			} else {
				record.Preview = runtimeSessionItemPreviewText(&item)
			}
		}
	}
	return cloneRuntimeSessionRecord(record), true
}

func (m *ThreadManager) ClearEphemeralRecords() {
	if m == nil {
		return
	}
	m.ephemeralMu.Lock()
	clear(m.ephemeral)
	m.ephemeralMu.Unlock()
}

func (m *ThreadManager) ShutdownStatuses() []*ThreadStatusNotification {
	status := m.StatusManager()
	threadIDs := status.LoadedThreadIDs()
	notifications := make([]*ThreadStatusNotification, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if notification := status.NoteThreadShutdown(threadID); notification != nil {
			notifications = append(notifications, notification)
		}
		status.RemoveThread(threadID)
	}
	return notifications
}

func (m *ThreadManager) ClaimTerminal(threadID string, turnID string) bool {
	if m == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return false
	}
	key := activeTurnDiffKey(threadID, turnID)
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()
	if _, ok := m.terminals[key]; ok {
		return false
	}
	m.terminals[key] = struct{}{}
	return true
}

func cloneActiveRuntimeTurn(active *activeRuntimeTurn) *activeRuntimeTurn {
	if active == nil {
		return nil
	}
	clone := *active
	clone.Params = cloneTurnStartParams(active.Params)
	return &clone
}
