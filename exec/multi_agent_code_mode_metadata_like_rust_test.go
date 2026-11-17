package exec

import (
	"context"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/model"
	"codex_go/tool"
)

// TestExecMultiAgentExposureFollowsModelMetadataLikeRust pins Rust #61854
// core/src/tools/spec_plan.rs: `multi_agent_tools_in_code_mode` may override
// `multi_agent_v2.non_code_mode_only`, and its absence keeps the runtime
// setting in charge.
func TestExecMultiAgentExposureFollowsModelMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	runner := NewLocalRunner(home)
	req := &Request{Exec: cli.ExecOptions{Prompt: "delegate", Shared: cli.SharedOptions{CWD: t.TempDir(), Model: "probe-code-mode"}}}
	enabled, disabled := true, false
	cases := []struct {
		name            string
		metadata        *bool
		nonCodeModeOnly bool
		want            tool.Exposure
	}{
		{name: "metadata true follows tool mode", metadata: &enabled, nonCodeModeOnly: true, want: tool.ExposureModelVisible},
		{name: "metadata false stays direct only", metadata: &disabled, nonCodeModeOnly: false, want: tool.ExposureDirectModelOnly},
		{name: "absent honors non_code_mode_only", metadata: nil, nonCodeModeOnly: true, want: tool.ExposureDirectModelOnly},
		{name: "absent follows tool mode", metadata: nil, nonCodeModeOnly: false, want: tool.ExposureModelVisible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := model.BundledModelsResponse()
			response.Models = append(response.Models, model.ModelInfo{
				Slug:                      "probe-code-mode",
				MultiAgentToolsInCodeMode: tc.metadata,
			})
			agentRunner := &model.ResponsesAgentRunner{ModelsManager: model.NewStaticModelsManager(response)}
			cfg := &config.Config{Values: map[string]any{
				"features": map[string]any{"multi_agent_v2": map[string]any{
					"enabled":            true,
					"wait_agent_enabled": true,
					"non_code_mode_only": tc.nonCodeModeOnly,
				}},
				"agents": map[string]any{"max_concurrent_threads_per_session": int64(4)},
			}}
			tools, err := runner.multiAgentToolsForRun(context.Background(), req, cfg, "thread-root", "turn-root", agentRunner)
			if err != nil {
				t.Fatalf("multiAgentToolsForRun: %v", err)
			}
			if tools == nil || tools.version != "v2" {
				t.Fatalf("multi-agent tools = %#v", tools)
			}
			if tools.exposure != tc.want {
				t.Fatalf("exposure = %q, want %q", tools.exposure, tc.want)
			}
		})
	}
}
