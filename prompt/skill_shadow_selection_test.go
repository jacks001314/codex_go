package prompt

import (
	"strings"
	"testing"
)

func TestRunSkillShadowSelectionDoesNotMutateCatalogAndMatchesRustMethods(t *testing.T) {
	documents := []SkillSelectionDocument{{ID: 7, Name: "slides", Description: "Create presentations."}, {ID: 9, Name: "sheets", Description: "Analyze tabular data."}}
	before := append([]SkillSelectionDocument(nil), documents...)
	observations := RunSkillShadowSelection("create slides", documents, []int{9, 7})
	wantMethods := []string{"weighted_lexical_v1", "fielded_bm25_v1", "character_ngram_v1", "character_routing_card_v1", "multi_query_lexical_v1", "routing_card_exact_v1", "lru_v1", "lru_plus_lexical_v1", "lru_plus_character_routing_v1", "lru_plus_lexical_character_routing_v1"}
	if len(observations) != len(wantMethods) {
		t.Fatalf("observations = %#v", observations)
	}
	for i, observation := range observations {
		if observation.Method != wantMethods[i] || observation.QueryScript != "ascii_latin" || len(observation.CandidateIDs) == 0 {
			t.Fatalf("observation %d = %#v", i, observation)
		}
		if i < 6 && observation.CandidateIDs[0] != 7 {
			t.Fatalf("observation %d = %#v, want lexical top candidate 7", i, observation)
		}
	}
	if documents[0] != before[0] || documents[1] != before[1] {
		t.Fatalf("catalog mutated: %#v", documents)
	}
}

func TestLruPlusCharacterRoutingRecoversRecentlyInvokedSkill(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments."},
		{ID: 30, Name: "monorepo", Description: "Follow repository conventions."},
	}
	selection := NewLruPlusCharacterRoutingSkillSelector(NewLruSkillSelector([]int{30, 10})).Select("continue", documents, 50)
	if len(selection.CandidateIDs) != 2 || selection.CandidateIDs[0] != 30 || selection.CandidateIDs[1] != 10 {
		t.Fatalf("candidate ids = %#v", selection.CandidateIDs)
	}
}

func TestLruPlusCharacterRoutingColdStartFallsBackToRoutingCard(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks.", RoutingMetadata: "ci;failing"},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments.", RoutingMetadata: "python;env"},
	}
	selection := NewLruPlusCharacterRoutingSkillSelector(NewLruSkillSelector(nil)).Select("failing", documents, 50)
	if len(selection.CandidateIDs) == 0 || selection.CandidateIDs[0] != 10 {
		t.Fatalf("candidate ids = %#v, want routing-card lead 10", selection.CandidateIDs)
	}
}

func TestLruPlusLexicalCharacterRoutingFusesAllThreeSignals(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks.", RoutingMetadata: "ci;failing"},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments.", RoutingMetadata: "python;env"},
		{ID: 30, Name: "monorepo", Description: "Follow repository conventions.", RoutingMetadata: "repo"},
	}
	selection := NewLruPlusLexicalCharacterRoutingSkillSelector(NewLruSkillSelector([]int{30, 10})).Select("manage python environments", documents, 50)
	if len(selection.CandidateIDs) == 0 {
		t.Fatalf("candidate ids empty")
	}
	if selection.CandidateIDs[0] != 20 {
		t.Fatalf("candidate ids = %#v, want lexical lead 20", selection.CandidateIDs)
	}
	seen := map[int]bool{}
	for _, id := range selection.CandidateIDs {
		seen[id] = true
	}
	if !seen[30] || !seen[10] {
		t.Fatalf("candidate ids = %#v, want recent 30/10 also present", selection.CandidateIDs)
	}
}

func TestLruPlusCharacterRoutingRespectsLimitAndDeduplicates(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments."},
	}
	selection := NewLruPlusCharacterRoutingSkillSelector(NewLruSkillSelector([]int{10, 10, 20})).Select("continue", documents, 1)
	if len(selection.CandidateIDs) != 1 {
		t.Fatalf("candidate ids = %#v, want limit 1", selection.CandidateIDs)
	}
}

func TestShadowTaskContextContinuationRecoversPriorRequest(t *testing.T) {
	context := NewShadowTaskContext()
	first := context.BeginTurn("turn-1", ShadowQuery{Text: "create slides with speaker notes"}, true)
	if first.Query.Text != "create slides with speaker notes" || len(first.RecentSkills) != 0 {
		t.Fatalf("first snapshot = %#v", first)
	}
	context.Record("turn-1", "slides")
	second := context.BeginTurn("turn-2", ShadowQuery{Text: "continue"}, true)
	if !strings.Contains(second.Query.Text, "create slides with speaker notes") {
		t.Fatalf("second augmented query = %#v", second.Query)
	}
	if len(second.RecentSkills) != 1 || second.RecentSkills[0] != "slides" {
		t.Fatalf("second recent skills = %#v", second.RecentSkills)
	}
}

func TestShadowTaskContextTurnIsolationAndBoundedHistory(t *testing.T) {
	context := NewShadowTaskContext()
	context.BeginTurn("turn-1", ShadowQuery{Text: "request one"}, true)
	context.Record("turn-1", "skill-a")
	context.Record("turn-1", "skill-b")
	first := context.BeginTurn("turn-2", ShadowQuery{Text: "request two"}, true)
	if len(first.RecentSkills) != 2 || first.RecentSkills[0] != "skill-b" {
		t.Fatalf("turn-2 recent skills = %#v", first.RecentSkills)
	}
	context.Record("turn-2", "skill-c")
	second := context.BeginTurn("turn-3", ShadowQuery{Text: "request three"}, true)
	if len(second.RecentSkills) != 3 || second.RecentSkills[0] != "skill-c" {
		t.Fatalf("turn-3 recent skills = %#v", second.RecentSkills)
	}
	for index := 0; index < 60; index++ {
		context.BeginTurn("bulk-"+string(rune('a'+index%26)), ShadowQuery{Text: "bulk request"}, true)
		context.Record("bulk-"+string(rune('a'+index%26)), "skill-"+string(rune('a'+index%26)))
	}
	last := context.BeginTurn("final", ShadowQuery{Text: "final request"}, true)
	if len(last.RecentSkills) > skillShadowMaxRecentSkills {
		t.Fatalf("recent skills exceeded bound: %d", len(last.RecentSkills))
	}
}

func TestShadowTaskContextSkipsNonSubstantivePriorRequests(t *testing.T) {
	context := NewShadowTaskContext()
	context.BeginTurn("turn-1", ShadowQuery{Text: "continue"}, false)
	next := context.BeginTurn("turn-2", ShadowQuery{Text: "create slides"}, true)
	if next.Query.Text != "create slides" || strings.Contains(next.Query.Text, "continue") {
		t.Fatalf("non-substantive prior leaked into query = %#v", next.Query)
	}
}

func TestTaskContextFusionRecoversSkillFromEarlierTurn(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments."},
		{ID: 30, Name: "monorepo", Description: "Follow repository conventions."},
	}
	observations := RunSkillShadowSelectionWithTaskContext(
		ShadowQuery{Text: "continue"},
		documents,
		nil,
		ShadowQuery{Text: "manage python environments"},
		[]int{20},
	)
	var taskContextObservation *SkillShadowSelectionObservation
	for index := range observations {
		if observations[index].Method == "task_context_fusion_v1" {
			taskContextObservation = &observations[index]
			break
		}
	}
	if taskContextObservation == nil || len(taskContextObservation.CandidateIDs) == 0 || taskContextObservation.CandidateIDs[0] != 20 {
		t.Fatalf("task context fusion = %#v", taskContextObservation)
	}
}

func TestLruSkillSelectorPreservesRecentInvocationOrder(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments."},
		{ID: 30, Name: "monorepo", Description: "Follow repository conventions."},
	}
	selection := NewLruSkillSelector([]int{30, 10}).Select("continue", documents, 50)
	if len(selection.CandidateIDs) != 2 || selection.CandidateIDs[0] != 30 || selection.CandidateIDs[1] != 10 {
		t.Fatalf("candidate ids = %#v", selection.CandidateIDs)
	}
}

func TestLruSkillSelectorFiltersStaleOrDuplicateSkillsAndRespectsLimit(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments."},
	}
	selection := NewLruSkillSelector([]int{99, 10, 10, 20}).Select("yes", documents, 1)
	if len(selection.CandidateIDs) != 1 || selection.CandidateIDs[0] != 10 {
		t.Fatalf("candidate ids = %#v", selection.CandidateIDs)
	}
}

func TestLruPlusLexicalUsesLexicalResultsBeforeInvocationHistoryExists(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments."},
	}
	selection := NewLruPlusLexicalSkillSelector(NewLruSkillSelector(nil)).Select("manage python environments", documents, 50)
	if len(selection.CandidateIDs) != 1 || selection.CandidateIDs[0] != 20 {
		t.Fatalf("candidate ids = %#v", selection.CandidateIDs)
	}
}

func TestLruPlusLexicalMergesRecentAndNewlyMatchingSkills(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 20, Name: "python-tools", Description: "Manage Python environments."},
		{ID: 30, Name: "monorepo", Description: "Follow repository conventions."},
	}
	selection := NewLruPlusLexicalSkillSelector(NewLruSkillSelector([]int{30, 10})).Select("manage python environments", documents, 50)
	want := []int{20, 30, 10}
	if len(selection.CandidateIDs) != len(want) {
		t.Fatalf("candidate ids = %#v", selection.CandidateIDs)
	}
	for i := range want {
		if selection.CandidateIDs[i] != want[i] {
			t.Fatalf("candidate ids = %#v, want %#v", selection.CandidateIDs, want)
		}
	}
}

func TestLruPlusLexicalPromotesSkillsSupportedByBothSignals(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 10, Name: "ci", Description: "Investigate failing checks."},
		{ID: 30, Name: "monorepo", Description: "Follow repository conventions."},
	}
	selection := NewLruPlusLexicalSkillSelector(NewLruSkillSelector([]int{30, 10})).Select("investigate failing ci checks", documents, 50)
	want := []int{10, 30}
	if len(selection.CandidateIDs) != len(want) {
		t.Fatalf("candidate ids = %#v", selection.CandidateIDs)
	}
	for i := range want {
		if selection.CandidateIDs[i] != want[i] {
			t.Fatalf("candidate ids = %#v, want %#v", selection.CandidateIDs, want)
		}
	}
}

func TestLruPlusLexicalFusionKeepsTopRankedSkillAheadOfWeakOverlap(t *testing.T) {
	recent := append([]int{1}, append(rangeInts(100, 148), 2)...)
	lexical := append(rangeInts(200, 249), 2)
	fused := fuseSkillRankingsWithConstant([][]int{recent, lexical}, SkillShadowSelectionMaxResults, skillSelectorRRFConstant)
	topRanked := -1
	weakOverlap := -1
	for index, candidate := range fused {
		if candidate == 1 {
			topRanked = index
		}
		if candidate == 2 {
			weakOverlap = index
		}
	}
	if topRanked < 0 || weakOverlap < 0 || topRanked >= weakOverlap {
		t.Fatalf("fused = %#v, top-ranked 1 at %d, weak overlap 2 at %d", fused, topRanked, weakOverlap)
	}
}

func rangeInts(start, end int) []int {
	out := make([]int, 0, end-start)
	for value := start; value < end; value++ {
		out = append(out, value)
	}
	return out
}

func TestSkillShadowQueryScriptMatchesRustCategories(t *testing.T) {
	for query, want := range map[string]string{"123": "none", "slides": "ascii_latin", "制作幻灯片": "cjk", "slides 幻灯片": "mixed", "привет": "other"} {
		if got := skillShadowQueryScript(query); got != want {
			t.Fatalf("script(%q) = %q, want %q", query, got, want)
		}
	}
}
