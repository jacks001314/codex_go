package chatwidget

import (
	"strings"
	"unicode"

	bottompane "codex_go/internal/tui/bottom_pane"

	"github.com/rivo/uniseg"
)

var DefaultStatusLineItems = []string{"model-with-reasoning", "current-dir"}
var DefaultTerminalTitleItems = []string{"activity", "project-name"}

var TerminalTitleSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	TerminalTitleActionRequiredPrefix       = "[ ! ] Action Required"
	TerminalTitleActionRequiredHiddenPrefix = "[ . ] Action Required"
)

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

type StatusSurfacePreviewItem string

const (
	StatusPreviewAppName            StatusSurfacePreviewItem = "app-name"
	StatusPreviewProjectName        StatusSurfacePreviewItem = "project-name"
	StatusPreviewProjectRoot        StatusSurfacePreviewItem = "project-root"
	StatusPreviewCurrentDir         StatusSurfacePreviewItem = "current-dir"
	StatusPreviewStatus             StatusSurfacePreviewItem = "status"
	StatusPreviewThreadTitle        StatusSurfacePreviewItem = "thread-title"
	StatusPreviewGitBranch          StatusSurfacePreviewItem = "git-branch"
	StatusPreviewPullRequestNumber  StatusSurfacePreviewItem = "pull-request-number"
	StatusPreviewBranchChanges      StatusSurfacePreviewItem = "branch-changes"
	StatusPreviewPermissions        StatusSurfacePreviewItem = "permissions"
	StatusPreviewApprovalMode       StatusSurfacePreviewItem = "approval-mode"
	StatusPreviewContextRemaining   StatusSurfacePreviewItem = "context-remaining"
	StatusPreviewContextUsed        StatusSurfacePreviewItem = "context-used"
	StatusPreviewFiveHourLimit      StatusSurfacePreviewItem = "five-hour-limit"
	StatusPreviewWeeklyLimit        StatusSurfacePreviewItem = "weekly-limit"
	StatusPreviewCodexVersion       StatusSurfacePreviewItem = "codex-version"
	StatusPreviewContextWindowSize  StatusSurfacePreviewItem = "context-window-size"
	StatusPreviewUsedTokens         StatusSurfacePreviewItem = "used-tokens"
	StatusPreviewTotalInputTokens   StatusSurfacePreviewItem = "total-input-tokens"
	StatusPreviewTotalOutputTokens  StatusSurfacePreviewItem = "total-output-tokens"
	StatusPreviewSessionID          StatusSurfacePreviewItem = "session-id"
	StatusPreviewFastMode           StatusSurfacePreviewItem = "fast-mode"
	StatusPreviewRawOutput          StatusSurfacePreviewItem = "raw-output"
	StatusPreviewWorkspaceHeadline  StatusSurfacePreviewItem = "workspace-headline"
	StatusPreviewModel              StatusSurfacePreviewItem = "model"
	StatusPreviewModelWithReasoning StatusSurfacePreviewItem = "model-with-reasoning"
	StatusPreviewReasoning          StatusSurfacePreviewItem = "reasoning"
	StatusPreviewTaskProgress       StatusSurfacePreviewItem = "task-progress"
)

type PreviewValue struct {
	Text          string
	IsPlaceholder bool
}

type StatusSurfacePreviewData struct {
	values map[StatusSurfacePreviewItem]PreviewValue
}

type StatusSurfaceSelections struct {
	StatusLineItems           []bottompane.StatusLineItem
	InvalidStatusLineItems    []string
	TerminalTitleItems        []TerminalTitleItem
	InvalidTerminalTitleItems []string
}

type TerminalTitleRenderOptions struct {
	SpinnerText    string
	ActionRequired bool
	ActionHidden   bool
}

func NewStatusSurfaceSelections(statusLineIDs, terminalTitleIDs []string) StatusSurfaceSelections {
	if statusLineIDs == nil {
		statusLineIDs = append([]string(nil), DefaultStatusLineItems...)
	}
	if terminalTitleIDs == nil {
		terminalTitleIDs = append([]string(nil), DefaultTerminalTitleItems...)
	}
	statusItems, invalidStatus := ParseStatusLineItemsWithInvalids(statusLineIDs)
	titleItems, invalidTitle := ParseTerminalTitleItemsWithInvalids(terminalTitleIDs)
	return StatusSurfaceSelections{
		StatusLineItems:           statusItems,
		InvalidStatusLineItems:    invalidStatus,
		TerminalTitleItems:        titleItems,
		InvalidTerminalTitleItems: invalidTitle,
	}
}

func (s StatusSurfaceSelections) UsesGitBranch() bool {
	return containsStatusLineItem(s.StatusLineItems, bottompane.StatusLineGitBranch) ||
		containsTerminalTitleItem(s.TerminalTitleItems, TerminalTitleGitBranch)
}

func (s StatusSurfaceSelections) UsesGitSummary() bool {
	return containsStatusLineItem(s.StatusLineItems, bottompane.StatusLinePullRequestNumber) ||
		containsStatusLineItem(s.StatusLineItems, bottompane.StatusLineBranchChanges)
}

func (s StatusSurfaceSelections) UsesWorkspaceHeadline() bool {
	return containsStatusLineItem(s.StatusLineItems, bottompane.StatusLineWorkspaceHeadline)
}

func ParseStatusLineItemsWithInvalids(ids []string) ([]bottompane.StatusLineItem, []string) {
	items := []bottompane.StatusLineItem{}
	invalid := []string{}
	seenInvalid := map[string]bool{}
	for _, id := range ids {
		item, ok := ParseStatusLineItem(id)
		if ok {
			items = append(items, item)
			continue
		}
		if !seenInvalid[id] {
			seenInvalid[id] = true
			invalid = append(invalid, `"`+id+`"`)
		}
	}
	return items, invalid
}

func ParseTerminalTitleItemsWithInvalids(ids []string) ([]TerminalTitleItem, []string) {
	items := []TerminalTitleItem{}
	invalid := []string{}
	seenInvalid := map[string]bool{}
	for _, id := range ids {
		item, ok := ParseTerminalTitleItem(id)
		if ok {
			items = append(items, item)
			continue
		}
		if !seenInvalid[id] {
			seenInvalid[id] = true
			invalid = append(invalid, `"`+id+`"`)
		}
	}
	return items, invalid
}

func ParseStatusLineItem(id string) (bottompane.StatusLineItem, bool) {
	switch id {
	case "model", "model-name":
		return bottompane.StatusLineModelName, true
	case "model-with-reasoning":
		return bottompane.StatusLineModelWithReasoning, true
	case "reasoning":
		return bottompane.StatusLineReasoning, true
	case "current-dir":
		return bottompane.StatusLineCurrentDir, true
	case "project-name", "project", "project-root":
		return bottompane.StatusLineProjectRoot, true
	case "git-branch":
		return bottompane.StatusLineGitBranch, true
	case "pull-request-number":
		return bottompane.StatusLinePullRequestNumber, true
	case "branch-changes":
		return bottompane.StatusLineBranchChanges, true
	case "run-state", "status":
		return bottompane.StatusLineStatus, true
	case "permissions":
		return bottompane.StatusLinePermissions, true
	case "approval-mode", "approval":
		return bottompane.StatusLineApprovalMode, true
	case "context-remaining":
		return bottompane.StatusLineContextRemaining, true
	case "context-used", "context-usage":
		return bottompane.StatusLineContextUsed, true
	case "five-hour-limit":
		return bottompane.StatusLineFiveHourLimit, true
	case "weekly-limit":
		return bottompane.StatusLineWeeklyLimit, true
	case "codex-version":
		return bottompane.StatusLineCodexVersion, true
	case "context-window-size":
		return bottompane.StatusLineContextWindowSize, true
	case "used-tokens":
		return bottompane.StatusLineUsedTokens, true
	case "total-input-tokens":
		return bottompane.StatusLineTotalInputTokens, true
	case "total-output-tokens":
		return bottompane.StatusLineTotalOutputTokens, true
	case "thread-id", "session-id":
		return bottompane.StatusLineSessionID, true
	case "fast-mode":
		return bottompane.StatusLineFastMode, true
	case "raw-output":
		return bottompane.StatusLineRawOutput, true
	case "thread-title":
		return bottompane.StatusLineThreadTitle, true
	case "workspace-headline":
		return bottompane.StatusLineWorkspaceHeadline, true
	case "task-progress":
		return bottompane.StatusLineTaskProgress, true
	default:
		return 0, false
	}
}

func StatusLineItemID(item bottompane.StatusLineItem) string {
	switch item {
	case bottompane.StatusLineModelName:
		return "model"
	case bottompane.StatusLineModelWithReasoning:
		return "model-with-reasoning"
	case bottompane.StatusLineReasoning:
		return "reasoning"
	case bottompane.StatusLineCurrentDir:
		return "current-dir"
	case bottompane.StatusLineProjectRoot:
		return "project-name"
	case bottompane.StatusLineGitBranch:
		return "git-branch"
	case bottompane.StatusLinePullRequestNumber:
		return "pull-request-number"
	case bottompane.StatusLineBranchChanges:
		return "branch-changes"
	case bottompane.StatusLineStatus:
		return "run-state"
	case bottompane.StatusLinePermissions:
		return "permissions"
	case bottompane.StatusLineApprovalMode:
		return "approval-mode"
	case bottompane.StatusLineContextRemaining:
		return "context-remaining"
	case bottompane.StatusLineContextUsed:
		return "context-used"
	case bottompane.StatusLineFiveHourLimit:
		return "five-hour-limit"
	case bottompane.StatusLineWeeklyLimit:
		return "weekly-limit"
	case bottompane.StatusLineCodexVersion:
		return "codex-version"
	case bottompane.StatusLineContextWindowSize:
		return "context-window-size"
	case bottompane.StatusLineUsedTokens:
		return "used-tokens"
	case bottompane.StatusLineTotalInputTokens:
		return "total-input-tokens"
	case bottompane.StatusLineTotalOutputTokens:
		return "total-output-tokens"
	case bottompane.StatusLineSessionID:
		return "thread-id"
	case bottompane.StatusLineFastMode:
		return "fast-mode"
	case bottompane.StatusLineRawOutput:
		return "raw-output"
	case bottompane.StatusLineThreadTitle:
		return "thread-title"
	case bottompane.StatusLineWorkspaceHeadline:
		return "workspace-headline"
	case bottompane.StatusLineTaskProgress:
		return "task-progress"
	default:
		return ""
	}
}

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

func (item TerminalTitleItem) ID() string {
	return string(item)
}

func (item TerminalTitleItem) SeparatorFromPrevious(previous *TerminalTitleItem) string {
	if previous == nil {
		return ""
	}
	if *previous == TerminalTitleSpinner || item == TerminalTitleSpinner {
		return " "
	}
	return " | "
}

func (item TerminalTitleItem) PreviewItem() (StatusSurfacePreviewItem, bool) {
	switch item {
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

func StatusLineItemPreviewItem(item bottompane.StatusLineItem) StatusSurfacePreviewItem {
	switch item {
	case bottompane.StatusLineModelName:
		return StatusPreviewModel
	case bottompane.StatusLineModelWithReasoning:
		return StatusPreviewModelWithReasoning
	case bottompane.StatusLineReasoning:
		return StatusPreviewReasoning
	case bottompane.StatusLineCurrentDir:
		return StatusPreviewCurrentDir
	case bottompane.StatusLineProjectRoot:
		return StatusPreviewProjectRoot
	case bottompane.StatusLineGitBranch:
		return StatusPreviewGitBranch
	case bottompane.StatusLinePullRequestNumber:
		return StatusPreviewPullRequestNumber
	case bottompane.StatusLineBranchChanges:
		return StatusPreviewBranchChanges
	case bottompane.StatusLineStatus:
		return StatusPreviewStatus
	case bottompane.StatusLinePermissions:
		return StatusPreviewPermissions
	case bottompane.StatusLineApprovalMode:
		return StatusPreviewApprovalMode
	case bottompane.StatusLineContextRemaining:
		return StatusPreviewContextRemaining
	case bottompane.StatusLineContextUsed:
		return StatusPreviewContextUsed
	case bottompane.StatusLineFiveHourLimit:
		return StatusPreviewFiveHourLimit
	case bottompane.StatusLineWeeklyLimit:
		return StatusPreviewWeeklyLimit
	case bottompane.StatusLineCodexVersion:
		return StatusPreviewCodexVersion
	case bottompane.StatusLineContextWindowSize:
		return StatusPreviewContextWindowSize
	case bottompane.StatusLineUsedTokens:
		return StatusPreviewUsedTokens
	case bottompane.StatusLineTotalInputTokens:
		return StatusPreviewTotalInputTokens
	case bottompane.StatusLineTotalOutputTokens:
		return StatusPreviewTotalOutputTokens
	case bottompane.StatusLineSessionID:
		return StatusPreviewSessionID
	case bottompane.StatusLineFastMode:
		return StatusPreviewFastMode
	case bottompane.StatusLineRawOutput:
		return StatusPreviewRawOutput
	case bottompane.StatusLineThreadTitle:
		return StatusPreviewThreadTitle
	case bottompane.StatusLineWorkspaceHeadline:
		return StatusPreviewWorkspaceHeadline
	case bottompane.StatusLineTaskProgress:
		return StatusPreviewTaskProgress
	default:
		return ""
	}
}

func NewStatusSurfacePreviewData(values map[StatusSurfacePreviewItem]string) StatusSurfacePreviewData {
	data := DefaultStatusSurfacePreviewData()
	for item, value := range values {
		data.SetLive(item, value)
	}
	return data
}

func DefaultStatusSurfacePreviewData() StatusSurfacePreviewData {
	data := StatusSurfacePreviewData{values: map[StatusSurfacePreviewItem]PreviewValue{}}
	for _, item := range StatusSurfacePreviewItems() {
		data.SetPlaceholder(item, statusSurfacePlaceholder(item))
	}
	return data
}

func StatusSurfacePreviewItems() []StatusSurfacePreviewItem {
	return []StatusSurfacePreviewItem{
		StatusPreviewAppName,
		StatusPreviewProjectName,
		StatusPreviewProjectRoot,
		StatusPreviewCurrentDir,
		StatusPreviewStatus,
		StatusPreviewThreadTitle,
		StatusPreviewGitBranch,
		StatusPreviewPullRequestNumber,
		StatusPreviewBranchChanges,
		StatusPreviewPermissions,
		StatusPreviewApprovalMode,
		StatusPreviewContextRemaining,
		StatusPreviewContextUsed,
		StatusPreviewFiveHourLimit,
		StatusPreviewWeeklyLimit,
		StatusPreviewCodexVersion,
		StatusPreviewContextWindowSize,
		StatusPreviewUsedTokens,
		StatusPreviewTotalInputTokens,
		StatusPreviewTotalOutputTokens,
		StatusPreviewSessionID,
		StatusPreviewFastMode,
		StatusPreviewRawOutput,
		StatusPreviewWorkspaceHeadline,
		StatusPreviewModel,
		StatusPreviewModelWithReasoning,
		StatusPreviewReasoning,
		StatusPreviewTaskProgress,
	}
}

func (d StatusSurfacePreviewData) ValueFor(item StatusSurfacePreviewItem) (string, bool) {
	value, ok := d.values[item]
	return value.Text, ok
}

func (d StatusSurfacePreviewData) LiveValueFor(item StatusSurfacePreviewItem) (string, bool) {
	value, ok := d.values[item]
	if !ok || value.IsPlaceholder {
		return "", false
	}
	return value.Text, true
}

func (d *StatusSurfacePreviewData) SetLive(item StatusSurfacePreviewItem, value string) {
	d.ensure()
	d.values[item] = PreviewValue{Text: value}
}

func (d *StatusSurfacePreviewData) SetPlaceholder(item StatusSurfacePreviewItem, value string) {
	d.ensure()
	if existing, ok := d.values[item]; ok && !existing.IsPlaceholder {
		return
	}
	d.values[item] = PreviewValue{Text: value, IsPlaceholder: true}
}

func (d *StatusSurfacePreviewData) SuppressPlaceholder(item StatusSurfacePreviewItem) {
	if d == nil || d.values == nil {
		return
	}
	if existing, ok := d.values[item]; ok && existing.IsPlaceholder {
		delete(d.values, item)
	}
}

func (d StatusSurfacePreviewData) StatusLineForItems(items []bottompane.StatusLineItem, useThemeColors bool) (bottompane.StatusLine, bool) {
	segments := []bottompane.StatusLineSegment{}
	for _, item := range items {
		previewItem := StatusLineItemPreviewItem(item)
		if previewItem == "" {
			continue
		}
		if value, ok := d.ValueFor(previewItem); ok {
			segments = append(segments, bottompane.StatusLineSegment{Item: item, Text: value})
		}
	}
	return bottompane.StatusLineFromSegments(segments, useThemeColors)
}

func (d StatusSurfacePreviewData) RateLimitItemName(item StatusSurfacePreviewItem, fallback string) string {
	if value, ok := d.LiveValueFor(item); ok {
		if copy, ok := rateLimitPreviewCopy(value); ok {
			return copy.Name
		}
	}
	return fallback
}

func (d StatusSurfacePreviewData) RateLimitItemDescription(item StatusSurfacePreviewItem, fallback string) string {
	if value, ok := d.LiveValueFor(item); ok {
		if copy, ok := rateLimitPreviewCopy(value); ok {
			return copy.Description
		}
	}
	return fallback
}

func (d *StatusSurfacePreviewData) ensure() {
	if d.values == nil {
		d.values = map[StatusSurfacePreviewItem]PreviewValue{}
	}
}

func TerminalTitleText(items []TerminalTitleItem, data StatusSurfacePreviewData, opts TerminalTitleRenderOptions) (string, bool) {
	if opts.ActionRequired && containsTerminalTitleItem(items, TerminalTitleSpinner) {
		prefix := TerminalTitleActionRequiredPrefix
		if opts.ActionHidden {
			prefix = TerminalTitleActionRequiredHiddenPrefix
		}
		return BuildActionRequiredTitleText(prefix, items, []TerminalTitleItem{TerminalTitleStatus}, data), true
	}

	var previous *TerminalTitleItem
	var builder strings.Builder
	for _, item := range items {
		value, ok := terminalTitleValue(item, data, opts)
		if !ok || value == "" {
			continue
		}
		builder.WriteString(item.SeparatorFromPrevious(previous))
		builder.WriteString(value)
		current := item
		previous = &current
	}
	title := builder.String()
	return title, title != ""
}

func PreviewLineForTitleItems(items []TerminalTitleItem, data StatusSurfacePreviewData) (string, bool) {
	if containsTerminalTitleItem(items, TerminalTitleSpinner) {
		return BuildActionRequiredTitleText(TerminalTitleActionRequiredPrefix, items, nil, data), true
	}
	return TerminalTitleText(items, data, TerminalTitleRenderOptions{})
}

func BuildActionRequiredTitleText(prefix string, items []TerminalTitleItem, excluded []TerminalTitleItem, data StatusSurfacePreviewData) string {
	parts := []string{prefix}
	for _, item := range items {
		if item == TerminalTitleSpinner || containsTerminalTitleItem(excluded, item) {
			continue
		}
		value, ok := terminalTitleValue(item, data, TerminalTitleRenderOptions{})
		if ok && value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " | ")
}

func TerminalTitleSpinnerFrame(frameIndex int) string {
	if len(TerminalTitleSpinnerFrames) == 0 {
		return ""
	}
	if frameIndex < 0 {
		frameIndex = -frameIndex
	}
	return TerminalTitleSpinnerFrames[frameIndex%len(TerminalTitleSpinnerFrames)]
}

func TruncateTerminalTitlePart(value string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	graphemes := []string{}
	iter := uniseg.NewGraphemes(value)
	for iter.Next() {
		graphemes = append(graphemes, iter.Str())
	}
	if len(graphemes) <= maxChars || maxChars <= 3 {
		if len(graphemes) > maxChars {
			graphemes = graphemes[:maxChars]
		}
		return strings.Join(graphemes, "")
	}
	return strings.Join(graphemes[:maxChars-3], "") + "..."
}

func terminalTitleValue(item TerminalTitleItem, data StatusSurfacePreviewData, opts TerminalTitleRenderOptions) (string, bool) {
	if item == TerminalTitleSpinner {
		if opts.SpinnerText == "" {
			return "", false
		}
		return opts.SpinnerText, true
	}
	previewItem, ok := item.PreviewItem()
	if !ok {
		return "", false
	}
	value, ok := data.ValueFor(previewItem)
	return value, ok
}

type rateLimitPreviewCopyData struct {
	Name        string
	Description string
}

func rateLimitPreviewCopy(value string) (rateLimitPreviewCopyData, bool) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	switch {
	case strings.HasPrefix(value, "secondary usage "):
		return rateLimitPreviewCopyData{
			Name:        "secondary-usage-limit",
			Description: "Remaining usage on the secondary usage limit (omitted when unavailable)",
		}, true
	case strings.HasPrefix(value, "usage "):
		return rateLimitPreviewCopyData{
			Name:        "usage-limit",
			Description: "Remaining usage on the primary usage limit (omitted when unavailable)",
		}, true
	case strings.HasPrefix(value, "5h "):
		return rateLimitPreviewCopyData{
			Name:        "five-hour-limit",
			Description: "Remaining usage on the 5-hour usage limit (omitted when unavailable)",
		}, true
	case strings.HasPrefix(value, "daily "):
		return rateLimitPreviewCopyData{
			Name:        "daily-limit",
			Description: "Remaining usage on the daily usage limit (omitted when unavailable)",
		}, true
	case strings.HasPrefix(value, "weekly "):
		return rateLimitPreviewCopyData{
			Name:        "weekly-limit",
			Description: "Remaining usage on the weekly usage limit (omitted when unavailable)",
		}, true
	case strings.HasPrefix(value, "monthly "):
		return rateLimitPreviewCopyData{
			Name:        "monthly-limit",
			Description: "Remaining usage on the monthly usage limit (omitted when unavailable)",
		}, true
	case strings.HasPrefix(value, "annual "):
		return rateLimitPreviewCopyData{
			Name:        "annual-limit",
			Description: "Remaining usage on the annual usage limit (omitted when unavailable)",
		}, true
	default:
		return rateLimitPreviewCopyData{}, false
	}
}

func statusSurfacePlaceholder(item StatusSurfacePreviewItem) string {
	switch item {
	case StatusPreviewAppName:
		return "codex"
	case StatusPreviewProjectName:
		return "my-project"
	case StatusPreviewProjectRoot:
		return "my-project"
	case StatusPreviewCurrentDir:
		return "~/my-project/subdir"
	case StatusPreviewStatus:
		return "Working"
	case StatusPreviewThreadTitle:
		return "thread title"
	case StatusPreviewGitBranch:
		return "feat/awesome-feature"
	case StatusPreviewPullRequestNumber:
		return "PR #123"
	case StatusPreviewBranchChanges:
		return "+12 -3"
	case StatusPreviewPermissions:
		return "Workspace"
	case StatusPreviewApprovalMode:
		return "on-request"
	case StatusPreviewContextRemaining:
		return "Context 0% left"
	case StatusPreviewContextUsed:
		return "Context 0% used"
	case StatusPreviewFiveHourLimit:
		return "primary 0%"
	case StatusPreviewWeeklyLimit:
		return "secondary 0%"
	case StatusPreviewCodexVersion:
		return "0.0.0"
	case StatusPreviewContextWindowSize:
		return "0 window"
	case StatusPreviewUsedTokens:
		return "0 used"
	case StatusPreviewTotalInputTokens:
		return "0 in"
	case StatusPreviewTotalOutputTokens:
		return "0 out"
	case StatusPreviewSessionID:
		return "550e8400-e29b-41d4"
	case StatusPreviewFastMode:
		return "Fast on"
	case StatusPreviewRawOutput:
		return "raw output"
	case StatusPreviewWorkspaceHeadline:
		return "Workspace headline"
	case StatusPreviewModel:
		return "gpt-5.2-codex"
	case StatusPreviewModelWithReasoning:
		return "gpt-5.2-codex medium"
	case StatusPreviewReasoning:
		return "medium"
	case StatusPreviewTaskProgress:
		return "Tasks 0/0"
	default:
		return ""
	}
}

func containsStatusLineItem(items []bottompane.StatusLineItem, target bottompane.StatusLineItem) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func containsTerminalTitleItem(items []TerminalTitleItem, target TerminalTitleItem) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
