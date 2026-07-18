package tui

import (
	"strconv"
	"strings"

	"codex_go/appserver"
)

// Rust parity: codex-rs/tui/src/startup_hooks_review.rs.

type StartupHookReview struct {
	HookID      string
	NeedsReview bool
}

type StartupHooksReviewOutcomeKind string

const (
	StartupHooksReviewContinue         StartupHooksReviewOutcomeKind = "continue"
	StartupHooksReviewOpenHooksBrowser StartupHooksReviewOutcomeKind = "open_hooks_browser"
)

type StartupHooksReviewOutcome struct {
	Kind  StartupHooksReviewOutcomeKind
	Entry *appserver.HookListEntry
}

type StartupHooksReviewSelection string

const (
	StartupHooksReviewSelectionReviewHooks             StartupHooksReviewSelection = "review_hooks"
	StartupHooksReviewSelectionTrustAllAndContinue     StartupHooksReviewSelection = "trust_all_and_continue"
	StartupHooksReviewSelectionContinueWithoutTrusting StartupHooksReviewSelection = "continue_without_trusting"
)

type StartupHooksReviewScreen struct {
	Entry         appserver.HookListEntry
	TrustAllError string
	TrustingAll   bool

	list    *SelectionList
	done    bool
	outcome StartupHooksReviewOutcome
}

func NewStartupHooksReviewOutcomeContinue() StartupHooksReviewOutcome {
	return StartupHooksReviewOutcome{Kind: StartupHooksReviewContinue}
}

func NewStartupHooksReviewOutcomeOpen(entry appserver.HookListEntry) StartupHooksReviewOutcome {
	copyEntry := cloneHookListEntry(entry)
	return StartupHooksReviewOutcome{Kind: StartupHooksReviewOpenHooksBrowser, Entry: &copyEntry}
}

func NewStartupHooksReviewScreen(entry appserver.HookListEntry) *StartupHooksReviewScreen {
	return &StartupHooksReviewScreen{
		Entry: entry,
		list: NewSelectionList([]SelectionItem{
			{ID: string(StartupHooksReviewSelectionReviewHooks), Label: "Review hooks"},
			{ID: string(StartupHooksReviewSelectionTrustAllAndContinue), Label: "Trust all and continue"},
			{ID: string(StartupHooksReviewSelectionContinueWithoutTrusting), Label: "Continue without trusting (hooks won't run)"},
		}),
	}
}

func StartupHooksReviewNeededCount(entry *appserver.HookListEntry) int {
	if entry == nil {
		return 0
	}
	count := 0
	for _, hook := range entry.Hooks {
		if HookNeedsReview(hook) {
			count++
		}
	}
	return count
}

func StartupHooksReviewIsNeeded(bypassHookTrust bool, entry *appserver.HookListEntry) bool {
	return !bypassHookTrust && StartupHooksReviewNeededCount(entry) > 0
}

func StartupHooksTrustUpdates(entry *appserver.HookListEntry) []HookTrustUpdate {
	if entry == nil {
		return nil
	}
	updates := make([]HookTrustUpdate, 0, StartupHooksReviewNeededCount(entry))
	for _, hook := range entry.Hooks {
		if HookNeedsReview(hook) {
			updates = append(updates, HookTrustUpdate{Key: hook.Key, CurrentHash: hook.CurrentHash})
		}
	}
	return updates
}

func MaybeStartupHooksReviewOutcome(bypassHookTrust bool, entry appserver.HookListEntry) (StartupHooksReviewOutcome, bool) {
	if !StartupHooksReviewIsNeeded(bypassHookTrust, &entry) {
		return NewStartupHooksReviewOutcomeContinue(), true
	}
	return StartupHooksReviewOutcome{}, false
}

func (s *StartupHooksReviewScreen) Rows() []string {
	if s == nil {
		return nil
	}
	rows := []string{
		"Hooks need review",
		startupHooksReviewCountLine(StartupHooksReviewNeededCount(&s.Entry)),
		"Hooks can run outside the sandbox after you trust them.",
	}
	if s.TrustAllError != "" {
		rows = append(rows, splitNonEmptyLines(s.TrustAllError)...)
	} else if s.TrustingAll {
		rows = append(rows, "Trusting hooks...")
	}
	rows = append(rows, "")
	rows = append(rows, s.renderSelectionRows()...)
	rows = append(rows, "", "Press enter to confirm or esc to go back")
	return rows
}

func (s *StartupHooksReviewScreen) Move(delta int) {
	if s == nil || s.TrustingAll {
		return
	}
	s.list.Move(delta)
}

func (s *StartupHooksReviewScreen) Select(selection StartupHooksReviewSelection) {
	if s == nil || s.TrustingAll {
		return
	}
	switch selection {
	case StartupHooksReviewSelectionReviewHooks:
		s.finish(NewStartupHooksReviewOutcomeOpen(s.Entry))
	case StartupHooksReviewSelectionTrustAllAndContinue:
		s.TrustAllError = ""
		s.TrustingAll = true
	case StartupHooksReviewSelectionContinueWithoutTrusting:
		s.finish(NewStartupHooksReviewOutcomeContinue())
	}
}

func (s *StartupHooksReviewScreen) HandleKey(key string) {
	if s == nil {
		return
	}
	switch startupHooksReviewNormalizeKey(key) {
	case "up", "k", "ctrl-p", "ctrl-k":
		s.Move(-1)
	case "down", "j", "ctrl-n", "ctrl-j":
		s.Move(1)
	case "1":
		s.setSelected(0)
		s.Select(StartupHooksReviewSelectionReviewHooks)
	case "2":
		s.setSelected(1)
		s.Select(StartupHooksReviewSelectionTrustAllAndContinue)
	case "3":
		s.setSelected(2)
		s.Select(StartupHooksReviewSelectionContinueWithoutTrusting)
	case "enter":
		selection, ok := s.HighlightedSelection()
		if ok {
			s.Select(selection)
		}
	case "esc", "escape":
		s.Select(StartupHooksReviewSelectionContinueWithoutTrusting)
	}
}

func (s *StartupHooksReviewScreen) HighlightedSelection() (StartupHooksReviewSelection, bool) {
	if s == nil || s.list == nil {
		return "", false
	}
	item, ok := s.list.SelectedItem()
	if !ok {
		return "", false
	}
	return StartupHooksReviewSelection(item.ID), true
}

func (s *StartupHooksReviewScreen) SetTrustingAll(trusting bool) {
	if s == nil {
		return
	}
	s.TrustingAll = trusting
	if trusting {
		s.TrustAllError = ""
	}
}

func (s *StartupHooksReviewScreen) TrustAllSucceeded() {
	if s == nil {
		return
	}
	s.TrustingAll = false
	s.finish(NewStartupHooksReviewOutcomeContinue())
}

func (s *StartupHooksReviewScreen) TrustAllFailed(err string) {
	if s == nil {
		return
	}
	s.TrustingAll = false
	s.TrustAllError = strings.TrimSpace(err)
}

func (s *StartupHooksReviewScreen) IsDone() bool {
	return s != nil && s.done
}

func (s *StartupHooksReviewScreen) Outcome() StartupHooksReviewOutcome {
	if s == nil || !s.done {
		return StartupHooksReviewOutcome{}
	}
	return s.outcome
}

func (s *StartupHooksReviewScreen) TrustUpdates() []HookTrustUpdate {
	if s == nil {
		return nil
	}
	return StartupHooksTrustUpdates(&s.Entry)
}

func (s *StartupHooksReviewScreen) finish(outcome StartupHooksReviewOutcome) {
	s.outcome = outcome
	s.done = true
}

func (s *StartupHooksReviewScreen) setSelected(index int) {
	if s == nil || s.list == nil || s.TrustingAll {
		return
	}
	s.list.Select(index)
}

func (s *StartupHooksReviewScreen) renderSelectionRows() []string {
	labels := []struct {
		selection StartupHooksReviewSelection
		label     string
	}{
		{StartupHooksReviewSelectionReviewHooks, "Review hooks"},
		{StartupHooksReviewSelectionTrustAllAndContinue, "Trust all and continue"},
		{StartupHooksReviewSelectionContinueWithoutTrusting, "Continue without trusting (hooks won't run)"},
	}
	highlighted, _ := s.HighlightedSelection()
	rows := make([]string, 0, len(labels))
	for idx, item := range labels {
		selected := !s.TrustingAll && highlighted == item.selection
		row := NumberedSelectionPrefix(idx, selected) + item.label
		if s.TrustingAll {
			row += " (disabled)"
		}
		if selected {
			row = RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	return rows
}

func startupHooksReviewCountLine(count int) string {
	if count == 1 {
		return "1 hook is new or changed."
	}
	return strconv.Itoa(count) + " hooks are new or changed."
}

func startupHooksReviewNormalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "+", "-")
	return key
}

func splitNonEmptyLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func cloneHookListEntry(entry appserver.HookListEntry) appserver.HookListEntry {
	entry.Hooks = append([]appserver.HookMetadata(nil), entry.Hooks...)
	entry.Warnings = append([]string(nil), entry.Warnings...)
	entry.Errors = append([]appserver.HookErrorInfo(nil), entry.Errors...)
	return entry
}
