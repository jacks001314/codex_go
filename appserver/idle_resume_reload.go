package appserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"codex_go/sandbox"
	"codex_go/session"
)

// prepareUnsubscribedIdleResume mirrors Rust #61136. A loaded idle thread
// without subscribers is only a cache entry: when resume asks for settings that
// differ from the live runtime, shut the entry down before dispatch so the cold
// resume reads the rollout again. Matching requests keep the live thread.
func (r *RuntimeRouter) prepareUnsubscribedIdleResume(request *Request) (*Request, error) {
	if r == nil || request == nil || request.Method != MethodThreadResume || r.threads == nil {
		return request, nil
	}
	copyRequest := *request
	if request.InternalParams != nil {
		encoded, err := json.Marshal(request.InternalParams)
		if err != nil {
			return nil, err
		}
		copyRequest.Params = encoded
		copyRequest.InternalParams = nil
	}
	var params ThreadResumeParams
	if err := copyRequest.DecodeParams(&params); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" || !r.threadIsLoaded(threadID) || r.activeRuntimeTurnSnapshot(threadID) != nil ||
		len(r.threads.Subscribers(threadID)) > 0 {
		return request, nil
	}
	settings := r.threadSettingsForTurn(threadID)
	if settings == nil {
		return request, nil
	}
	// A corrupt live snapshot is validated by the subscribed path; here an
	// unavailable profile only means the sandbox override cannot be proven to
	// match, which the comparison already reports as a mismatch.
	profile, _ := r.subscribedIdlePermissionProfile(threadID, settings)
	if len(resumeOverrideMismatchDetails(&params, settings, profile)) == 0 {
		return request, nil
	}
	if err := r.reloadIdleThreadForResume(threadID); err != nil {
		return nil, err
	}
	return request, nil
}

// resumeOverrideMismatchDetails lists the resume overrides that the live
// runtime would ignore, mirroring Rust's collect_resume_override_mismatches.
func resumeOverrideMismatchDetails(params *ThreadResumeParams, settings *Settings, profile *sandbox.PermissionProfile) []string {
	if params == nil || settings == nil {
		return nil
	}
	var mismatches []string
	if params.Model != nil && strings.TrimSpace(*params.Model) != settings.Model {
		mismatches = append(mismatches, "model")
	}
	if params.ModelProvider != nil && strings.TrimSpace(*params.ModelProvider) != settings.ModelProvider {
		mismatches = append(mismatches, "model_provider")
	}
	if params.ServiceTierSet && !sameOptionalString(params.ServiceTier, settings.ServiceTier) {
		mismatches = append(mismatches, "service_tier")
	}
	if params.CWD != nil && strings.TrimSpace(*params.CWD) != "" && !sameAppPath(*params.CWD, settings.CWD) {
		mismatches = append(mismatches, "cwd")
	}
	if params.RuntimeWorkspaceRoots != nil && !samePathList(params.RuntimeWorkspaceRoots, settings.RuntimeWorkspaceRoots) {
		mismatches = append(mismatches, "runtime_workspace_roots")
	}
	if params.ApprovalPolicy != nil && fmt.Sprint(params.ApprovalPolicy) != fmt.Sprint(threadSettingsApprovalPolicy(settings.ApprovalPolicy)) {
		mismatches = append(mismatches, "approval_policy")
	}
	if params.ApprovalsReviewer != nil && strings.TrimSpace(*params.ApprovalsReviewer) != threadSettingsApprovalsReviewer(settings.ApprovalsReviewer) {
		mismatches = append(mismatches, "approvals_reviewer")
	}
	if params.Sandbox != nil {
		requested, err := parseTurnSandboxPolicy(params.Sandbox)
		if err != nil || requested == nil || profile == nil || !resumeSandboxLabelMatches(string(requested.Kind), profile, settings.CWD) {
			mismatches = append(mismatches, "sandbox")
		}
	}
	if params.Permissions != nil {
		mismatches = append(mismatches, "permissions")
	}
	if params.Personality != nil && !sameOptionalString(params.Personality, settings.Personality) {
		mismatches = append(mismatches, "personality")
	}
	if params.Config != nil {
		mismatches = append(mismatches, "config")
	}
	if params.BaseInstructions != nil {
		mismatches = append(mismatches, "base_instructions")
	}
	if params.DeveloperInstructions != nil {
		mismatches = append(mismatches, "developer_instructions")
	}
	return mismatches
}

func sameOptionalString(a *string, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}

func samePathList(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameAppPath(a[i], b[i]) {
			return false
		}
	}
	return true
}

// reloadIdleThreadForResume closes the idle runtime before the manager forgets
// it. A failed shutdown leaves the thread loaded so a retry cannot duplicate a
// thread that timed out during teardown.
func (r *RuntimeRouter) reloadIdleThreadForResume(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || r.threads == nil || threadID == "" {
		return nil
	}
	r.idleLifecycle.mu.Lock()
	r.idleLifecycle.ensureMapsLocked()
	if _, pending := r.idleLifecycle.pending[threadID]; pending {
		r.idleLifecycle.mu.Unlock()
		return fmt.Errorf("%w: thread %s is closing; retry after the thread is closed", session.ErrConflict, threadID)
	}
	done := make(chan struct{})
	r.idleLifecycle.pending[threadID] = done
	r.idleLifecycle.mu.Unlock()

	if closeErr := r.closeLiveThreadRuntimeForResume(threadID); closeErr != nil {
		// Rust keeps the live thread and logs a warning when shutdown cannot be
		// submitted; the resume then continues without applying the overrides.
		slog.Warn("failed to shut down idle thread for resume", "thread_id", threadID, "error", closeErr)
	} else {
		r.markThreadUnloaded(threadID)
		r.threads.ReleaseLiveThreads([]session.ThreadID{session.ThreadID(threadID)})
	}

	r.idleLifecycle.mu.Lock()
	delete(r.idleLifecycle.pending, threadID)
	close(done)
	r.idleLifecycle.mu.Unlock()
	return nil
}

// closeLiveThreadRuntimeForResume closes the resources a live idle thread holds
// and reports the first failure. Registry cleanup stays in markThreadUnloaded.
func (r *RuntimeRouter) closeLiveThreadRuntimeForResume(threadID string) error {
	// Close the rollout writer first: it is the shutdown step whose failure must
	// leave the live thread untouched for a later retry.
	if err := r.threads.CloseLiveThreadsFor([]session.ThreadID{session.ThreadID(threadID)}); err != nil {
		return fmt.Errorf("failed to close idle thread %s runtime: %w", threadID, err)
	}
	if err := r.deleteCodeModeRuntime(threadID); err != nil {
		slog.Warn("failed to close idle thread code-mode runtime", "thread_id", threadID, "error", err)
	}
	if r.mcpRuntimes != nil {
		if err := r.mcpRuntimes.closeThread(threadID); err != nil {
			slog.Warn("failed to close idle thread MCP runtime", "thread_id", threadID, "error", err)
		}
	}
	if err := r.closeThreadManagedNetwork(threadID); err != nil {
		slog.Warn("failed to close idle thread managed network", "thread_id", threadID, "error", err)
	}
	return nil
}
