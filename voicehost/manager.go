package voicehost

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State describes the lifecycle position of one voice session.
type State string

const (
	// StateCreated is the initial state before a runtime or transport starts.
	StateCreated State = "created"
	// StateNegotiating means SDP exchange is in progress.
	StateNegotiating State = "negotiating"
	// StateStreaming means the ordered event channel is open and audio may flow.
	StateStreaming State = "streaming"
	// StateReconnecting means the session lost its transport and is retrying.
	StateReconnecting State = "reconnecting"
	// StateClosed is terminal; a closed session cannot transition further.
	StateClosed State = "closed"
)

var (
	// ErrSessionExists indicates a session id is already active.
	ErrSessionExists = errors.New("voice session already exists")
	// ErrSessionNotFound indicates a session id is not active.
	ErrSessionNotFound = errors.New("voice session not found")
	// ErrInvalidStateTransition indicates a transition violates the state machine.
	ErrInvalidStateTransition = errors.New("invalid voice session state transition")
)

// Session is an immutable snapshot of session state. Callers hold the returned
// value without a lock.
type Session struct {
	ID             string
	State          State
	RuntimeName    string
	InputDeviceID  string
	OutputDeviceID string
	Format         AudioFormat
	StartedAt      *time.Time
	LastActivity   *time.Time
	ClosedAt       *time.Time
	CloseReason    string
}

// Manager tracks the small number of voice sessions owned by a process. It is
// safe for concurrent use and only exposes snapshots, never live pointers.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewManager returns an empty session manager.
func NewManager() *Manager {
	return &Manager{sessions: map[string]*Session{}}
}

// Create registers a session in the created state. It fails if the id is
// already active.
func (m *Manager) Create(id string) (*Session, error) {
	if m == nil || id == "" {
		return nil, ErrSessionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; ok {
		return nil, ErrSessionExists
	}
	now := time.Now().UTC()
	session := &Session{ID: id, State: StateCreated, StartedAt: &now, LastActivity: &now}
	m.sessions[id] = session
	return m.cloneLocked(session), nil
}

// Session returns a snapshot for id.
func (m *Manager) Session(id string) (*Session, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	return m.cloneLocked(session), true
}

// Transition moves a session from wantState to nextState. It returns
// ErrSessionNotFound or ErrInvalidStateTransition without mutating state on a
// mismatch.
func (m *Manager) Transition(id string, wantState, nextState State) (*Session, error) {
	if m == nil {
		return nil, ErrSessionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if session.State != wantState || !validTransition(wantState, nextState) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidStateTransition, session.State, nextState)
	}
	now := time.Now().UTC()
	session.State = nextState
	session.LastActivity = &now
	if nextState == StateClosed {
		session.ClosedAt = &now
	}
	return m.cloneLocked(session), nil
}

// Close marks a non-terminal session closed with a reason. It returns the
// current snapshot without mutating an already closed session.
func (m *Manager) Close(id string, reason string) (*Session, error) {
	if m == nil {
		return nil, ErrSessionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if session.State != StateClosed {
		now := time.Now().UTC()
		session.State = StateClosed
		session.ClosedAt = &now
		session.LastActivity = &now
		session.CloseReason = reason
	}
	return m.cloneLocked(session), nil
}

// UpdateSnapshot applies update to the live session under the manager lock and
// returns the resulting snapshot.
func (m *Manager) UpdateSnapshot(id string, update func(*Session)) (*Session, error) {
	if m == nil {
		return nil, ErrSessionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if update != nil {
		update(session)
	}
	return m.cloneLocked(session), nil
}

// Delete removes a session. Deleting an unknown id returns ErrSessionNotFound.
func (m *Manager) Delete(id string) error {
	if m == nil {
		return ErrSessionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; !ok {
		return ErrSessionNotFound
	}
	delete(m.sessions, id)
	return nil
}

// List returns snapshots in no guaranteed order.
func (m *Manager) List() []*Session {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshots := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		snapshots = append(snapshots, m.cloneLocked(session))
	}
	return snapshots
}

func validTransition(from, to State) bool {
	if from == to {
		return false
	}
	if from == StateClosed {
		return false
	}
	if to == StateClosed {
		return true
	}
	switch from {
	case StateCreated:
		return to == StateNegotiating
	case StateNegotiating:
		return to == StateStreaming
	case StateStreaming:
		return to == StateReconnecting
	case StateReconnecting:
		return to == StateStreaming
	default:
		return false
	}
}

func (m *Manager) cloneLocked(session *Session) *Session {
	if session == nil {
		return nil
	}
	clone := *session
	clone.StartedAt = cloneTime(session.StartedAt)
	clone.LastActivity = cloneTime(session.LastActivity)
	clone.ClosedAt = cloneTime(session.ClosedAt)
	return &clone
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
