package memories

import (
	"strconv"
	"strings"
)

type Citation struct {
	Entries    []*CitationEntry `json:"entries"`
	RolloutIDs []string         `json:"rolloutIds"`
}

type CitationEntry struct {
	Path      string `json:"path"`
	LineStart int64  `json:"lineStart"`
	LineEnd   int64  `json:"lineEnd"`
	Note      string `json:"note"`
}

func ParseCitation(citations []string) *Citation {
	result := &Citation{Entries: []*CitationEntry{}, RolloutIDs: []string{}}
	seenRolloutIDs := map[string]bool{}
	for _, citation := range citations {
		if entriesBlock, ok := extractBlock(citation, "<citation_entries>", "</citation_entries>"); ok {
			for _, line := range strings.Split(entriesBlock, "\n") {
				if entry := parseCitationEntry(line); entry != nil {
					result.Entries = append(result.Entries, entry)
				}
			}
		}
		if idsBlock, ok := extractIDsBlock(citation); ok {
			for _, line := range strings.Split(idsBlock, "\n") {
				id := strings.TrimSpace(line)
				if id == "" || seenRolloutIDs[id] {
					continue
				}
				seenRolloutIDs[id] = true
				result.RolloutIDs = append(result.RolloutIDs, id)
			}
		}
	}
	if len(result.Entries) == 0 && len(result.RolloutIDs) == 0 {
		return nil
	}
	return result
}

func ThreadIDsFromCitation(citation *Citation) []string {
	if citation == nil {
		return nil
	}
	out := make([]string, 0, len(citation.RolloutIDs))
	for _, id := range citation.RolloutIDs {
		if IsValidThreadID(id) {
			out = append(out, id)
		}
	}
	return out
}

func IsValidThreadID(id string) bool {
	value := strings.TrimSpace(id)
	if value == "" || value == "." || value == ".." || strings.Contains(value, "..") {
		return false
	}
	return !strings.ContainsAny(value, `/\:`)
}

func parseCitationEntry(line string) *CitationEntry {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	location, notePart, ok := strings.Cut(line, "|note=[")
	if !ok || !strings.HasSuffix(notePart, "]") {
		return nil
	}
	note := strings.TrimSpace(strings.TrimSuffix(notePart, "]"))
	location = strings.TrimSpace(location)
	index := strings.LastIndex(location, ":")
	if index < 0 {
		return nil
	}
	path := location[:index]
	lineRange := location[index+1:]
	startText, endText, ok := strings.Cut(lineRange, "-")
	if !ok {
		return nil
	}
	lineStart, err := strconv.ParseInt(strings.TrimSpace(startText), 10, 64)
	if err != nil {
		return nil
	}
	lineEnd, err := strconv.ParseInt(strings.TrimSpace(endText), 10, 64)
	if err != nil {
		return nil
	}
	return &CitationEntry{
		Path:      strings.TrimSpace(path),
		LineStart: lineStart,
		LineEnd:   lineEnd,
		Note:      note,
	}
}

func extractIDsBlock(text string) (string, bool) {
	if block, ok := extractBlock(text, "<rollout_ids>", "</rollout_ids>"); ok {
		return block, true
	}
	return extractBlock(text, "<thread_ids>", "</thread_ids>")
}

func extractBlock(text string, open string, close string) (string, bool) {
	_, rest, ok := strings.Cut(text, open)
	if !ok {
		return "", false
	}
	body, _, ok := strings.Cut(rest, close)
	return body, ok
}
