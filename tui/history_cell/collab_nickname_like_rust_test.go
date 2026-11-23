package historycell

import (
	"strings"
	"testing"

	"codex_go/protocol"
)

// Rust #63444: spawn/wait rows show the subagent's nickname once it is known
// and keep the raw thread id until then.
func TestCollabAgentToolCallResolvesNicknamesLikeRust(t *testing.T) {
	receivers := []string{"/root/worker"}
	completed := "completed"
	states := map[string]protocol.CollabAgentState{
		"/root/worker": {Status: completed},
	}
	resolve := func(threadID string) string {
		if threadID == "/root/worker" {
			return "Scout"
		}
		return ""
	}

	spawn, ok := NewCollabAgentToolCallWithResolver(
		ptrCollabItem(protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", receivers, nil, nil, "completed")),
		true,
		resolve,
	)
	if !ok {
		t.Fatal("spawn row did not render")
	}
	if got := spawn.DisplayLines(80); len(got) == 0 || got[0] != "• Spawned Scout" {
		t.Fatalf("spawn row = %#v", got)
	}

	wait, ok := NewCollabAgentToolCallWithResolver(
		ptrCollabItem(protocol.CollabToolCallItem("wait", "wait", "/root", receivers, nil, states, "completed")),
		true,
		resolve,
	)
	if !ok {
		t.Fatal("wait row did not render")
	}
	lines := wait.DisplayLines(80)
	if len(lines) != 2 || lines[1] != "  └ Scout: completed" {
		t.Fatalf("wait row = %#v", lines)
	}

	unknown, ok := NewCollabAgentToolCallWithResolver(
		ptrCollabItem(protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", []string{"/root/other"}, nil, nil, "completed")),
		true,
		resolve,
	)
	if !ok {
		t.Fatal("unknown spawn row did not render")
	}
	if got := unknown.DisplayLines(80); len(got) == 0 || !strings.Contains(got[0], "/root/other") {
		t.Fatalf("unresolved row should keep the thread id: %#v", got)
	}
}

func ptrCollabItem(item protocol.ThreadItem) *protocol.ThreadItem {
	return &item
}
