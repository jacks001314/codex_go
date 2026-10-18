package appserver

import (
	"context"
	"testing"
	"time"

	promptctx "codex_go/prompt"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

func TestSkillTurnDurationMetricsEmitOncePerPluginLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := &RuntimeRouter{services: RuntimeServices{SkillInjectionMetrics: metrics}}
	router.startSkillTurnMetrics("thread-1", "turn-1", "gpt-5.4", "high", time.Now().Add(-2*time.Second))
	router.recordSkillTurnPluginUsage("thread-1", "turn-1", "docs@market")
	router.recordSkillTurnPluginUsage("thread-1", "turn-1", "docs@market")
	router.recordSkillTurnPluginUsage("thread-1", "turn-1", "sample@market")
	router.recordSkillTurnPluginUsage("thread-1", "turn-1", "")
	router.finishSkillTurnMetrics("thread-1", "turn-1", TurnStatusCompleted)

	records := metrics.Records()
	if len(records) != 3 {
		t.Fatalf("metrics = %#v, want one histogram per plugin", records)
	}
	byPlugin := map[string]*state.TaskMetric{}
	for _, record := range records {
		if record.Name != skillTurnDurationMetricName || record.Kind != "histogram" {
			t.Fatalf("metric = %#v, want skill turn duration histogram", record)
		}
		if record.Value < 2 {
			t.Fatalf("duration = %d, want at least 2 seconds", record.Value)
		}
		if record.Tags["model_slug"] != "gpt-5.4" || record.Tags["reasoning_effort"] != "high" || record.Tags["status"] != "completed" {
			t.Fatalf("tags = %#v", record.Tags)
		}
		byPlugin[record.Tags["plugin_id"]] = record
	}
	for _, pluginID := range []string{"docs_market", "sample_market", "unattributed"} {
		if byPlugin[pluginID] == nil {
			t.Fatalf("missing plugin_id %q in %#v", pluginID, byPlugin)
		}
	}

	router.finishSkillTurnMetrics("thread-1", "turn-1", TurnStatusCompleted)
	if got := len(metrics.Records()); got != 3 {
		t.Fatalf("metrics after duplicate finish = %d, want 3", got)
	}
}

func TestSkillTurnDurationMapsTerminalStatusesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status TurnStatus
		failed bool
		want   string
	}{
		{name: "completed", status: TurnStatusCompleted, want: "completed"},
		{name: "completed after turn error", status: TurnStatusCompleted, failed: true, want: "error"},
		{name: "failed", status: TurnStatusFailed, want: "error"},
		{name: "interrupted", status: TurnStatusInterrupted, want: "aborted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics := state.NewTaskMetrics()
			router := &RuntimeRouter{services: RuntimeServices{SkillInjectionMetrics: metrics}}
			router.startSkillTurnMetrics("thread-status", "turn-status", "gpt-5.4", "medium", time.Now())
			router.recordSkillTurnPluginUsage("thread-status", "turn-status", "docs@market")
			if tc.failed {
				router.markSkillTurnMetricsFailed("thread-status", "turn-status")
			}
			router.finishSkillTurnMetrics("thread-status", "turn-status", tc.status)
			records := metrics.Records()
			if len(records) != 1 || records[0].Tags["status"] != tc.want {
				t.Fatalf("metrics = %#v, want status %q", records, tc.want)
			}
		})
	}
}

func TestImplicitSkillInjectionMetricIncludesPluginAndTurnDimensionsLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := &RuntimeRouter{services: RuntimeServices{SkillInjectionMetrics: metrics}}
	router.startSkillTurnMetrics("thread-implicit", "turn-implicit", "gpt-5.4", "high", time.Now())
	skill := promptctx.InstructionsSkillMetadata{
		Name:        "exec-skill",
		Scope:       "environment",
		LocatorKind: "executor package",
		PackageID:   "authority:exec:exec-skill",
		ResourceID:  "authority:exec:exec-skill/SKILL.md",
		PluginID:    "docs@market",
	}
	provider := router.implicitSkillInvocationEventProvider(
		"thread-implicit",
		"turn-implicit",
		"gpt-5.4",
		&turn.TurnStartParams{},
		[]promptctx.InstructionsSkillMetadata{skill},
	)
	provider(context.Background(), &tool.Invocation{
		ToolName: tool.NamespacedName("skills", "read"),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"package":"authority:exec:exec-skill"}`,
		},
	}, &tool.Output{Success: true})

	records := metrics.Records()
	if len(records) != 1 {
		t.Fatalf("metrics = %#v, want one implicit injection", records)
	}
	record := records[0]
	if record.Name != skillInjectionMetricName || record.Tags["status"] != "ok" || record.Tags["skill"] != "exec-skill" || record.Tags["invoke_type"] != telemetry.SkillInvocationTypeImplicit {
		t.Fatalf("metric = %#v", record)
	}
	if record.Tags["plugin_id"] != "docs_market" || record.Tags["model_slug"] != "gpt-5.4" || record.Tags["reasoning_effort"] != "high" {
		t.Fatalf("attribution tags = %#v", record.Tags)
	}
}
