package appserver

import (
	"context"
	"testing"

	"codex_go/plugin"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
)

func pluginInstallMetricsRuntime(t *testing.T, action MCPElicitationAction, withPlugin bool) (*pluginInstallRuntime, *state.TaskMetrics) {
	t.Helper()
	metrics := state.NewTaskMetrics()
	plugins := plugin.NewPluginService()
	if withPlugin {
		plugins.AddPlugin(plugin.PluginDetail{
			Summary: plugin.PluginSummary{
				ID:            "docs@market",
				Name:          "docs",
				InstallPolicy: plugin.InstallAllowed,
			},
		})
	}
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: action}))
	}))
	return &pluginInstallRuntime{
		broker:   broker,
		plugins:  plugins,
		threadID: "thread-1",
		turnID:   "turn-1",
		metrics:  metrics,
	}, metrics
}

// Mirrors Rust's record_plugin_install_elicitation_sent/_suggestion: one
// counter when the elicitation is dispatched and one for the outcome, tagged by
// the tool type, response action, and completion.
func TestPluginInstallRuntimeRecordsMetricsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		action        MCPElicitationAction
		withPlugin    bool
		wantAction    string
		wantCompleted string
	}{
		{"accepted install", MCPElicitationActionAccept, true, "accept", "true"},
		{"declined", MCPElicitationActionDecline, false, "decline", "false"},
	} {
		runtime, metrics := pluginInstallMetricsRuntime(t, testCase.action, testCase.withPlugin)
		if _, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
			CallID:       "call-1",
			SuggestionID: "request_plugin_install_call-1",
			Tool:         plugin.DiscoverableInfo{ID: "docs@market", Name: "Docs"},
			Elicitation: &tool.PluginInstallElicitation{
				Message:         "Use docs",
				Meta:            map[string]any{"tool_id": "docs@market"},
				RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}); err != nil {
			t.Fatalf("%s: RequestPluginInstall() error = %v", testCase.name, err)
		}
		records := metrics.Records()
		if len(records) != 2 {
			t.Fatalf("%s: records = %#v", testCase.name, records)
		}
		if sent := records[0]; sent.Name != telemetry.PluginInstallElicitationSentMetric ||
			sent.Kind != "counter" || sent.Inc != 1 || sent.Tags["tool_type"] != "plugin" {
			t.Fatalf("%s: elicitation sent = %#v", testCase.name, sent)
		}
		suggestion := records[1]
		if suggestion.Name != telemetry.PluginInstallSuggestionMetric || suggestion.Inc != 1 ||
			suggestion.Tags["tool_type"] != "plugin" ||
			suggestion.Tags["response_action"] != testCase.wantAction ||
			suggestion.Tags["completed"] != testCase.wantCompleted {
			t.Fatalf("%s: suggestion = %#v", testCase.name, suggestion)
		}
	}
}

// A request that never reaches the broker (no runtime) records nothing.
func TestPluginInstallRuntimeRecordsNoMetricsWithoutElicitation(t *testing.T) {
	metrics := state.NewTaskMetrics()
	runtime := &pluginInstallRuntime{metrics: metrics}
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{CallID: "call-1"})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result.Sent {
		t.Fatalf("result = %#v", result)
	}
	if records := metrics.Records(); len(records) != 0 {
		t.Fatalf("records = %#v", records)
	}
}
