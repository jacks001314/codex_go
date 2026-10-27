package tool

import "testing"

func TestIsStateChangingMultiAgentToolLikeRust(t *testing.T) {
	team := "team"
	empty := ""
	for _, test := range []struct {
		name      string
		tool      ToolName
		namespace *string
		want      bool
	}{
		{name: "state-changing tool in configured namespace", tool: NamespacedName(team, "spawn_agent"), namespace: &team, want: true},
		{name: "send_message", tool: NamespacedName(team, "send_message"), namespace: &team, want: true},
		{name: "followup_task", tool: NamespacedName(team, "followup_task"), namespace: &team, want: true},
		{name: "interrupt_agent", tool: NamespacedName(team, "interrupt_agent"), namespace: &team, want: true},
		{name: "wait_agent stays read-only", tool: NamespacedName(team, "wait_agent"), namespace: &team},
		{name: "list_agents stays read-only", tool: NamespacedName(team, "list_agents"), namespace: &team},
		{name: "configured namespace mismatch", tool: NamespacedName("other", "send_message"), namespace: &team},
		{name: "default namespace with no configured namespace", tool: NamespacedName("", "send_message"), want: true},
		{name: "functions namespace with no configured namespace", tool: NamespacedName("functions", "send_message"), want: true},
		{name: "custom namespace with no configured namespace", tool: NamespacedName("other", "send_message")},
		{name: "empty configured namespace is exact", tool: NamespacedName("", "send_message"), namespace: &empty, want: true},
		{name: "empty configured namespace rejects functions", tool: NamespacedName("functions", "send_message"), namespace: &empty},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsStateChangingMultiAgentTool(test.tool, test.namespace); got != test.want {
				t.Fatalf("IsStateChangingMultiAgentTool(%s) = %v, want %v", test.tool.Key(), got, test.want)
			}
		})
	}
}
