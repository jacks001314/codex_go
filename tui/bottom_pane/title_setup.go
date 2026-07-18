package bottompane

import "strings"

// Rust parity: codex-rs/tui/src/bottom_pane/title_setup.rs.

type TitleSetupState struct {
	Template string
	Preview  string
}

type TerminalTitleItem string

const (
	TerminalTitleAppName            TerminalTitleItem = "app-name"
	TerminalTitleProject            TerminalTitleItem = "project-name"
	TerminalTitleCurrentDir         TerminalTitleItem = "current-dir"
	TerminalTitleSpinner            TerminalTitleItem = "activity"
	TerminalTitleStatus             TerminalTitleItem = "run-state"
	TerminalTitleThread             TerminalTitleItem = "thread-title"
	TerminalTitleGitBranch          TerminalTitleItem = "git-branch"
	TerminalTitleContextRemaining   TerminalTitleItem = "context-remaining"
	TerminalTitleContextUsed        TerminalTitleItem = "context-used"
	TerminalTitleFiveHourLimit      TerminalTitleItem = "five-hour-limit"
	TerminalTitleWeeklyLimit        TerminalTitleItem = "weekly-limit"
	TerminalTitleCodexVersion       TerminalTitleItem = "codex-version"
	TerminalTitleUsedTokens         TerminalTitleItem = "used-tokens"
	TerminalTitleTotalInputTokens   TerminalTitleItem = "total-input-tokens"
	TerminalTitleTotalOutputTokens  TerminalTitleItem = "total-output-tokens"
	TerminalTitleSessionID          TerminalTitleItem = "thread-id"
	TerminalTitleFastMode           TerminalTitleItem = "fast-mode"
	TerminalTitleModel              TerminalTitleItem = "model"
	TerminalTitleModelWithReasoning TerminalTitleItem = "model-with-reasoning"
	TerminalTitleReasoning          TerminalTitleItem = "reasoning"
	TerminalTitleTaskProgress       TerminalTitleItem = "task-progress"
)

func ParseTerminalTitleItem(id string) (TerminalTitleItem, bool) {
	switch id {
	case "app-name":
		return TerminalTitleAppName, true
	case "project-name", "project":
		return TerminalTitleProject, true
	case "current-dir":
		return TerminalTitleCurrentDir, true
	case "activity", "spinner":
		return TerminalTitleSpinner, true
	case "run-state", "status":
		return TerminalTitleStatus, true
	case "thread-title", "thread":
		return TerminalTitleThread, true
	case "git-branch":
		return TerminalTitleGitBranch, true
	case "context-remaining":
		return TerminalTitleContextRemaining, true
	case "context-used", "context-usage":
		return TerminalTitleContextUsed, true
	case "five-hour-limit":
		return TerminalTitleFiveHourLimit, true
	case "weekly-limit":
		return TerminalTitleWeeklyLimit, true
	case "codex-version":
		return TerminalTitleCodexVersion, true
	case "used-tokens":
		return TerminalTitleUsedTokens, true
	case "total-input-tokens":
		return TerminalTitleTotalInputTokens, true
	case "total-output-tokens":
		return TerminalTitleTotalOutputTokens, true
	case "thread-id", "session-id":
		return TerminalTitleSessionID, true
	case "fast-mode":
		return TerminalTitleFastMode, true
	case "model", "model-name":
		return TerminalTitleModel, true
	case "model-with-reasoning":
		return TerminalTitleModelWithReasoning, true
	case "reasoning":
		return TerminalTitleReasoning, true
	case "task-progress":
		return TerminalTitleTaskProgress, true
	default:
		return "", false
	}
}

func ParseTerminalTitleItems(ids []string) ([]TerminalTitleItem, bool) {
	items := make([]TerminalTitleItem, 0, len(ids))
	for _, id := range ids {
		item, ok := ParseTerminalTitleItem(id)
		if !ok {
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

func (i TerminalTitleItem) PreviewItem() (StatusSurfacePreviewItem, bool) {
	switch i {
	case TerminalTitleAppName:
		return StatusPreviewAppName, true
	case TerminalTitleProject:
		return StatusPreviewProjectName, true
	case TerminalTitleCurrentDir:
		return StatusPreviewCurrentDir, true
	case TerminalTitleSpinner:
		return "", false
	case TerminalTitleStatus:
		return StatusPreviewStatus, true
	case TerminalTitleThread:
		return StatusPreviewThreadTitle, true
	case TerminalTitleGitBranch:
		return StatusPreviewGitBranch, true
	case TerminalTitleContextRemaining:
		return StatusPreviewContextRemaining, true
	case TerminalTitleContextUsed:
		return StatusPreviewContextUsed, true
	case TerminalTitleFiveHourLimit:
		return StatusPreviewFiveHourLimit, true
	case TerminalTitleWeeklyLimit:
		return StatusPreviewWeeklyLimit, true
	case TerminalTitleCodexVersion:
		return StatusPreviewCodexVersion, true
	case TerminalTitleUsedTokens:
		return StatusPreviewUsedTokens, true
	case TerminalTitleTotalInputTokens:
		return StatusPreviewTotalInputTokens, true
	case TerminalTitleTotalOutputTokens:
		return StatusPreviewTotalOutputTokens, true
	case TerminalTitleSessionID:
		return StatusPreviewSessionID, true
	case TerminalTitleFastMode:
		return StatusPreviewFastMode, true
	case TerminalTitleModel:
		return StatusPreviewModel, true
	case TerminalTitleModelWithReasoning:
		return StatusPreviewModelWithReasoning, true
	case TerminalTitleReasoning:
		return StatusPreviewReasoning, true
	case TerminalTitleTaskProgress:
		return StatusPreviewTaskProgress, true
	default:
		return "", false
	}
}

func (i TerminalTitleItem) SeparatorFromPrevious(previous *TerminalTitleItem) string {
	if previous == nil {
		return ""
	}
	if *previous == TerminalTitleSpinner || i == TerminalTitleSpinner {
		return " "
	}
	return " | "
}

func PreviewLineForTitleItems(items []TerminalTitleItem, previewData StatusSurfacePreviewData) (string, bool) {
	if containsTerminalTitleItem(items, TerminalTitleSpinner) {
		stringItems := make([]string, 0, len(items))
		for _, item := range items {
			stringItems = append(stringItems, string(item))
		}
		return BuildActionRequiredTitleText(ActionRequiredPreviewPrefix, stringItems, nil, func(item string) (string, bool) {
			titleItem, ok := ParseTerminalTitleItem(item)
			if !ok {
				return "", false
			}
			previewItem, ok := titleItem.PreviewItem()
			if !ok {
				return "", false
			}
			return previewData.ValueFor(previewItem)
		}), true
	}
	var previous *TerminalTitleItem
	var builder strings.Builder
	for _, item := range items {
		previewItem, ok := item.PreviewItem()
		if !ok {
			continue
		}
		value, ok := previewData.ValueFor(previewItem)
		if !ok || value == "" {
			continue
		}
		builder.WriteString(item.SeparatorFromPrevious(previous))
		builder.WriteString(value)
		current := item
		previous = &current
	}
	value := builder.String()
	return value, value != ""
}

func NewTitleSetupState(template string, ids []string, previewData StatusSurfacePreviewData) TitleSetupState {
	items, ok := ParseTerminalTitleItems(ids)
	preview := ""
	if ok {
		preview, _ = PreviewLineForTitleItems(items, previewData)
	}
	return TitleSetupState{Template: template, Preview: preview}
}

func containsTerminalTitleItem(items []TerminalTitleItem, target TerminalTitleItem) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
