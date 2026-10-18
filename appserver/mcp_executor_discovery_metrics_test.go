package appserver

import (
	"testing"
	"time"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/turn"
)

type executorDiscoveryMetricRecorder struct {
	counters []executorDiscoveryMetricSample
}

type executorDiscoveryMetricSample struct {
	name string
	tags map[string]string
}

func (r *executorDiscoveryMetricRecorder) Counter(name string, inc int, tags map[string]string) {
	copied := make(map[string]string, len(tags))
	for key, value := range tags {
		copied[key] = value
	}
	r.counters = append(r.counters, executorDiscoveryMetricSample{name: name, tags: copied})
}

func (r *executorDiscoveryMetricRecorder) Histogram(string, int, map[string]string) {}

func (r *executorDiscoveryMetricRecorder) HistogramWithBounds(string, int, []float64, map[string]string) {
}

func (r *executorDiscoveryMetricRecorder) RecordDuration(string, time.Duration, map[string]string) {
}

func TestExecutorMCPDiscoveryEmitsOutcomeMetricsLikeRust(t *testing.T) {
	reader := &mcpExecutorDiscoveryTestReader{response: &execserverclient.EnvironmentConfigReadResponse{
		Config: execserverclient.EnvironmentConfigLayerStack{Layers: []execserverclient.EnvironmentConfigLayer{{
			TOML: `
[mcp_servers.allowed]
url = "http://executor-only.invalid/mcp"

[mcp_servers.unavailable]
url = "http://executor-only.invalid/unavailable"
enabled = false
`,
		}}},
	}}
	execServerURL, closeExecServer := startMCPDiscoveryExecServer(t, reader)
	defer closeExecServer()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "file:///workspace")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "executor-1", ExecServerURL: execServerURL}); err != nil {
		t.Fatal(err)
	}
	metrics := &executorDiscoveryMetricRecorder{}
	router := NewRuntimeRouter(RuntimeServices{
		Config:      config.NewConfigService(t.TempDir()),
		Environment: manager,
		TurnMetrics: metrics,
	})
	defer router.Close()
	allowedURL := "http://executor-only.invalid/mcp"
	cfg := &config.Config{
		Values: map[string]any{},
		Requirements: &config.ConfigRequirements{MCPServers: map[string]config.MCPServerRequirement{
			"allowed": {Identity: &config.MCPServerIdentity{URL: &allowedURL}},
		}},
	}
	params := &turn.TurnStartParams{
		ThreadID: "thread-executor-mcp-metrics",
		Environments: []map[string]any{{
			"environmentId": "executor-1",
			"cwd":           "file:///workspace",
		}},
	}
	if err := router.registerActiveRuntimeTurn(params.ThreadID, "turn-executor-mcp-metrics", nil, 1, params); err != nil {
		t.Fatal(err)
	}
	if service := router.mcpServiceForThread(params.ThreadID, cfg); service == nil {
		t.Fatal("managed MCP service = nil")
	}

	var discovery, found, unavailable bool
	for _, sample := range metrics.counters {
		switch sample.name {
		case executorMCPDiscoveryMetric:
			discovery = discovery || sample.tags["outcome"] == "success"
		case executorMCPDiscoveryServerMetric:
			switch sample.tags["server_name"] {
			case "allowed":
				found = found || sample.tags["outcome"] == "found"
			case "unavailable":
				unavailable = unavailable || sample.tags["outcome"] == "unavailable"
			}
		}
	}
	if !discovery || !found || !unavailable {
		t.Fatalf("discovery metrics = %#v, want attempt success plus found/unavailable servers", metrics.counters)
	}
}
