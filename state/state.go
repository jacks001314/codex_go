package state

import (
	"fmt"
	"sync"
	"time"

	"codex_go/compact"
)

type TaskKind string

const (
	TaskRegular          TaskKind = "regular"
	TaskReview           TaskKind = "review"
	TaskCompact          TaskKind = "compact"
	TaskUserShellCommand TaskKind = "userShellCommand"
)

type MailboxDeliveryPhase string

const (
	MailboxCurrentTurn MailboxDeliveryPhase = "currentTurn"
	MailboxNextTurn    MailboxDeliveryPhase = "nextTurn"
)

type ActiveTurn struct {
	Task      *RunningTask
	TurnState *TurnState
}

func NewActiveTurn() *ActiveTurn {
	return &ActiveTurn{TurnState: NewTurnState()}
}

type RunningTask struct {
	ID           string
	Kind         TaskKind
	StartedAt    time.Time
	Cancelled    bool
	CancelReason string
}

type TurnState struct {
	mu                        sync.Mutex
	pendingApprovals          map[string]any
	pendingRequestPermissions map[string]any
	pendingUserInput          map[string]any
	pendingElicitations       map[string]any
	pendingDynamicTools       map[string]any
	pendingInput              []string
	mailboxDeliveryPhase      MailboxDeliveryPhase
	grantedPermissionsByEnvID map[string]map[string]any
	strictAutoReviewEnabled   bool
	toolCalls                 uint64
	hasMemoryCitation         bool
	tokenUsageAtTurnStart     int64
}

func NewTurnState() *TurnState {
	return &TurnState{
		pendingApprovals:          map[string]any{},
		pendingRequestPermissions: map[string]any{},
		pendingUserInput:          map[string]any{},
		pendingElicitations:       map[string]any{},
		pendingDynamicTools:       map[string]any{},
		mailboxDeliveryPhase:      MailboxCurrentTurn,
		grantedPermissionsByEnvID: map[string]map[string]any{},
	}
}

func (s *TurnState) InsertPendingApproval(key string, value any) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.pendingApprovals[key]
	s.pendingApprovals[key] = value
	return old
}

func (s *TurnState) RemovePendingApproval(key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.pendingApprovals[key]
	delete(s.pendingApprovals, key)
	return value
}

func (s *TurnState) ClearPendingWaiters() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingApprovals = map[string]any{}
	s.pendingRequestPermissions = map[string]any{}
	s.pendingUserInput = map[string]any{}
	s.pendingElicitations = map[string]any{}
	s.pendingDynamicTools = map[string]any{}
}

func (s *TurnState) PendingWaiterCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingApprovals) + len(s.pendingRequestPermissions) + len(s.pendingUserInput) + len(s.pendingElicitations) + len(s.pendingDynamicTools)
}

func (s *TurnState) SetMailboxDeliveryPhase(phase MailboxDeliveryPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mailboxDeliveryPhase = phase
}

func (s *TurnState) AcceptMailboxDeliveryForCurrentTurn() {
	s.SetMailboxDeliveryPhase(MailboxCurrentTurn)
}

func (s *TurnState) AcceptsMailboxDeliveryForCurrentTurn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mailboxDeliveryPhase == MailboxCurrentTurn
}

func (s *TurnState) RecordGrantedPermissions(environmentID string, permissions map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.grantedPermissionsByEnvID[environmentID]
	if current == nil {
		current = map[string]any{}
	}
	for key, value := range permissions {
		current[key] = value
	}
	s.grantedPermissionsByEnvID[environmentID] = current
}

func (s *TurnState) GrantedPermissions(environmentID string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAnyMap(s.grantedPermissionsByEnvID[environmentID])
}

func (s *TurnState) SetStrictAutoReviewEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strictAutoReviewEnabled = enabled
}

func (s *TurnState) StrictAutoReviewEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.strictAutoReviewEnabled
}

func (s *TurnState) IncrementToolCalls() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCalls++
	return s.toolCalls
}

func (s *TurnState) ToolCalls() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCalls
}

func (s *TurnState) RecordMemoryCitation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasMemoryCitation = true
}

func (s *TurnState) HasMemoryCitation() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasMemoryCitation
}

type SessionState struct {
	mu                        sync.Mutex
	history                   []any
	latestRateLimits          map[string]any
	serverReasoningIncluded   bool
	mcpDependencyPrompted     map[string]bool
	previousTurnSettings      map[string]any
	autoCompactWindow         *compact.Window
	activeConnectorSelection  map[string]bool
	pendingSessionStartSource []string
	grantedPermissionsByEnvID map[string]map[string]any
	nextTurnIsFirst           bool
}

func NewSessionState(threadID string) *SessionState {
	return &SessionState{
		mcpDependencyPrompted:     map[string]bool{},
		autoCompactWindow:         compact.NewWindow(threadID),
		activeConnectorSelection:  map[string]bool{},
		grantedPermissionsByEnvID: map[string]map[string]any{},
		nextTurnIsFirst:           true,
	}
}

func (s *SessionState) RecordItems(items ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, items...)
}

func (s *SessionState) CloneHistory() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]any(nil), s.history...)
}

func (s *SessionState) ReplaceHistory(items []any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append([]any(nil), items...)
	if s.autoCompactWindow != nil {
		s.autoCompactWindow.ClearPrefill()
	}
}

func (s *SessionState) SetPreviousTurnSettings(settings map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previousTurnSettings = cloneAnyMap(settings)
}

func (s *SessionState) PreviousTurnSettings() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAnyMap(s.previousTurnSettings)
}

func (s *SessionState) TakeNextTurnIsFirst() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	first := s.nextTurnIsFirst
	s.nextTurnIsFirst = false
	return first
}

func (s *SessionState) SetNextTurnIsFirst(value bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTurnIsFirst = value
}

func (s *SessionState) AutoCompactWindow() *compact.Window {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autoCompactWindow
}

func (s *SessionState) RecordMCPDependencyPrompted(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		s.mcpDependencyPrompted[name] = true
	}
}

func (s *SessionState) MCPDependencyPrompted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.mcpDependencyPrompted))
	for name := range s.mcpDependencyPrompted {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

func (s *SessionState) MergeConnectorSelection(ids ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.activeConnectorSelection[id] = true
	}
	out := make([]string, 0, len(s.activeConnectorSelection))
	for id := range s.activeConnectorSelection {
		out = append(out, id)
	}
	sortStrings(out)
	return out
}

func (s *SessionState) SetRateLimits(snapshot map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latestRateLimits == nil {
		s.latestRateLimits = map[string]any{}
	}
	for key, value := range snapshot {
		s.latestRateLimits[key] = value
	}
}

func (s *SessionState) RateLimits() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAnyMap(s.latestRateLimits)
}

func (s *SessionState) RecordGrantedPermissions(environmentID string, permissions map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.grantedPermissionsByEnvID[environmentID]
	if current == nil {
		current = map[string]any{}
	}
	for key, value := range permissions {
		current[key] = value
	}
	s.grantedPermissionsByEnvID[environmentID] = current
}

func (s *SessionState) GrantedPermissions(environmentID string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAnyMap(s.grantedPermissionsByEnvID[environmentID])
}

type BackgroundTerminalInfo struct {
	ID        string
	Command   []string
	CWD       string
	StartedAt time.Time
	ExitedAt  *time.Time
	ExitCode  *int
}

type Services struct {
	mu              sync.Mutex
	session         *SessionState
	activeTurn      *ActiveTurn
	tasks           *TaskRegistry
	metrics         *TaskMetrics
	guardianStore   *ReviewStore
	guardianBreaker *CircuitBreaker
	terminals       map[string]BackgroundTerminalInfo
}

func NewServices(threadID string) *Services {
	return &Services{
		session:         NewSessionState(threadID),
		activeTurn:      NewActiveTurn(),
		tasks:           NewTaskRegistry(),
		metrics:         NewTaskMetrics(),
		guardianStore:   NewReviewStore(),
		guardianBreaker: NewCircuitBreaker(),
		terminals:       map[string]BackgroundTerminalInfo{},
	}
}

func (s *Services) Session() *SessionState {
	return s.session
}

func (s *Services) Tasks() *TaskRegistry {
	return s.tasks
}

func (s *Services) Metrics() *TaskMetrics {
	return s.metrics
}

func (s *Services) GuardianStore() *ReviewStore {
	return s.guardianStore
}

func (s *Services) GuardianBreaker() *CircuitBreaker {
	return s.guardianBreaker
}

func (s *Services) StartTask(id string, kind TaskKind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeTurn.Task = &RunningTask{ID: id, Kind: kind, StartedAt: time.Now().UTC()}
}

func (s *Services) FinishTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn.Task == nil || s.activeTurn.Task.ID != id {
		return fmt.Errorf("active task mismatch: %s", id)
	}
	s.activeTurn.Task = nil
	return nil
}

func (s *Services) ActiveTurn() *ActiveTurn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &ActiveTurn{Task: cloneRunningTask(s.activeTurn.Task), TurnState: s.activeTurn.TurnState}
}

func (s *Services) RecordTerminal(info BackgroundTerminalInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminals[info.ID] = info
}

func (s *Services) FinishTerminal(id string, exitCode int, when time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.terminals[id]
	if !ok {
		return false
	}
	info.ExitCode = &exitCode
	info.ExitedAt = &when
	s.terminals[id] = info
	return true
}

func (s *Services) ListTerminals(includeExited bool) []BackgroundTerminalInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BackgroundTerminalInfo, 0, len(s.terminals))
	for _, terminal := range s.terminals {
		if !includeExited && terminal.ExitedAt != nil {
			continue
		}
		out = append(out, cloneTerminal(terminal))
	}
	return out
}

func cloneRunningTask(task *RunningTask) *RunningTask {
	if task == nil {
		return nil
	}
	cloned := *task
	return &cloned
}

func cloneTerminal(info BackgroundTerminalInfo) BackgroundTerminalInfo {
	cloned := info
	cloned.Command = append([]string(nil), info.Command...)
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		current := values[i]
		j := i - 1
		for j >= 0 && values[j] > current {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = current
	}
}
