package app

import (
	"path/filepath"
	"strings"

	"codex_go/internal/appserver"
	"codex_go/internal/model"
)

// Rust parity subset: codex-rs/tui/src/app/startup_prompts.rs.

const ModelAvailabilityNUXMaxShowCount uint32 = 4

const (
	HideGPT51CodexMaxMigrationPromptConfig = "hide_gpt_5_1_codex_max_migration_prompt"
	HideGPT51MigrationPromptConfig         = "hide_gpt5_1_migration_prompt"
)

type StartupPrompt struct {
	Title string
	Body  string
}

type SkillLoadWarningKey struct {
	Path    string
	Message string
}

type SkillLoadWarningState struct {
	active map[SkillLoadWarningKey]bool
}

func NewSkillLoadWarningState() *SkillLoadWarningState {
	return &SkillLoadWarningState{active: map[SkillLoadWarningKey]bool{}}
}

func (s *SkillLoadWarningState) Clear() {
	if s == nil {
		return
	}
	s.active = map[SkillLoadWarningKey]bool{}
}

func (s *SkillLoadWarningState) NewlyActiveErrors(errors []appserver.SkillErrorInfo) []appserver.SkillErrorInfo {
	if s == nil {
		s = NewSkillLoadWarningState()
	}
	previous := s.active
	current := map[SkillLoadWarningKey]bool{}
	newlyActive := []appserver.SkillErrorInfo{}
	for _, skillError := range errors {
		key := SkillLoadWarningKey{
			Path:    skillError.Path,
			Message: skillError.Message,
		}
		if current[key] {
			continue
		}
		current[key] = true
		if !previous[key] {
			newlyActive = append(newlyActive, skillError)
		}
	}
	s.active = current
	return newlyActive
}

func SkillLoadWarningMessages(errors []appserver.SkillErrorInfo) []string {
	if len(errors) == 0 {
		return nil
	}
	messages := []string{
		"Skipped loading " + formatUintForStartupPrompts(uint64(len(errors))) + " skill(s) due to invalid SKILL.md files.",
	}
	for _, skillError := range errors {
		messages = append(messages, skillError.Path+": "+skillError.Message)
	}
	return messages
}

type StartupTooltipOverride struct {
	ModelSlug string
	Message   string
}

type StartupTooltipDecision struct {
	Override          StartupTooltipOverride
	UpdatedShownCount map[string]uint32
	Show              bool
}

type ModelMigrationAcceptedActions struct {
	FromModel              string
	TargetModel            string
	TargetReasoningEffort  string
	PersistAcknowledgement bool
	UpdateModel            bool
	UpdateReasoningEffort  bool
	PersistModelSelection  bool
}

type ProjectConfigDisabledFolder struct {
	Folder string
	Reason string
}

func SelectModelAvailabilityNUX(availableModels []model.ModelSummary, shownCount map[string]uint32) (StartupTooltipOverride, bool) {
	for _, preset := range availableModels {
		if preset.AvailabilityNux == nil {
			continue
		}
		modelSlug := preset.Model
		if modelSlug == "" {
			modelSlug = preset.ID
		}
		if shownCount[modelSlug] >= ModelAvailabilityNUXMaxShowCount {
			continue
		}
		return StartupTooltipOverride{
			ModelSlug: modelSlug,
			Message:   preset.AvailabilityNux.Message,
		}, true
	}
	return StartupTooltipOverride{}, false
}

func PrepareStartupTooltipOverrideDecision(isFirstRun bool, showTooltips bool, availableModels []model.ModelSummary, shownCount map[string]uint32) StartupTooltipDecision {
	if isFirstRun || !showTooltips {
		return StartupTooltipDecision{}
	}
	tooltipOverride, ok := SelectModelAvailabilityNUX(availableModels, shownCount)
	if !ok {
		return StartupTooltipDecision{}
	}
	updated := make(map[string]uint32, len(shownCount)+1)
	for key, value := range shownCount {
		updated[key] = value
	}
	updated[tooltipOverride.ModelSlug] = updated[tooltipOverride.ModelSlug] + 1
	return StartupTooltipDecision{
		Override:          tooltipOverride,
		UpdatedShownCount: updated,
		Show:              true,
	}
}

func ShouldShowModelMigrationPrompt(currentModel string, targetModel string, seenMigrations map[string]string, availableModels []model.ModelSummary) bool {
	currentModel = strings.TrimSpace(currentModel)
	targetModel = strings.TrimSpace(targetModel)
	if currentModel == "" || targetModel == "" || currentModel == targetModel {
		return false
	}
	if seenMigrations != nil && strings.TrimSpace(seenMigrations[currentModel]) == targetModel {
		return false
	}
	if _, ok := TargetPresetForUpgrade(availableModels, targetModel); !ok {
		return false
	}
	for _, preset := range availableModels {
		if strings.TrimSpace(preset.Model) == currentModel && preset.Upgrade != nil {
			return true
		}
		if preset.Upgrade != nil && strings.TrimSpace(*preset.Upgrade) == targetModel {
			return true
		}
	}
	return false
}

func MigrationPromptHidden(notices map[string]bool, migrationConfigKey string) bool {
	switch strings.TrimSpace(migrationConfigKey) {
	case HideGPT51CodexMaxMigrationPromptConfig, HideGPT51MigrationPromptConfig:
		return notices != nil && notices[migrationConfigKey]
	default:
		return false
	}
}

func TargetPresetForUpgrade(availableModels []model.ModelSummary, targetModel string) (model.ModelSummary, bool) {
	targetModel = strings.TrimSpace(targetModel)
	for _, preset := range availableModels {
		if strings.TrimSpace(preset.Model) == targetModel && !preset.Hidden {
			return preset, true
		}
	}
	return model.ModelSummary{}, false
}

func ApplyAcceptedModelMigrationActions(fromModel string, target model.ModelSummary) ModelMigrationAcceptedActions {
	targetModel := strings.TrimSpace(target.Model)
	if targetModel == "" {
		targetModel = strings.TrimSpace(target.ID)
	}
	return ModelMigrationAcceptedActions{
		FromModel:              strings.TrimSpace(fromModel),
		TargetModel:            targetModel,
		TargetReasoningEffort:  strings.TrimSpace(target.DefaultReasoningEffort),
		PersistAcknowledgement: true,
		UpdateModel:            targetModel != "",
		UpdateReasoningEffort:  strings.TrimSpace(target.DefaultReasoningEffort) != "",
		PersistModelSelection:  targetModel != "",
	}
}

func BuildProjectConfigWarningMessages(disabled []ProjectConfigDisabledFolder) []string {
	if len(disabled) == 0 {
		return nil
	}
	message := "Project-local config, hooks, and exec policies are disabled in the following folders until the project is trusted, but skills still load."
	for index, folder := range disabled {
		message += "\n    " + formatUintForStartupPrompts(uint64(index+1)) + ". " + strings.TrimSpace(folder.Folder)
		message += "\n       " + strings.TrimSpace(folder.Reason)
	}
	return []string{message}
}

func NormalizeAdditionalWritableRootsForCWD(roots []string, baseCWD string) []string {
	if len(roots) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if filepath.IsAbs(root) {
			normalized = append(normalized, filepath.Clean(root))
			continue
		}
		normalized = append(normalized, filepath.Clean(filepath.Join(baseCWD, root)))
	}
	return normalized
}

func formatUintForStartupPrompts(value uint64) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}
