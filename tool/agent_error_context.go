package tool

import "errors"

// Rust parity: codex-rs/protocol/src/agent_error.rs (#51355, `c0c230e673`).
//
// Bounded agent failure diagnostics, carried separately from an error's
// behavior and its user-visible message. Spawn failures can share an error kind
// and message despite different causes (a capacity limit against a registry
// rejection, for example), so the failure origin travels on the error itself as
// a bounded label that telemetry can use instead of a message or a correlation
// id. The first annotation wins, so an outer spawn stage cannot overwrite the
// more precise origin an inner stage recorded (Rust's `CodexErr::with_agent_context`
// uses `Option::get_or_insert`).
type AgentErrorContext string

// The bounded failure origins, spelled the way Rust's `AgentErrorContext`
// serializes (`#[strum(serialize_all = "snake_case")]`) so a failure reports the
// same label upstream and here.
//
// Rust's `ExecutionCapacity`, `ResidencyCapacity` and `RuntimeShutdown` arms
// have no Go producer, so they are not declared here:
//
//   - `agent.ExecutionLimiter` (Rust `agent_execution_limiter`) and
//     `agent.Residency` (Rust `V2Residency`) exist in Go but nothing in the
//     spawn path consults them: `rg -n 'EnsureCapacity\(|TryReservePendingSlot' --glob '*.go'`
//     matches only their definitions and their own tests.
//   - The app-server spawn path has no shutdown fence (only `codex exec`'s
//     controller checks `shuttingDown`), so no spawn failure reports it.
const (
	// AgentErrorContextRegistryCapacity is the spawn-slot limit Rust reports as
	// `RegistryCapacity` (core/src/agent/registry.rs `reserve_spawn_slot`).
	AgentErrorContextRegistryCapacity AgentErrorContext = "registry_capacity"
	// AgentErrorContextDuplicatePath is an agent path that is already taken
	// (Rust's `DuplicatePath`, registry.rs `reserve_agent_path`).
	AgentErrorContextDuplicatePath AgentErrorContext = "duplicate_path"
	// AgentErrorContextNicknameUnavailable is an exhausted nickname pool (Rust's
	// `NicknameUnavailable`, registry.rs `reserve_agent_nickname`).
	AgentErrorContextNicknameUnavailable AgentErrorContext = "nickname_unavailable"
	// AgentErrorContextManagerUnavailable is a spawn whose thread manager is gone
	// (Rust's `ManagerUnavailable`, control/runtime_context.rs `upgrade`).
	AgentErrorContextManagerUnavailable AgentErrorContext = "manager_unavailable"
	// AgentErrorContextForkHistory is a full-history fork that could not be
	// prepared or persisted (Rust's `ForkHistory`, control/spawn.rs
	// `spawn_forked_thread`).
	AgentErrorContextForkHistory AgentErrorContext = "fork_history"
	// AgentErrorContextChildStartup is a child thread that could not be created
	// (Rust's `ChildStartup`, control/spawn.rs `spawn_new_thread`/`create_thread`).
	AgentErrorContextChildStartup AgentErrorContext = "child_startup"
	// AgentErrorContextInputAdmission is the child's initial input that could not
	// be admitted (Rust's `InputAdmission`, control/spawn.rs `send_input`).
	AgentErrorContextInputAdmission AgentErrorContext = "input_admission"
)

// agentContextError annotates an existing error without replacing it: Error()
// and Unwrap() keep the wrapped chain (and therefore every errors.Is/As and the
// user-visible message) intact.
type agentContextError struct {
	err     error
	context AgentErrorContext
}

func (e *agentContextError) Error() string { return e.err.Error() }

func (e *agentContextError) Unwrap() error { return e.err }

func (e *agentContextError) AgentErrorContext() AgentErrorContext { return e.context }

// WithAgentErrorContext attaches bounded diagnostics to err, preserving a more
// precise annotation the error already carries. A nil error and an empty
// context are returned unchanged.
func WithAgentErrorContext(err error, context AgentErrorContext) error {
	if err == nil || context == "" {
		return err
	}
	if _, ok := AgentErrorContextOf(err); ok {
		return err
	}
	return &agentContextError{err: err, context: context}
}

// AgentErrorContextOf reports the bounded failure origin attached to err.
func AgentErrorContextOf(err error) (AgentErrorContext, bool) {
	var carrier interface {
		AgentErrorContext() AgentErrorContext
	}
	if errors.As(err, &carrier) {
		if context := carrier.AgentErrorContext(); context != "" {
			return context, true
		}
	}
	return "", false
}
