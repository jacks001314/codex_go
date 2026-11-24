package exec

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"codex_go/config"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
)

// execResumePermissionState is the sandbox selection a resumed `codex exec`
// thread saved in its rollout (Rust #62194 PersistedResumeSettings).
type execResumePermissionState struct {
	// Found reports that the rollout recorded thread settings at all.
	Found bool
	// ActiveID is the named or built-in permission profile the thread used.
	ActiveID string
	// Unnamed is the raw inferred permission profile recorded for a thread
	// that used no named selection.
	Unnamed json.RawMessage
	// ApprovalPolicy and ApprovalsReviewer are the approvals the thread last
	// saved, which a resume inherits unless this run selects its own
	// (Rust #63515).
	ApprovalPolicy    string
	ApprovalsReviewer string
}

// execResumePermissionState loads the saved permission selection for a resumed
// record. An unreadable rollout simply restores nothing, which falls back to
// the read-only recovery below.
func (r *Runner) execResumePermissionState(record *session.Record) execResumePermissionState {
	state := execResumePermissionState{}
	if r == nil || record == nil {
		return state
	}
	path, found, err := rollout.FindRolloutPathByRolloutID(r.CodexHome, string(record.ID))
	if err != nil || !found {
		return state
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return state
	}
	persisted, ok := rollout.LatestPersistedResumeSettings(string(record.ID), lines)
	if !ok {
		return state
	}
	state.Found = true
	state.ApprovalPolicy = strings.TrimSpace(persisted.ApprovalPolicy)
	state.ApprovalsReviewer = strings.TrimSpace(persisted.ApprovalsReviewer)
	if len(persisted.ActivePermissionProfile) > 0 {
		var active sandbox.ActivePermissionProfile
		if err := json.Unmarshal(persisted.ActivePermissionProfile, &active); err == nil {
			state.ActiveID = strings.TrimSpace(active.ID)
		}
	}
	if len(persisted.UnnamedPermissionProfile) > 0 {
		state.Unnamed = append(json.RawMessage(nil), persisted.UnnamedPermissionProfile...)
	}
	return state
}

// execAdditionalWorkspaceRoots reports the directories requested with --add-dir
// as absolute paths relative to the run's working directory.
func execAdditionalWorkspaceRoots(req *Request, cwd string) []string {
	if req == nil {
		return nil
	}
	seen := map[string]bool{}
	var roots []string
	for _, raw := range append(append([]string(nil), req.Exec.Shared.AddDirs...), req.Root.Shared.AddDirs...) {
		root := strings.TrimSpace(raw)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(cwd, root)
		}
		root = filepath.Clean(root)
		if seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

// resolveExecPermissionProfileForRun resolves the permission profile of one
// exec run. An explicit --sandbox selection wins; otherwise a resume restores
// the selection saved by the thread, and a history without one falls back to
// the built-in read-only profile instead of widening to the current config
// default (Rust #62194).
func (r *Runner) resolveExecPermissionProfileForRun(cfg *config.Config, req *Request, resume *execResumePermissionState) (*config.SandboxPermissionProfileResolution, error) {
	if cfg == nil {
		return nil, nil
	}
	cwd := requestCWD(req)
	addDirs := execAdditionalWorkspaceRoots(req, cwd)
	roots := append([]string{cwd}, addDirs...)
	if id := execSandboxProfileID(req); id != "" {
		return resolveExecPermissionProfileWithAddDirs(cfg, id, cwd, roots, addDirs)
	}
	if resume == nil {
		return resolveExecPermissionProfileWithAddDirs(cfg, "", cwd, roots, addDirs)
	}
	if resume.ActiveID != "" {
		resolution, err := resolveExecPermissionProfileWithAddDirs(cfg, resume.ActiveID, cwd, roots, addDirs)
		if err != nil {
			return nil, fmt.Errorf("saved permission profile %q cannot be loaded; pass --sandbox to choose one explicitly: %w", resume.ActiveID, err)
		}
		return resolution, nil
	}
	if len(resume.Unnamed) > 0 {
		profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(resume.Unnamed))
		if err != nil {
			return nil, fmt.Errorf("saved sandbox permissions cannot be restored; pass --sandbox to choose one explicitly: %w", err)
		}
		resolution := &config.SandboxPermissionProfileResolution{
			Profile:        profile,
			ProfileJSON:    string(resume.Unnamed),
			WorkspaceRoots: roots,
		}
		appendExecWritableRoots(resolution, cwd, addDirs)
		return resolution, nil
	}
	// Older histories recorded no permission selection; they ran read-only.
	return resolveExecPermissionProfileWithAddDirs(cfg, sandbox.BuiltInPermissionProfileReadOnly, cwd, roots, addDirs)
}

// validateExecRequirementConstrainedSandbox mirrors Rust #63515's final guard:
// an explicit full-access selector may be downgraded to read-only by managed
// requirements, but it must not then run with approvals disabled. The server
// or local exec would otherwise hide the rejected full-access request behind a
// read-only fallback whose approval policy can never ask for access.
func validateExecRequirementConstrainedSandbox(cfg *config.Config, req *Request, effective *config.SandboxPermissionProfileResolution, approvalPolicy sandbox.AskForApproval) error {
	if cfg == nil || cfg.Requirements == nil || approvalPolicy != sandbox.ApprovalNever {
		return nil
	}
	requestedID := execSandboxProfileID(req)
	if requestedID == "" || cfg.PermissionProfileRequirementWarningFor(requestedID) == "" {
		return nil
	}
	if effective == nil || effective.Profile == nil || effective.Profile.Disabled ||
		effective.Profile.SandboxPolicy == nil || effective.Profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		return nil
	}
	cwd := requestCWD(req)
	addDirs := execAdditionalWorkspaceRoots(req, cwd)
	roots := append([]string{cwd}, addDirs...)
	// Resolve the explicit selection without requirements to distinguish a
	// deliberately read-only request from a downgraded full-access one.
	unconstrained := *cfg
	unconstrained.Requirements = nil
	original, err := resolveExecPermissionProfileWithAddDirs(
		&unconstrained,
		requestedID,
		cwd,
		roots,
		addDirs,
	)
	if err != nil {
		return err
	}
	if original == nil || original.Profile == nil || !original.Profile.Disabled {
		return nil
	}
	return errors.New("`approval_policy = \"never\"` cannot be used because requirements do not allow `sandbox_mode = \"danger-full-access\"`; Codex would fall back to read-only permissions with approvals disabled. Choose an `approval_policy` based on what you need, such as `on-request`, or choose an allowed sandbox mode.")
}

func resolveExecPermissionProfileWithAddDirs(cfg *config.Config, profileID string, cwd string, roots []string, addDirs []string) (*config.SandboxPermissionProfileResolution, error) {
	resolution, err := cfg.ResolveSandboxPermissionProfileWithWorkspaceRoots(profileID, cwd, roots)
	if err != nil {
		return nil, err
	}
	appendExecWritableRoots(resolution, cwd, addDirs)
	return resolution, nil
}

// appendExecWritableRoots honors --add-dir on both profile shapes: the legacy
// workspace-write policy carries an explicit writable-root list, while a
// managed profile binds its symbolic project roots to these directories.
func appendExecWritableRoots(resolution *config.SandboxPermissionProfileResolution, cwd string, addDirs []string) {
	if resolution == nil || resolution.Profile == nil || len(addDirs) == 0 || resolution.Profile.Disabled {
		return
	}
	wanted := append([]string{cwd}, addDirs...)
	if policy := resolution.Profile.SandboxPolicy; policy != nil && policy.Kind == sandbox.SandboxWorkspaceWrite {
		seen := map[string]bool{}
		var writable []string
		for _, root := range append(append([]string(nil), policy.WritableRoots...), wanted...) {
			root = strings.TrimSpace(root)
			if root == "" || seen[root] {
				continue
			}
			seen[root] = true
			writable = append(writable, root)
		}
		policy.WritableRoots = writable
		if len(resolution.WorkspaceRoots) == 0 {
			resolution.WorkspaceRoots = append([]string(nil), writable...)
		}
		return
	}
	if materialized, err := sandbox.MaterializePermissionProfileWorkspaceRoots(resolution.Profile, cwd, wanted); err == nil && materialized != nil {
		resolution.Profile = materialized
		if len(resolution.WorkspaceRoots) == 0 {
			resolution.WorkspaceRoots = append([]string(nil), wanted...)
		}
	}
}
