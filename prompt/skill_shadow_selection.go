package prompt

import (
	"time"
	"unicode"
)

const SkillShadowSelectionMaxResults = 20

type SkillShadowSelectionObservation struct {
	Method                string
	CandidateIDs          []int
	QueryTermCount        int
	QueryTruncated        bool
	CandidateSetTruncated bool
	QueryScript           string
	Duration              time.Duration
}

func RunSkillShadowSelection(query string, documents []SkillSelectionDocument) []SkillShadowSelectionObservation {
	selectors := []struct {
		method   string
		selectFn func(string, []SkillSelectionDocument, int) CheapSkillSelection
	}{
		{"weighted_lexical_v1", SelectSkillsWeightedLexical},
		{"fielded_bm25_v1", SelectSkillsFieldedBM25},
		{"character_ngram_v1", SelectSkillsCharacterNgram},
		{"multi_query_lexical_v1", SelectSkillsMultiQueryLexical},
	}
	out := make([]SkillShadowSelectionObservation, 0, len(selectors))
	for _, selector := range selectors {
		started := time.Now()
		selection := selector.selectFn(query, documents, SkillShadowSelectionMaxResults)
		duration := time.Since(started)
		out = append(out, SkillShadowSelectionObservation{
			Method: selector.method, CandidateIDs: selection.CandidateIDs,
			QueryTermCount: selection.QueryTermCount, QueryTruncated: selection.QueryTruncated,
			CandidateSetTruncated: selection.CandidateSetTruncated, QueryScript: skillShadowQueryScript(query),
			Duration: duration,
		})
	}
	return out
}

func skillShadowQueryScript(query string) string {
	latin, cjk, other := false, false, false
	for _, r := range query {
		if !unicode.IsLetter(r) {
			continue
		}
		switch {
		case r <= unicode.MaxASCII:
			latin = true
		case isSkillShadowCJK(r):
			cjk = true
		default:
			other = true
		}
	}
	count := 0
	if latin {
		count++
	}
	if cjk {
		count++
	}
	if other {
		count++
	}
	if count > 1 {
		return "mixed"
	}
	if latin {
		return "ascii_latin"
	}
	if cjk {
		return "cjk"
	}
	if other {
		return "other"
	}
	return "none"
}

func isSkillShadowCJK(r rune) bool {
	return r >= 0x1100 && r <= 0x11ff || r >= 0x3040 && r <= 0x30ff || r >= 0x3100 && r <= 0x318f || r >= 0x31a0 && r <= 0x31ff || r >= 0x3400 && r <= 0x4dbf || r >= 0x4e00 && r <= 0x9fff || r >= 0xa960 && r <= 0xa97f || r >= 0xac00 && r <= 0xd7ff || r >= 0xf900 && r <= 0xfaff || r >= 0x20000 && r <= 0x2fa1f
}
