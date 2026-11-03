package appserver

import "codex_go/execpolicy"

// execPolicyForThread returns the exec policy a turn's shell calls are matched
// against. Rust's ExecPolicyManager loads the user/project rules once and
// merges the managed requirements on top; Go keeps the world-state view and
// rebuilds the same merge for each turn so rule-file edits and environment
// overlays take effect without restarting the thread.
func (r *RuntimeRouter) execPolicyForThread(threadID string) *execpolicy.Policy {
	if r == nil {
		return nil
	}
	user := r.loadedExecPolicy()
	managed := r.managedCommandPolicyForThread(threadID)
	switch {
	case managed == nil:
		return user
	case user == nil:
		return managed.Policy()
	default:
		return execpolicy.NewRequirementsPolicy(user).MergeOverlay(managed).Policy()
	}
}
