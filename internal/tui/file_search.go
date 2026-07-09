package tui

import "strings"

type FileSearchResult struct {
	Path  string
	Score int
}

func FilterFileSearchResults(results []FileSearchResult, query string) []FileSearchResult {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]FileSearchResult(nil), results...)
	}
	out := []FileSearchResult{}
	for _, result := range results {
		if strings.Contains(strings.ToLower(result.Path), query) {
			out = append(out, result)
		}
	}
	return out
}
