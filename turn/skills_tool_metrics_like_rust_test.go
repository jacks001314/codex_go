package turn

import (
	"context"
	"testing"

	"codex_go/tool"
)

type recordedSkillMetric struct {
	name string
	tags map[string]string
}

type recordingSkillMetrics struct {
	records []recordedSkillMetric
}

func (m *recordingSkillMetrics) Counter(name string, inc int, tags map[string]string) {
	for i := 0; i < inc; i++ {
		copied := make(map[string]string, len(tags))
		for key, value := range tags {
			copied[key] = value
		}
		m.records = append(m.records, recordedSkillMetric{name: name, tags: copied})
	}
}

func newSkillMetricsTestExecutor(metrics SkillToolMetrics, name string, discoveryV2 bool) *skillsToolExecutor {
	return newSkillsToolExecutor(&ToolRegistryOptions{
		SkillMetrics:      metrics,
		DiscoveryV2Skills: discoveryV2,
	}, name, "", &orchestratorSkillCatalogCache{}, &executorSkillCatalogCache{})
}

// Rust #59295 records one bounded outcome counter per skills.read call, tagged
// with the step's discovery version.
func TestSkillsReadEmitsOutcomeMetricsLikeRust(t *testing.T) {
	metrics := &recordingSkillMetrics{}
	executor := newSkillMetricsTestExecutor(metrics, skillsToolReadName, false)
	_, _ = executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "read",
		ToolName: tool.NamespacedName("skills", "read"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"package":""}`},
	})
	if len(metrics.records) != 1 {
		t.Fatalf("metrics = %#v, want one read record", metrics.records)
	}
	record := metrics.records[0]
	if record.name != "codex.skills.read.result" || record.tags["outcome"] != "source_unavailable" || record.tags["discovery_version"] != "v1" {
		t.Fatalf("read metric = %#v", record)
	}

	v2 := &recordingSkillMetrics{}
	executor = newSkillMetricsTestExecutor(v2, skillsToolReadName, true)
	_, _ = executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "read-malformed",
		ToolName: tool.NamespacedName("skills", "read"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{`},
	})
	if len(v2.records) != 1 || v2.records[0].tags["outcome"] != "invalid_arguments" || v2.records[0].tags["discovery_version"] != "v2" {
		t.Fatalf("v2 read metric = %#v", v2.records)
	}
}

// Rust #59295 tags installations outcomes with the requested source filter and
// records an empty listing separately from a failure.
func TestSkillsInstallationsEmitsOutcomeMetricsLikeRust(t *testing.T) {
	metrics := &recordingSkillMetrics{}
	executor := newSkillMetricsTestExecutor(metrics, skillsToolInstallationsName, true)
	_, _ = executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "installations-invalid",
		ToolName: tool.NamespacedName("skills", "installations"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"package":"","source":{"kind":"cloud"}}`},
	})
	_, _ = executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "installations-empty",
		ToolName: tool.NamespacedName("skills", "installations"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"package":"skill://demo@1/skills/deploy"}`},
	})
	if len(metrics.records) != 2 {
		t.Fatalf("metrics = %#v, want two installations records", metrics.records)
	}
	if first := metrics.records[0]; first.name != "codex.skills.installations.result" || first.tags["outcome"] != "invalid_package" || first.tags["source_filter"] != "cloud" || first.tags["discovery_version"] != "v2" {
		t.Fatalf("invalid-package metric = %#v", first)
	}
	if second := metrics.records[1]; second.tags["outcome"] != "empty" || second.tags["source_filter"] != "all" || second.tags["discovery_version"] != "v2" {
		t.Fatalf("empty metric = %#v", second)
	}
}
