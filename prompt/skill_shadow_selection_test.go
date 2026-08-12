package prompt

import "testing"

func TestRunSkillShadowSelectionDoesNotMutateCatalogAndMatchesRustMethods(t *testing.T) {
	documents := []SkillSelectionDocument{{ID: 7, Name: "slides", Description: "Create presentations."}, {ID: 9, Name: "sheets", Description: "Analyze tabular data."}}
	before := append([]SkillSelectionDocument(nil), documents...)
	observations := RunSkillShadowSelection("create slides", documents, []int{9, 7})
	wantMethods := []string{"weighted_lexical_v1", "fielded_bm25_v1", "character_ngram_v1", "character_routing_card_v1", "multi_query_lexical_v1", "routing_card_exact_v1", "lru_v1", "lru_plus_lexical_v1"}
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
