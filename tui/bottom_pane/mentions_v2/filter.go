package mentionsv2

import (
	"sort"
	"strings"

	"codex_go/filesearch"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/filter.rs.

func Filter(candidates []Candidate, query string) []Candidate {
	rows := FilteredCandidates(candidates, nil, query, SearchModeResults, false)
	out := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, Candidate{
			ID:          row.DisplayName,
			Label:       row.DisplayName,
			DisplayName: row.DisplayName,
			Description: row.Description,
			MentionType: row.MentionType,
			Selection:   row.Selection,
		})
	}
	return out
}

func FilteredCandidates(candidates []Candidate, fileMatches []filesearch.FileMatch, query string, searchMode SearchMode, showFileMatches bool) []SearchResult {
	filter := strings.TrimSpace(query)
	out := []SearchResult{}
	for _, candidate := range candidates {
		if !searchMode.Accepts(candidate.MentionType) {
			continue
		}
		if filter == "" {
			out = append(out, candidate.ToResult(nil, 0))
			continue
		}
		if indices, score, ok := bestToolMatch(candidate, filter); ok {
			out = append(out, candidate.ToResult(indices, score))
		}
	}
	if showFileMatches {
		for _, fileMatch := range fileMatches {
			row := FileMatchToRow(fileMatch)
			if searchMode.Accepts(row.MentionType) {
				out = append(out, row)
			}
		}
	}
	sortRows(out, filter)
	return out
}

func bestToolMatch(candidate Candidate, filter string) ([]int, int, bool) {
	if match, ok := filesearch.FuzzyMatch(candidate.DisplayName, filter); ok {
		return match.Indices, match.Score, true
	}
	bestScore := 0
	bestOK := false
	for _, term := range candidate.SearchTerms {
		if term == candidate.DisplayName {
			continue
		}
		if match, ok := filesearch.FuzzyMatch(term, filter); ok && (!bestOK || match.Score < bestScore) {
			bestScore = match.Score
			bestOK = true
		}
	}
	if !bestOK {
		return nil, 0, false
	}
	return nil, bestScore, true
}

func sortRows(rows []SearchResult, filter string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a := rows[i]
		b := rows[j]
		if orderA, orderB := mentionTypeOrder(a.MentionType), mentionTypeOrder(b.MentionType); orderA != orderB {
			return orderA < orderB
		}
		if a.MentionType.IsFilesystem() && b.MentionType.IsFilesystem() {
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			return a.DisplayName < b.DisplayName
		}
		if filter == "" {
			return a.DisplayName < b.DisplayName
		}
		aIndirect := len(a.MatchIndices) == 0
		bIndirect := len(b.MatchIndices) == 0
		if aIndirect != bIndirect {
			return !aIndirect
		}
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		return a.DisplayName < b.DisplayName
	})
}

func mentionTypeOrder(mentionType MentionType) int {
	switch mentionType {
	case MentionTypePlugin:
		return 0
	case MentionTypeSkill:
		return 1
	case MentionTypeFile, MentionTypeDirectory:
		return 2
	default:
		return 3
	}
}

func FileMatchToRow(fileMatch filesearch.FileMatch) SearchResult {
	mentionType := MentionTypeFile
	if fileMatch.MatchType == filesearch.MatchDirectory {
		mentionType = MentionTypeDirectory
	}
	return SearchResult{
		DisplayName:  fileMatch.Path,
		MentionType:  mentionType,
		Selection:    FileSelection(fileMatch.Path),
		MatchIndices: append([]int(nil), fileMatch.Indices...),
		Score:        fileMatch.Score,
	}
}
