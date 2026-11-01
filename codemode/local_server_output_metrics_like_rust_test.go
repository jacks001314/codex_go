package codemode

import (
	"context"
	"testing"

	"codex_go/tool"
)

// Rust's LocalServerOutputProvider forwards sample_process_metrics to the
// wrapped provider (codex-rs/core/src/code_mode_host.rs). Enabling server-side
// output truncation must not downgrade the metrics surface.

type metricsRecordingProvider struct {
	sample tool.CodeModeProcessMetricsSample
	calls  int
}

func (*metricsRecordingProvider) NewSession(tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	return nil
}

func (p *metricsRecordingProvider) SampleProcessMetrics(context.Context) (tool.CodeModeProcessMetricsSample, error) {
	p.calls++
	return p.sample, nil
}

type plainProvider struct{}

func (plainProvider) NewSession(tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession { return nil }

func TestLocalServerOutputProviderForwardsProcessMetricsLikeRust(t *testing.T) {
	inner := &metricsRecordingProvider{sample: tool.CodeModeProcessMetricsSample{
		Kind:    tool.CodeModeProcessMetricsSnapshot,
		Metrics: tool.CodeModeProcessMetrics{ProcessID: 4321},
	}}
	wrapped := NewLocalServerOutputProvider(inner)
	sampler, ok := wrapped.(tool.CodeModeProcessMetricsSampler)
	if !ok {
		t.Fatal("wrapped provider does not expose process metrics")
	}
	got, err := sampler.SampleProcessMetrics(context.Background())
	if err != nil {
		t.Fatalf("SampleProcessMetrics: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner sample calls = %d, want 1", inner.calls)
	}
	if got.Kind != tool.CodeModeProcessMetricsSnapshot || got.Metrics.ProcessID != 4321 {
		t.Fatalf("sample = %#v", got)
	}
}

func TestLocalServerOutputProviderReportsUnsupportedWithoutASamplerLikeRust(t *testing.T) {
	sampler, ok := NewLocalServerOutputProvider(plainProvider{}).(tool.CodeModeProcessMetricsSampler)
	if !ok {
		t.Fatal("wrapped provider does not expose process metrics")
	}
	got, err := sampler.SampleProcessMetrics(context.Background())
	if err != nil {
		t.Fatalf("SampleProcessMetrics: %v", err)
	}
	// Rust's trait default answers Unsupported for providers that cannot sample.
	if got.Kind != tool.CodeModeProcessMetricsUnsupported {
		t.Fatalf("sample = %#v", got)
	}
}
