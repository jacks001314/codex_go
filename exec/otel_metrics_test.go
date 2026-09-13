package exec

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
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
