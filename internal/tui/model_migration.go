package tui

import "strings"

// Rust parity: codex-rs/tui/src/model_migration.rs.

type ModelMigrationNotice struct {
	From string
	To   string
}

type ModelMigrationOutcome string

const (
	ModelMigrationAccepted ModelMigrationOutcome = "accepted"
	ModelMigrationRejected ModelMigrationOutcome = "rejected"
	ModelMigrationExit     ModelMigrationOutcome = "exit"
)

type ModelMigrationCopy struct {
	Heading   []string
	Content   []string
	CanOptOut bool
	Markdown  *string
}

type MigrationMenuOption string

const (
	MigrationMenuTryNewModel      MigrationMenuOption = "try_new_model"
	MigrationMenuUseExistingModel MigrationMenuOption = "use_existing_model"
)

func MigrationMenuOptions() []MigrationMenuOption {
	return []MigrationMenuOption{MigrationMenuTryNewModel, MigrationMenuUseExistingModel}
}

func (o MigrationMenuOption) Label() string {
	switch o {
	case MigrationMenuTryNewModel:
		return "Try new model"
	case MigrationMenuUseExistingModel:
		return "Use existing model"
	default:
		return string(o)
	}
}

func MigrationCopyForModels(currentModel string, targetModel string, modelLink *string, migrationCopy *string, migrationMarkdown *string, targetDisplayName string, targetDescription *string, canOptOut bool) ModelMigrationCopy {
	if migrationMarkdown != nil {
		markdown := FillMigrationMarkdown(*migrationMarkdown, currentModel, targetModel)
		return ModelMigrationCopy{CanOptOut: canOptOut, Markdown: &markdown}
	}

	heading := []string{"Codex just got an upgrade. Introducing " + targetDisplayName + "."}
	description := ""
	if migrationCopy != nil {
		description = *migrationCopy
	} else if targetDescription != nil && strings.TrimSpace(*targetDescription) != "" {
		description = *targetDescription
	} else {
		description = targetDisplayName + " is recommended for better performance and reliability."
	}

	content := []string{}
	if migrationCopy == nil {
		content = append(content,
			"We recommend switching from "+currentModel+" to "+targetModel+".",
			"",
		)
	}
	if modelLink != nil {
		content = append(content, description+" Learn more about "+targetDisplayName+" at "+*modelLink, "")
	} else {
		content = append(content, description, "")
	}
	if canOptOut {
		content = append(content, "You can continue using "+currentModel+" if you prefer.")
	} else {
		content = append(content, "Press enter to continue")
	}

	return ModelMigrationCopy{
		Heading:   heading,
		Content:   content,
		CanOptOut: canOptOut,
	}
}

func FillMigrationMarkdown(template string, currentModel string, targetModel string) string {
	return strings.ReplaceAll(strings.ReplaceAll(template, "{model_from}", currentModel), "{model_to}", targetModel)
}

type ModelMigrationScreen struct {
	copy              ModelMigrationCopy
	done              bool
	outcome           ModelMigrationOutcome
	highlightedOption MigrationMenuOption
}

func NewModelMigrationScreen(copy ModelMigrationCopy) *ModelMigrationScreen {
	return &ModelMigrationScreen{
		copy:              copy,
		outcome:           ModelMigrationAccepted,
		highlightedOption: MigrationMenuTryNewModel,
	}
}

func (s *ModelMigrationScreen) HandleKey(key string) {
	if s == nil || s.done {
		return
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "release" {
		return
	}
	if key == "ctrl-c" || key == "ctrl-d" {
		s.finishWith(ModelMigrationExit)
		return
	}
	if s.copy.CanOptOut {
		s.handleMenuKey(key)
		return
	}
	if key == "esc" || key == "escape" || key == "enter" {
		s.accept()
	}
}

func (s *ModelMigrationScreen) IsDone() bool {
	return s != nil && s.done
}

func (s *ModelMigrationScreen) Outcome() ModelMigrationOutcome {
	if s == nil {
		return ModelMigrationAccepted
	}
	return s.outcome
}

func (s *ModelMigrationScreen) HighlightedOption() MigrationMenuOption {
	if s == nil {
		return MigrationMenuTryNewModel
	}
	return s.highlightedOption
}

func (s *ModelMigrationScreen) Rows() []string {
	if s == nil {
		return nil
	}
	rows := []string{""}
	if s.copy.Markdown != nil {
		rows = append(rows, splitMigrationRows(*s.copy.Markdown)...)
	} else {
		for _, heading := range s.copy.Heading {
			rows = append(rows, "> "+heading)
		}
		rows = append(rows, "")
		rows = append(rows, s.copy.Content...)
	}
	if s.copy.CanOptOut {
		rows = append(rows, "", "Choose how you'd like Codex to proceed.", "")
		for idx, option := range MigrationMenuOptions() {
			selected := s.highlightedOption == option
			row := NumberedSelectionPrefix(idx, selected) + option.Label()
			if selected {
				row = RenderSelectedRow(row)
			}
			rows = append(rows, row)
		}
		rows = append(rows, "", "Use Up/Down to move, press Enter to confirm")
	}
	return rows
}

func (s *ModelMigrationScreen) accept() {
	s.finishWith(ModelMigrationAccepted)
}

func (s *ModelMigrationScreen) reject() {
	s.finishWith(ModelMigrationRejected)
}

func (s *ModelMigrationScreen) finishWith(outcome ModelMigrationOutcome) {
	s.outcome = outcome
	s.done = true
}

func (s *ModelMigrationScreen) handleMenuKey(key string) {
	switch key {
	case "up", "k":
		s.highlightedOption = MigrationMenuTryNewModel
	case "down", "j":
		s.highlightedOption = MigrationMenuUseExistingModel
	case "1":
		s.highlightedOption = MigrationMenuTryNewModel
		s.accept()
	case "2":
		s.highlightedOption = MigrationMenuUseExistingModel
		s.reject()
	case "enter", "esc", "escape":
		if s.highlightedOption == MigrationMenuUseExistingModel {
			s.reject()
		} else {
			s.accept()
		}
	}
}

func splitMigrationRows(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}
