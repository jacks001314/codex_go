package prompt

import (
	"strings"
	"time"
	"unicode"
)

const SkillShadowSelectionMaxResults = 50

type SkillShadowSelectionObservation struct {
	Method                string
	CandidateIDs          []int
	QueryTermCount        int
	QueryTruncated        bool
	CandidateSetTruncated bool
	QueryScript           string
	Duration              time.Duration
}

// LruSkillSelector evaluates the most recently invoked skills (Rust
// LruSkillSelector, #38197): stale and duplicate entries are filtered against
// the eligible catalog while preserving recency order, capped at 50 results.
type LruSkillSelector struct {
	recentSkillIDs []int
}

func NewLruSkillSelector(recentSkillIDs []int) *LruSkillSelector {
	return &LruSkillSelector{recentSkillIDs: append([]int(nil), recentSkillIDs...)}
}

func (s *LruSkillSelector) Clone() *LruSkillSelector {
	return NewLruSkillSelector(s.recentSkillIDs)
}

func (s *LruSkillSelector) Method() string { return "lru_v1" }

func (s *LruSkillSelector) Select(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	terms := strings.Fields(query)
	termCount := len(terms)
	if termCount > skillSelectorMaxQueryTerms {
		termCount = skillSelectorMaxQueryTerms
	}
	result := CheapSkillSelection{
		QueryTermCount:        termCount,
		QueryTruncated:        len(terms) > skillSelectorMaxQueryTerms,
		CandidateSetTruncated: len(documents) > skillSelectorMaxCandidates,
	}
	if limit <= 0 {
		return result
	}
	eligible := make(map[int]bool, min(len(documents), skillSelectorMaxCandidates))
	for _, document := range documents[:min(len(documents), skillSelectorMaxCandidates)] {
		eligible[document.ID] = true
	}
	effectiveLimit := min(limit, SkillShadowSelectionMaxResults)
	seen := make(map[int]bool, len(s.recentSkillIDs))
	candidateIDs := make([]int, 0, min(len(s.recentSkillIDs), effectiveLimit))
	for _, id := range s.recentSkillIDs {
		if !eligible[id] || seen[id] {
			continue
		}
		seen[id] = true
		candidateIDs = append(candidateIDs, id)
		if len(candidateIDs) >= effectiveLimit {
			break
		}
	}
	result.CandidateIDs = candidateIDs
	return result
}

// LruPlusLexicalSkillSelector fuses the 50 most recent skills with weighted
// lexical matches through reciprocal rank fusion (Rust
// LruPlusLexicalSkillSelector, #38204).
type LruPlusLexicalSkillSelector struct {
	lru *LruSkillSelector
}

func NewLruPlusLexicalSkillSelector(lru *LruSkillSelector) *LruPlusLexicalSkillSelector {
	return &LruPlusLexicalSkillSelector{lru: lru}
}

func (s *LruPlusLexicalSkillSelector) Method() string { return "lru_plus_lexical_v1" }

func (s *LruPlusLexicalSkillSelector) Select(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	if limit <= 0 {
		return CheapSkillSelection{}
	}
	recent := s.lru.Select(query, documents, SkillShadowSelectionMaxResults)
	lexical := SelectSkillsWeightedLexical(query, documents, SkillShadowSelectionMaxResults)
	return CheapSkillSelection{
		CandidateIDs:          fuseSkillRankingsWithConstant([][]int{recent.CandidateIDs, lexical.CandidateIDs}, min(limit, SkillShadowSelectionMaxResults), skillSelectorRRFConstant),
		QueryTermCount:        max(recent.QueryTermCount, lexical.QueryTermCount),
		QueryTruncated:        recent.QueryTruncated || lexical.QueryTruncated,
		CandidateSetTruncated: recent.CandidateSetTruncated || lexical.CandidateSetTruncated,
	}
}

func RunSkillShadowSelection(query string, documents []SkillSelectionDocument, recentSkillIDs []int) []SkillShadowSelectionObservation {
	lru := NewLruSkillSelector(recentSkillIDs)
	lruPlusLexical := NewLruPlusLexicalSkillSelector(lru.Clone())
	selectors := []struct {
		method   string
		selectFn func(string, []SkillSelectionDocument, int) CheapSkillSelection
	}{
		{"weighted_lexical_v1", SelectSkillsWeightedLexical},
		{"fielded_bm25_v1", SelectSkillsFieldedBM25},
		{"character_ngram_v1", SelectSkillsCharacterNgram},
		{"character_routing_card_v1", SelectSkillsCharacterRoutingCard},
		{"multi_query_lexical_v1", SelectSkillsMultiQueryLexical},
		{"routing_card_exact_v1", SelectSkillsRoutingCardLexical},
		{lru.Method(), lru.Select},
		{lruPlusLexical.Method(), lruPlusLexical.Select},
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
