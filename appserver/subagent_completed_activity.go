package appserver

import (
	"strings"

	"codex_go/agent"
)

// parentAgentPathOf returns the agent path of a child agent's direct parent:
// the path with its last segment removed (Rust
// `AgentPath::as_str().rsplit_once('/')` in
// agent/control/completion.rs:44-49). It reports false when the path has no
// parent segment, which is Rust's `AgentPath::try_from(parent).ok()` returning
// `None`.
func parentAgentPathOf(agentPath string) (string, bool) {
	agentPath = strings.TrimSpace(agentPath)
	if !strings.HasPrefix(agentPath, "/") {
		return "", false
	}
	index := strings.LastIndex(agentPath, "/")
	if index <= 0 {
		return "", false
	}
	return agentPath[:index], true
}

// agentThreadIDForPath resolves an agent path to its live thread, mirroring
// Rust `AgentControl::resolve_agent_reference` for the completion routing
// (agent/control/completion.rs:54-63). A path no live agent owns resolves to
// false, which makes the caller drop the activity notice rather than misroute
// it.
func (r *RuntimeRouter) agentThreadIDForPath(agentPath string) (string, bool) {
	if r == nil || r.agentRegistry == nil {
		return "", false
	}
	path := strings.TrimSpace(agentPath)
	if path == "" {
		return "", false
	}
	return r.agentRegistry.AgentIDForPath(agent.AgentPath(path))
}
