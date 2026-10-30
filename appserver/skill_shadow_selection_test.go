package appserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	promptctx "codex_go/prompt"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

type recordedSkillShadowMetric struct {
	kind, name string
	value      int
	duration   time.Duration
	boundaries []float64
	tags       map[string]string
}
type recordingSkillShadowMetrics struct {
	mu      sync.Mutex
	records []recordedSkillShadowMetric
}

func (m *recordingSkillShadowMetrics) Counter(name string, inc int, tags map[string]string) {
	m.record("counter", name, inc, 0, nil, tags)
}
func (m *recordingSkillShadowMetrics) Histogram(name string, value int, tags map[string]string) {
	m.record("histogram", name, value, 0, nil, tags)
}
func (m *recordingSkillShadowMetrics) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	m.record("histogram", name, value, 0, boundaries, tags)
}
func (m *recordingSkillShadowMetrics) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	m.record("duration", name, 0, duration, nil, tags)
}
func (m *recordingSkillShadowMetrics) record(kind, name string, value int, duration time.Duration, boundaries []float64, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := map[string]string{}
	for key, item := range tags {
		copied[key] = item
	}
	m.records = append(m.records, recordedSkillShadowMetric{kind, name, value, duration, append([]float64(nil), boundaries...), copied})
}
func (m *recordingSkillShadowMetrics) Records() []recordedSkillShadowMetric {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedSkillShadowMetric(nil), m.records...)
}

func skillShadowMetricRecords(metrics *recordingSkillShadowMetrics, name string) []recordedSkillShadowMetric {
	out := make([]recordedSkillShadowMetric, 0)
	for _, record := range metrics.Records() {
		if record.name == name {
			out = append(out, record)
		}
	}
	return out
}

// waitForSkillShadowMetric waits for background ranking output to land, since
// shadow ranking now runs off the turn preparation path (Rust #49812).
func waitForSkillShadowMetric(t *testing.T, metrics *recordingSkillShadowMetrics, name string, want int) []recordedSkillShadowMetric {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		matched := skillShadowMetricRecords(metrics, name)
		if len(matched) >= want {
			return matched
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s records = %d, want %d", name, len(matched), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func skillShadowTurnForTest(t *testing.T, router *RuntimeRouter, threadID string) *skillShadowTurn {
	t.Helper()
	state := router.skillShadowThreadStateFor(threadID)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.turn == nil {
		t.Fatalf("no shadow turn for thread %q", threadID)
	}
	return state.turn
}

func waitForSkillShadowRanked(t *testing.T, turn *skillShadowTurn) []skillShadowRankedSelection {
	t.Helper()
	select {
	case <-turn.rankedCh:
	case <-time.After(10 * time.Second):
		t.Fatalf("shadow ranking for turn %q did not finish", turn.turnID)
	}
	return turn.waitRanked()
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
	router.runSkillShadowSelection("thread-one", "turn-one", cfg, &turn.TurnStartParams{Input: []turn.TurnUserInput{{Type: "text", Text: "create slides"}}}, skills)
	waitForSkillShadowMetric(t, metrics, skillShadowRunMetric, 11)

	records := metrics.Records()
	if len(records) != 66 {
		t.Fatalf("metric records = %d, want 66", len(records))
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
	for _, method := range []string{"weighted_lexical_v1", "fielded_bm25_v1", "character_ngram_v1", "character_routing_card_v1", "multi_query_lexical_v1", "routing_card_exact_v1", "lru_v1", "lru_plus_lexical_v1", "lru_plus_character_routing_v1", "lru_plus_lexical_character_routing_v1"} {
		if !methods[method] {
			t.Fatalf("missing method %q in %#v", method, methods)
		}
	}
}

func TestRuntimeRouterSkillShadowSelectionDisabledBySkillSearchFeature(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	defer router.Close()
	router.runSkillShadowSelection("thread-disabled", "turn-disabled", &config.Config{Values: map[string]any{"features": map[string]any{"skill_search": false}}}, &turn.TurnStartParams{Prompt: "slides"}, []promptctx.InstructionsSkillMetadata{{Name: "slides"}})
	if records := metrics.Records(); len(records) != 0 {
		t.Fatalf("records = %#v", records)
	}
}

func TestRuntimeRouterSkillShadowSelectionExcludesExplicitSkillsLikeRust(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	router.runSkillShadowSelection("thread-explicit", "turn-explicit", cfg, &turn.TurnStartParams{Input: []turn.TurnUserInput{{Type: "skill", Name: "slides"}}}, []promptctx.InstructionsSkillMetadata{
		{Name: "slides", Description: "Create presentations."},
		{Name: "sheets", Description: "Analyze tabular data."},
	})
	waitForSkillShadowMetric(t, metrics, skillShadowCatalogMetric, 11)
	for _, record := range metrics.Records() {
		if record.name == skillShadowCatalogMetric && record.value != 1 {
			t.Fatalf("catalog metric = %#v, want explicitly selected skill excluded", record)
		}
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

func TestSkillShadowRecentInvocationsRefreshRecencyAndEvictOldSkills(t *testing.T) {
	recent := []string{}
	for index := 0; index <= promptctx.SkillShadowSelectionMaxResults; index++ {
		recent = skillShadowRecordRecent(recent, fmt.Sprintf("skill-%d", index))
	}
	recent = skillShadowRecordRecent(recent, "skill-1")

	if len(recent) != promptctx.SkillShadowSelectionMaxResults {
		t.Fatalf("recent length = %d, want %d", len(recent), promptctx.SkillShadowSelectionMaxResults)
	}
	if recent[0] != "skill-1" || recent[len(recent)-1] != "skill-2" {
		t.Fatalf("recent = %#v, want skill-1 first and skill-2 last", recent)
	}
	for _, skill := range recent {
		if skill == "skill-0" {
			t.Fatalf("skill-0 was not evicted: %#v", recent)
		}
	}
}

func TestSkillShadowRankBucketsDistinguishResultsAboveTwenty(t *testing.T) {
	tests := []struct {
		rank int
		want string
	}{
		{rank: 0, want: "miss"},
		{rank: 1, want: "1"},
		{rank: 5, want: "2_5"},
		{rank: 10, want: "6_10"},
		{rank: 20, want: "11_20"},
		{rank: 21, want: "21_50"},
		{rank: 50, want: "21_50"},
		{rank: 51, want: "miss"},
	}
	for _, test := range tests {
		if got := skillShadowRankBucket(test.rank); got != test.want {
			t.Fatalf("rankBucket(%d) = %q, want %q", test.rank, got, test.want)
		}
	}
}

func TestSkillShadowInvocationRecordingEmitsRankMetrics(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	params := &turn.TurnStartParams{Prompt: "manage python environments"}
	skills := []promptctx.InstructionsSkillMetadata{
		{Name: "slides", Path: "/skills/slides", Description: "Create presentations."},
		{Name: "python-tools", Path: "/skills/python-tools", Description: "Manage Python environments."},
	}
	router.runSkillShadowSelection("thread-invocation", "turn-one", cfg, params, skills)
	router.recordSkillShadowInvocation("thread-invocation", "turn-one", skills[1])

	// The invocation is recorded before the background ranking exists; it is
	// queued and scored once the frozen rankings are ready (Rust #49812).
	invocations := waitForSkillShadowMetric(t, metrics, skillShadowInvocationMetric, 11)
	var lruRank string
	for _, record := range invocations {
		if record.tags["query_script"] != "ascii_latin" {
			t.Fatalf("invocation tags = %#v", record.tags)
		}
		if record.tags["method"] == "lru_v1" {
			lruRank = record.tags["rank"]
		}
	}
	if lruRank != "miss" {
		t.Fatalf("lru_v1 rank before history = %q, want miss", lruRank)
	}

	// Second turn: lru_v1 recovers the skill invoked on the earlier turn.
	router.runSkillShadowSelection("thread-invocation", "turn-two", cfg, params, skills)
	router.recordSkillShadowInvocation("thread-invocation", "turn-two", skills[1])
	for _, record := range waitForSkillShadowMetric(t, metrics, skillShadowInvocationMetric, 22) {
		if record.tags["method"] != "lru_v1" {
			continue
		}
		lruRank = record.tags["rank"]
	}
	if lruRank != "1" {
		t.Fatalf("lru_v1 rank on second turn = %q, want 1", lruRank)
	}
}

func TestSkillShadowInvocationSkipsExplicitInvokeType(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	skills := []promptctx.InstructionsSkillMetadata{{Name: "slides", Path: "/skills/slides", Description: "Create presentations."}}
	router.runSkillShadowSelection("thread-explicit-type", "turn-one", cfg, &turn.TurnStartParams{Prompt: "slides"}, skills)
	waitForSkillShadowRanked(t, skillShadowTurnForTest(t, router, "thread-explicit-type"))
	router.trackSkillInvocationEvent(context.Background(), "thread-explicit-type", "turn-one", "model", "", skills[0], telemetry.SkillInvocationTypeExplicit)
	for _, record := range metrics.Records() {
		if record.name == skillShadowInvocationMetric {
			t.Fatalf("explicit invocation recorded: %#v", record)
		}
	}
}

func TestSkillShadowTaskContextFusionRecoversEarlierTurnSkill(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	skills := []promptctx.InstructionsSkillMetadata{
		{Name: "slides", Path: "/skills/slides", Description: "Create presentations."},
		{Name: "python-tools", Path: "/skills/python-tools", Description: "Manage Python environments."},
	}
	// Turn one: explicit skill intent "slides" plus a successful implicit
	// invocation of python-tools; both are relevance evidence for future turns.
	router.runSkillShadowSelection("thread-tc", "turn-one", cfg, &turn.TurnStartParams{Input: []turn.TurnUserInput{{Type: "skill", Name: "slides"}}}, skills)
	router.recordSkillShadowInvocation("thread-tc", "turn-one", skills[1])
	// Turn two: a bare continuation; the task-context fusion must recover the
	// python-tools skill from the earlier turn (Rust #39008).
	router.runSkillShadowSelection("thread-tc", "turn-two", cfg, &turn.TurnStartParams{Prompt: "continue"}, skills)
	ranked := waitForSkillShadowRanked(t, skillShadowTurnForTest(t, router, "thread-tc"))
	var taskFusion []string
	for _, selection := range ranked {
		if selection.method == "task_context_fusion_v1" {
			taskFusion = selection.skillResources
			break
		}
	}
	found := false
	for _, resource := range taskFusion {
		if resource == "/skills/python-tools" {
			found = true
		}
	}
	if !found {
		t.Fatalf("task_context_fusion_v1 did not recover python-tools: %#v", taskFusion)
	}
}

func TestSkillShadowTurnPreparationAdvancesWhileRankingBlocked(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	t.Cleanup(func() { _ = router.Close() })
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	skills := []promptctx.InstructionsSkillMetadata{
		{Name: "slides", Path: "/skills/slides", Description: "Create presentations."},
		{Name: "python-tools", Path: "/skills/python-tools", Description: "Manage Python environments."},
	}
	// Occupy the whole process-wide ranking budget (Rust RANKING_SLOTS).
	held := 0
	for ; held < skillShadowRankingSlots; held++ {
		skillShadowRankingBudget <- struct{}{}
	}
	t.Cleanup(func() {
		for ; held > 0; held-- {
			<-skillShadowRankingBudget
		}
	})
	prepared := make(chan struct{})
	go func() {
		router.runSkillShadowSelection("thread-blocked", "turn-one", cfg, &turn.TurnStartParams{Prompt: "manage python environments"}, skills)
		close(prepared)
	}()
	select {
	case <-prepared:
	case <-time.After(5 * time.Second):
		t.Fatal("turn preparation waited for a ranking slot")
	}
	if runs := skillShadowMetricRecords(metrics, skillShadowRunMetric); len(runs) != 0 {
		t.Fatalf("ranking ran while the budget was occupied: %#v", runs)
	}
	// Relevance history advances immediately, before any ranking exists.
	router.recordSkillShadowInvocation("thread-blocked", "turn-one", skills[1])
	state := router.skillShadowThreadStateFor("thread-blocked")
	state.mu.Lock()
	recent := append([]string(nil), state.recent...)
	state.mu.Unlock()
	if len(recent) != 1 || recent[0] != "/skills/python-tools" {
		t.Fatalf("recent = %#v, want the invocation recorded while ranking is blocked", recent)
	}
	for ; held > 0; held-- {
		<-skillShadowRankingBudget
	}
	// The queued observation is scored against the frozen rankings once the
	// worker gets a slot.
	waitForSkillShadowMetric(t, metrics, skillShadowInvocationMetric, 11)
}

func TestSkillShadowRankingSharesProcessWideCapacity(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	t.Cleanup(func() { _ = router.Close() })
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var mu sync.Mutex
	started := 0
	original := skillShadowRank
	skillShadowRank = func(query promptctx.ShadowQuery, documents []promptctx.SkillSelectionDocument, recentSkillIDs []int, taskQuery promptctx.ShadowQuery, taskRecentIDs []int) []promptctx.SkillShadowSelectionObservation {
		mu.Lock()
		started++
		mu.Unlock()
		<-release
		return nil
	}
	t.Cleanup(func() { skillShadowRank = original })
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	skills := []promptctx.InstructionsSkillMetadata{{Name: "slides", Path: "/skills/slides", Description: "Create presentations."}}
	for _, threadID := range []string{"thread-a", "thread-b", "thread-c"} {
		router.runSkillShadowSelection(threadID, "turn-one", cfg, &turn.TurnStartParams{Prompt: "create slides"}, skills)
	}
	waitForSkillShadowRankingsStarted(t, &mu, &started, 2)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	blocked := started
	mu.Unlock()
	if blocked != 2 {
		t.Fatalf("rankings started = %d, want the shared budget of 2", blocked)
	}
	releaseOnce.Do(func() { close(release) })
	waitForSkillShadowRankingsStarted(t, &mu, &started, 3)
}

func waitForSkillShadowRankingsStarted(t *testing.T, mu *sync.Mutex, started *int, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		value := *started
		mu.Unlock()
		if value >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("rankings started = %d, want %d", value, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSkillShadowScoresQueuedInvocationsAfterTurnStateDropped(t *testing.T) {
	metrics := &recordingSkillShadowMetrics{}
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})
	t.Cleanup(func() { _ = router.Close() })
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	original := skillShadowRank
	skillShadowRank = func(query promptctx.ShadowQuery, documents []promptctx.SkillSelectionDocument, recentSkillIDs []int, taskQuery promptctx.ShadowQuery, taskRecentIDs []int) []promptctx.SkillShadowSelectionObservation {
		<-release
		return original(query, documents, recentSkillIDs, taskQuery, taskRecentIDs)
	}
	t.Cleanup(func() { skillShadowRank = original })
	cfg := &config.Config{Values: map[string]any{"skills": map[string]any{"shadow_selection_enabled": true}}}
	params := &turn.TurnStartParams{Prompt: "manage python environments"}
	skills := []promptctx.InstructionsSkillMetadata{
		{Name: "slides", Path: "/skills/slides", Description: "Create presentations."},
		{Name: "python-tools", Path: "/skills/python-tools", Description: "Manage Python environments."},
	}
	router.runSkillShadowSelection("thread-dropped", "turn-one", cfg, params, skills)
	router.recordSkillShadowInvocation("thread-dropped", "turn-one", skills[1])
	// Starting the next turn drops the first turn's state, but its queued
	// observation must still be scored against its own frozen history.
	router.runSkillShadowSelection("thread-dropped", "turn-two", cfg, params, skills)
	router.recordSkillShadowInvocation("thread-dropped", "turn-two", skills[1])
	releaseOnce.Do(func() { close(release) })
	records := waitForSkillShadowMetric(t, metrics, skillShadowInvocationMetric, 22)
	ranks := make([]string, 0, 2)
	for _, record := range records {
		if record.tags["method"] == "lru_v1" {
			ranks = append(ranks, record.tags["rank"])
		}
	}
	sort.Strings(ranks)
	if len(ranks) != 2 || ranks[0] != "1" || ranks[1] != "miss" {
		t.Fatalf("lru_v1 ranks per turn = %#v, want the first turn frozen cold and the second warm", ranks)
	}
}
