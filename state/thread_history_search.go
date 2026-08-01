package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
)

const (
	threadHistorySnippetBeforeChars = 48
	threadHistorySnippetAfterChars  = 96
)

type ThreadHistorySearchOccurrencesParams struct {
	ThreadID   string
	SearchTerm string
	Cursor     *string
	PageSize   int
}

type ThreadHistorySearchTextRange struct {
	Start uint32
	End   uint32
}

type ThreadHistoryOccurrence struct {
	TurnID            string
	ItemID            string
	Snippet           string
	SnippetMatchRange ThreadHistorySearchTextRange
	TurnCursor        string
}

type ThreadHistoryOccurrencePage struct {
	Items      []ThreadHistoryOccurrence
	NextCursor *string
}

type threadHistorySearchCursor struct {
	ThreadID            string `json:"threadId"`
	SearchTerm          string `json:"searchTerm"`
	NextRolloutOrdinal  int64  `json:"nextRolloutOrdinal"`
	NextOccurrenceIndex int    `json:"nextOccurrenceIndex"`
}

type threadHistorySearchCandidate struct {
	turnID             string
	itemID             string
	rolloutOrdinal     int64
	itemJSON           string
	turnRolloutOrdinal int64
}

type threadHistorySearchMatch struct {
	start int
	end   int
}

type threadHistoryRuneSpan struct {
	lowerStart, lowerEnd       int
	originalStart, originalEnd int
}

type threadHistoryMatchingCandidate struct {
	row                  threadHistorySearchCandidate
	text                 string
	matches              []threadHistorySearchMatch
	firstOccurrenceIndex int
}

func (r *StateRuntime) SearchThreadHistoryOccurrences(ctx context.Context, params ThreadHistorySearchOccurrencesParams) (*ThreadHistoryOccurrencePage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(params.SearchTerm) == "" {
		return nil, invalidThreadHistory("thread/searchOccurrences requires search_term")
	}
	if params.PageSize <= 0 {
		return nil, invalidThreadHistory("thread/searchOccurrences requires page_size greater than zero")
	}
	lineage, db, err := r.prepareThreadHistoryRead(ctx, params.ThreadID, "thread/searchOccurrences")
	if err != nil {
		return nil, err
	}
	cursor, err := parseThreadHistorySearchCursor(params.Cursor, params.ThreadID, params.SearchTerm)
	if err != nil {
		return nil, err
	}
	cursorSegment := -1
	if cursor != nil {
		for i := range lineage {
			if cursor.NextRolloutOrdinal >= int64(lineage[i].Start) && (lineage[i].End == nil || cursor.NextRolloutOrdinal < int64(lineage[i].End.EndOrdinalExclusive)) {
				cursorSegment = i
				break
			}
		}
		if cursorSegment < 0 {
			return nil, invalidThreadHistory("invalid cursor: position outside thread lineage")
		}
	}

	matcher := newThreadHistoryLiteralMatcher(params.SearchTerm)
	items := make([]ThreadHistoryOccurrence, 0, params.PageSize)
	effectiveTurnOrdinals := map[string]int64{}
	startSegment := 0
	if cursorSegment >= 0 {
		startSegment = cursorSegment
	}
	for segmentIndex := startSegment; segmentIndex < len(lineage); segmentIndex++ {
		segment := lineage[segmentIndex]
		nextOrdinal := int64(segment.Start)
		if segmentIndex == cursorSegment {
			nextOrdinal = cursor.NextRolloutOrdinal
		}
		endOrdinal := int64(^uint64(0) >> 1)
		if segment.End != nil {
			endOrdinal = int64(segment.End.EndOrdinalExclusive)
		}
		rows, queryErr := queryThreadHistorySearchCandidates(ctx, db, segment.ThreadID, nextOrdinal, int64(segment.Start), endOrdinal)
		if queryErr != nil {
			return nil, queryErr
		}
		matchingRows, scanErr := collectThreadHistorySearchCandidates(rows, cursor, matcher, params.PageSize, len(items))
		if closeErr := rows.Close(); scanErr == nil && closeErr != nil {
			scanErr = closeErr
		}
		if scanErr != nil {
			return nil, scanErr
		}

		for _, candidate := range matchingRows {
			turnOrdinal := candidate.row.turnRolloutOrdinal
			if segmentIndex+1 != len(lineage) {
				if cached, ok := effectiveTurnOrdinals[candidate.row.turnID]; ok {
					turnOrdinal = cached
				} else {
					turnOrdinal, err = findVisibleThreadHistoryTurnOrdinal(ctx, db, lineage, candidate.row.turnID)
					if err != nil {
						return nil, err
					}
					effectiveTurnOrdinals[candidate.row.turnID] = turnOrdinal
				}
			}
			turnCursor, cursorErr := serializeHistoryCursor(params.ThreadID, historyCursorTurns, turnOrdinal, true)
			if cursorErr != nil {
				return nil, cursorErr
			}
			for occurrenceIndex := candidate.firstOccurrenceIndex; occurrenceIndex < len(candidate.matches); occurrenceIndex++ {
				if len(items) == params.PageSize {
					next, serializeErr := serializeThreadHistorySearchCursor(threadHistorySearchCursor{
						ThreadID: params.ThreadID, SearchTerm: params.SearchTerm,
						NextRolloutOrdinal: candidate.row.rolloutOrdinal, NextOccurrenceIndex: occurrenceIndex,
					})
					if serializeErr != nil {
						return nil, serializeErr
					}
					return &ThreadHistoryOccurrencePage{Items: items, NextCursor: next}, nil
				}
				items = append(items, makeThreadHistoryOccurrence(candidate.row, candidate.text, candidate.matches[occurrenceIndex], *turnCursor))
			}
		}
	}
	return &ThreadHistoryOccurrencePage{Items: items}, nil
}

func queryThreadHistorySearchCandidates(ctx context.Context, db *sql.DB, threadID string, nextOrdinal, segmentStart, endOrdinal int64) (*sql.Rows, error) {
	rows, err := db.QueryContext(ctx, `
SELECT turn_id, item_id, rollout_ordinal, item_json, turn_rollout_ordinal
FROM (
    SELECT items.turn_id, items.item_id, items.rollout_ordinal, items.item_json,
           turns.rollout_ordinal AS turn_rollout_ordinal
    FROM thread_items AS items
    JOIN thread_turns AS turns
      ON turns.thread_id = items.thread_id AND turns.turn_id = items.turn_id
    WHERE items.thread_id = ?
      AND items.item_type = 'userMessage'
      AND items.rollout_ordinal >= ? AND items.rollout_ordinal < ?
      AND turns.rollout_ordinal >= ? AND turns.rollout_ordinal < ?

    UNION ALL

    SELECT items.turn_id, items.item_id, items.rollout_ordinal, items.item_json,
           turns.rollout_ordinal AS turn_rollout_ordinal
    FROM thread_turns AS turns
    JOIN thread_items AS items
      ON items.thread_id = turns.thread_id
     AND items.turn_id = turns.turn_id
     AND items.item_id = turns.final_agent_item_id
    WHERE turns.thread_id = ?
      AND turns.final_agent_item_id IS NOT NULL
      AND items.rollout_ordinal >= ? AND items.rollout_ordinal < ?
      AND turns.rollout_ordinal >= ? AND turns.rollout_ordinal < ?
)
ORDER BY rollout_ordinal ASC`,
		threadID, nextOrdinal, endOrdinal, segmentStart, endOrdinal,
		threadID, nextOrdinal, endOrdinal, segmentStart, endOrdinal)
	if err != nil {
		return nil, fmt.Errorf("search thread history occurrences: %w", err)
	}
	return rows, nil
}

func collectThreadHistorySearchCandidates(rows *sql.Rows, cursor *threadHistorySearchCursor, matcher threadHistoryLiteralMatcher, pageSize, existingItems int) ([]threadHistoryMatchingCandidate, error) {
	matchingRows := []threadHistoryMatchingCandidate{}
	matchingOccurrences := 0
	for rows.Next() {
		var row threadHistorySearchCandidate
		if err := rows.Scan(&row.turnID, &row.itemID, &row.rolloutOrdinal, &row.itemJSON, &row.turnRolloutOrdinal); err != nil {
			return nil, fmt.Errorf("scan thread history search candidate: %w", err)
		}
		if row.rolloutOrdinal < 0 || row.turnRolloutOrdinal < 0 {
			return nil, errors.New("invalid stored thread history ordinal")
		}
		text, ok, err := searchableThreadHistoryItemText(row.itemJSON)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		firstOccurrenceIndex := 0
		if cursor != nil && cursor.NextRolloutOrdinal == row.rolloutOrdinal {
			firstOccurrenceIndex = cursor.NextOccurrenceIndex
		}
		remaining := pageSize + 1 - existingItems - matchingOccurrences
		if remaining < 0 {
			remaining = 0
		}
		matches := matcher.findRanges(text, firstOccurrenceIndex+remaining)
		if len(matches) <= firstOccurrenceIndex {
			continue
		}
		matchingOccurrences += len(matches) - firstOccurrenceIndex
		matchingRows = append(matchingRows, threadHistoryMatchingCandidate{
			row: row, text: text, matches: matches, firstOccurrenceIndex: firstOccurrenceIndex,
		})
		if matchingOccurrences == pageSize+1-existingItems {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search thread history occurrences: %w", err)
	}
	return matchingRows, nil
}

func findVisibleThreadHistoryTurnOrdinal(ctx context.Context, db *sql.DB, lineage []threadHistoryLineageSegment, turnID string) (int64, error) {
	for i := len(lineage) - 1; i >= 0; i-- {
		segment := lineage[i]
		var ordinal int64
		err := db.QueryRowContext(ctx, `
SELECT rollout_ordinal FROM thread_turns
WHERE thread_id = ? AND turn_id = ? AND rollout_ordinal >= ?
  AND (? IS NULL OR rollout_ordinal < ?)`,
			segment.ThreadID, turnID, sqliteInt(segment.Start), nullableHistoryEnd(segment.End), nullableHistoryEnd(segment.End)).Scan(&ordinal)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("resolve visible thread history turn: %w", err)
		}
		if ordinal < 0 {
			return 0, errors.New("invalid stored thread history ordinal")
		}
		return ordinal, nil
	}
	return 0, invalidThreadHistory("turn not found: " + turnID)
}

func parseThreadHistorySearchCursor(value *string, threadID, searchTerm string) (*threadHistorySearchCursor, error) {
	if value == nil {
		return nil, nil
	}
	var cursor threadHistorySearchCursor
	if err := json.Unmarshal([]byte(*value), &cursor); err != nil || cursor.ThreadID != threadID || cursor.SearchTerm != searchTerm || cursor.NextRolloutOrdinal < 0 || cursor.NextOccurrenceIndex < 0 {
		return nil, invalidThreadHistory("invalid cursor: " + *value)
	}
	return &cursor, nil
}

func serializeThreadHistorySearchCursor(cursor threadHistorySearchCursor) (*string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return nil, fmt.Errorf("serialize thread history search cursor: %w", err)
	}
	value := string(data)
	return &value, nil
}

func searchableThreadHistoryItemText(itemJSON string) (string, bool, error) {
	var item struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(itemJSON), &item); err != nil {
		return "", false, fmt.Errorf("failed to deserialize stored thread item: %w", err)
	}
	switch item.Type {
	case "userMessage":
		var text strings.Builder
		for _, input := range item.Content {
			if input.Type == "text" {
				part := stripThreadHistoryUserMessagePrefix(input.Text)
				if part != "" {
					text.WriteString(part)
				}
			}
		}
		if text.Len() == 0 {
			return "", false, nil
		}
		return text.String(), true, nil
	case "agentMessage":
		text := markdownThreadHistorySearchText(item.Text)
		return text, text != "", nil
	default:
		return "", false, nil
	}
}

func stripThreadHistoryUserMessagePrefix(value string) string {
	const prefix = "## My request for Codex:"
	if index := strings.Index(value, prefix); index >= 0 {
		return strings.TrimSpace(value[index+len(prefix):])
	}
	return strings.TrimSpace(value)
}

func markdownThreadHistorySearchText(markdown string) string {
	source := []byte(strings.TrimSpace(markdown))
	if len(source) == 0 {
		return ""
	}
	document := goldmark.New().Parser().Parse(gmtext.NewReader(source))
	var out strings.Builder
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch value := node.(type) {
			case *ast.Text:
				out.Write(value.Value(source))
				if value.SoftLineBreak() || value.HardLineBreak() {
					out.WriteByte(' ')
				}
			case *ast.String:
				out.Write(value.Value)
			case *ast.RawHTML:
				out.Write(value.Text(source))
			case *ast.CodeBlock:
				out.Write(value.Text(source))
				return ast.WalkSkipChildren, nil
			case *ast.FencedCodeBlock:
				out.Write(value.Text(source))
				return ast.WalkSkipChildren, nil
			case *ast.HTMLBlock:
				out.Write(value.Text(source))
				return ast.WalkSkipChildren, nil
			case *ast.ThematicBreak:
				out.WriteByte(' ')
			}
			return ast.WalkContinue, nil
		}
		switch node.(type) {
		case *ast.Paragraph, *ast.Heading, *ast.Blockquote, *ast.CodeBlock, *ast.FencedCodeBlock, *ast.List, *ast.ListItem:
			out.WriteByte(' ')
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(out.String()), " ")
}

type threadHistoryLiteralMatcher struct {
	lowerNeedle string
}

func newThreadHistoryLiteralMatcher(needle string) threadHistoryLiteralMatcher {
	return threadHistoryLiteralMatcher{lowerNeedle: strings.ToLower(needle)}
}

func (m threadHistoryLiteralMatcher) findRanges(text string, limit int) []threadHistorySearchMatch {
	if limit <= 0 || m.lowerNeedle == "" {
		return nil
	}
	spans := make([]threadHistoryRuneSpan, 0, utf8.RuneCountInString(text))
	var lower strings.Builder
	for originalStart, r := range text {
		lowered := strings.ToLower(string(r))
		lowerStart := lower.Len()
		lower.WriteString(lowered)
		spans = append(spans, threadHistoryRuneSpan{
			lowerStart: lowerStart, lowerEnd: lower.Len(),
			originalStart: originalStart, originalEnd: originalStart + utf8.RuneLen(r),
		})
	}
	lowerText := lower.String()
	matches := make([]threadHistorySearchMatch, 0, limit)
	searchFrom := 0
	for len(matches) < limit && searchFrom <= len(lowerText) {
		relative := strings.Index(lowerText[searchFrom:], m.lowerNeedle)
		if relative < 0 {
			break
		}
		start := searchFrom + relative
		end := start + len(m.lowerNeedle)
		originalStart, originalEnd, ok := originalRangeForLowerMatch(spans, start, end)
		if ok {
			matches = append(matches, threadHistorySearchMatch{start: originalStart, end: originalEnd})
		}
		searchFrom = end
	}
	return matches
}

func originalRangeForLowerMatch(spans []threadHistoryRuneSpan, start, end int) (int, int, bool) {
	for i := range spans {
		if start >= spans[i].lowerStart && start < spans[i].lowerEnd {
			for j := i; j < len(spans); j++ {
				if end-1 >= spans[j].lowerStart && end-1 < spans[j].lowerEnd {
					return spans[i].originalStart, spans[j].originalEnd, true
				}
			}
			break
		}
	}
	return 0, 0, false
}

func makeThreadHistoryOccurrence(row threadHistorySearchCandidate, text string, matched threadHistorySearchMatch, turnCursor string) ThreadHistoryOccurrence {
	snippetStart := threadHistoryCharStartBefore(text, matched.start, threadHistorySnippetBeforeChars)
	snippetEnd := threadHistoryCharEndAfter(text, matched.end, threadHistorySnippetAfterChars)
	leadingEllipsis := snippetStart > 0
	trailingEllipsis := snippetEnd < len(text)
	var snippet strings.Builder
	if leadingEllipsis {
		snippet.WriteString("... ")
	}
	snippet.WriteString(text[snippetStart:snippetEnd])
	if trailingEllipsis {
		snippet.WriteString(" ...")
	}
	matchStart := threadHistoryUTF16Len(text[snippetStart:matched.start])
	if leadingEllipsis {
		matchStart += 4
	}
	matchLength := threadHistoryUTF16Len(text[matched.start:matched.end])
	return ThreadHistoryOccurrence{
		TurnID: row.turnID, ItemID: row.itemID, Snippet: snippet.String(),
		SnippetMatchRange: ThreadHistorySearchTextRange{Start: matchStart, End: matchStart + matchLength},
		TurnCursor:        turnCursor,
	}
}

func threadHistoryUTF16Len(value string) uint32 {
	var length uint64
	for _, r := range value {
		length++
		if r > 0xffff {
			length++
		}
		if length >= uint64(^uint32(0)) {
			return ^uint32(0)
		}
	}
	return uint32(length)
}

func threadHistoryCharStartBefore(text string, byteIndex, charsBefore int) int {
	boundaries := make([]int, 0, charsBefore+1)
	for index := range text[:byteIndex] {
		boundaries = append(boundaries, index)
	}
	target := len(boundaries) - 1 - charsBefore
	if target < 0 {
		return 0
	}
	return boundaries[target]
}

func threadHistoryCharEndAfter(text string, byteIndex, charsAfter int) int {
	count := 0
	for offset := range text[byteIndex:] {
		if count == charsAfter {
			return byteIndex + offset
		}
		count++
	}
	return len(text)
}
