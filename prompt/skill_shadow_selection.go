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

// LruPlusCharacterRoutingSkillSelector fuses the 50 most recently invoked skills
// with character routing-card matches through reciprocal rank fusion (Rust
// LruPlusCharacterRoutingSkillSelector, #38993).
type LruPlusCharacterRoutingSkillSelector struct {
	lru       *LruSkillSelector
	character func(string, []SkillSelectionDocument, int) CheapSkillSelection
}

func NewLruPlusCharacterRoutingSkillSelector(lru *LruSkillSelector) *LruPlusCharacterRoutingSkillSelector {
	return &LruPlusCharacterRoutingSkillSelector{lru: lru, character: SelectSkillsCharacterRoutingCard}
}

func (s *LruPlusCharacterRoutingSkillSelector) Method() string {
	return "lru_plus_character_routing_v1"
}

func (s *LruPlusCharacterRoutingSkillSelector) Select(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	if limit <= 0 {
		return CheapSkillSelection{}
	}
	recent := s.lru.Select(query, documents, SkillShadowSelectionMaxResults)
	character := s.character(query, documents, SkillShadowSelectionMaxResults)
	return CheapSkillSelection{
		CandidateIDs:          fuseSkillRankingsWithConstant([][]int{recent.CandidateIDs, character.CandidateIDs}, min(limit, SkillShadowSelectionMaxResults), skillSelectorRRFConstant),
		QueryTermCount:        max(recent.QueryTermCount, character.QueryTermCount),
		QueryTruncated:        recent.QueryTruncated || character.QueryTruncated,
		CandidateSetTruncated: recent.CandidateSetTruncated || character.CandidateSetTruncated,
	}
}

// LruPlusLexicalCharacterRoutingSkillSelector fuses the 50 most recently invoked
// skills, weighted lexical matches, and character routing-card matches through
// reciprocal rank fusion (Rust LruPlusLexicalCharacterRoutingSkillSelector, #38993).
type LruPlusLexicalCharacterRoutingSkillSelector struct {
	lru       *LruSkillSelector
	character func(string, []SkillSelectionDocument, int) CheapSkillSelection
}

func NewLruPlusLexicalCharacterRoutingSkillSelector(lru *LruSkillSelector) *LruPlusLexicalCharacterRoutingSkillSelector {
	return &LruPlusLexicalCharacterRoutingSkillSelector{lru: lru, character: SelectSkillsCharacterRoutingCard}
}

func (s *LruPlusLexicalCharacterRoutingSkillSelector) Method() string {
	return "lru_plus_lexical_character_routing_v1"
}

func (s *LruPlusLexicalCharacterRoutingSkillSelector) Select(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	if limit <= 0 {
		return CheapSkillSelection{}
	}
	recent := s.lru.Select(query, documents, SkillShadowSelectionMaxResults)
	lexical := SelectSkillsWeightedLexical(query, documents, SkillShadowSelectionMaxResults)
	character := s.character(query, documents, SkillShadowSelectionMaxResults)
	return CheapSkillSelection{
		CandidateIDs:          fuseSkillRankingsWithConstant([][]int{recent.CandidateIDs, lexical.CandidateIDs, character.CandidateIDs}, min(limit, SkillShadowSelectionMaxResults), skillSelectorRRFConstant),
		QueryTermCount:        max(max(recent.QueryTermCount, lexical.QueryTermCount), character.QueryTermCount),
		QueryTruncated:        recent.QueryTruncated || lexical.QueryTruncated || character.QueryTruncated,
		CandidateSetTruncated: recent.CandidateSetTruncated || lexical.CandidateSetTruncated || character.CandidateSetTruncated,
	}
}

// ShadowQuery is the bounded text (plus truncation flag) used by shadow skill
// selection and the task-context fusion (Rust ShadowQuery).
type ShadowQuery struct {
	Text      string
	Truncated bool
}

// TaskContextSnapshot is the augmented query plus the recently relevant skills
// frozen at the start of a turn (Rust TaskContextSnapshot). Predictions never
// include the active turn's own recordings.
type TaskContextSnapshot struct {
	Query        ShadowQuery
	RecentSkills []string
}

// RunSkillShadowSelection runs the shadow-selection experiment without task
// context (context-free helper for tests and legacy callers).
func RunSkillShadowSelection(query string, documents []SkillSelectionDocument, recentSkillIDs []int) []SkillShadowSelectionObservation {
	return RunSkillShadowSelectionWithTaskContext(ShadowQuery{Text: query}, documents, recentSkillIDs, ShadowQuery{}, nil)
}

// RunSkillShadowSelectionWithTaskContext runs the shadow-selection experiment,
// mirroring Rust ShadowSelectionExperiment::run (#38993/#39008): the control
// selectors, the LRU and character-routing fusions, and the task-context fusion
// over the caller-mapped augmented query and recent-skill ids (the caller maps
// TaskContextSnapshot.RecentSkills through the eligible resource table, mirroring
// Rust eligible_skill_ids_by_resource).
func RunSkillShadowSelectionWithTaskContext(query ShadowQuery, documents []SkillSelectionDocument, recentSkillIDs []int, taskQuery ShadowQuery, taskRecentIDs []int) []SkillShadowSelectionObservation {
	lru := NewLruSkillSelector(recentSkillIDs)
	lruPlusLexical := NewLruPlusLexicalSkillSelector(lru.Clone())
	lruPlusCharacter := NewLruPlusCharacterRoutingSkillSelector(lru.Clone())
	lruPlusLexicalCharacter := NewLruPlusLexicalCharacterRoutingSkillSelector(lru.Clone())
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
		{lruPlusCharacter.Method(), lruPlusCharacter.Select},
		{lruPlusLexicalCharacter.Method(), lruPlusLexicalCharacter.Select},
	}
	out := make([]SkillShadowSelectionObservation, 0, len(selectors))
	for _, selector := range selectors {
		started := time.Now()
		selection := selector.selectFn(query.Text, documents, SkillShadowSelectionMaxResults)
		duration := time.Since(started)
		out = append(out, SkillShadowSelectionObservation{
			Method: selector.method, CandidateIDs: selection.CandidateIDs,
			QueryTermCount: selection.QueryTermCount, QueryTruncated: selection.QueryTruncated || query.Truncated,
			CandidateSetTruncated: selection.CandidateSetTruncated, QueryScript: skillShadowQueryScript(query.Text),
			Duration: duration,
		})
	}
	if taskRecentIDs != nil || taskQuery.Text != "" {
		taskLru := NewLruSkillSelector(taskRecentIDs)
		taskSelector := NewLruPlusCharacterRoutingSkillSelector(taskLru)
		started := time.Now()
		selection := taskSelector.Select(taskQuery.Text, documents, SkillShadowSelectionMaxResults)
		out = append(out, SkillShadowSelectionObservation{
			Method: "task_context_fusion_v1", CandidateIDs: selection.CandidateIDs,
			QueryTermCount: selection.QueryTermCount, QueryTruncated: selection.QueryTruncated || taskQuery.Truncated,
			CandidateSetTruncated: selection.CandidateSetTruncated, QueryScript: skillShadowQueryScript(taskQuery.Text),
			Duration: time.Since(started),
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
