package appserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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
	closing bool
	turnWG  sync.WaitGroup

	ephemeralMu sync.RWMutex
	ephemeral   map[string]*session.Record

	subscriptionsMu sync.Mutex
	subscriptions   map[string]map[string]struct{}

	liveThreadsMu sync.Mutex
	liveThreads   map[session.ThreadID]*managedLiveThread

	terminalMu sync.Mutex
	terminals  map[string]struct{}

	statusMu sync.Mutex
	status   *ThreadStatusManager
}

func NewThreadManager(status *ThreadStatusManager) *ThreadManager {
	if status == nil {
		status = NewThreadStatusManager()
	}
	return &ThreadManager{
		active:        map[string]*activeRuntimeTurn{},
		diffs:         map[string]*runtimeutil.DiffTracker{},
		ephemeral:     map[string]*session.Record{},
		subscriptions: map[string]map[string]struct{}{},
		liveThreads:   map[session.ThreadID]*managedLiveThread{},
		terminals:     map[string]struct{}{},
		status:        status,
	}
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
	defer m.liveThreadsMu.Unlock()
	if _, ok := m.liveThreads[record.ID]; ok {
		return nil
	}
	guard, err := session.OpenLiveThread(store, record, threadUsesPaginatedHistory(record))
	if err != nil {
		return writerOwnershipError(err)
	}
	defer func() { _ = guard.Discard() }()
	m.liveThreads[record.ID] = &managedLiveThread{persistence: guard.Commit()}
	return nil
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
	m.liveThreadsMu.Lock()
	liveThreads := make([]*managedLiveThread, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if liveThread := m.liveThreads[threadID]; liveThread != nil {
			liveThreads = append(liveThreads, liveThread)
			delete(m.liveThreads, threadID)
		}
	}
	m.liveThreadsMu.Unlock()
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
	var closeErr error
	for _, liveThread := range liveThreads {
		if err := liveThread.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	return closeErr
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
		return fmt.Errorf("%w: thread manager is closing", session.ErrConflict)
	}
	if active := m.active[threadID]; active != nil {
		return fmt.Errorf("%w: thread %s already has active turn %s", session.ErrConflict, threadID, active.TurnID)
	}
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
		return fmt.Errorf("%w: thread manager is closing", session.ErrConflict)
	}
	if active := m.active[threadID]; active != nil {
		if active.TurnID == "" {
			active.TurnID = turnID
			active.Cancel = cancel
			active.StartedAtMS = startedAtMS
			active.Params = cloneTurnStartParams(params)
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
	m.active[threadID] = &activeRuntimeTurn{
		ThreadID: threadID, TurnID: turnID, Cancel: cancel, StartedAtMS: startedAtMS,
		Params: cloneTurnStartParams(params),
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
			record.Preview = runtimeSessionItemPreviewText(&item)
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
