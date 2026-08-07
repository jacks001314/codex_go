package agent

import (
	"errors"
	"sync"
	"sync/atomic"
)

var ErrAgentLimitReached = errors.New("agent limit reached")

// ErrAgentDepthLimitReached mirrors Rust's spawn/resume depth rejection:
// the model is told to solve the task itself instead of ending the session.
var ErrAgentDepthLimitReached = errors.New("Agent depth limit reached. Solve the task yourself.")

type MultiAgentVersion string

const (
	VersionV1 MultiAgentVersion = "v1"
	VersionV2 MultiAgentVersion = "v2"
)

type SessionSource string

const (
	SourceCli      SessionSource = "cli"
	SourceSubAgent SessionSource = "subagent"
)

type Operation struct {
	Kind        string
	TriggerTurn bool
}

type ExecutionLimiter struct {
	active     atomic.Uint64
	maxThreads uint64
}

func NewExecutionLimiter(maxThreads uint64) *ExecutionLimiter {
	if maxThreads == 0 {
		maxThreads = ^uint64(0)
	}
	return &ExecutionLimiter{maxThreads: maxThreads}
}

func (l *ExecutionLimiter) HasCapacity() bool {
	return l == nil || l.active.Load() < l.maxThreads
}

func (l *ExecutionLimiter) EnsureCapacity(version MultiAgentVersion, source SessionSource) error {
	if !IsExecutionLimited(version, source) {
		return nil
	}
	if l.HasCapacity() {
		return nil
	}
	return ErrAgentLimitReached
}

func (l *ExecutionLimiter) Guard(version MultiAgentVersion, source SessionSource) *ExecutionGuard {
	if l == nil || !IsExecutionLimited(version, source) {
		return nil
	}
	l.active.Add(1)
	return &ExecutionGuard{limiter: l}
}

func (l *ExecutionLimiter) Active() uint64 {
	if l == nil {
		return 0
	}
	return l.active.Load()
}

type ExecutionGuard struct {
	limiter *ExecutionLimiter
	once    sync.Once
}

func (g *ExecutionGuard) Release() {
	if g == nil || g.limiter == nil {
		return
	}
	g.once.Do(func() {
		g.limiter.active.Add(^uint64(0))
	})
}

func OpStartsTurn(op *Operation) bool {
	if op == nil {
		return false
	}
	return op.Kind == "user_input" || (op.Kind == "inter_agent_communication" && op.TriggerTurn)
}

func IsExecutionLimited(version MultiAgentVersion, source SessionSource) bool {
	return version == VersionV2 && source == SourceSubAgent
}

type Residency struct {
	mu           sync.Mutex
	residents    []string
	pendingSlots int
}

func NewResidency() *Residency {
	return &Residency{}
}

func (r *Residency) TryReservePendingSlot(capacity int) (*ResidencySlot, bool) {
	if r == nil {
		return nil, false
	}
	if capacity <= 0 {
		capacity = int(^uint(0) >> 1)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.residents)+r.pendingSlots >= capacity {
		return nil, false
	}
	r.pendingSlots++
	return &ResidencySlot{residency: r, active: true}, true
}

func (r *Residency) Touch(threadID string) {
	if r == nil || threadID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touchLocked(threadID)
}

func (r *Residency) Remove(threadID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.residents = removeAgentControlString(r.residents, threadID)
}

func (r *Residency) PopLRUCandidate(protectedThreadID string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	candidates := len(r.residents)
	for i := 0; i < candidates; i++ {
		candidate := r.residents[0]
		r.residents = r.residents[1:]
		if candidate == protectedThreadID {
			r.residents = append(r.residents, candidate)
			continue
		}
		return candidate, true
	}
	return "", false
}

func (r *Residency) ResidentCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.residents)
}

func (r *Residency) PendingSlotCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pendingSlots
}

func (r *Residency) commitSlot(threadID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pendingSlots > 0 {
		r.pendingSlots--
	}
	r.touchLocked(threadID)
}

func (r *Residency) releasePendingSlot() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pendingSlots > 0 {
		r.pendingSlots--
	}
}

func (r *Residency) touchLocked(threadID string) {
	r.residents = removeAgentControlString(r.residents, threadID)
	r.residents = append(r.residents, threadID)
}

type ResidencySlot struct {
	residency *Residency
	active    bool
	once      sync.Once
}

func (s *ResidencySlot) Commit(threadID string) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.active {
			s.residency.commitSlot(threadID)
			s.active = false
		}
	})
}

func (s *ResidencySlot) Release() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.active {
			s.residency.releasePendingSlot()
			s.active = false
		}
	})
}

func IsV2ResidentSessionSource(source SessionSource) bool {
	return source == SourceSubAgent
}

func removeAgentControlString(values []string, needle string) []string {
	out := values[:0]
	for _, value := range values {
		if value != needle {
			out = append(out, value)
		}
	}
	return out
}
