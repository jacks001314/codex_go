package appserver

import (
	"context"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

// TestTurnTokenUsageByModelAttributesUsageToProducingModel mirrors Rust #44656:
// each response's resolved model owns its token usage, repeated responses from
// the same model aggregate into one entry, and responses without a model fall
// back to the turn's model.
func TestTurnTokenUsageByModelAttributesUsageToProducingModel(t *testing.T) {
	responses := []*model.AgentResponse{
		{Model: "gpt-5", Usage: model.AgentUsage{InputTokens: 10, CachedInputTokens: 3, OutputTokens: 4, TotalTokens: 14}},
		{Model: "gpt-5-mini", Usage: model.AgentUsage{InputTokens: 5, OutputTokens: 1, TotalTokens: 6}},
		{Model: "gpt-5", Usage: model.AgentUsage{InputTokens: 2, CacheWriteInputTokens: 7, ReasoningOutputTokens: 9, TotalTokens: 11}},
		{Usage: model.AgentUsage{InputTokens: 1, TotalTokens: 1}},
	}
	got := turnTokenUsageByModel(responses, "gpt-5")
	if len(got) != 2 {
		t.Fatalf("models = %#v, want gpt-5 and gpt-5-mini", got)
	}
	if got[0].model != "gpt-5" || got[1].model != "gpt-5-mini" {
		t.Fatalf("model order = %q/%q, want sorted gpt-5/gpt-5-mini", got[0].model, got[1].model)
	}
	want := model.AgentUsage{InputTokens: 13, CachedInputTokens: 3, CacheWriteInputTokens: 7, OutputTokens: 4, ReasoningOutputTokens: 9, TotalTokens: 26}
	if got[0].usage != want {
		t.Fatalf("gpt-5 usage = %#v, want %#v", got[0].usage, want)
	}
	if got[1].usage.TotalTokens != 6 || got[1].usage.InputTokens != 5 {
		t.Fatalf("gpt-5-mini usage = %#v", got[1].usage)
	}

	if empty := turnTokenUsageByModel(nil, "gpt-5"); len(empty) != 0 {
		t.Fatalf("turnTokenUsageByModel(nil) = %#v, want empty", empty)
	}
}

// TestEmitTurnMemoryMetricRecordsGates mirrors Rust emit_turn_memory_metric:
// one codex.turn.memory counter with read_allowed computed from the feature and
// config gates, plus the individual gate/citation tags.
func TestEmitTurnMemoryMetricRecordsGates(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
	router.emitTurnMemoryMetric(metrics, true, false, true)

	records := metrics.Records()
	if len(records) != 1 || records[0].Name != telemetry.TurnMemoryMetric || records[0].Kind != "counter" || records[0].Inc != 1 {
		t.Fatalf("records = %#v", records)
	}
	tags := records[0].Tags
	if tags["read_allowed"] != "false" || tags["feature_enabled"] != "true" ||
		tags["config_use_memories"] != "false" || tags["has_citations"] != "true" {
		t.Fatalf("tags = %#v", tags)
	}

	router.emitTurnMemoryMetric(metrics, true, true, false)
	last := metrics.Records()[len(metrics.Records())-1]
	if last.Tags["read_allowed"] != "true" || last.Tags["has_citations"] != "false" {
		t.Fatalf("second tags = %#v", last.Tags)
	}
}

// TestEmitTurnToolCallNetworkAndProcessMetrics covers the remaining per-turn
// metric sources of Rust #44656.
func TestEmitTurnToolCallNetworkAndProcessMetrics(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})

	router.emitTurnToolCallMetric(metrics, 3, true)
	router.emitTurnNetworkProxyMetric(metrics, false, true)
	router.emitTurnRunningProcessesMetric(metrics, "no-such-thread")

	byName := map[string]*state.TaskMetric{}
	for _, record := range metrics.Records() {
		byName[record.Name+"/"+record.Kind] = record
	}
	toolCall := byName[telemetry.TurnToolCallMetric+"/histogram"]
	if toolCall == nil || toolCall.Value != 3 || toolCall.Tags[telemetry.TurnTmpMemoryTag] != "true" {
		t.Fatalf("tool call metric = %#v", toolCall)
	}
	network := byName[telemetry.TurnNetworkProxyMetric+"/counter"]
	if network == nil || network.Inc != 1 || network.Tags["active"] != "false" || network.Tags[telemetry.TurnTmpMemoryTag] != "true" {
		t.Fatalf("network proxy metric = %#v", network)
	}
	processes := byName[telemetry.TurnUnifiedExecRunningProcessesMetric+"/histogram"]
	if processes == nil || processes.Value != 0 {
		t.Fatalf("running processes metric = %#v", processes)
	}
}

type turnMetricsAgent struct {
	model string
	usage model.AgentUsage
}

func (a *turnMetricsAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	return &model.AgentResponse{
		ResponseID: "resp-turn-metrics",
		Message:    "done",
		Model:      a.model,
		Usage:      a.usage,
		Items:      []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "done"}},
	}, nil
}

// TestRuntimeRouterTurnCompletionEmitsPerModelTokenUsage proves the wiring: a
// real completed turn records the codex.turn.token_usage histogram attributed
// to the model that produced the response (#44656).
func TestRuntimeRouterTurnCompletionEmitsPerModelTokenUsage(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "codex")
	metrics := state.NewTaskMetrics()
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := &turnMetricsAgent{
		model: "gpt-5-mini",
		usage: model.AgentUsage{
			InputTokens:           10,
			CachedInputTokens:     2,
			CacheWriteInputTokens: 1,
			OutputTokens:          3,
			ReasoningOutputTokens: 4,
			TotalTokens:           18,
		},
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Config:       config.NewConfigService(codexHome),
		TurnMetrics:  metrics,
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	seen := map[string]int{}
	for _, record := range metrics.Records() {
		if record.Name != telemetry.TurnTokenUsageMetric {
			continue
		}
		if record.Tags["model"] != "gpt-5-mini" {
			t.Fatalf("model tag = %q, want gpt-5-mini", record.Tags["model"])
		}
		seen[record.Tags[telemetry.TurnTokenTypeTag]] = record.Value
	}
	if len(seen) != 6 {
		t.Fatalf("token type samples = %#v, want 6", seen)
	}
	want := map[string]int{
		telemetry.TurnTokenTypeTotal:           18,
		telemetry.TurnTokenTypeInput:           10,
		telemetry.TurnTokenTypeCachedInput:     2,
		telemetry.TurnTokenTypeCacheWriteInput: 1,
		telemetry.TurnTokenTypeOutput:          3,
		telemetry.TurnTokenTypeReasoningOutput: 4,
	}
	for tokenType, value := range want {
		if seen[tokenType] != value {
			t.Fatalf("sample %s = %d, want %d (all %#v)", tokenType, seen[tokenType], value, seen)
		}
	}

	memoryRecords := 0
	for _, record := range metrics.Records() {
		if record.Name == telemetry.TurnMemoryMetric {
			memoryRecords++
		}
	}
	if memoryRecords != 1 {
		t.Fatalf("codex.turn.memory records = %d, want 1", memoryRecords)
	}
	counts := map[string]int{}
	for _, record := range metrics.Records() {
		counts[record.Name]++
	}
	for _, name := range []string{telemetry.TurnMemoryMetric, telemetry.TurnToolCallMetric, telemetry.TurnNetworkProxyMetric, telemetry.TurnUnifiedExecRunningProcessesMetric} {
		if counts[name] != 1 {
			t.Fatalf("records for %s = %d, want 1 (all %#v)", name, counts[name], counts)
		}
	}
}

// TestEmitTurnTokenUsageMetricsEmitsPerModelSamples proves the histogram is
// emitted once per model and token type with the token_type / tmp_mem_enabled
// tags, and that a turn with no reported usage still keeps one zero sample.
func TestEmitTurnTokenUsageMetricsEmitsPerModelSamples(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
	responses := []*model.AgentResponse{
		{Model: "gpt-5", Usage: model.AgentUsage{InputTokens: 10, CachedInputTokens: 3, OutputTokens: 4, ReasoningOutputTokens: 2, CacheWriteInputTokens: 1, TotalTokens: 14}},
		{Model: "gpt-5-mini", Usage: model.AgentUsage{InputTokens: 5, OutputTokens: 1, TotalTokens: 6}},
	}
	router.emitTurnTokenUsageMetrics(metrics, responses, "gpt-5", true)

	records := metrics.Records()
	if len(records) != 12 {
		t.Fatalf("records = %d, want 12 (2 models x 6 token types)", len(records))
	}
	seen := map[string]map[string]int{}
	for _, record := range records {
		if record.Name != telemetry.TurnTokenUsageMetric {
			t.Fatalf("metric name = %q", record.Name)
		}
		if record.Tags[telemetry.TurnTmpMemoryTag] != "true" {
			t.Fatalf("tmp_mem_enabled tag = %q", record.Tags[telemetry.TurnTmpMemoryTag])
		}
		modelName := record.Tags["model"]
		if seen[modelName] == nil {
			seen[modelName] = map[string]int{}
		}
		seen[modelName][record.Tags[telemetry.TurnTokenTypeTag]] = record.Value
	}
	if seen["gpt-5"][telemetry.TurnTokenTypeTotal] != 14 || seen["gpt-5"][telemetry.TurnTokenTypeCachedInput] != 3 || seen["gpt-5"][telemetry.TurnTokenTypeOutput] != 4 {
		t.Fatalf("gpt-5 samples = %#v", seen["gpt-5"])
	}
	if seen["gpt-5-mini"][telemetry.TurnTokenTypeTotal] != 6 {
		t.Fatalf("gpt-5-mini samples = %#v", seen["gpt-5-mini"])
	}

	emptyMetrics := state.NewTaskMetrics()
	router.emitTurnTokenUsageMetrics(emptyMetrics, nil, "gpt-5", false)
	emptyRecords := emptyMetrics.Records()
	if len(emptyRecords) != 6 {
		t.Fatalf("empty turn records = %d, want 6 zero samples", len(emptyRecords))
	}
	for _, record := range emptyRecords {
		if record.Value != 0 || record.Tags["model"] != "gpt-5" || record.Tags[telemetry.TurnTmpMemoryTag] != "false" {
			t.Fatalf("empty turn record = %#v", record)
		}
	}
}
