package app

import (
	"path/filepath"
	"strings"

	"codex_go/model"
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

// ModelUpgrade mirrors Rust's `ModelUpgrade` (protocol/src/openai_models.rs):
// the migration metadata a saved model selection carries once its catalog entry
// is gone. The startup prompt reads the replacement id, the config key its
// acknowledgement is stored under, and the migration markdown.
type ModelUpgrade struct {
	ID                 string
	MigrationConfigKey string
	ModelLink          *string
	UpgradeCopy        *string
	MigrationMarkdown  *string
	RetirementAt       *int64
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

// ModelUpgradeForMigration mirrors Rust's `model_upgrade_for_migration`
// (#47932): a saved model selection can outlive its catalog entry, so the
// catalog preset's own upgrade wins when the model is still listed, and
// otherwise the fallback migration metadata is scoped to the provider that owns
// the model slug - other providers may still support the same slug.
func ModelUpgradeForMigration(modelProviderID string, modelSlug string, availableModels []model.ModelSummary) (ModelUpgrade, bool) {
	modelSlug = strings.TrimSpace(modelSlug)
	for _, preset := range availableModels {
		if strings.TrimSpace(preset.Model) != modelSlug {
			continue
		}
		// Rust returns the preset's own upgrade (which may be absent) without
		// consulting the fallback table, so a catalog entry that has no upgrade
		// suppresses the provider-scoped fallback.
		if preset.Upgrade == nil {
			return ModelUpgrade{}, false
		}
		upgrade := ModelUpgrade{
			ID:                 strings.TrimSpace(*preset.Upgrade),
			MigrationConfigKey: modelSlug,
		}
		if info := preset.UpgradeInfo; info != nil {
			upgrade.ModelLink = cloneStringPointer(info.ModelLink)
			upgrade.UpgradeCopy = cloneStringPointer(info.UpgradeCopy)
			upgrade.MigrationMarkdown = cloneStringPointer(info.MigrationMarkdown)
			if info.RetirementAt != nil {
				retirementAt := *info.RetirementAt
				upgrade.RetirementAt = &retirementAt
			}
		}
		return upgrade, true
	}

	var targetModel, currentName, targetName string
	switch {
	case modelProviderID == model.OpenAIProviderID && modelSlug == "gpt-5.4":
		targetModel, currentName, targetName = "gpt-6-sol", "GPT-5.4", "GPT-6 Sol"
	case modelProviderID == model.AmazonBedrockProviderID && modelSlug == "openai.gpt-5.4":
		targetModel, currentName, targetName = "openai.gpt-6-sol", "GPT-5.4 on Amazon Bedrock", "GPT-6 Sol on Amazon Bedrock"
	case modelProviderID == model.OpenAIProviderID && modelSlug == "gpt-5.4-mini":
		targetModel, currentName, targetName = "gpt-6-luna", "GPT-5.4 Mini", "GPT-6 Luna"
	default:
		return ModelUpgrade{}, false
	}
	availability := "no longer available"
	if modelSlug == "openai.gpt-5.4" {
		availability = "no longer offered in Codex"
	}
	markdown := currentName + " is " + availability + "\n\nCodex now uses " + targetName +
		" in place of " + currentName + ". Switch to " + targetName + " to continue.\n"
	return ModelUpgrade{
		ID:                 targetModel,
		MigrationConfigKey: modelSlug,
		MigrationMarkdown:  &markdown,
	}, true
}

func ShouldShowModelMigrationPrompt(modelProviderID string, currentModel string, targetModel string, seenMigrations map[string]string, availableModels []model.ModelSummary) bool {
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
	// The provider-scoped fallback migration links a saved selection that is no
	// longer in the catalog to its replacement.
	if upgrade, ok := ModelUpgradeForMigration(modelProviderID, currentModel, availableModels); ok && upgrade.ID == targetModel {
		return true
	}
	for _, preset := range availableModels {
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
