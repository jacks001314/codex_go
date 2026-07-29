package prompt

import "testing"

func TestRunSkillShadowSelectionDoesNotMutateCatalogAndMatchesRustMethods(t *testing.T) {
	documents := []SkillSelectionDocument{{ID: 7, Name: "slides", Description: "Create presentations."}, {ID: 9, Name: "sheets", Description: "Analyze tabular data."}}
	before := append([]SkillSelectionDocument(nil), documents...)
	observations := RunSkillShadowSelection("create slides", documents)
	wantMethods := []string{"weighted_lexical_v1", "fielded_bm25_v1", "character_ngram_v1", "character_routing_card_v1", "multi_query_lexical_v1", "routing_card_exact_v1"}
	if len(observations) != len(wantMethods) {
		t.Fatalf("observations = %#v", observations)
	}
	for i, observation := range observations {
		if observation.Method != wantMethods[i] || observation.QueryScript != "ascii_latin" || len(observation.CandidateIDs) == 0 || observation.CandidateIDs[0] != 7 {
			t.Fatalf("observation %d = %#v", i, observation)
		}
	}
	if documents[0] != before[0] || documents[1] != before[1] {
		t.Fatalf("catalog mutated: %#v", documents)
	}
}

func TestSkillShadowQueryScriptMatchesRustCategories(t *testing.T) {
	for query, want := range map[string]string{"123": "none", "slides": "ascii_latin", "制作幻灯片": "cjk", "slides 幻灯片": "mixed", "привет": "other"} {
		if got := skillShadowQueryScript(query); got != want {
			t.Fatalf("script(%q) = %q, want %q", query, got, want)
		}
	}
}
