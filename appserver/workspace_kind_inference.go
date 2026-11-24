package appserver

import (
	"context"
	"strings"

	"codex_go/turn"
)

// inferredWorkspaceKind derives a TUI composer turn's workspace kind when the
// client did not send one (Rust #63545): project when the thread is assigned
// to a project, projectless otherwise. Helper requests without a client
// message id and connections other than the TUI keep no inferred value.
func (r *RuntimeRouter) inferredWorkspaceKind(connectionID string, params *turn.TurnStartParams) string {
	if r == nil || params == nil {
		return ""
	}
	if strings.TrimSpace(params.ClientUserMessageID) == "" {
		return ""
	}
	info, ok := r.connectionClientInfo(connectionID)
	if !ok || strings.TrimSpace(info.Name) != "codex-tui" {
		return ""
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" || r.services.StateRuntime == nil {
		return ""
	}
	assigned, err := r.services.StateRuntime.ThreadHasProject(context.Background(), threadID, nil)
	if err != nil {
		return ""
	}
	if assigned {
		return "project"
	}
	return "projectless"
}
