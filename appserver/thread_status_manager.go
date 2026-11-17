package appserver

import (
	"strings"
	"sync"
)

type ThreadStatusNotification struct {
	ThreadID string       `json:"threadId"`
	Status   ThreadStatus `json:"status"`
}

type ThreadStatusManager struct {
	mu               sync.Mutex
	runtimes         map[string]*runtimeFacts
	runningTurnCount int
}

func NewThreadStatusManager() *ThreadStatusManager {
	return &ThreadStatusManager{runtimes: map[string]*runtimeFacts{}}
}

func (m *ThreadStatusManager) UpsertThread(threadID string, emit bool) *ThreadStatusNotification {
	return m.mutate(threadID, emit, func(runtime *runtimeFacts) {
		runtime.isLoaded = true
	})
}

func (m *ThreadStatusManager) RemoveThread(threadID string) *ThreadStatusNotification {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, hadPrevious := m.statusForLocked(threadID)
	if removed, ok := m.runtimes[threadID]; ok {
		delete(m.runtimes, threadID)
		// Rust #49084: a removed running thread no longer counts toward
		// graceful-restart draining.
		if removed.running {
			m.runningTurnCount--
		}
	}
	next := NotLoadedStatus()
	if hadPrevious && !equalStatus(previous, next) {
		return &ThreadStatusNotification{ThreadID: threadID, Status: next}
	}
	return nil
}

func (m *ThreadStatusManager) LoadedStatusForThread(threadID string) ThreadStatus {
	if m == nil {
		return NotLoadedStatus()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statusForLocked(threadID)
	if !ok {
		return NotLoadedStatus()
	}
	return cloneStatus(status)
}

func (m *ThreadStatusManager) LoadedStatusesForThreads(threadIDs []string) map[string]ThreadStatus {
	out := make(map[string]ThreadStatus, len(threadIDs))
	for _, threadID := range threadIDs {
		out[threadID] = m.LoadedStatusForThread(threadID)
	}
	return out
}

func (m *ThreadStatusManager) LoadedThreadIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.runtimes))
	for threadID, runtime := range m.runtimes {
		if loadedThreadStatus(runtime).Type != NotLoadedStatus().Type {
			out = append(out, threadID)
		}
	}
	return out
}

func (m *ThreadStatusManager) RunningTurnCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Rust #49084 maintains the count incrementally instead of rescanning the
	// runtime map while holding the state lock.
	return m.runningTurnCount
}

func (m *ThreadStatusManager) NoteTurnStarted(threadID string) *ThreadStatusNotification {
	return m.NoteTurnStartedForTurn(threadID, "")
}

// NoteTurnStartedForTurn records the thread's latest started turn. Rust #61801:
// a terminal event from an earlier turn can arrive after this one started, so
// the thread keeps the id that may still finish the turn.
func (m *ThreadStatusManager) NoteTurnStartedForTurn(threadID string, turnID string) *ThreadStatusNotification {
	turnID = strings.TrimSpace(turnID)
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.isLoaded = true
		runtime.running = true
		runtime.hasSystemError = false
		if turnID != "" {
			runtime.lastStartedTurnID = turnID
		}
	})
}

func (m *ThreadStatusManager) NoteTurnCompleted(threadID string) *ThreadStatusNotification {
	return m.NoteTurnCompletedForTurn(threadID, "")
}

// NoteTurnCompletedForTurn ignores a completion for a turn that is no longer
// the latest one, preserving the newer turn's running state and pending
// requests (Rust #61801).
func (m *ThreadStatusManager) NoteTurnCompletedForTurn(threadID string, turnID string) *ThreadStatusNotification {
	return m.finishTurn(threadID, turnID)
}

func (m *ThreadStatusManager) NoteTurnInterrupted(threadID string) *ThreadStatusNotification {
	return m.NoteTurnInterruptedForTurn(threadID, "")
}

// NoteTurnInterruptedForTurn mirrors NoteTurnCompletedForTurn for aborts
// (Rust #61801).
func (m *ThreadStatusManager) NoteTurnInterruptedForTurn(threadID string, turnID string) *ThreadStatusNotification {
	return m.finishTurn(threadID, turnID)
}

func (m *ThreadStatusManager) NoteThreadShutdown(threadID string) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.running = false
		runtime.pendingPermissionRequests = 0
		runtime.pendingUserInputRequests = 0
		runtime.isLoaded = false
		runtime.lastStartedTurnID = ""
	})
}

func (m *ThreadStatusManager) NoteSystemError(threadID string) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.running = false
		runtime.pendingPermissionRequests = 0
		runtime.pendingUserInputRequests = 0
		runtime.hasSystemError = true
		runtime.lastStartedTurnID = ""
	})
}

func (m *ThreadStatusManager) NotePermissionRequested(threadID string) *ThreadStatusActiveGuard {
	m.notePendingRequest(threadID, guardPermission)
	return &ThreadStatusActiveGuard{manager: m, threadID: threadID, guardType: guardPermission}
}

func (m *ThreadStatusManager) NotePermissionRequestedWithNotification(threadID string) (*ThreadStatusActiveGuard, *ThreadStatusNotification) {
	notification := m.notePendingRequest(threadID, guardPermission)
	return &ThreadStatusActiveGuard{manager: m, threadID: threadID, guardType: guardPermission}, notification
}

func (m *ThreadStatusManager) NoteUserInputRequested(threadID string) *ThreadStatusActiveGuard {
	m.notePendingRequest(threadID, guardUserInput)
	return &ThreadStatusActiveGuard{manager: m, threadID: threadID, guardType: guardUserInput}
}

func (m *ThreadStatusManager) NoteUserInputRequestedWithNotification(threadID string) (*ThreadStatusActiveGuard, *ThreadStatusNotification) {
	notification := m.notePendingRequest(threadID, guardUserInput)
	return &ThreadStatusActiveGuard{manager: m, threadID: threadID, guardType: guardUserInput}, notification
}

// finishTurn clears the active state only when the event belongs to the latest
// started turn. With no recorded turn, behavior is unchanged (Rust
// `last_started_turn_id.is_none_or(...)`).
func (m *ThreadStatusManager) finishTurn(threadID string, turnID string) *ThreadStatusNotification {
	turnID = strings.TrimSpace(turnID)
	return m.mutateGuarded(threadID, true, func(runtime *runtimeFacts) bool {
		return runtime.lastStartedTurnID == "" || turnID == "" || turnID == runtime.lastStartedTurnID
	}, func(runtime *runtimeFacts) {
		runtime.running = false
		runtime.pendingPermissionRequests = 0
		runtime.pendingUserInputRequests = 0
		runtime.lastStartedTurnID = ""
	})
}

// IsLatestStartedTurn reports whether turnID is the most recently started turn
// for the thread. Rust guards turn-transition cancellation with the same
// ThreadState::last_started_turn_id check, so a late terminal event from an
// older turn cannot cancel the newer turn's requests.
func (m *ThreadStatusManager) IsLatestStartedTurn(threadID string, turnID string) bool {
	if m == nil {
		return true
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[threadID]
	if runtime == nil || runtime.lastStartedTurnID == "" || turnID == "" {
		return true
	}
	return runtime.lastStartedTurnID == turnID
}

func (m *ThreadStatusManager) notePendingRequest(threadID string, guardType activeGuardType) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.isLoaded = true
		counter := pendingCounter(runtime, guardType)
		*counter = *counter + 1
	})
}

func (m *ThreadStatusManager) noteActiveGuardReleased(threadID string, guardType activeGuardType) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		counter := pendingCounter(runtime, guardType)
		if *counter > 0 {
			*counter = *counter - 1
		}
	})
}

func (m *ThreadStatusManager) mutate(threadID string, emit bool, apply func(*runtimeFacts)) *ThreadStatusNotification {
	return m.mutateGuarded(threadID, emit, nil, apply)
}

func (m *ThreadStatusManager) mutateGuarded(threadID string, emit bool, allow func(*runtimeFacts) bool, apply func(*runtimeFacts)) *ThreadStatusNotification {
	if m == nil || threadID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[threadID]
	if runtime != nil && allow != nil && !allow(runtime) {
		return nil
	}
	previous, hadPrevious := m.statusForLocked(threadID)
	if runtime == nil {
		runtime = &runtimeFacts{}
		m.runtimes[threadID] = runtime
	}
	wasRunning := runtime.running
	apply(runtime)
	switch {
	case !wasRunning && runtime.running:
		m.runningTurnCount++
	case wasRunning && !runtime.running:
		m.runningTurnCount--
	}
	next := loadedThreadStatus(runtime)
	if !emit || (hadPrevious && equalStatus(previous, next)) {
		return nil
	}
	return &ThreadStatusNotification{ThreadID: threadID, Status: next}
}

func (m *ThreadStatusManager) statusForLocked(threadID string) (ThreadStatus, bool) {
	runtime := m.runtimes[threadID]
	if runtime == nil {
		return ThreadStatus{}, false
	}
	return loadedThreadStatus(runtime), true
}

type ThreadStatusActiveGuard struct {
	manager   *ThreadStatusManager
	threadID  string
	guardType activeGuardType
	once      sync.Once
}

func (g *ThreadStatusActiveGuard) Release() *ThreadStatusNotification {
	var notification *ThreadStatusNotification
	if g == nil {
		return nil
	}
	g.once.Do(func() {
		notification = g.manager.noteActiveGuardReleased(g.threadID, g.guardType)
	})
	return notification
}

type activeGuardType int

const (
	guardPermission activeGuardType = iota
	guardUserInput
)

type runtimeFacts struct {
	isLoaded                  bool
	running                   bool
	pendingPermissionRequests uint32
	pendingUserInputRequests  uint32
	hasSystemError            bool
	lastStartedTurnID         string
}

func loadedThreadStatus(runtime *runtimeFacts) ThreadStatus {
	if runtime == nil || !runtime.isLoaded {
		return NotLoadedStatus()
	}
	flags := []ThreadActiveFlag{}
	if runtime.pendingPermissionRequests > 0 {
		flags = append(flags, ThreadActiveFlagWaitingOnApproval)
	}
	if runtime.pendingUserInputRequests > 0 {
		flags = append(flags, ThreadActiveFlagWaitingOnUserInput)
	}
	if runtime.running || len(flags) > 0 {
		return ActiveStatus(flags...)
	}
	if runtime.hasSystemError {
		return ThreadStatus{Type: "systemError"}
	}
	return IdleStatus()
}

func ResolveThreadStatus(status ThreadStatus, hasInProgressTurn bool) ThreadStatus {
	if hasInProgressTurn && (status.Type == "idle" || status.Type == "notLoaded") {
		return ActiveStatus()
	}
	return cloneStatus(status)
}

func pendingCounter(runtime *runtimeFacts, guardType activeGuardType) *uint32 {
	if guardType == guardPermission {
		return &runtime.pendingPermissionRequests
	}
	return &runtime.pendingUserInputRequests
}

func equalStatus(a ThreadStatus, b ThreadStatus) bool {
	if a.Type != b.Type || len(a.ActiveFlags) != len(b.ActiveFlags) {
		return false
	}
	for i := range a.ActiveFlags {
		if a.ActiveFlags[i] != b.ActiveFlags[i] {
			return false
		}
	}
	return true
}

func cloneStatus(status ThreadStatus) ThreadStatus {
	status.ActiveFlags = append([]ThreadActiveFlag(nil), status.ActiveFlags...)
	return status
}
