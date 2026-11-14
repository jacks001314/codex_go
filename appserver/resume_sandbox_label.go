package appserver

import (
	"codex_go/sandbox"
	"log/slog"
)

// resumeSandboxLabelMatches uses the active profile's label without constructing
// a legacy sandbox policy. External sandboxes satisfy full-access requests.
func resumeSandboxLabelMatches(requested string, profile *sandbox.PermissionProfile, cwd string) bool {
	label := profile.LegacySandboxPolicyLabel(cwd)
	return requested == label || requested == "danger-full-access" && label == "external-sandbox"
}

func (r *RuntimeRouter) warnRunningResumeSandboxOverride(params *ThreadResumeParams, turnID string) {
	if params == nil || params.Sandbox == nil || r.threads == nil {
		return
	}
	requested, err := parseTurnSandboxPolicy(params.Sandbox)
	if err != nil || requested == nil {
		return
	}
	var raw, cwd string
	r.threads.UpdateTurn(params.ThreadID, turnID, func(active *activeRuntimeTurn) {
		if active == nil {
			return
		}
		if active.Params != nil {
			cwd = active.Params.CWD
		}
		if active.RunConfig != nil {
			raw = active.RunConfig.PermissionProfileJSON
		}
	})
	if raw == "" {
		return
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(raw)
	if err != nil {
		return
	}
	if !resumeSandboxLabelMatches(string(requested.Kind), profile, cwd) {
		slog.Warn("thread/resume sandbox override ignored for running thread",
			"thread_id", params.ThreadID, "requested", requested.Kind, "active", profile.LegacySandboxPolicyLabel(cwd))
	}
}
