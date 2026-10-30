package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"codex_go/execpolicy"
	"codex_go/sandbox"
)

// ErrToolCallNotActive reports a lookup for a call that ended, was cancelled or
// never ran under a registry that opts into host lookup (Rust #57586).
var ErrToolCallNotActive = errors.New("tool call is not active")

// HostToolInvocationLookup opts a session into live V2 tool-call lookup and
// requires unique active call IDs within each turn. Host-spawned children
// inherit the opt-in (Rust agent/host/invocation.rs).
type HostToolInvocationLookup struct{}

// InvocationRegistry tracks the live call IDs of each turn. Rust stores this in
// thread extension data; Go exposes it directly so a host can install it on a
// turn dispatcher.
type InvocationRegistry struct {
	mu      sync.Mutex
	entries map[invocationKey]*invocationEntry
}

type invocationKey struct {
	turnID string
	callID string
}

type invocationEntry struct {
	mu        sync.Mutex
	finished  chan struct{}
	activated bool
	cancelled bool
}

// InvocationReservation keeps a dispatch's call ID reserved until the terminal
// callback returns. Releasing it removes the key so a later turn can reuse the
// ID.
type InvocationReservation struct {
	registry *InvocationRegistry
	key      invocationKey
}

// InvocationGuard revokes host access when execution ends, including unwinding
// or cancellation.
type InvocationGuard struct {
	entry *invocationEntry
	once  sync.Once
}

// ToolInvocationContext is a handle to one live tool call. It remains usable
// only until dispatch ends.
type ToolInvocationContext struct {
	registry *InvocationRegistry
	key      invocationKey
	entry    *invocationEntry
	// ThreadID, TurnID and CallID identify the call this handle refers to so a
	// host can confirm a child request belongs to the issuing runtime before
	// preparing or starting it (Rust #57586).
	ThreadID string
	TurnID   string
	CallID   string
}

// NewInvocationRegistry returns an empty opt-in registry.
func NewInvocationRegistry() *InvocationRegistry {
	return &InvocationRegistry{entries: map[invocationKey]*invocationEntry{}}
}

// Reserve claims callID for turnID. A duplicate active ID in the same turn is
// rejected with the model-visible message Rust produces.
func (r *InvocationRegistry) Reserve(turnID string, callID string) (*InvocationReservation, error) {
	if r == nil {
		return nil, nil
	}
	key := invocationKey{turnID: turnID, callID: callID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[invocationKey]*invocationEntry{}
	}
	if _, exists := r.entries[key]; exists {
		return nil, fmt.Errorf("tool call ID %s is already active in this turn", callID)
	}
	r.entries[key] = &invocationEntry{finished: make(chan struct{})}
	return &InvocationReservation{registry: r, key: key}, nil
}

// Activate publishes the live context after pre-tool hooks admit the call.
func (reservation *InvocationReservation) Activate() *InvocationGuard {
	if reservation == nil || reservation.registry == nil {
		return nil
	}
	reservation.registry.mu.Lock()
	entry := reservation.registry.entries[reservation.key]
	reservation.registry.mu.Unlock()
	if entry == nil {
		return nil
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.cancelled || entry.activated {
		return nil
	}
	entry.activated = true
	return &InvocationGuard{entry: entry}
}

// Release removes the reserved key. It is safe to call more than once.
func (reservation *InvocationReservation) Release() {
	if reservation == nil || reservation.registry == nil {
		return
	}
	registry := reservation.registry
	registry.mu.Lock()
	delete(registry.entries, reservation.key)
	registry.mu.Unlock()
}

// Release revokes the observation. Subsequent lookups report that the call is
// no longer active.
func (guard *InvocationGuard) Release() {
	if guard == nil || guard.entry == nil {
		return
	}
	guard.once.Do(func() {
		guard.entry.mu.Lock()
		if !guard.entry.cancelled {
			guard.entry.cancelled = true
			close(guard.entry.finished)
		}
		guard.entry.mu.Unlock()
	})
}

// LookupToolInvocation returns the live invocation for (turnID, callID) on
// threadID. It fails when the registry never tracked the call, the call has not
// been published yet, or dispatch already ended.
func (r *InvocationRegistry) LookupToolInvocation(threadID string, turnID string, callID string) (*ToolInvocationContext, error) {
	if r == nil {
		return nil, ErrToolCallNotActive
	}
	key := invocationKey{turnID: turnID, callID: callID}
	r.mu.Lock()
	entry := r.entries[key]
	r.mu.Unlock()
	if entry == nil {
		return nil, ErrToolCallNotActive
	}
	entry.mu.Lock()
	active := entry.activated && !entry.cancelled
	entry.mu.Unlock()
	if !active {
		return nil, ErrToolCallNotActive
	}
	return &ToolInvocationContext{
		registry: r,
		key:      key,
		entry:    entry,
		ThreadID: threadID,
		TurnID:   turnID,
		CallID:   callID,
	}, nil
}

// Active reports whether this handle still refers to a live call.
func (context *ToolInvocationContext) Active() bool {
	if context == nil || context.entry == nil {
		return false
	}
	context.entry.mu.Lock()
	defer context.entry.mu.Unlock()
	return context.entry.activated && !context.entry.cancelled
}

// HostThreadSpawnReservation is a fresh child ID reserved in the host's tree.
// Core derives the parent, session and path; the controller's runtime
// coordination is supplied by the host (Rust agent/host/types.rs).
type HostThreadSpawnReservation struct {
	ThreadID string
}

// ThreadCreateRequest describes the child a host asks the agent runtime to
// create from a live invocation reservation (Rust thread_spawn::ThreadCreateRequest).
type ThreadCreateRequest struct {
	TaskName        string
	AgentType       *string
	Model           *string
	ReasoningEffort *string
	ForkContext     bool
	ForkTurns       *string
}

// PreparedChildCapabilityRoot names one capability root captured from the
// parent invocation (Rust SelectedCapabilityRoot).
type PreparedChildCapabilityRoot struct {
	ID            string
	EnvironmentID string
	Path          string
}

// PreparedChildContext holds the parent inputs captured from a live tool
// invocation for a host starting or forking a child (Rust #58390
// PreparedChildContext). The host binds the destination's services, chooses the
// child's name, role, and history, and applies the requested child settings;
// repository instructions are discovered at the destination.
type PreparedChildContext struct {
	Originator string
	// UserInstructions and ThreadInstructions collapse into Go's single
	// captured developer-instruction field because a live Go step has no
	// separate AGENTS.md instruction providers; the destination rediscovers
	// repository instructions.
	UserInstructions   string
	ThreadInstructions string
	CWD                string
	Environments       []map[string]any
	// SelectedCapabilityRoots are the roots visible to the invoking step.
	SelectedCapabilityRoots []PreparedChildCapabilityRoot
	// ResolvedPermissionProfile is the parent's resolved permission profile at
	// preparation time.
	ResolvedPermissionProfile *sandbox.PermissionProfile
	ApprovalPolicy            string
	ApprovalsReviewer         string
	// ExecPolicy is the parent's merged managed command policy at preparation
	// time. Inherit it only for compatible policy sources.
	ExecPolicy *execpolicy.RequirementsPolicy
	// ExecPolicySourceRequirements is the managed requirement used to load the
	// policy, before any session amendments.
	ExecPolicySourceRequirements *execpolicy.RequirementsPolicy
	// DynamicTools lists the current tool declarations a fresh child can
	// inherit. The host supplies their execution route.
	DynamicTools      []json.RawMessage
	DisabledPluginIDs []string
}
