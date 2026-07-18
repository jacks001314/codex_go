package appserver

import (
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/internal/config"
	promptctx "codex_go/internal/prompt"
	"codex_go/internal/session"
	"codex_go/internal/state"
	"codex_go/internal/turn"
)

type recordedSkillShadowMetric struct {
	kind, name string
	value      int
	duration   time.Duration
	tags       map[string]string
}
type recordingSkillShadowMetrics struct {
	mu      sync.Mutex
	records []recordedSkillShadowMetric
}

func (m *recordingSkillShadowMetrics) Counter(name string, inc int, tags map[string]string) {
	m.record("counter", name, inc, 0, tags)
}
func (m *recordingSkillShadowMetrics) Histogram(name string, value int, tags map[string]string) {
	m.record("histogram", name, value, 0, tags)
}
func (m *recordingSkillShadowMetrics) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	m.record("duration", name, 0, duration, tags)
}
func (m *recordingSkillShadowMetrics) record(kind, name string, value int, duration time.Duration, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := map[string]string{}
	for key, item := range tags {
		copied[key] = item
	}
	m.records = append(m.records, recordedSkillShadowMetric{kind, name, value, duration, copied})
}
func (m *recordingSkillShadowMetrics) Records() []recordedSkillShadowMetric {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedSkillShadowMetric(nil), m.records...)
}

func TestRuntimeRouterSkillShadowSelectionRecordsRustMethodsWithoutChangingCatalog(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	disabled := false
	skills := []promptctx.InstructionsSkillMetadata{
		{Name: "slides", Description: "Create presentations."},
		{Name: "sheets", Description: "Analyze tabular data."},
		{Name: "hidden", Description: "Create slides secretly.", AllowImplicitInvocation: &disabled},
	}
	router.runSkillShadowSelection(cfg, &turn.TurnStartParams{Input: []turn.TurnUserInput{{Type: "text", Text: "create slides"}}}, skills)

	records := metrics.Records()
	if len(records) != 24 {
		t.Fatalf("metric records = %d, want 24", len(records))
	}
	methods := map[string]bool{}
	for _, record := range records {
		methods[record.tags["method"]] = true
		if record.tags["query_script"] != "ascii_latin" || record.tags["query_truncated"] != "false" {
			t.Fatalf("tags = %#v", record.tags)
		}
		for key, value := range record.tags {
			if strings.Contains(key, "create slides") || strings.Contains(value, "create slides") {
				t.Fatalf("query leaked into metric tags: %#v", record.tags)
			}
		}
		if record.name == skillShadowCatalogMetric && (record.kind != "histogram" || record.value != 2) {
			t.Fatalf("catalog metric = %#v", record)
		}
	}
	for _, method := range []string{"weighted_lexical_v1", "fielded_bm25_v1", "character_ngram_v1", "multi_query_lexical_v1"} {
		if !methods[method] {
			t.Fatalf("missing method %q in %#v", method, methods)
		}
	}
}

func TestRuntimeRouterSkillShadowSelectionDisabledBySkillSearchFeature(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	defer router.Close()
	router.runSkillShadowSelection(&config.Config{Values: map[string]any{"features": map[string]any{"skill_search": false}}}, &turn.TurnStartParams{Prompt: "slides"}, []promptctx.InstructionsSkillMetadata{{Name: "slides"}})
	if records := metrics.Records(); len(records) != 0 {
		t.Fatalf("records = %#v", records)
	}
}

func TestDefaultRuntimeRouterInstallsSkillShadowMetrics(t *testing.T) {
	home := t.TempDir()
	router := NewDefaultRuntimeRouter(session.NewStore(home), home)
	defer router.Close()
	metrics, ok := router.services.SkillShadowMetrics.(*state.TaskMetrics)
	if !ok || metrics == nil {
		t.Fatalf("SkillShadowMetrics = %#v", router.services.SkillShadowMetrics)
	}
}

func TestSkillShadowQueryIsBoundedAndIncludesSkillMentionsLikeRust(t *testing.T) {
	query, truncated := skillShadowQuery(&turn.TurnStartParams{Input: []turn.TurnUserInput{{Type: "skill", Name: "slides"}, {Type: "text", Text: strings.Repeat("界", skillShadowMaxQueryBytes)}}})
	if !truncated || len(query) > skillShadowMaxQueryBytes || !strings.HasPrefix(query, "slides ") {
		t.Fatalf("query bytes=%d truncated=%v prefix=%q", len(query), truncated, query[:min(len(query), 16)])
	}
}

func TestSkillSelectionIsOptInAndPreservesExplicitOnlySkills(t *testing.T) {
	disabled := false
	skills := []promptctx.InstructionsSkillMetadata{
		{Name: "slides", Description: "create presentations"},
		{Name: "sheets", Description: "analyze tabular data"},
		{Name: "manual", Description: "explicit only", AllowImplicitInvocation: &disabled},
	}
	params := &turn.TurnStartParams{Prompt: "create a presentation"}
	if got := selectSkillMetadata(&config.Config{Values: map[string]any{}}, params, skills); len(got) != 3 {
		t.Fatalf("default selection changed catalog = %#v", got)
	}
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"selection_enabled": true}}}
	got := selectSkillMetadata(cfg, params, skills)
	names := map[string]bool{}
	for _, skill := range got {
		names[skill.Name] = true
	}
	if !names["slides"] || names["sheets"] || !names["manual"] {
		t.Fatalf("selected skills = %#v", got)
	}
	if empty := selectSkillMetadata(cfg, &turn.TurnStartParams{}, skills); len(empty) != len(skills) {
		t.Fatalf("empty query removed skills = %#v", empty)
	}
}
