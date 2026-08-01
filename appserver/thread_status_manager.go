package appserver

import (
	"sync"
)

type ThreadStatusNotification struct {
	ThreadID string       `json:"threadId"`
	Status   ThreadStatus `json:"status"`
}

type ThreadStatusManager struct {
	mu       sync.Mutex
	runtimes map[string]*runtimeFacts
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
	delete(m.runtimes, threadID)
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
	count := 0
	for _, runtime := range m.runtimes {
		if runtime.running {
			count++
		}
	}
	return count
}

func (m *ThreadStatusManager) NoteTurnStarted(threadID string) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.isLoaded = true
		runtime.running = true
		runtime.hasSystemError = false
	})
}

func (m *ThreadStatusManager) NoteTurnCompleted(threadID string) *ThreadStatusNotification {
	return m.clearActiveState(threadID)
}

func (m *ThreadStatusManager) NoteTurnInterrupted(threadID string) *ThreadStatusNotification {
	return m.clearActiveState(threadID)
}

func (m *ThreadStatusManager) NoteThreadShutdown(threadID string) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.running = false
		runtime.pendingPermissionRequests = 0
		runtime.pendingUserInputRequests = 0
		runtime.isLoaded = false
	})
}

func (m *ThreadStatusManager) NoteSystemError(threadID string) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.running = false
		runtime.pendingPermissionRequests = 0
		runtime.pendingUserInputRequests = 0
		runtime.hasSystemError = true
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

func (m *ThreadStatusManager) clearActiveState(threadID string) *ThreadStatusNotification {
	return m.mutate(threadID, true, func(runtime *runtimeFacts) {
		runtime.running = false
		runtime.pendingPermissionRequests = 0
		runtime.pendingUserInputRequests = 0
	})
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
	if m == nil || threadID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, hadPrevious := m.statusForLocked(threadID)
	runtime := m.runtimes[threadID]
	if runtime == nil {
		runtime = &runtimeFacts{}
		m.runtimes[threadID] = runtime
	}
	apply(runtime)
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
