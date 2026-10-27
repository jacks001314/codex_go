package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex_go/tool"
)

func guardianMultiAgentReviewRegistry(t *testing.T, enabled bool, namespace string) *tool.Registry {
	t.Helper()
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller:                     NewMemoryToolController(),
		Exposure:                       tool.ExposureModelVisible,
		Version:                        VersionV2,
		Namespace:                      namespace,
		WaitConfigured:                 true,
		WaitMin:                        0,
		WaitMax:                        100 * time.Millisecond,
		HideSpawnMetadata:              true,
		ExposeSpawnModelOverrides:      true,
		GuardianNativeMultiAgentReview: enabled,
	}); err != nil {
		t.Fatalf("RegisterMultiAgentHandlersWithOptions: %v", err)
	}
	return registry
}

// Mirrors Rust #57451/#57455: native MAv2 history opts only the state-changing
// calls into Guardian review.
func TestMultiAgentV2GuardianReviewOptInLikeRust(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		tool    string
		want    bool
	}{
		{name: "spawn_agent", enabled: true, tool: "spawn_agent", want: true},
		{name: "send_message", enabled: true, tool: "send_message", want: true},
		{name: "followup_task", enabled: true, tool: "followup_task", want: true},
		{name: "interrupt_agent", enabled: true, tool: "interrupt_agent", want: true},
		{name: "wait_agent", enabled: true, tool: "wait_agent"},
		{name: "list_agents", enabled: true, tool: "list_agents"},
		{name: "feature off", enabled: false, tool: "send_message"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := guardianMultiAgentReviewRegistry(t, test.enabled, "team")
			executor, ok := registry.Lookup(tool.NamespacedName("team", test.tool))
			if !ok {
				t.Fatalf("missing team.%s", test.tool)
			}
			optIn, ok := executor.(tool.GuardianReviewOptIn)
			if !ok {
				t.Fatalf("team.%s executor %T does not opt into Guardian review", test.tool, executor)
			}
			if got := optIn.RequiresGuardianReview(&tool.Invocation{ToolName: tool.NamespacedName("team", test.tool)}); got != test.want {
				t.Fatalf("RequiresGuardianReview() = %v, want %v", got, test.want)
			}
		})
	}
}

// Mirrors Rust #57451: a denied MAv2 review stops dispatch before the handler.
func TestMultiAgentV2GuardianReviewDenialStopsDispatchLikeRust(t *testing.T) {
	registry := guardianMultiAgentReviewRegistry(t, true, "team")
	router := tool.NewRouter(registry)
	var reviewed string
	router.SetGuardianReview(func(_ context.Context, invocation *tool.Invocation) error {
		reviewed = invocation.ToolName.Key()
		return tool.RespondToModel("review denied")
	})
	_, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "call-send",
		ToolName: tool.NamespacedName("team", "send_message"),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"target":"/root","message":"opaque"}`,
		},
	})
	var callErr *tool.FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() || callErr.ModelMessage() != "review denied" {
		t.Fatalf("dispatch error = %v, want the model-facing review denial", err)
	}
	if reviewed != "team.send_message" {
		t.Fatalf("reviewed tool = %q, want team.send_message", reviewed)
	}
}
