package chatwidget

import (
	"fmt"
	"math"
	"strings"
	"time"

	codextui "codex_go/internal/tui"
	bottompane "codex_go/internal/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/chatwidget/status_controls.rs plus the
// setup-facing item metadata in bottom_pane/status_line_setup.rs and
// bottom_pane/title_setup.rs.

type StatusDetailsCapitalization int

const (
	StatusDetailsCapitalizeFirst StatusDetailsCapitalization = iota
	StatusDetailsPreserve
)

type StatusTokenUsage struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	TotalTokens       int64
}

func (u StatusTokenUsage) CachedInput() int64 {
	if u.CachedInputTokens < 0 {
		return 0
	}
	return u.CachedInputTokens
}

func (u StatusTokenUsage) NonCachedInput() int64 {
	value := u.InputTokens - u.CachedInput()
	if value < 0 {
		return 0
	}
	return value
}

func (u StatusTokenUsage) BlendedTotal() int64 {
	output := u.OutputTokens
	if output < 0 {
		output = 0
	}
	return u.NonCachedInput() + output
}

func (u StatusTokenUsage) TokensInContextWindow() int64 {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.BlendedTotal()
}

func (u StatusTokenUsage) PercentOfContextWindowRemaining(contextWindow int64) int64 {
	const baselineTokens int64 = 12000
	if contextWindow <= baselineTokens {
		return 0
	}
	effective := contextWindow - baselineTokens
	used := u.TokensInContextWindow() - baselineTokens
	if used < 0 {
		used = 0
	}
	remaining := effective - used
	if remaining < 0 {
		remaining = 0
	}
	percent := float64(remaining) / float64(effective) * 100
	return clampInt64(int64(percent+0.5), 0, 100)
}

type StatusLinePullRequest struct {
	Number int64
	URL    string
}

type GitBranchDiffStats struct {
	Additions int64
	Deletions int64
}

type StatusLineGitSummary struct {
	PullRequest       *StatusLinePullRequest
	BranchChangeStats *GitBranchDiffStats
}

func (s StatusLineGitSummary) PullRequestText() (string, bool) {
	if s.PullRequest == nil || s.PullRequest.Number <= 0 {
		return "", false
	}
	return "PR #" + formatInt64(s.PullRequest.Number), true
}

func (s StatusLineGitSummary) PullRequestURL() (string, bool) {
	if s.PullRequest == nil || strings.TrimSpace(s.PullRequest.URL) == "" {
		return "", false
	}
	return strings.TrimSpace(s.PullRequest.URL), true
}

func (s StatusLineGitSummary) BranchChangesText() (string, bool) {
	if s.BranchChangeStats == nil {
		return "", false
	}
	if s.BranchChangeStats.Additions == 0 && s.BranchChangeStats.Deletions == 0 {
		return "No changes", true
	}
	return "+" + formatInt64(s.BranchChangeStats.Additions) + " -" + formatInt64(s.BranchChangeStats.Deletions), true
}

type StatusControlsRuntime struct {
	CWD                 string
	ProjectName         string
	ProjectRoot         string
	ModelName           string
	ReasoningEffort     string
	StatusText          string
	Permissions         string
	ApprovalMode        string
	ContextWindowSize   *int64
	LastTokenUsage      StatusTokenUsage
	TotalTokenUsage     StatusTokenUsage
	ThreadID            string
	FastMode            bool
	RawOutput           bool
	ThreadTitle         string
	WorkspaceHeadline   string
	TaskProgress        string
	CodexVersion        string
	HasCodexBackendAuth bool
	RateLimitSnapshots  map[string]RateLimitSnapshot
}

type StatusControlsState struct {
	StatusState StatusState
	Runtime     StatusControlsRuntime

	StatusLineConfigured     bool
	StatusLineIDs            []string
	StatusLineUseThemeColors bool

	TerminalTitleConfigured bool
	TerminalTitleIDs        []string

	TerminalTitleSetupActive             bool
	TerminalTitleSetupOriginalConfigured bool
	TerminalTitleSetupOriginalIDs        []string

	StatusLineEnabled      bool
	LastStatusLine         bottompane.StatusLine
	LastStatusLineRendered bool
	StatusLineHyperlink    string
	StatusLineHyperlinkSet bool
	ActiveAgentLabel       string
	ActiveAgentLabelSet    bool

	LastTerminalTitle         string
	LastTerminalTitleRendered bool

	StatusLineBranchCWD            string
	StatusLineBranch               string
	StatusLineBranchSet            bool
	StatusLineBranchPending        bool
	StatusLineBranchLookupComplete bool

	StatusLineGitSummaryCWD            string
	StatusLineGitSummary               *StatusLineGitSummary
	StatusLineGitSummaryPending        bool
	StatusLineGitSummaryLookupComplete bool

	StatusLineInvalidItemsWarned    bool
	TerminalTitleInvalidItemsWarned bool

	WorkspaceHeadlinePendingRequestID *uint64
	WorkspaceHeadlineLastRequestedAt  time.Time
	WorkspaceHeadlineLastRequestedSet bool
	WorkspaceMessagesDisabled         bool
	NextWorkspaceHeadlineRequestID    uint64

	initialized bool
}

type StatusControlEffects struct {
	RefreshStatusSurfaces bool
	RefreshStatusLine     bool
	RefreshTerminalTitle  bool
}

type StatusSurfaceRefreshResult struct {
	Selections StatusSurfaceSelections

	StatusLineEnabled     bool
	StatusLineRendered    bool
	StatusLine            bottompane.StatusLine
	StatusLineText        string
	StatusLineHyperlink   string
	StatusLineHyperlinkOK bool

	TerminalTitleRendered bool
	TerminalTitleText     string

	RequestGitBranch     bool
	RequestGitBranchCWD  string
	RequestGitSummary    bool
	RequestGitSummaryCWD string

	InvalidStatusLineWarning    string
	InvalidTerminalTitleWarning string

	RequestWorkspaceHeadline         bool
	WorkspaceHeadlineRequestID       uint64
	ScheduleWorkspaceHeadlineRefresh bool
	ScheduleWorkspaceHeadlineAfter   time.Duration
}

type WorkspaceHeadlineFetchKind string

const (
	WorkspaceHeadlineFetchAvailable       WorkspaceHeadlineFetchKind = "available"
	WorkspaceHeadlineFetchFeatureDisabled WorkspaceHeadlineFetchKind = "feature_disabled"
	WorkspaceHeadlineFetchFailed          WorkspaceHeadlineFetchKind = "failed"
)

const WorkspaceHeadlineRefreshInterval = 5 * time.Minute

type WorkspaceHeadlineFetchResult struct {
	Kind     WorkspaceHeadlineFetchKind
	Headline string
	Error    string
}

type WorkspaceHeadlineUpdateResult struct {
	Matched                          bool
	RefreshStatusLine                bool
	ScheduleWorkspaceHeadlineRefresh bool
	ScheduleWorkspaceHeadlineAfter   time.Duration
	StatusLineText                   string
}

func NewStatusControlsState(runtime StatusControlsRuntime) *StatusControlsState {
	state := &StatusControlsState{Runtime: runtime}
	state.ensure()
	return state
}

func (s *StatusControlsState) ensure() {
	if s == nil || s.initialized {
		return
	}
	s.StatusState = NewStatusState()
	s.StatusLineUseThemeColors = true
	s.initialized = true
}

func (s *StatusControlsState) SetStatus(header string, details *string, capitalization StatusDetailsCapitalization, maxLines int) StatusControlEffects {
	if s == nil {
		return StatusControlEffects{}
	}
	s.ensure()
	statusDetails := ""
	if details != nil && *details != "" {
		statusDetails = strings.TrimLeft(*details, " \t\r\n")
		if capitalization == StatusDetailsCapitalizeFirst {
			statusDetails = capitalizeFirstASCII(statusDetails)
		}
	}
	if maxLines <= 0 {
		maxLines = StatusDetailsDefaultMaxLines
	}
	s.StatusState.SetStatus(StatusIndicatorState{
		Header:          header,
		Details:         statusDetails,
		DetailsMaxLines: maxLines,
	})
	effects := StatusControlEffects{}
	if s.TerminalTitleUsesStatus() {
		effects.RefreshStatusSurfaces = true
		s.RefreshStatusSurfaces()
	}
	return effects
}

func (s *StatusControlsState) SetStatusHeader(header string) StatusControlEffects {
	return s.SetStatus(header, nil, StatusDetailsCapitalizeFirst, StatusDetailsDefaultMaxLines)
}

func (s *StatusControlsState) SetStatusLine(line bottompane.StatusLine, ok bool) {
	if s == nil {
		return
	}
	s.LastStatusLine = line
	s.LastStatusLineRendered = ok
}

func (s *StatusControlsState) SetStatusLineHyperlink(url string, ok bool) {
	if s == nil {
		return
	}
	s.StatusLineHyperlink = strings.TrimSpace(url)
	s.StatusLineHyperlinkSet = ok && s.StatusLineHyperlink != ""
}

func (s *StatusControlsState) SetActiveAgentLabel(label string, ok bool) {
	if s == nil {
		return
	}
	s.ActiveAgentLabel = strings.TrimSpace(label)
	s.ActiveAgentLabelSet = ok && s.ActiveAgentLabel != ""
}

func (s *StatusControlsState) RefreshStatusLine() StatusSurfaceRefreshResult {
	return s.RefreshStatusSurfaces()
}

func (s *StatusControlsState) CancelStatusLineSetup() StatusControlEffects {
	return StatusControlEffects{}
}

func (s *StatusControlsState) SetupStatusLine(items []bottompane.StatusLineItem, useThemeColors bool) StatusSurfaceRefreshResult {
	if s == nil {
		return StatusSurfaceRefreshResult{}
	}
	s.ensure()
	s.StatusLineConfigured = true
	s.StatusLineIDs = StatusLineItemIDs(items)
	s.StatusLineUseThemeColors = useThemeColors
	return s.RefreshStatusLine()
}

func (s *StatusControlsState) PreviewTerminalTitle(items []TerminalTitleItem) StatusSurfaceRefreshResult {
	if s == nil {
		return StatusSurfaceRefreshResult{}
	}
	s.ensure()
	if !s.TerminalTitleSetupActive {
		s.TerminalTitleSetupActive = true
		s.TerminalTitleSetupOriginalConfigured = s.TerminalTitleConfigured
		s.TerminalTitleSetupOriginalIDs = append([]string(nil), s.TerminalTitleIDs...)
	}
	s.TerminalTitleConfigured = true
	s.TerminalTitleIDs = TerminalTitleItemIDs(items)
	return s.RefreshTerminalTitle()
}

func (s *StatusControlsState) RevertTerminalTitleSetupPreview() StatusSurfaceRefreshResult {
	if s == nil || !s.TerminalTitleSetupActive {
		return StatusSurfaceRefreshResult{}
	}
	s.TerminalTitleConfigured = s.TerminalTitleSetupOriginalConfigured
	s.TerminalTitleIDs = append([]string(nil), s.TerminalTitleSetupOriginalIDs...)
	s.TerminalTitleSetupActive = false
	s.TerminalTitleSetupOriginalConfigured = false
	s.TerminalTitleSetupOriginalIDs = nil
	return s.RefreshTerminalTitle()
}

func (s *StatusControlsState) CancelTerminalTitleSetup() StatusSurfaceRefreshResult {
	return s.RevertTerminalTitleSetupPreview()
}

func (s *StatusControlsState) SetupTerminalTitle(items []TerminalTitleItem) StatusSurfaceRefreshResult {
	if s == nil {
		return StatusSurfaceRefreshResult{}
	}
	s.ensure()
	s.TerminalTitleSetupActive = false
	s.TerminalTitleSetupOriginalConfigured = false
	s.TerminalTitleSetupOriginalIDs = nil
	s.TerminalTitleConfigured = true
	s.TerminalTitleIDs = TerminalTitleItemIDs(items)
	return s.RefreshTerminalTitle()
}

func (s *StatusControlsState) SetStatusLineBranch(cwd string, branch *string) bool {
	if s == nil {
		return false
	}
	s.ensure()
	if s.StatusLineBranchCWD != cwd {
		s.StatusLineBranchPending = false
		return false
	}
	if branch == nil {
		s.StatusLineBranch = ""
		s.StatusLineBranchSet = false
	} else {
		s.StatusLineBranch = strings.TrimSpace(*branch)
		s.StatusLineBranchSet = s.StatusLineBranch != ""
	}
	s.StatusLineBranchPending = false
	s.StatusLineBranchLookupComplete = true
	s.RefreshStatusSurfaces()
	return true
}

func (s *StatusControlsState) SetStatusLineGitSummary(cwd string, summary StatusLineGitSummary) bool {
	if s == nil {
		return false
	}
	s.ensure()
	if s.StatusLineGitSummaryCWD != cwd {
		s.StatusLineGitSummaryPending = false
		return false
	}
	summaryCopy := summary
	s.StatusLineGitSummary = &summaryCopy
	s.StatusLineGitSummaryPending = false
	s.StatusLineGitSummaryLookupComplete = true
	s.RefreshStatusSurfaces()
	return true
}

func (s *StatusControlsState) ConfiguredStatusLineItems() []string {
	if s == nil || !s.StatusLineConfigured {
		return append([]string(nil), DefaultStatusLineItems...)
	}
	if len(s.StatusLineIDs) == 0 {
		return []string{}
	}
	return append([]string(nil), s.StatusLineIDs...)
}

func (s *StatusControlsState) ConfiguredTerminalTitleItems() []string {
	if s == nil || !s.TerminalTitleConfigured {
		return append([]string(nil), DefaultTerminalTitleItems...)
	}
	if len(s.TerminalTitleIDs) == 0 {
		return []string{}
	}
	return append([]string(nil), s.TerminalTitleIDs...)
}

func (s *StatusControlsState) TerminalTitleUsesStatus() bool {
	for _, id := range s.ConfiguredTerminalTitleItems() {
		id = strings.TrimSpace(id)
		if id == "run-state" || id == "status" {
			return true
		}
	}
	return false
}

func (s *StatusControlsState) RefreshStatusSurfaces() StatusSurfaceRefreshResult {
	if s == nil {
		return StatusSurfaceRefreshResult{}
	}
	s.ensure()
	selections := NewStatusSurfaceSelections(s.ConfiguredStatusLineItems(), s.ConfiguredTerminalTitleItems())
	result := StatusSurfaceRefreshResult{Selections: selections}
	result.InvalidStatusLineWarning = s.warnInvalidStatusLineItemsOnce(selections.InvalidStatusLineItems)
	result.InvalidTerminalTitleWarning = s.warnInvalidTerminalTitleItemsOnce(selections.InvalidTerminalTitleItems)
	branchRequest, branchCWD := s.syncBranchState(selections)
	summaryRequest, summaryCWD := s.syncGitSummaryState(selections)
	workspaceRequest, workspaceRequestID := s.syncWorkspaceHeadlineState(selections, time.Now())
	result.RequestGitBranch = branchRequest
	result.RequestGitBranchCWD = branchCWD
	result.RequestGitSummary = summaryRequest
	result.RequestGitSummaryCWD = summaryCWD
	result.RequestWorkspaceHeadline = workspaceRequest
	result.WorkspaceHeadlineRequestID = workspaceRequestID
	s.refreshStatusLineFromSelections(selections, &result)
	s.refreshTerminalTitleFromSelections(selections, &result)
	return result
}

func (s *StatusControlsState) RefreshTerminalTitle() StatusSurfaceRefreshResult {
	if s == nil {
		return StatusSurfaceRefreshResult{}
	}
	s.ensure()
	selections := NewStatusSurfaceSelections(s.ConfiguredStatusLineItems(), s.ConfiguredTerminalTitleItems())
	result := StatusSurfaceRefreshResult{Selections: selections}
	result.InvalidTerminalTitleWarning = s.warnInvalidTerminalTitleItemsOnce(selections.InvalidTerminalTitleItems)
	branchRequest, branchCWD := s.syncBranchState(selections)
	summaryRequest, summaryCWD := s.syncGitSummaryState(selections)
	workspaceRequest, workspaceRequestID := s.syncWorkspaceHeadlineState(selections, time.Now())
	result.RequestGitBranch = branchRequest
	result.RequestGitBranchCWD = branchCWD
	result.RequestGitSummary = summaryRequest
	result.RequestGitSummaryCWD = summaryCWD
	result.RequestWorkspaceHeadline = workspaceRequest
	result.WorkspaceHeadlineRequestID = workspaceRequestID
	s.refreshTerminalTitleFromSelections(selections, &result)
	return result
}

func (s *StatusControlsState) warnInvalidStatusLineItemsOnce(invalidItems []string) string {
	if s == nil || s.StatusLineInvalidItemsWarned || len(invalidItems) == 0 || strings.TrimSpace(s.Runtime.ThreadID) == "" {
		return ""
	}
	s.StatusLineInvalidItemsWarned = true
	label := "items"
	if len(invalidItems) == 1 {
		label = "item"
	}
	return "Ignored invalid status line " + label + ": " + codextui.ProperJoin(invalidItems) + "."
}

func (s *StatusControlsState) warnInvalidTerminalTitleItemsOnce(invalidItems []string) string {
	if s == nil || s.TerminalTitleInvalidItemsWarned || len(invalidItems) == 0 || strings.TrimSpace(s.Runtime.ThreadID) == "" {
		return ""
	}
	s.TerminalTitleInvalidItemsWarned = true
	label := "items"
	if len(invalidItems) == 1 {
		label = "item"
	}
	return "Ignored invalid terminal title " + label + ": " + codextui.ProperJoin(invalidItems) + "."
}

func (s *StatusControlsState) syncBranchState(selections StatusSurfaceSelections) (bool, string) {
	if !selections.UsesGitBranch() {
		s.StatusLineBranch = ""
		s.StatusLineBranchSet = false
		s.StatusLineBranchPending = false
		s.StatusLineBranchLookupComplete = false
		return false, ""
	}
	cwd := s.statusLineCWD()
	if s.StatusLineBranchCWD != cwd {
		s.StatusLineBranchCWD = cwd
		s.StatusLineBranch = ""
		s.StatusLineBranchSet = false
		s.StatusLineBranchPending = false
		s.StatusLineBranchLookupComplete = false
	}
	if !s.StatusLineBranchLookupComplete && !s.StatusLineBranchPending {
		s.StatusLineBranchPending = true
		return true, cwd
	}
	return false, ""
}

func (s *StatusControlsState) syncGitSummaryState(selections StatusSurfaceSelections) (bool, string) {
	if !selections.UsesGitSummary() {
		s.StatusLineGitSummary = nil
		s.StatusLineGitSummaryPending = false
		s.StatusLineGitSummaryLookupComplete = false
		return false, ""
	}
	cwd := s.statusLineCWD()
	if s.StatusLineGitSummaryCWD != cwd {
		s.StatusLineGitSummaryCWD = cwd
		s.StatusLineGitSummary = nil
		s.StatusLineGitSummaryPending = false
		s.StatusLineGitSummaryLookupComplete = false
	}
	if !s.StatusLineGitSummaryLookupComplete && !s.StatusLineGitSummaryPending {
		s.StatusLineGitSummaryPending = true
		return true, cwd
	}
	return false, ""
}

func (s *StatusControlsState) syncWorkspaceHeadlineState(selections StatusSurfaceSelections, now time.Time) (bool, uint64) {
	if !selections.UsesWorkspaceHeadline() {
		s.Runtime.WorkspaceHeadline = ""
		s.WorkspaceHeadlinePendingRequestID = nil
		s.WorkspaceHeadlineLastRequestedAt = time.Time{}
		s.WorkspaceHeadlineLastRequestedSet = false
		s.WorkspaceMessagesDisabled = false
		return false, 0
	}
	if !s.workspaceHeadlineShouldFetch(now) {
		return false, 0
	}
	requestID := s.NextWorkspaceHeadlineRequestID
	s.NextWorkspaceHeadlineRequestID++
	s.WorkspaceHeadlinePendingRequestID = cloneUint64PtrChatwidget(requestID)
	s.WorkspaceHeadlineLastRequestedAt = now
	s.WorkspaceHeadlineLastRequestedSet = true
	return true, requestID
}

func cloneUint64PtrChatwidget(value uint64) *uint64 {
	copied := value
	return &copied
}

func (s *StatusControlsState) workspaceHeadlineShouldFetch(now time.Time) bool {
	if s == nil ||
		s.WorkspaceHeadlinePendingRequestID != nil ||
		s.WorkspaceMessagesDisabled ||
		!s.Runtime.HasCodexBackendAuth {
		return false
	}
	if !s.WorkspaceHeadlineLastRequestedSet {
		return true
	}
	return now.Sub(s.WorkspaceHeadlineLastRequestedAt) >= WorkspaceHeadlineRefreshInterval
}

func (s *StatusControlsState) SetStatusLineWorkspaceHeadline(requestID uint64, fetch WorkspaceHeadlineFetchResult) WorkspaceHeadlineUpdateResult {
	if s == nil || s.WorkspaceHeadlinePendingRequestID == nil || *s.WorkspaceHeadlinePendingRequestID != requestID {
		return WorkspaceHeadlineUpdateResult{}
	}
	s.WorkspaceHeadlinePendingRequestID = nil
	switch fetch.Kind {
	case WorkspaceHeadlineFetchAvailable:
		s.WorkspaceMessagesDisabled = false
		s.Runtime.WorkspaceHeadline = strings.TrimSpace(fetch.Headline)
	case WorkspaceHeadlineFetchFeatureDisabled:
		s.WorkspaceMessagesDisabled = true
		s.Runtime.WorkspaceHeadline = ""
	case WorkspaceHeadlineFetchFailed:
		// Keep the existing headline and retry according to the normal refresh cadence.
	default:
	}

	selections := NewStatusSurfaceSelections(s.ConfiguredStatusLineItems(), s.ConfiguredTerminalTitleItems())
	result := StatusSurfaceRefreshResult{Selections: selections}
	s.refreshStatusLineFromSelections(selections, &result)
	update := WorkspaceHeadlineUpdateResult{
		Matched:           true,
		RefreshStatusLine: true,
		StatusLineText:    result.StatusLineText,
	}
	if !s.WorkspaceMessagesDisabled && selections.UsesWorkspaceHeadline() {
		update.ScheduleWorkspaceHeadlineRefresh = true
		update.ScheduleWorkspaceHeadlineAfter = WorkspaceHeadlineRefreshInterval
	}
	return update
}

func (s *StatusControlsState) refreshStatusLineFromSelections(selections StatusSurfaceSelections, result *StatusSurfaceRefreshResult) {
	enabled := len(selections.StatusLineItems) > 0
	s.StatusLineEnabled = enabled
	result.StatusLineEnabled = enabled
	if !enabled {
		s.SetStatusLine(bottompane.StatusLine{}, false)
		s.SetStatusLineHyperlink("", false)
		return
	}
	segments := []bottompane.StatusLineSegment{}
	for _, item := range selections.StatusLineItems {
		if value, ok := s.StatusLineValueForItem(item); ok {
			segments = append(segments, bottompane.StatusLineSegment{Item: item, Text: value})
		}
	}
	line, ok := bottompane.StatusLineFromSegments(segments, s.StatusLineUseThemeColors)
	s.SetStatusLine(line, ok)
	result.StatusLineRendered = ok
	result.StatusLine = line
	if ok {
		result.StatusLineText = line.PlainText()
	}
	if containsStatusLineItem(selections.StatusLineItems, bottompane.StatusLinePullRequestNumber) {
		if url, ok := s.statusLinePullRequestURL(); ok {
			s.SetStatusLineHyperlink(url, true)
			result.StatusLineHyperlink = url
			result.StatusLineHyperlinkOK = true
			return
		}
	}
	s.SetStatusLineHyperlink("", false)
}

func (s *StatusControlsState) refreshTerminalTitleFromSelections(selections StatusSurfaceSelections, result *StatusSurfaceRefreshResult) {
	title, ok := TerminalTitleText(selections.TerminalTitleItems, s.TerminalTitlePreviewData(), TerminalTitleRenderOptions{})
	s.LastTerminalTitle = title
	s.LastTerminalTitleRendered = ok
	result.TerminalTitleText = title
	result.TerminalTitleRendered = ok
}

func (s *StatusControlsState) StatusLineValueForItem(item bottompane.StatusLineItem) (string, bool) {
	if s == nil {
		return "", false
	}
	switch item {
	case bottompane.StatusLineModelName:
		return nonEmptyTrimmed(s.Runtime.ModelName)
	case bottompane.StatusLineModelWithReasoning:
		model, ok := nonEmptyTrimmed(s.Runtime.ModelName)
		if !ok {
			return "", false
		}
		reasoning := StatusLineReasoningEffortLabel(s.Runtime.ReasoningEffort)
		if reasoning == "" || reasoning == "default" {
			return model, true
		}
		return model + " " + reasoning, true
	case bottompane.StatusLineReasoning:
		return StatusLineReasoningEffortLabel(s.Runtime.ReasoningEffort), true
	case bottompane.StatusLineCurrentDir:
		return nonEmptyTrimmed(s.statusLineCWD())
	case bottompane.StatusLineProjectRoot:
		if value, ok := nonEmptyTrimmed(s.Runtime.ProjectRoot); ok {
			return value, true
		}
		return nonEmptyTrimmed(s.Runtime.ProjectName)
	case bottompane.StatusLineGitBranch:
		if s.StatusLineBranchSet {
			return s.StatusLineBranch, true
		}
		return "", false
	case bottompane.StatusLinePullRequestNumber:
		if s.StatusLineGitSummary == nil {
			return "", false
		}
		return s.StatusLineGitSummary.PullRequestText()
	case bottompane.StatusLineBranchChanges:
		if s.StatusLineGitSummary == nil {
			return "", false
		}
		return s.StatusLineGitSummary.BranchChangesText()
	case bottompane.StatusLineStatus:
		return s.RunStateStatusText(), true
	case bottompane.StatusLinePermissions:
		return nonEmptyTrimmed(s.Runtime.Permissions)
	case bottompane.StatusLineApprovalMode:
		return nonEmptyTrimmed(s.Runtime.ApprovalMode)
	case bottompane.StatusLineContextRemaining:
		remaining, ok := s.StatusLineContextRemainingPercent()
		if !ok {
			return "", false
		}
		return fmt.Sprintf("Context %d%% left", remaining), true
	case bottompane.StatusLineContextUsed:
		used, ok := s.StatusLineContextUsedPercent()
		if !ok {
			return "", false
		}
		return fmt.Sprintf("Context %d%% used", used), true
	case bottompane.StatusLineContextWindowSize:
		size, ok := s.StatusLineContextWindowSize()
		if !ok {
			return "", false
		}
		return FormatTokensCompact(size) + " window", true
	case bottompane.StatusLineUsedTokens:
		total := s.StatusLineTotalUsage().BlendedTotal()
		if total <= 0 {
			return "", false
		}
		return FormatTokensCompact(total) + " used", true
	case bottompane.StatusLineTotalInputTokens:
		return FormatTokensCompact(s.StatusLineTotalUsage().InputTokens) + " in", true
	case bottompane.StatusLineTotalOutputTokens:
		return FormatTokensCompact(s.StatusLineTotalUsage().OutputTokens) + " out", true
	case bottompane.StatusLineFiveHourLimit:
		window, secondary, ok := s.fiveHourStatusWindow()
		if !ok {
			return "", false
		}
		label := LimitLabelForWindow(window.WindowDurationMins, secondary)
		return StatusLineLimitDisplay(window, label)
	case bottompane.StatusLineWeeklyLimit:
		window, secondary, ok := s.weeklyStatusWindow()
		if !ok {
			return "", false
		}
		label := LimitLabelForWindow(window.WindowDurationMins, secondary)
		return StatusLineLimitDisplay(window, label)
	case bottompane.StatusLineCodexVersion:
		if value, ok := nonEmptyTrimmed(s.Runtime.CodexVersion); ok {
			return value, true
		}
		return "0.0.0", true
	case bottompane.StatusLineSessionID:
		return nonEmptyTrimmed(s.Runtime.ThreadID)
	case bottompane.StatusLineFastMode:
		if s.Runtime.FastMode {
			return "Fast on", true
		}
		return "Fast off", true
	case bottompane.StatusLineRawOutput:
		if s.Runtime.RawOutput {
			return "raw output", true
		}
		return "", false
	case bottompane.StatusLineThreadTitle:
		if value, ok := nonEmptyTrimmed(s.Runtime.ThreadTitle); ok {
			return value, true
		}
		return nonEmptyTrimmed(s.Runtime.ThreadID)
	case bottompane.StatusLineWorkspaceHeadline:
		return nonEmptyTrimmed(s.Runtime.WorkspaceHeadline)
	case bottompane.StatusLineTaskProgress:
		return nonEmptyTrimmed(s.Runtime.TaskProgress)
	default:
		return "", false
	}
}

func (s *StatusControlsState) StatusSurfacePreviewValueForItem(item StatusSurfacePreviewItem) (string, bool) {
	if s == nil {
		return "", false
	}
	switch item {
	case StatusPreviewAppName:
		return "codex", true
	case StatusPreviewProjectName:
		if value, ok := nonEmptyTrimmed(s.Runtime.ProjectName); ok {
			return value, true
		}
		return nonEmptyTrimmed(s.Runtime.ProjectRoot)
	case StatusPreviewStatus:
		return s.RunStateStatusText(), true
	case StatusPreviewTaskProgress:
		return nonEmptyTrimmed(s.Runtime.TaskProgress)
	}
	statusLineItem, ok := statusLineItemForPreviewItem(item)
	if !ok {
		return "", false
	}
	return s.StatusLineValueForItem(statusLineItem)
}

func (s *StatusControlsState) StatusSurfacePreviewData() StatusSurfacePreviewData {
	data := DefaultStatusSurfacePreviewData()
	for _, item := range StatusSurfacePreviewItems() {
		if value, ok := s.StatusSurfacePreviewValueForItem(item); ok {
			data.SetLive(item, value)
		}
	}
	if _, ok := s.codexRateLimitSnapshot(); ok {
		for _, item := range []StatusSurfacePreviewItem{StatusPreviewFiveHourLimit, StatusPreviewWeeklyLimit} {
			if _, ok := s.StatusSurfacePreviewValueForItem(item); !ok {
				data.SuppressPlaceholder(item)
			}
		}
	}
	return data
}

func (s *StatusControlsState) TerminalTitlePreviewData() StatusSurfacePreviewData {
	data := s.StatusSurfacePreviewData()
	for _, item := range AllTerminalTitleItems() {
		previewItem, ok := item.PreviewItem()
		if !ok {
			continue
		}
		if value, ok := s.TerminalTitleValueForItem(item); ok {
			data.SetLive(previewItem, value)
		}
	}
	return data
}

func (s *StatusControlsState) TerminalTitleValueForItem(item TerminalTitleItem) (string, bool) {
	switch item {
	case TerminalTitleAppName:
		return "codex", true
	case TerminalTitleProject:
		if value, ok := nonEmptyTrimmed(s.Runtime.ProjectName); ok {
			return value, true
		}
		if value, ok := nonEmptyTrimmed(s.Runtime.ProjectRoot); ok {
			return value, true
		}
		return nonEmptyTrimmed(s.statusLineCWD())
	case TerminalTitleCurrentDir:
		value, ok := nonEmptyTrimmed(s.statusLineCWD())
		if !ok {
			return "", false
		}
		return TruncateTerminalTitlePart(value, 32), true
	case TerminalTitleSpinner:
		return "", false
	case TerminalTitleStatus:
		return s.RunStateStatusText(), true
	case TerminalTitleThread:
		value, ok := s.StatusLineValueForItem(bottompane.StatusLineThreadTitle)
		if !ok {
			return "", false
		}
		return TruncateTerminalTitlePart(value, 48), true
	case TerminalTitleGitBranch:
		if !s.StatusLineBranchSet {
			return "", false
		}
		return TruncateTerminalTitlePart(s.StatusLineBranch, 32), true
	case TerminalTitleContextRemaining:
		return s.truncatedStatusLineValue(bottompane.StatusLineContextRemaining)
	case TerminalTitleContextUsed:
		return s.truncatedStatusLineValue(bottompane.StatusLineContextUsed)
	case TerminalTitleFiveHourLimit:
		return s.truncatedStatusLineValue(bottompane.StatusLineFiveHourLimit)
	case TerminalTitleWeeklyLimit:
		return s.truncatedStatusLineValue(bottompane.StatusLineWeeklyLimit)
	case TerminalTitleCodexVersion:
		return s.truncatedStatusLineValue(bottompane.StatusLineCodexVersion)
	case TerminalTitleUsedTokens:
		return s.truncatedStatusLineValue(bottompane.StatusLineUsedTokens)
	case TerminalTitleTotalInputTokens:
		return s.truncatedStatusLineValue(bottompane.StatusLineTotalInputTokens)
	case TerminalTitleTotalOutputTokens:
		return s.truncatedStatusLineValue(bottompane.StatusLineTotalOutputTokens)
	case TerminalTitleSessionID:
		return s.truncatedStatusLineValue(bottompane.StatusLineSessionID)
	case TerminalTitleFastMode:
		return s.truncatedStatusLineValue(bottompane.StatusLineFastMode)
	case TerminalTitleModel:
		value, ok := s.StatusLineValueForItem(bottompane.StatusLineModelName)
		if !ok {
			return "", false
		}
		return TruncateTerminalTitlePart(value, 32), true
	case TerminalTitleModelWithReasoning:
		value, ok := s.StatusLineValueForItem(bottompane.StatusLineModelWithReasoning)
		if !ok {
			return "", false
		}
		return TruncateTerminalTitlePart(value, 32), true
	case TerminalTitleReasoning:
		value, ok := s.StatusLineValueForItem(bottompane.StatusLineReasoning)
		if !ok {
			return "", false
		}
		return TruncateTerminalTitlePart(value, 32), true
	case TerminalTitleTaskProgress:
		return s.truncatedStatusLineValue(bottompane.StatusLineTaskProgress)
	default:
		return "", false
	}
}

func (s *StatusControlsState) truncatedStatusLineValue(item bottompane.StatusLineItem) (string, bool) {
	value, ok := s.StatusLineValueForItem(item)
	if !ok {
		return "", false
	}
	return TruncateTerminalTitlePart(value, 32), true
}

func (s *StatusControlsState) RunStateStatusText() string {
	if value, ok := nonEmptyTrimmed(s.Runtime.StatusText); ok {
		return value
	}
	if value, ok := nonEmptyTrimmed(s.StatusState.CurrentStatus.Header); ok {
		return value
	}
	return "Working"
}

func (s *StatusControlsState) StatusLineContextWindowSize() (int64, bool) {
	if s == nil || s.Runtime.ContextWindowSize == nil {
		return 0, false
	}
	return *s.Runtime.ContextWindowSize, true
}

func (s *StatusControlsState) StatusLineContextRemainingPercent() (int64, bool) {
	contextWindow, ok := s.StatusLineContextWindowSize()
	if !ok {
		return 100, true
	}
	return s.Runtime.LastTokenUsage.PercentOfContextWindowRemaining(contextWindow), true
}

func (s *StatusControlsState) StatusLineContextUsedPercent() (int64, bool) {
	remaining, ok := s.StatusLineContextRemainingPercent()
	if !ok {
		return 0, false
	}
	return clampInt64(100-remaining, 0, 100), true
}

func (s *StatusControlsState) StatusLineTotalUsage() StatusTokenUsage {
	if s == nil {
		return StatusTokenUsage{}
	}
	return s.Runtime.TotalTokenUsage
}

func StatusLineLimitDisplay(window *RateLimitWindow, label string) (string, bool) {
	if window == nil {
		return "", false
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = primaryLimitFallbackLabel
	}
	remaining := math.Max(0, math.Min(100, 100-window.UsedPercent))
	return fmt.Sprintf("%s %.0f%% left", label, remaining), true
}

func StatusLineReasoningEffortLabel(effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" || strings.EqualFold(effort, "none") {
		return "default"
	}
	return effort
}

func (s *StatusControlsState) statusLinePullRequestURL() (string, bool) {
	if s == nil || s.StatusLineGitSummary == nil {
		return "", false
	}
	return s.StatusLineGitSummary.PullRequestURL()
}

func (s *StatusControlsState) statusLineCWD() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.Runtime.CWD)
}

func (s *StatusControlsState) codexRateLimitSnapshot() (RateLimitSnapshot, bool) {
	if s == nil || s.Runtime.RateLimitSnapshots == nil {
		return RateLimitSnapshot{}, false
	}
	if snapshot, ok := s.Runtime.RateLimitSnapshots["codex"]; ok {
		return snapshot, true
	}
	if snapshot, ok := s.Runtime.RateLimitSnapshots[""]; ok {
		return snapshot, true
	}
	return RateLimitSnapshot{}, false
}

func (s *StatusControlsState) fiveHourStatusWindow() (*RateLimitWindow, bool, bool) {
	snapshot, ok := s.codexRateLimitSnapshot()
	if !ok {
		return nil, false, false
	}
	if window, secondary, ok := findPrimaryCodexWindow(snapshot, "5h"); ok {
		return window, secondary, true
	}
	if window, secondary, ok := secondaryWindowWithLabelWhenWeeklyIsAvailable(snapshot, "5h"); ok {
		return window, secondary, true
	}
	if window, secondary, ok := nonWeeklyPrimaryWindow(snapshot); ok {
		return window, secondary, true
	}
	if window, secondary, ok := nonWeeklySecondaryWindowWhenPrimaryIsWeekly(snapshot); ok {
		return window, secondary, true
	}
	return nil, false, false
}

func (s *StatusControlsState) weeklyStatusWindow() (*RateLimitWindow, bool, bool) {
	snapshot, ok := s.codexRateLimitSnapshot()
	if !ok {
		return nil, false, false
	}
	if window, secondary, ok := findCodexWindow(snapshot, "weekly"); ok {
		return window, secondary, true
	}
	if snapshot.Secondary != nil {
		return snapshot.Secondary, true, true
	}
	return nil, false, false
}

func findCodexWindow(snapshot RateLimitSnapshot, label string) (*RateLimitWindow, bool, bool) {
	if snapshot.Primary != nil && matchesWindowLabel(snapshot.Primary, label) {
		return snapshot.Primary, false, true
	}
	if snapshot.Secondary != nil && matchesWindowLabel(snapshot.Secondary, label) {
		return snapshot.Secondary, true, true
	}
	return nil, false, false
}

func findPrimaryCodexWindow(snapshot RateLimitSnapshot, label string) (*RateLimitWindow, bool, bool) {
	if snapshot.Primary != nil && matchesWindowLabel(snapshot.Primary, label) {
		return snapshot.Primary, false, true
	}
	return nil, false, false
}

func secondaryWindowWithLabelWhenWeeklyIsAvailable(snapshot RateLimitSnapshot, label string) (*RateLimitWindow, bool, bool) {
	if _, _, ok := findCodexWindow(snapshot, "weekly"); !ok {
		return nil, false, false
	}
	if snapshot.Secondary != nil && matchesWindowLabel(snapshot.Secondary, label) {
		return snapshot.Secondary, true, true
	}
	return nil, false, false
}

func nonWeeklyPrimaryWindow(snapshot RateLimitSnapshot) (*RateLimitWindow, bool, bool) {
	if snapshot.Primary == nil || matchesWindowLabel(snapshot.Primary, "weekly") {
		return nil, false, false
	}
	return snapshot.Primary, false, true
}

func nonWeeklySecondaryWindowWhenPrimaryIsWeekly(snapshot RateLimitSnapshot) (*RateLimitWindow, bool, bool) {
	if snapshot.Primary == nil || !matchesWindowLabel(snapshot.Primary, "weekly") {
		return nil, false, false
	}
	if snapshot.Secondary == nil || matchesWindowLabel(snapshot.Secondary, "weekly") {
		return nil, false, false
	}
	return snapshot.Secondary, true, true
}

func matchesWindowLabel(window *RateLimitWindow, label string) bool {
	if window == nil || window.WindowDurationMins == nil {
		return false
	}
	value, ok := LimitsDuration(*window.WindowDurationMins)
	return ok && value == label
}

type StatusLineSetupItem struct {
	Item        bottompane.StatusLineItem
	ID          string
	Name        string
	Description string
	Preview     string
	Selected    bool
}

type StatusLineSetupView struct {
	Title          string
	FooterHint     string
	Items          []StatusLineSetupItem
	UseThemeColors bool
	PreviewText    string
}

func NewStatusLineSetupView(configured []bottompane.StatusLineItem, useThemeColors bool, previewData StatusSurfacePreviewData) StatusLineSetupView {
	if configured == nil {
		configured = NewStatusSurfaceSelections(nil, nil).StatusLineItems
	}
	items := make([]StatusLineSetupItem, 0, len(AllStatusLineItems()))
	for _, item := range AllStatusLineItems() {
		id := StatusLineItemID(item)
		preview := ""
		if previewItem := StatusLineItemPreviewItem(item); previewItem != "" {
			preview, _ = previewData.ValueFor(previewItem)
		}
		items = append(items, StatusLineSetupItem{
			Item:        item,
			ID:          id,
			Name:        id,
			Description: StatusLineItemDescription(item, previewData),
			Preview:     preview,
			Selected:    containsStatusLineItem(configured, item),
		})
	}
	line, ok := previewData.StatusLineForItems(configured, useThemeColors)
	previewText := ""
	if ok {
		previewText = line.PlainText()
	}
	return StatusLineSetupView{
		Title:          "Status line setup",
		FooterHint:     "Space toggle | Enter save | Esc cancel",
		Items:          items,
		UseThemeColors: useThemeColors,
		PreviewText:    previewText,
	}
}

type TerminalTitleSetupItem struct {
	Item        TerminalTitleItem
	ID          string
	Name        string
	Description string
	Preview     string
	Selected    bool
}

type TerminalTitleSetupView struct {
	Title       string
	FooterHint  string
	Items       []TerminalTitleSetupItem
	PreviewText string
}

func NewTerminalTitleSetupView(configured []TerminalTitleItem, previewData StatusSurfacePreviewData) TerminalTitleSetupView {
	if configured == nil {
		configured = NewStatusSurfaceSelections(nil, nil).TerminalTitleItems
	}
	items := make([]TerminalTitleSetupItem, 0, len(AllTerminalTitleItems()))
	for _, item := range AllTerminalTitleItems() {
		id := item.ID()
		preview := ""
		if previewItem, ok := item.PreviewItem(); ok {
			preview, _ = previewData.ValueFor(previewItem)
		}
		items = append(items, TerminalTitleSetupItem{
			Item:        item,
			ID:          id,
			Name:        id,
			Description: TerminalTitleItemDescription(item, previewData),
			Preview:     preview,
			Selected:    containsTerminalTitleItem(configured, item),
		})
	}
	previewText, _ := PreviewLineForTitleItems(configured, previewData)
	return TerminalTitleSetupView{
		Title:       "Terminal title setup",
		FooterHint:  "Space toggle | Enter save | Esc cancel",
		Items:       items,
		PreviewText: previewText,
	}
}

func AllStatusLineItems() []bottompane.StatusLineItem {
	return []bottompane.StatusLineItem{
		bottompane.StatusLineModelName,
		bottompane.StatusLineModelWithReasoning,
		bottompane.StatusLineReasoning,
		bottompane.StatusLineCurrentDir,
		bottompane.StatusLineProjectRoot,
		bottompane.StatusLineGitBranch,
		bottompane.StatusLinePullRequestNumber,
		bottompane.StatusLineBranchChanges,
		bottompane.StatusLineStatus,
		bottompane.StatusLinePermissions,
		bottompane.StatusLineApprovalMode,
		bottompane.StatusLineContextRemaining,
		bottompane.StatusLineContextUsed,
		bottompane.StatusLineFiveHourLimit,
		bottompane.StatusLineWeeklyLimit,
		bottompane.StatusLineCodexVersion,
		bottompane.StatusLineContextWindowSize,
		bottompane.StatusLineUsedTokens,
		bottompane.StatusLineTotalInputTokens,
		bottompane.StatusLineTotalOutputTokens,
		bottompane.StatusLineSessionID,
		bottompane.StatusLineFastMode,
		bottompane.StatusLineRawOutput,
		bottompane.StatusLineThreadTitle,
		bottompane.StatusLineWorkspaceHeadline,
		bottompane.StatusLineTaskProgress,
	}
}

func AllTerminalTitleItems() []TerminalTitleItem {
	return []TerminalTitleItem{
		TerminalTitleAppName,
		TerminalTitleProject,
		TerminalTitleCurrentDir,
		TerminalTitleSpinner,
		TerminalTitleStatus,
		TerminalTitleThread,
		TerminalTitleGitBranch,
		TerminalTitleContextRemaining,
		TerminalTitleContextUsed,
		TerminalTitleFiveHourLimit,
		TerminalTitleWeeklyLimit,
		TerminalTitleCodexVersion,
		TerminalTitleUsedTokens,
		TerminalTitleTotalInputTokens,
		TerminalTitleTotalOutputTokens,
		TerminalTitleSessionID,
		TerminalTitleFastMode,
		TerminalTitleModel,
		TerminalTitleModelWithReasoning,
		TerminalTitleReasoning,
		TerminalTitleTaskProgress,
	}
}

func StatusLineItemIDs(items []bottompane.StatusLineItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := StatusLineItemID(item); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func TerminalTitleItemIDs(items []TerminalTitleItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := item.ID(); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func StatusLineItemDescription(item bottompane.StatusLineItem, previewData StatusSurfacePreviewData) string {
	switch item {
	case bottompane.StatusLineModelName:
		return "Current model name"
	case bottompane.StatusLineModelWithReasoning:
		return "Current model name with reasoning level"
	case bottompane.StatusLineReasoning:
		return "Current reasoning level"
	case bottompane.StatusLineCurrentDir:
		return "Current working directory"
	case bottompane.StatusLineProjectRoot:
		return "Project name (omitted when unavailable)"
	case bottompane.StatusLineGitBranch:
		return "Current Git branch (omitted when unavailable)"
	case bottompane.StatusLinePullRequestNumber:
		return "Open pull request number for the current branch (omitted when unavailable)"
	case bottompane.StatusLineBranchChanges:
		return "Committed branch changes against the default branch (omitted when unavailable)"
	case bottompane.StatusLineStatus:
		return "Compact session run-state text (Ready, Working, Thinking)"
	case bottompane.StatusLinePermissions:
		return "Active permission profile or sandbox mode"
	case bottompane.StatusLineApprovalMode:
		return "Active command approval mode"
	case bottompane.StatusLineContextRemaining:
		return "Percentage of context window remaining (omitted when unknown)"
	case bottompane.StatusLineContextUsed:
		return "Percentage of context window used (omitted when unknown)"
	case bottompane.StatusLineFiveHourLimit:
		return previewData.RateLimitItemDescription(StatusPreviewFiveHourLimit, "Remaining usage on the primary usage limit (omitted when unavailable)")
	case bottompane.StatusLineWeeklyLimit:
		return previewData.RateLimitItemDescription(StatusPreviewWeeklyLimit, "Remaining usage on the secondary usage limit (omitted when unavailable)")
	case bottompane.StatusLineCodexVersion:
		return "Codex application version"
	case bottompane.StatusLineContextWindowSize:
		return "Total context window size in tokens (omitted when unknown)"
	case bottompane.StatusLineUsedTokens:
		return "Total tokens used in session (omitted when zero)"
	case bottompane.StatusLineTotalInputTokens:
		return "Total input tokens used in session"
	case bottompane.StatusLineTotalOutputTokens:
		return "Total output tokens used in session"
	case bottompane.StatusLineSessionID:
		return "Current thread identifier (omitted until thread starts)"
	case bottompane.StatusLineFastMode:
		return "Whether Fast mode is currently active"
	case bottompane.StatusLineRawOutput:
		return "Whether raw scrollback mode is active"
	case bottompane.StatusLineThreadTitle:
		return "Current thread title, or thread identifier when unnamed"
	case bottompane.StatusLineWorkspaceHeadline:
		return "Workspace notification headline (Enterprise workspaces only; omitted when unavailable)"
	case bottompane.StatusLineTaskProgress:
		return "Latest task progress from update_plan (omitted until available)"
	default:
		return ""
	}
}

func TerminalTitleItemDescription(item TerminalTitleItem, previewData StatusSurfacePreviewData) string {
	switch item {
	case TerminalTitleAppName:
		return "Codex app name"
	case TerminalTitleProject:
		return "Project name (falls back to current directory name)"
	case TerminalTitleCurrentDir:
		return "Current working directory"
	case TerminalTitleSpinner:
		return "Spinner while working, action-required message while blocked."
	case TerminalTitleStatus:
		return "Compact session run-state text (Ready, Working, Thinking)"
	case TerminalTitleThread:
		return "Current thread title, or thread identifier when unnamed"
	case TerminalTitleGitBranch:
		return "Current Git branch (omitted when unavailable)"
	case TerminalTitleContextRemaining:
		return "Percentage of context window remaining (omitted when unknown)"
	case TerminalTitleContextUsed:
		return "Percentage of context window used (omitted when unknown)"
	case TerminalTitleFiveHourLimit:
		return previewData.RateLimitItemDescription(StatusPreviewFiveHourLimit, "Remaining usage on the primary usage limit (omitted when unavailable)")
	case TerminalTitleWeeklyLimit:
		return previewData.RateLimitItemDescription(StatusPreviewWeeklyLimit, "Remaining usage on the secondary usage limit (omitted when unavailable)")
	case TerminalTitleCodexVersion:
		return "Codex application version"
	case TerminalTitleUsedTokens:
		return "Total tokens used in session (omitted when zero)"
	case TerminalTitleTotalInputTokens:
		return "Total input tokens used in session"
	case TerminalTitleTotalOutputTokens:
		return "Total output tokens used in session"
	case TerminalTitleSessionID:
		return "Current thread identifier (omitted until thread starts)"
	case TerminalTitleFastMode:
		return "Whether Fast mode is currently active"
	case TerminalTitleModel:
		return "Current model name"
	case TerminalTitleModelWithReasoning:
		return "Current model name with reasoning level"
	case TerminalTitleReasoning:
		return "Current reasoning level"
	case TerminalTitleTaskProgress:
		return "Latest task progress from update_plan (omitted until available)"
	default:
		return ""
	}
}

func statusLineItemForPreviewItem(item StatusSurfacePreviewItem) (bottompane.StatusLineItem, bool) {
	switch item {
	case StatusPreviewProjectRoot:
		return bottompane.StatusLineProjectRoot, true
	case StatusPreviewCurrentDir:
		return bottompane.StatusLineCurrentDir, true
	case StatusPreviewThreadTitle:
		return bottompane.StatusLineThreadTitle, true
	case StatusPreviewGitBranch:
		return bottompane.StatusLineGitBranch, true
	case StatusPreviewPullRequestNumber:
		return bottompane.StatusLinePullRequestNumber, true
	case StatusPreviewBranchChanges:
		return bottompane.StatusLineBranchChanges, true
	case StatusPreviewPermissions:
		return bottompane.StatusLinePermissions, true
	case StatusPreviewApprovalMode:
		return bottompane.StatusLineApprovalMode, true
	case StatusPreviewContextRemaining:
		return bottompane.StatusLineContextRemaining, true
	case StatusPreviewContextUsed:
		return bottompane.StatusLineContextUsed, true
	case StatusPreviewFiveHourLimit:
		return bottompane.StatusLineFiveHourLimit, true
	case StatusPreviewWeeklyLimit:
		return bottompane.StatusLineWeeklyLimit, true
	case StatusPreviewCodexVersion:
		return bottompane.StatusLineCodexVersion, true
	case StatusPreviewContextWindowSize:
		return bottompane.StatusLineContextWindowSize, true
	case StatusPreviewUsedTokens:
		return bottompane.StatusLineUsedTokens, true
	case StatusPreviewTotalInputTokens:
		return bottompane.StatusLineTotalInputTokens, true
	case StatusPreviewTotalOutputTokens:
		return bottompane.StatusLineTotalOutputTokens, true
	case StatusPreviewSessionID:
		return bottompane.StatusLineSessionID, true
	case StatusPreviewFastMode:
		return bottompane.StatusLineFastMode, true
	case StatusPreviewRawOutput:
		return bottompane.StatusLineRawOutput, true
	case StatusPreviewWorkspaceHeadline:
		return bottompane.StatusLineWorkspaceHeadline, true
	case StatusPreviewModel:
		return bottompane.StatusLineModelName, true
	case StatusPreviewModelWithReasoning:
		return bottompane.StatusLineModelWithReasoning, true
	case StatusPreviewReasoning:
		return bottompane.StatusLineReasoning, true
	default:
		return 0, false
	}
}

func nonEmptyTrimmed(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != ""
}

func capitalizeFirstASCII(text string) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}

func clampInt64(value, minValue, maxValue int64) int64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
