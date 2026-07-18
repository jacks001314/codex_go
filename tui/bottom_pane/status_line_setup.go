package bottompane

// Rust parity: codex-rs/tui/src/bottom_pane/status_line_setup.rs.

const StatusLineUseThemeColorsItemID = "status-line-use-theme-colors"

type StatusLineSetupState struct {
	Items          []string
	UseThemeColors bool
	PreviewText    string
}

type StatusLineSetupItem struct {
	Item        StatusLineItem
	ID          string
	Name        string
	Description string
	Preview     string
	Enabled     bool
}

func NewStatusLineSetupState(configured []string, useThemeColors bool, previewData StatusSurfacePreviewData) StatusLineSetupState {
	items, _ := ParseStatusLineItems(configured)
	lineItems := []StatusLineItem{}
	for _, item := range items {
		lineItems = append(lineItems, item.Item)
	}
	line, ok := previewData.StatusLineForItems(lineItems, useThemeColors)
	preview := ""
	if ok {
		preview = line.PlainText()
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return StatusLineSetupState{Items: ids, UseThemeColors: useThemeColors, PreviewText: preview}
}

func ParseStatusLineItems(ids []string) ([]StatusLineSetupItem, []string) {
	items := []StatusLineSetupItem{}
	invalid := []string{}
	for _, id := range ids {
		item, ok := ParseStatusLineItem(id)
		if !ok {
			invalid = append(invalid, id)
			continue
		}
		items = append(items, StatusLineSetupItem{Item: item, ID: StatusLineItemID(item), Name: StatusLineItemID(item), Enabled: true})
	}
	return items, invalid
}

func ParseStatusLineItem(id string) (StatusLineItem, bool) {
	switch id {
	case "model", "model-name":
		return StatusLineModelName, true
	case "model-with-reasoning":
		return StatusLineModelWithReasoning, true
	case "reasoning":
		return StatusLineReasoning, true
	case "current-dir":
		return StatusLineCurrentDir, true
	case "project-name", "project", "project-root":
		return StatusLineProjectRoot, true
	case "git-branch":
		return StatusLineGitBranch, true
	case "pull-request-number":
		return StatusLinePullRequestNumber, true
	case "branch-changes":
		return StatusLineBranchChanges, true
	case "run-state", "status":
		return StatusLineStatus, true
	case "permissions":
		return StatusLinePermissions, true
	case "approval-mode", "approval":
		return StatusLineApprovalMode, true
	case "context-remaining":
		return StatusLineContextRemaining, true
	case "context-used", "context-usage":
		return StatusLineContextUsed, true
	case "five-hour-limit":
		return StatusLineFiveHourLimit, true
	case "weekly-limit":
		return StatusLineWeeklyLimit, true
	case "codex-version":
		return StatusLineCodexVersion, true
	case "context-window-size":
		return StatusLineContextWindowSize, true
	case "used-tokens":
		return StatusLineUsedTokens, true
	case "total-input-tokens":
		return StatusLineTotalInputTokens, true
	case "total-output-tokens":
		return StatusLineTotalOutputTokens, true
	case "thread-id", "session-id":
		return StatusLineSessionID, true
	case "fast-mode":
		return StatusLineFastMode, true
	case "raw-output":
		return StatusLineRawOutput, true
	case "thread-title":
		return StatusLineThreadTitle, true
	case "workspace-headline":
		return StatusLineWorkspaceHeadline, true
	case "task-progress":
		return StatusLineTaskProgress, true
	default:
		return 0, false
	}
}

func StatusLineItemID(item StatusLineItem) string {
	switch item {
	case StatusLineModelName:
		return "model"
	case StatusLineModelWithReasoning:
		return "model-with-reasoning"
	case StatusLineReasoning:
		return "reasoning"
	case StatusLineCurrentDir:
		return "current-dir"
	case StatusLineProjectRoot:
		return "project-name"
	case StatusLineGitBranch:
		return "git-branch"
	case StatusLinePullRequestNumber:
		return "pull-request-number"
	case StatusLineBranchChanges:
		return "branch-changes"
	case StatusLineStatus:
		return "run-state"
	case StatusLinePermissions:
		return "permissions"
	case StatusLineApprovalMode:
		return "approval-mode"
	case StatusLineContextRemaining:
		return "context-remaining"
	case StatusLineContextUsed:
		return "context-used"
	case StatusLineFiveHourLimit:
		return "five-hour-limit"
	case StatusLineWeeklyLimit:
		return "weekly-limit"
	case StatusLineCodexVersion:
		return "codex-version"
	case StatusLineContextWindowSize:
		return "context-window-size"
	case StatusLineUsedTokens:
		return "used-tokens"
	case StatusLineTotalInputTokens:
		return "total-input-tokens"
	case StatusLineTotalOutputTokens:
		return "total-output-tokens"
	case StatusLineSessionID:
		return "thread-id"
	case StatusLineFastMode:
		return "fast-mode"
	case StatusLineRawOutput:
		return "raw-output"
	case StatusLineThreadTitle:
		return "thread-title"
	case StatusLineWorkspaceHeadline:
		return "workspace-headline"
	case StatusLineTaskProgress:
		return "task-progress"
	default:
		return ""
	}
}

func StatusLineItemPreviewItem(item StatusLineItem) StatusSurfacePreviewItem {
	switch item {
	case StatusLineModelName:
		return StatusPreviewModel
	case StatusLineModelWithReasoning:
		return StatusPreviewModelWithReasoning
	case StatusLineReasoning:
		return StatusPreviewReasoning
	case StatusLineCurrentDir:
		return StatusPreviewCurrentDir
	case StatusLineProjectRoot:
		return StatusPreviewProjectRoot
	case StatusLineGitBranch:
		return StatusPreviewGitBranch
	case StatusLinePullRequestNumber:
		return StatusPreviewPullRequestNumber
	case StatusLineBranchChanges:
		return StatusPreviewBranchChanges
	case StatusLineStatus:
		return StatusPreviewStatus
	case StatusLinePermissions:
		return StatusPreviewPermissions
	case StatusLineApprovalMode:
		return StatusPreviewApprovalMode
	case StatusLineContextRemaining:
		return StatusPreviewContextRemaining
	case StatusLineContextUsed:
		return StatusPreviewContextUsed
	case StatusLineFiveHourLimit:
		return StatusPreviewFiveHourLimit
	case StatusLineWeeklyLimit:
		return StatusPreviewWeeklyLimit
	case StatusLineCodexVersion:
		return StatusPreviewCodexVersion
	case StatusLineContextWindowSize:
		return StatusPreviewContextWindowSize
	case StatusLineUsedTokens:
		return StatusPreviewUsedTokens
	case StatusLineTotalInputTokens:
		return StatusPreviewTotalInputTokens
	case StatusLineTotalOutputTokens:
		return StatusPreviewTotalOutputTokens
	case StatusLineSessionID:
		return StatusPreviewSessionID
	case StatusLineFastMode:
		return StatusPreviewFastMode
	case StatusLineRawOutput:
		return StatusPreviewRawOutput
	case StatusLineThreadTitle:
		return StatusPreviewThreadTitle
	case StatusLineWorkspaceHeadline:
		return StatusPreviewWorkspaceHeadline
	case StatusLineTaskProgress:
		return StatusPreviewTaskProgress
	default:
		return ""
	}
}

func StatusLineItemDescription(item StatusLineItem, previewData StatusSurfacePreviewData) string {
	switch item {
	case StatusLineFiveHourLimit:
		return previewData.RateLimitItemDescription(StatusPreviewFiveHourLimit, "Remaining usage on the primary usage limit (omitted when unavailable)")
	case StatusLineWeeklyLimit:
		return previewData.RateLimitItemDescription(StatusPreviewWeeklyLimit, "Remaining usage on the secondary usage limit (omitted when unavailable)")
	}
	descriptions := map[StatusLineItem]string{
		StatusLineModelName:          "Current model name",
		StatusLineModelWithReasoning: "Current model name with reasoning level",
		StatusLineReasoning:          "Current reasoning level",
		StatusLineCurrentDir:         "Current working directory",
		StatusLineProjectRoot:        "Project name (omitted when unavailable)",
		StatusLineGitBranch:          "Current Git branch (omitted when unavailable)",
		StatusLinePullRequestNumber:  "Open pull request number for the current branch (omitted when unavailable)",
		StatusLineBranchChanges:      "Committed branch changes against the default branch (omitted when unavailable)",
		StatusLineStatus:             "Compact session run-state text (Ready, Working, Thinking)",
		StatusLinePermissions:        "Active permission profile or sandbox mode",
		StatusLineApprovalMode:       "Active command approval mode",
		StatusLineContextRemaining:   "Percentage of context window remaining (omitted when unknown)",
		StatusLineContextUsed:        "Percentage of context window used (omitted when unknown)",
		StatusLineCodexVersion:       "Codex application version",
		StatusLineContextWindowSize:  "Total context window size in tokens (omitted when unknown)",
		StatusLineUsedTokens:         "Total tokens used in session (omitted when zero)",
		StatusLineTotalInputTokens:   "Total input tokens used in session",
		StatusLineTotalOutputTokens:  "Total output tokens used in session",
		StatusLineSessionID:          "Current thread identifier (omitted until thread starts)",
		StatusLineFastMode:           "Whether Fast mode is currently active",
		StatusLineRawOutput:          "Whether raw scrollback mode is active",
		StatusLineThreadTitle:        "Current thread title, or thread identifier when unnamed",
		StatusLineWorkspaceHeadline:  "Workspace notification headline (Enterprise workspaces only; omitted when unavailable)",
		StatusLineTaskProgress:       "Latest task progress from update_plan (omitted until available)",
	}
	return descriptions[item]
}
