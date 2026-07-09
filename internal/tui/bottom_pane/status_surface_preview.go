package bottompane

import (
	"strings"
	"unicode"
)

// Rust parity: codex-rs/tui/src/bottom_pane/status_surface_preview.rs.

type StatusSurfacePreview struct {
	ID   string
	Text string
}

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

type StatusPreviewValue struct {
	Text          string
	IsPlaceholder bool
}

type StatusSurfacePreviewData struct {
	values map[StatusSurfacePreviewItem]StatusPreviewValue
}

func DefaultStatusSurfacePreviewData() StatusSurfacePreviewData {
	data := StatusSurfacePreviewData{values: map[StatusSurfacePreviewItem]StatusPreviewValue{}}
	for _, item := range StatusSurfacePreviewItems() {
		data.SetPlaceholder(item, StatusSurfacePreviewPlaceholder(item))
	}
	return data
}

func NewStatusSurfacePreviewData(values map[StatusSurfacePreviewItem]string) StatusSurfacePreviewData {
	data := DefaultStatusSurfacePreviewData()
	for item, value := range values {
		data.SetLive(item, value)
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

func (d *StatusSurfacePreviewData) SetLive(item StatusSurfacePreviewItem, value string) {
	d.ensure()
	d.values[item] = StatusPreviewValue{Text: value}
}

func (d *StatusSurfacePreviewData) SetPlaceholder(item StatusSurfacePreviewItem, value string) {
	d.ensure()
	if existing, ok := d.values[item]; ok && !existing.IsPlaceholder {
		return
	}
	d.values[item] = StatusPreviewValue{Text: value, IsPlaceholder: true}
}

func (d *StatusSurfacePreviewData) SuppressPlaceholder(item StatusSurfacePreviewItem) {
	if d == nil || d.values == nil {
		return
	}
	if existing, ok := d.values[item]; ok && existing.IsPlaceholder {
		delete(d.values, item)
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

func (d StatusSurfacePreviewData) StatusLineForItems(items []StatusLineItem, useThemeColors bool) (StatusLine, bool) {
	segments := []StatusLineSegment{}
	for _, item := range items {
		previewItem := StatusLineItemPreviewItem(item)
		if previewItem == "" {
			continue
		}
		if value, ok := d.ValueFor(previewItem); ok {
			segments = append(segments, StatusLineSegment{Item: item, Text: value})
		}
	}
	return StatusLineFromSegments(segments, useThemeColors)
}

func (d *StatusSurfacePreviewData) ensure() {
	if d.values == nil {
		d.values = map[StatusSurfacePreviewItem]StatusPreviewValue{}
	}
}

type rateLimitPreviewCopyData struct {
	Name        string
	Description string
}

func rateLimitPreviewCopy(value string) (rateLimitPreviewCopyData, bool) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	switch {
	case strings.HasPrefix(value, "secondary usage "):
		return rateLimitPreviewCopyData{"secondary-usage-limit", "Remaining usage on the secondary usage limit (omitted when unavailable)"}, true
	case strings.HasPrefix(value, "usage "):
		return rateLimitPreviewCopyData{"usage-limit", "Remaining usage on the primary usage limit (omitted when unavailable)"}, true
	case strings.HasPrefix(value, "5h "):
		return rateLimitPreviewCopyData{"five-hour-limit", "Remaining usage on the 5-hour usage limit (omitted when unavailable)"}, true
	case strings.HasPrefix(value, "daily "):
		return rateLimitPreviewCopyData{"daily-limit", "Remaining usage on the daily usage limit (omitted when unavailable)"}, true
	case strings.HasPrefix(value, "weekly "):
		return rateLimitPreviewCopyData{"weekly-limit", "Remaining usage on the weekly usage limit (omitted when unavailable)"}, true
	case strings.HasPrefix(value, "monthly "):
		return rateLimitPreviewCopyData{"monthly-limit", "Remaining usage on the monthly usage limit (omitted when unavailable)"}, true
	case strings.HasPrefix(value, "annual "):
		return rateLimitPreviewCopyData{"annual-limit", "Remaining usage on the annual usage limit (omitted when unavailable)"}, true
	default:
		return rateLimitPreviewCopyData{}, false
	}
}

func StatusSurfacePreviewPlaceholder(item StatusSurfacePreviewItem) string {
	switch item {
	case StatusPreviewAppName:
		return "codex"
	case StatusPreviewProjectName, StatusPreviewProjectRoot:
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
