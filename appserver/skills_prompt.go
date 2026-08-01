package appserver

import (
	"fmt"
	"os"
	"strings"

	"codex_go/config"
	contextfrag "codex_go/context"
	promptctx "codex_go/prompt"
	"codex_go/turn"
)

type HostSkillsPromptOptions struct {
	CodexHome     string
	CWD           string
	Config        *config.Config
	Prompt        string
	Inputs        []turn.TurnUserInput
	ContextWindow int64
}

type HostSkillsPromptContext struct {
	Instructions string
	InputItems   []any
	Warnings     []string
}

// BuildHostSkillsPromptContext builds the model-visible host skill catalog and
// explicit skill bodies used by non-app-server entry points.
func BuildHostSkillsPromptContext(options *HostSkillsPromptOptions) (*HostSkillsPromptContext, error) {
	if options == nil {
		options = &HostSkillsPromptOptions{}
	}
	codexHome := strings.TrimSpace(options.CodexHome)
	configService := config.NewConfigService(codexHome)
	bundledEnabled := bundledSkillsEnabledFromConfig(options.Config)
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		Config:               configService,
		CodexHome:            codexHome,
		IncludeDefaultRoots:  codexHome != "",
		BundledSkillsEnabled: &bundledEnabled,
	})
	listParams := &SkillsListParams{}
	if cwd := strings.TrimSpace(options.CWD); cwd != "" {
		listParams.CWDs = []string{cwd}
	}
	if options.Config != nil {
		listParams.Config = skillConfigEntriesFromValues(options.Config.Values)
	}
	response, err := service.List(listParams)
	if err != nil {
		return nil, err
	}
	result := &HostSkillsPromptContext{}
	for _, skillErr := range response.Errors {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Failed to load skill at %s: %s", skillErr.Path, skillErr.Message))
	}
	metadata := promptHostSkillMetadataFromEntries(response.Skills)
	params := &turn.TurnStartParams{Prompt: options.Prompt, Input: append([]turn.TurnUserInput(nil), options.Inputs...), CWD: options.CWD}
	metadata = selectSkillMetadata(options.Config, params, metadata)

	if options.Config == nil || options.Config.IncludeSkillInstructions() {
		available := promptctx.RenderAvailableSkillsWithOptions(metadata, promptctx.AvailableSkillsRenderOptions{
			Budget:                   promptctx.DefaultSkillMetadataBudget(options.ContextWindow),
			IncludeUsageInstructions: true,
		})
		if available != nil {
			result.Instructions = strings.TrimSpace(available.Body)
			if available.WarningMessage != nil && strings.TrimSpace(*available.WarningMessage) != "" {
				result.Warnings = append(result.Warnings, strings.TrimSpace(*available.WarningMessage))
			}
		}
	}

	selected := promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: skillMentionInputsFromTurn(params),
		Skills: metadata,
	})
	for _, skill := range selected {
		contents := skill.Contents
		if strings.TrimSpace(contents) == "" {
			data, readErr := os.ReadFile(skill.Path)
			if readErr != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Failed to load skill `%s`: %s", skill.Name, readErr))
				continue
			}
			contents = string(data)
		}
		name, renderPath, contents, truncated := promptctx.TruncateSkillInstructionFields(skill.Name, firstNonEmpty(skill.LocatorPath, skill.Path), contents)
		rendered := contextfrag.Render(contextfrag.NewSkillInstructions(name, renderPath, contents))
		if item := renderedFragmentInputItem(rendered); item != nil {
			result.InputItems = append(result.InputItems, item)
		}
		if truncated {
			result.Warnings = append(result.Warnings, promptctx.SkillMainPromptTruncatedWarning(skill.Name))
		}
	}
	return result, nil
}

func bundledSkillsEnabledFromConfig(cfg *config.Config) bool {
	if cfg == nil || cfg.Values == nil {
		return true
	}
	skills, ok := cfg.Values["skills"].(map[string]any)
	if !ok {
		return true
	}
	bundled, ok := skills["bundled"].(map[string]any)
	if !ok {
		return true
	}
	enabled, ok := bundled["enabled"].(bool)
	return !ok || enabled
}
