package tui

import "strings"

// Rust parity: codex-rs/tui/src/update_prompt.rs.

const ReleaseNotesURL = "https://github.com/openai/codex/releases/latest"

type UpdatePrompt struct {
	Version string
	Action  UpdateAction
}

type UpdatePromptOutcome string

const (
	UpdatePromptOutcomeContinue  UpdatePromptOutcome = "continue"
	UpdatePromptOutcomeRunUpdate UpdatePromptOutcome = "run_update"
)

type UpdateSelection string

const (
	UpdateSelectionUpdateNow  UpdateSelection = "update_now"
	UpdateSelectionNotNow     UpdateSelection = "not_now"
	UpdateSelectionDontRemind UpdateSelection = "dont_remind"
)

type UpdatePromptScreen struct {
	LatestVersion  string
	CurrentVersion string
	UpdateAction   UpdateAction
	Highlighted    UpdateSelection
	Selection      *UpdateSelection
}

func NewUpdatePromptScreen(latestVersion string, currentVersion string, updateAction UpdateAction) *UpdatePromptScreen {
	return &UpdatePromptScreen{
		LatestVersion:  strings.TrimSpace(latestVersion),
		CurrentVersion: strings.TrimSpace(currentVersion),
		UpdateAction:   updateAction,
		Highlighted:    UpdateSelectionUpdateNow,
	}
}

func (s *UpdatePromptScreen) IsDone() bool {
	return s != nil && s.Selection != nil
}

func (s *UpdatePromptScreen) SetHighlight(highlight UpdateSelection) {
	if s == nil {
		return
	}
	if !validUpdateSelection(highlight) {
		return
	}
	s.Highlighted = highlight
}

func (s *UpdatePromptScreen) Select(selection UpdateSelection) {
	if s == nil || !validUpdateSelection(selection) {
		return
	}
	s.Highlighted = selection
	selected := selection
	s.Selection = &selected
}

func (s *UpdatePromptScreen) Move(delta int) {
	if s == nil || delta == 0 {
		return
	}
	next := s.Highlighted
	steps := delta
	if steps < 0 {
		steps = -steps
	}
	for i := 0; i < steps; i++ {
		if delta > 0 {
			next = nextUpdateSelection(next)
		} else {
			next = previousUpdateSelection(next)
		}
	}
	s.Highlighted = next
}

func (s *UpdatePromptScreen) HandleKey(key string) {
	if s == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "up", "k":
		s.Move(-1)
	case "down", "j":
		s.Move(1)
	case "1":
		s.Select(UpdateSelectionUpdateNow)
	case "2", "esc", "ctrl-c", "ctrl-d":
		s.Select(UpdateSelectionNotNow)
	case "3":
		s.Select(UpdateSelectionDontRemind)
	case "enter":
		s.Select(s.Highlighted)
	}
}

func (s *UpdatePromptScreen) Outcome() (UpdatePromptOutcome, UpdateAction) {
	if s == nil || s.Selection == nil || *s.Selection != UpdateSelectionUpdateNow {
		return UpdatePromptOutcomeContinue, ""
	}
	return UpdatePromptOutcomeRunUpdate, s.UpdateAction
}

func (s *UpdatePromptScreen) Rows(width int) []string {
	if s == nil {
		return nil
	}
	current := s.CurrentVersion
	if current == "" {
		current = "current"
	}
	latest := s.LatestVersion
	if latest == "" {
		latest = "latest"
	}
	rows := []string{
		"",
		"Update available! " + current + " -> " + latest,
		"",
		"  Release notes: " + ReleaseNotesURL,
		"",
	}
	options := []struct {
		selection UpdateSelection
		label     string
	}{
		{UpdateSelectionUpdateNow, "Update now (runs `" + s.UpdateAction.CommandString() + "`)"},
		{UpdateSelectionNotNow, "Skip"},
		{UpdateSelectionDontRemind, "Skip until next version"},
	}
	for i, option := range options {
		selected := s.Highlighted == option.selection
		line := NumberedSelectionPrefix(i, selected) + option.label
		if width > 0 {
			line = TruncateWithEllipsis(line, width)
		}
		if selected {
			line = RenderSelectedRow(line)
		}
		rows = append(rows, line)
	}
	rows = append(rows, "", "  Press Enter to continue")
	return rows
}

func validUpdateSelection(selection UpdateSelection) bool {
	switch selection {
	case UpdateSelectionUpdateNow, UpdateSelectionNotNow, UpdateSelectionDontRemind:
		return true
	default:
		return false
	}
}

func nextUpdateSelection(selection UpdateSelection) UpdateSelection {
	switch selection {
	case UpdateSelectionUpdateNow:
		return UpdateSelectionNotNow
	case UpdateSelectionNotNow:
		return UpdateSelectionDontRemind
	default:
		return UpdateSelectionUpdateNow
	}
}

func previousUpdateSelection(selection UpdateSelection) UpdateSelection {
	switch selection {
	case UpdateSelectionUpdateNow:
		return UpdateSelectionDontRemind
	case UpdateSelectionNotNow:
		return UpdateSelectionUpdateNow
	default:
		return UpdateSelectionNotNow
	}
}
