package tool

import (
	"context"
	"testing"
)

func TestRouterFinalizedPlanCapabilitiesLikeRust(t *testing.T) {
	registry := NewRegistry()
	for _, spec := range []Spec{
		{Name: PlainName("exec_command")},
		{Name: PlainName("write_stdin")},
		{Name: NamespacedName("collaboration", "spawn_agent")},
		{Name: NamespacedName("collaboration", "send_message")},
		{Name: NamespacedName("collaboration", "followup_task")},
		{Name: NamespacedName("collaboration", "wait_agent")},
	} {
		if err := registry.Register(NewExecutorFunc(spec, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true}, nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(registry)
	namespace := "collaboration"
	router.SetToolPlan("code_mode_only", map[string]CodeModeToolNameMetadata{
		"spawn_agent": {Name: "spawn_agent", Namespace: &namespace},
	}, ToolNamespacesInfo{"functions": {}})

	if router.ToolMode() != "code_mode_only" || !router.RequiresCodeModeWorker() {
		t.Fatalf("mode = %q worker = %v", router.ToolMode(), router.RequiresCodeModeWorker())
	}
	if !router.HasTerminalControls() || !router.CanManageChildren() {
		t.Fatalf("terminal=%v children=%v", router.HasTerminalControls(), router.CanManageChildren())
	}
	if info := router.ToolNamespacesInfo(); info == nil {
		t.Fatal("finalized namespace inventory was not retained")
	}
	router.SetToolMode("direct")
	if router.RequiresCodeModeWorker() || router.ToolMode() != "direct" {
		t.Fatalf("updated mode = %q", router.ToolMode())
	}
}
