package appserver

// Rust parity: codex-rs/core/src/context/approved_command_prefix_saved.rs and
// the world-state diff in permissions.rs (Rust 1bbfb5cfad). After an
// exec-policy amendment is approved, the newly saved command prefix is reported
// to the model exactly once as "Approved command prefix saved:" instead of
// re-injecting the full permissions instructions. The report comes from the
// permissions world-state diff (appserver/permissions_world_state.go), which
// compares the thread's accumulated approved prefixes against the persisted
// section snapshot.

import (
	"os"
	"strings"
	"sync"

	"codex_go/execpolicy"
	"codex_go/sandbox"
)

const approvedCommandPrefixSavedMessagePrefix = "Approved command prefix saved:"

type execPolicySavedState struct {
	mu       sync.Mutex
	approved map[string][][]string
}

func newExecPolicySavedState() *execPolicySavedState {
	return &execPolicySavedState{approved: map[string][][]string{}}
}

// remember records a prefix approved during a thread, deduped like Rust's
// per-session exec policy.
func (s *execPolicySavedState) remember(threadID string, prefix []string) {
	if s == nil || len(prefix) == 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prefixes := append(s.approved[threadID], append([]string(nil), prefix...))
	s.approved[threadID] = execpolicy.CanonicalCommandPrefixes(prefixes)
}

// approvedPrefixes returns the prefixes approved during a thread.
func (s *execPolicySavedState) approvedPrefixes(threadID string) [][]string {
	if s == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.approved[threadID]
	out := make([][]string, 0, len(stored))
	for _, prefix := range stored {
		out = append(out, append([]string(nil), prefix...))
	}
	return out
}

func (r *RuntimeRouter) rememberExecPolicyAmendmentSaved(threadID string, turnID string, prefix []string) {
	if r == nil || r.execPolicySaved == nil {
		return
	}
	r.execPolicySaved.remember(threadID, prefix)
}

// approvedCommandPrefixesForThread returns the thread's approved command
// prefixes: the on-disk exec policy's allow prefixes plus the prefixes approved
// through exec-policy amendments during this session (Rust's session
// `exec_policy.current_for_prefix_rules`).
func (r *RuntimeRouter) approvedCommandPrefixesForThread(threadID string) [][]string {
	prefixes := [][]string(nil)
	if policy := r.loadedExecPolicy(); policy != nil {
		prefixes = append(prefixes, policy.AllowedPrefixes()...)
	}
	if r != nil && r.execPolicySaved != nil {
		prefixes = append(prefixes, r.execPolicySaved.approvedPrefixes(threadID)...)
	}
	return execpolicy.CanonicalCommandPrefixes(prefixes)
}

// loadedExecPolicy loads the on-disk exec policy, tolerating a missing file
// (Rust's session policy starts empty when no rules file exists).
func (r *RuntimeRouter) loadedExecPolicy() *execpolicy.Policy {
	if r == nil || r.services.Config == nil {
		return nil
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		return nil
	}
	path := execpolicy.DefaultPolicyPath(codexHome)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	policy, err := execpolicy.LoadPolicies([]string{path})
	if err != nil {
		return nil
	}
	return policy
}

// approvedCommandPrefixSavedText renders Rust's ApprovedCommandPrefixSaved
// fragment body for the newly approved prefixes.
func approvedCommandPrefixSavedText(added [][]string) string {
	rendered := sandbox.FormatAllowPrefixes(execpolicy.CanonicalCommandPrefixes(added))
	if strings.TrimSpace(rendered) == "" {
		return ""
	}
	return approvedCommandPrefixSavedMessagePrefix + "\n" + rendered
}
