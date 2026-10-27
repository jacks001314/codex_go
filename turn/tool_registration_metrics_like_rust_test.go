package turn_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/metrics"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

type sampledToolRegistrationMetric struct {
	name       string
	value      int
	boundaries []float64
	tags       map[string]string
}

type samplingRegistrationMetricSink struct {
	samples []sampledToolRegistrationMetric
}

func (s *samplingRegistrationMetricSink) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	clonedTags := make(map[string]string, len(tags))
	for key, value := range tags {
		clonedTags[key] = value
	}
	s.samples = append(s.samples, sampledToolRegistrationMetric{
		name:       name,
		value:      value,
		boundaries: append([]float64(nil), boundaries...),
		tags:       clonedTags,
	})
}

func TestSamplingRecordsToolRegistrationsOnceAcrossTransportRetryLikeRust(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp-1\",\"error\":{\"code\":\"upstream_unavailable\",\"message\":\"temporarily unavailable\"}}}\n\n")
			return
		}
		fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"msg-1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"recovered\"}]}}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-2\"}}\n\n")
	}))
	defer server.Close()

	registry := tool.NewRegistry()
	register := func(name string, exposure tool.Exposure, count int) {
		t.Helper()
		for index := 0; index < count; index++ {
			toolName := name
			if count > 1 {
				toolName += string(rune('a' + index))
			}
			err := registry.Register(tool.NewExecutorFunc(
				tool.Spec{
					Name:        tool.PlainName(toolName),
					Description: "private description for " + toolName,
					Exposure:    exposure,
				},
				func(context.Context, *tool.Invocation) (*tool.Output, error) { return &tool.Output{}, nil },
			))
			if err != nil {
				t.Fatalf("Register(%s) error = %v", toolName, err)
			}
		}
	}
	register("default", "", 1)
	register("visible", tool.ExposureModelVisible, 2)
	register("direct_model_only", tool.ExposureDirectModelOnly, 1)
	register("discoverable", tool.ExposureDiscoverable, 2)
	register("deferred_model_only", tool.ExposureDeferredModelOnly, 1)
	register("code_mode_only", tool.ExposureCodeModeOnly, 1)
	register("hidden", tool.ExposureHidden, 1)

	agent := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{
			BaseURL:          server.URL + "/v1",
			StreamMaxRetries: 1,
		},
		Stream: true,
	})
	sink := &samplingRegistrationMetricSink{}
	runtime := turn.NewRuntime(&turn.RuntimeOptions{
		Agent:                   agent,
		Router:                  tool.NewRouter(registry),
		ToolRegistrationMetrics: sink,
	})
	result, err := runtime.Run(context.Background(), &turn.AgentLoopRequest{
		Prompt:           "hello",
		Model:            "gpt-test",
		ToolMode:         model.ToolModeDirect,
		IncrementalTools: true,
		HostedTools:      []any{map[string]any{"type": "web_search"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result == nil || len(result.Responses) != 1 {
		t.Fatalf("Run() result = %#v", result)
	}
	if attempts.Load() != 2 {
		t.Fatalf("HTTP attempts = %d, want 2", attempts.Load())
	}
	if len(sink.samples) != 6 {
		t.Fatalf("registration samples = %d, want one six-series set across the retry", len(sink.samples))
	}

	wantCounts := map[string]int{
		"direct":              3,
		"direct_model_only":   1,
		"deferred":            2,
		"deferred_model_only": 1,
		"code_mode_only":      1,
		"hidden":              1,
	}
	for index, sample := range sink.samples {
		if sample.name != metrics.ToolRegistrationsMetric {
			t.Fatalf("sample[%d] name = %q, want %q", index, sample.name, metrics.ToolRegistrationsMetric)
		}
		if !reflect.DeepEqual(sample.boundaries, metrics.ToolRegistrationsMetricBoundaries) {
			t.Fatalf("sample[%d] boundaries = %#v, want %#v", index, sample.boundaries, metrics.ToolRegistrationsMetricBoundaries)
		}
		exposure := sample.tags["exposure"]
		if want := wantCounts[exposure]; sample.value != want {
			t.Fatalf("sample[%d] exposure=%q count=%d, want %d", index, exposure, sample.value, want)
		}
		if sample.tags["tool_mode"] != model.ToolModeDirect {
			t.Fatalf("sample[%d] tool_mode = %q, want %q", index, sample.tags["tool_mode"], model.ToolModeDirect)
		}
		if sample.tags["incremental_tools"] != "true" {
			t.Fatalf("sample[%d] incremental_tools = %q, want true", index, sample.tags["incremental_tools"])
		}
		if len(sample.tags) != 3 {
			t.Fatalf("sample[%d] tags = %#v, want only exposure, tool_mode and incremental_tools", index, sample.tags)
		}
		for _, value := range sample.tags {
			if strings.Contains(value, "private description") {
				t.Fatalf("sample[%d] tags leaked a tool description: %#v", index, sample.tags)
			}
		}
		delete(wantCounts, exposure)
	}
	if len(wantCounts) != 0 {
		t.Fatalf("missing registration categories: %#v", wantCounts)
	}
}
