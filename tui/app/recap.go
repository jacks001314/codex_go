package app

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// This file ports the pure half of Rust tui/src/app/recap.rs: the recap history
// projection, prompt/schema construction, response parsing, and the automatic
// recap scheduling state machine. The temporary structured turn and the TUI
// surface live in the app/tea layers.

const (
	recapMinCompletedTurns     = 3
	recapMinTurnsBetweenRecaps = 2
	// RecapDelay is how long an unfocused, finished conversation must be idle
	// before an automatic recap is requested (Rust RECAP_DELAY).
	RecapDelay = 3 * time.Minute
	// RecapRetryDelay is the retry backoff after a failed automatic recap (Rust
	// RECAP_RETRY_DELAY).
	RecapRetryDelay = 30 * time.Second

	recapHistoryMaxTurns = 8
	// RecapMaxChars bounds the generated recap text (Rust RECAP_MAX_CHARS).
	RecapMaxChars = 320
	// RecapPromptMaxBytes bounds the history embedded in the recap prompt (Rust
	// RECAP_PROMPT_MAX_BYTES).
	RecapPromptMaxBytes = 900

	// ManualRecapFailureMessage is shown when a manual recap fails (Rust
	// MANUAL_RECAP_FAILURE_MESSAGE).
	ManualRecapFailureMessage = "Could not generate a recap. Please try again."
	// ManualRecapInProgressMessage is shown when a recap is already running.
	ManualRecapInProgressMessage = "A recap is already being generated."
	// ManualRecapEmptyHistoryMessage is shown when there is nothing to recap.
	ManualRecapEmptyHistoryMessage = "There is no conversation history to recap."

	// RecapPromptPrefix is the instruction preceding the bounded history (Rust
	// RECAP_PROMPT_PREFIX).
	RecapPromptPrefix = "Write a brief catch-up for a user returning to this Codex task. " +
		"In at most 40 words and one or two plain-text sentences, explain the " +
		"objective, what was completed or learned, and the next step or blocker. " +
		"Mention changed files, tests, approvals, or requested decisions only " +
		"when relevant. Never claim changes were made or tests passed unless " +
		"the conversation confirms it. If the task is complete, say so instead " +
		"of inventing more work. Use the user's language; omit greetings, " +
		"markdown, lists, and tool chatter.\n\nRecent conversation:\n"
)

// RenderRecapMessage renders one "Role: content" line truncated to maxBytes at a
// UTF-8 boundary. It reports false when even the role prefix does not fit (Rust
// render_recap_message).
func RenderRecapMessage(role string, content string, maxBytes int) (string, bool) {
	prefix := role + ": "
	budget := maxBytes - len(prefix)
	if budget < 0 {
		return "", false
	}
	end := budget
	if end > len(content) {
		end = len(content)
	}
	for end > 0 && end < len(content) && !utf8.RuneStart(content[end]) {
		end--
	}
	return prefix + content[:end], true
}

// RecapHistory builds the bounded "Recent conversation" block: the newest
// assistant/user messages (up to recapHistoryMaxTurns user turns) within the
// prompt byte budget, reserving half of it for the latest user request (Rust
// recap_history). Go's transcript message carries only rendered text, so the
// cell role mapping degenerates to RoleUser/RoleAssistant.
func RecapHistory(messages []codextui.Message) string {
	type entry struct {
		role    string
		content string
	}
	var entries []entry
	userTurns := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		isUser := message.Role == codextui.RoleUser
		role := ""
		switch {
		case isUser:
			role = "User"
		case message.Role == codextui.RoleAssistant:
			role = "Assistant"
		default:
			continue
		}
		content := strings.TrimSpace(firstNonEmptyString(message.RawText, message.Text))
		if content == "" {
			continue
		}
		entries = append(entries, entry{role: role, content: content})
		if isUser {
			userTurns++
			if userTurns == recapHistoryMaxTurns {
				break
			}
		}
	}
	if len(entries) == 0 {
		return ""
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	budget := RecapPromptMaxBytes - len(RecapPromptPrefix)
	if budget < 0 {
		budget = 0
	}
	latest := len(entries) - 1
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].role == "User" {
			latest = index
			break
		}
	}
	selected := make([]struct {
		index int
		text  string
	}, 0, len(entries))
	latestText, ok := RenderRecapMessage(entries[latest].role, entries[latest].content, budget/2)
	if !ok {
		latestText = ""
	}
	selected = append(selected, struct {
		index int
		text  string
	}{latest, latestText})
	remaining := budget - len(latestText)
	for index := len(entries) - 1; index >= 0; index-- {
		if index == latest || remaining <= 2 {
			continue
		}
		rendered, ok := RenderRecapMessage(entries[index].role, entries[index].content, remaining-2)
		if !ok {
			continue
		}
		remaining -= len(rendered) + 2
		selected = append(selected, struct {
			index int
			text  string
		}{index, rendered})
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].index < selected[j].index })
	texts := make([]string, 0, len(selected))
	for _, item := range selected {
		texts = append(texts, item.text)
	}
	return strings.Join(texts, "\n\n")
}

// RecapPrompt prepends the recap instructions to the history (Rust
// recap_prompt).
func RecapPrompt(history string) string {
	return RecapPromptPrefix + strings.TrimSpace(history)
}

// RecapOutputSchema is the JSON schema the temporary structured turn must
// satisfy (Rust recap_output_schema).
func RecapOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"recap": map[string]any{
				"type":      "string",
				"minLength": 1,
				"maxLength": RecapMaxChars,
			},
		},
		"required":             []string{"recap"},
		"additionalProperties": false,
	}
}

// ParseRecap extracts and bounds the generated recap text (Rust parse_recap).
func ParseRecap(response string) (string, bool) {
	var generated struct {
		Recap string `json:"recap"`
	}
	if err := json.Unmarshal([]byte(response), &generated); err != nil {
		return "", false
	}
	recap := strings.TrimSpace(generated.Recap)
	if recap == "" {
		return "", false
	}
	runes := []rune(recap)
	if len(runes) > RecapMaxChars {
		runes = runes[:RecapMaxChars]
	}
	return string(runes), true
}

// RecapProgress is the recap accounting persisted across a resume (Rust
// RecapProgress).
type RecapProgress struct {
	CompletedTurns        int
	LastRecappedTurnCount *int
}

// RecapProgressFromTurns seeds progress from a thread's completed turns.
func RecapProgressFromTurns(turns []appserver.Turn) RecapProgress {
	progress := RecapProgress{}
	for _, turn := range turns {
		if strings.EqualFold(strings.TrimSpace(string(turn.Status)), string(appserver.TurnStatusCompleted)) {
			progress.CompletedTurns++
		}
	}
	return progress
}

// RecapState drives automatic recap scheduling (Rust RecapState).
type RecapState struct {
	UnfocusedSince     *time.Time
	LastTurnFinishedAt *time.Time
	CompletedTurns     int
	LastRecappedTurns  *int
	TurnRevision       int
	RetryRevision      *int
}

// SeedFromTurns seeds the state from a thread's loaded turns.
func (s *RecapState) SeedFromTurns(turns []appserver.Turn, now time.Time) {
	s.SeedFromProgress(RecapProgressFromTurns(turns), now)
}

// SeedFromProgress merges progress and marks the last-turn timestamp.
func (s *RecapState) SeedFromProgress(progress RecapProgress, now time.Time) {
	if s == nil {
		return
	}
	if progress.CompletedTurns > s.CompletedTurns {
		s.CompletedTurns = progress.CompletedTurns
	}
	if progress.LastRecappedTurnCount != nil && (s.LastRecappedTurns == nil || *progress.LastRecappedTurnCount > *s.LastRecappedTurns) {
		value := *progress.LastRecappedTurnCount
		s.LastRecappedTurns = &value
	}
	if progress.CompletedTurns > 0 && s.LastTurnFinishedAt == nil {
		value := now
		s.LastTurnFinishedAt = &value
	}
}

// Progress reports the mergeable recap accounting.
func (s *RecapState) Progress() RecapProgress {
	if s == nil {
		return RecapProgress{}
	}
	progress := RecapProgress{CompletedTurns: s.CompletedTurns}
	if s.LastRecappedTurns != nil {
		value := *s.LastRecappedTurns
		progress.LastRecappedTurnCount = &value
	}
	return progress
}

// ResetForNewThread clears the per-thread accounting while preserving the
// unfocused anchor (Rust reset_for_new_thread).
func (s *RecapState) ResetForNewThread(now time.Time) {
	if s == nil {
		return
	}
	var unfocused *time.Time
	if s.UnfocusedSince != nil {
		value := now
		unfocused = &value
	}
	*s = RecapState{UnfocusedSince: unfocused}
}

// NoteFocusLost anchors the unfocused deadline (Rust note_focus_lost).
func (s *RecapState) NoteFocusLost(now time.Time) {
	if s == nil {
		return
	}
	if s.UnfocusedSince == nil {
		s.RetryRevision = nil
		value := now
		s.UnfocusedSince = &value
	}
}

// NoteFocusGained clears the unfocused anchor and any retry gating (Rust
// note_focus_gained).
func (s *RecapState) NoteFocusGained() {
	if s == nil {
		return
	}
	s.UnfocusedSince = nil
	s.RetryRevision = nil
}

// NoteTurnFinished records a completed turn (Rust note_turn_finished).
func (s *RecapState) NoteTurnFinished(status appserver.TurnStatus, now time.Time) {
	if s == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(string(status)), string(appserver.TurnStatusCompleted)) {
		s.CompletedTurns++
	}
	s.TurnRevision++
	value := now
	s.LastTurnFinishedAt = &value
}

// MarkRecapped records the completed-turn count a recap covered.
func (s *RecapState) MarkRecapped(completedTurnCount int) {
	if s == nil {
		return
	}
	value := completedTurnCount
	s.LastRecappedTurns = &value
}

// ShouldGenerate reports whether the automatic deadline has elapsed (Rust
// should_generate).
func (s *RecapState) ShouldGenerate(now time.Time) bool {
	deadline, ok := s.NextCheckDeadline()
	return ok && !now.Before(deadline)
}

// NextCheckDeadline computes when an automatic recap becomes eligible (Rust
// next_check_deadline).
func (s *RecapState) NextCheckDeadline() (time.Time, bool) {
	if s == nil || s.UnfocusedSince == nil {
		return time.Time{}, false
	}
	if s.CompletedTurns < recapMinCompletedTurns {
		return time.Time{}, false
	}
	if s.LastRecappedTurns != nil && s.CompletedTurns-*s.LastRecappedTurns < recapMinTurnsBetweenRecaps {
		return time.Time{}, false
	}
	if s.LastTurnFinishedAt == nil {
		return time.Time{}, false
	}
	anchor := *s.UnfocusedSince
	if s.LastTurnFinishedAt.After(anchor) {
		anchor = *s.LastTurnFinishedAt
	}
	return anchor.Add(RecapDelay), true
}

// BeginRetry gates an automatic retry to once per turn revision (Rust
// schedule_retry's guard).
func (s *RecapState) BeginRetry(turnRevision int) bool {
	if s == nil || s.TurnRevision != turnRevision || (s.RetryRevision != nil && *s.RetryRevision == turnRevision) {
		return false
	}
	value := turnRevision
	s.RetryRevision = &value
	return true
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
