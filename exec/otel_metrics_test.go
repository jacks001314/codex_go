package exec

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

// Mirrors the exec crate's OTEL wiring: the provider is built from the effective
// config, its metrics client is installed on the model runner, and a flush at
// the end of the run exports the recorded metrics.
func TestExecOtelProviderInstallsMetricsSinkLikeRust(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": true},
		"otel": map[string]any{"metrics_exporter": map[string]any{
			"otlp-http": map[string]any{
				"endpoint": server.URL + "/v1/metrics",
				"protocol": config.OtelHTTPProtocolJSON,
			},
		}},
	}}
	runner := &Runner{UseResponsesAPI: true, CodexHome: t.TempDir()}
	runner.configureOtelProvider(cfg, &Request{})
	sink := runner.otelMetricsSink()
	if sink == nil {
		t.Fatal("the OTEL provider did not produce a metrics sink")
	}

	agent, err := runner.agentForRun(cfg, &auth.ResolvedAuth{Auth: auth.FromAPIKey("sk-test")}, "openai", nil)
	if err != nil {
		t.Fatalf("agentForRun() error = %v", err)
	}
	responsesAgent, ok := agent.(*model.ResponsesAgentRunner)
	if !ok {
		t.Fatalf("agent = %T", agent)
	}
	if responsesAgent.Metrics == nil {
		t.Fatal("the model runner has no metrics sink")
	}
	if responsesAgent.Telemetry == nil {
		t.Fatal("the model runner has no session telemetry sink")
	}

	responsesAgent.Metrics.Counter("codex.thread.started", 1, nil)
	runner.shutdownOtelProvider(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "codex.thread.started") {
		t.Fatalf("exported bodies = %#v", bodies)
	}
}

// The default config keeps OTEL metrics inert (the built-in Statsig route is
// off in development builds), so no provider or sink is built.
func TestExecOtelProviderDefaultsToDisabled(t *testing.T) {
	runner := &Runner{UseResponsesAPI: true, CodexHome: t.TempDir()}
	runner.configureOtelProvider(&config.Config{Values: map[string]any{}}, &Request{})
	if sink := runner.otelMetricsSink(); sink != nil {
		t.Fatalf("metrics sink = %#v", sink)
	}
	runner.shutdownOtelProvider(context.Background())
}

// Mirrors the session metrics Rust records when a turn completes: the
// per-model token usage, the per-turn tool-call count, the per-tool-call pair,
// the unified-exec running-process sample, and the end-to-end duration.
func TestExecTurnMetricsLikeRust(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": true},
		"otel": map[string]any{"metrics_exporter": map[string]any{
			"otlp-http": map[string]any{
				"endpoint": server.URL + "/v1/metrics",
				"protocol": config.OtelHTTPProtocolJSON,
			},
		}},
	}}
	runner := &Runner{UseResponsesAPI: true, CodexHome: t.TempDir()}
	runner.configureOtelProvider(cfg, &Request{})
	if runner.otelMetricsSink() == nil {
		t.Fatal("the OTEL provider did not produce a metrics sink")
	}
	started := time.Now().UTC()
	finished := started.Add(25 * time.Millisecond)
	runner.emitTurnMetrics(&turn.AgentLoopResult{
		Response: &model.AgentResponse{
			Model: "gpt-test",
			Usage: model.AgentUsage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10},
		},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{ToolName: tool.PlainName("shell")},
			Output:     &tool.Output{Success: true},
			StartedAt:  started,
			FinishedAt: finished,
		}},
	}, "thread-1", "gpt-test", false)
	runner.emitTurnE2EDuration(started)
	runner.shutdownOtelProvider(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("no metrics were exported")
	}
	payload := strings.Join(bodies, "\n")
	for _, name := range []string{
		"codex.turn.token_usage",
		"codex.turn.tool.call",
		"codex.tool.call",
		"codex.tool.call.duration_ms",
		"codex.turn.unified_exec.running_processes",
		"codex.turn.e2e_duration_ms",
	} {
		if !strings.Contains(payload, name) {
			t.Fatalf("exported payload is missing %s: %s", name, payload)
		}
	}
}
