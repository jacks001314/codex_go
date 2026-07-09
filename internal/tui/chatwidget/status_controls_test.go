package chatwidget

import (
	"reflect"
	"strings"
	"testing"
	"time"

	bottompane "codex_go/internal/tui/bottom_pane"
)

func TestStatusControlsSetStatusRefreshesTitleWhenConfigured(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:         "/repo",
		ProjectName: "repo",
		ModelName:   "gpt-5",
	})
	state.TerminalTitleConfigured = true
	state.TerminalTitleIDs = []string{"project-name", "run-state"}

	details := "  running tests"
	effects := state.SetStatus("Thinking", &details, StatusDetailsCapitalizeFirst, 1)
	if !effects.RefreshStatusSurfaces {
		t.Fatal("SetStatus did not request status surface refresh for a run-state terminal title")
	}
	if got := state.StatusState.CurrentStatus; got.Header != "Thinking" || got.Details != "Running tests" || got.DetailsMaxLines != 1 {
		t.Fatalf("status = %#v", got)
	}
	if !state.LastTerminalTitleRendered || !strings.Contains(state.LastTerminalTitle, "repo") || !strings.Contains(state.LastTerminalTitle, "Thinking") {
		t.Fatalf("terminal title = %q rendered=%v", state.LastTerminalTitle, state.LastTerminalTitleRendered)
	}

	state.TerminalTitleIDs = []string{"project-name"}
	effects = state.SetStatusHeader("Working")
	if effects.RefreshStatusSurfaces {
		t.Fatal("SetStatusHeader refreshed surfaces even though terminal title does not use status")
	}
}

func TestStatusControlsStatusLineSetupBranchGitAndLimits(t *testing.T) {
	fiveHours := int64(5 * 60)
	weekly := int64(7 * 24 * 60)
	contextWindow := int64(32000)
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:               "/repo",
		ModelName:         "gpt-5",
		ReasoningEffort:   "high",
		Permissions:       "Workspace",
		ApprovalMode:      "on-request",
		ContextWindowSize: &contextWindow,
		LastTokenUsage: StatusTokenUsage{
			TotalTokens: 22000,
		},
		TotalTokenUsage: StatusTokenUsage{
			InputTokens:  2000,
			OutputTokens: 500,
			TotalTokens:  2500,
		},
		RateLimitSnapshots: map[string]RateLimitSnapshot{
			"codex": {
				Primary: &RateLimitWindow{
					UsedPercent:        60,
					WindowDurationMins: &fiveHours,
				},
				Secondary: &RateLimitWindow{
					UsedPercent:        91,
					WindowDurationMins: &weekly,
				},
			},
		},
	})

	result := state.SetupStatusLine([]bottompane.StatusLineItem{
		bottompane.StatusLineModelWithReasoning,
		bottompane.StatusLineCurrentDir,
		bottompane.StatusLineGitBranch,
		bottompane.StatusLinePullRequestNumber,
		bottompane.StatusLineBranchChanges,
		bottompane.StatusLineContextRemaining,
		bottompane.StatusLineContextUsed,
		bottompane.StatusLineFiveHourLimit,
		bottompane.StatusLineWeeklyLimit,
	}, true)
	if !result.RequestGitBranch || result.RequestGitBranchCWD != "/repo" {
		t.Fatalf("branch request = %#v", result)
	}
	if !result.RequestGitSummary || result.RequestGitSummaryCWD != "/repo" {
		t.Fatalf("summary request = %#v", result)
	}
	wantIDs := []string{"model-with-reasoning", "current-dir", "git-branch", "pull-request-number", "branch-changes", "context-remaining", "context-used", "five-hour-limit", "weekly-limit"}
	if !reflect.DeepEqual(state.StatusLineIDs, wantIDs) || !state.StatusLineConfigured {
		t.Fatalf("status line ids = %#v configured=%v", state.StatusLineIDs, state.StatusLineConfigured)
	}

	branch := "main"
	if !state.SetStatusLineBranch("/repo", &branch) {
		t.Fatal("matching branch result ignored")
	}
	if !state.SetStatusLineGitSummary("/repo", StatusLineGitSummary{
		PullRequest:       &StatusLinePullRequest{Number: 42, URL: "https://example.test/pull/42"},
		BranchChangeStats: &GitBranchDiffStats{Additions: 3, Deletions: 1},
	}) {
		t.Fatal("matching git summary ignored")
	}
	line := state.LastStatusLine.PlainText()
	for _, want := range []string{"gpt-5 high", "/repo", "main", "PR #42", "+3 -1", "Context 50% left", "Context 50% used", "5h 40% left", "weekly 9% left"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line %q missing %q", line, want)
		}
	}
	if !state.StatusLineHyperlinkSet || state.StatusLineHyperlink != "https://example.test/pull/42" {
		t.Fatalf("hyperlink = %q set=%v", state.StatusLineHyperlink, state.StatusLineHyperlinkSet)
	}
}

func TestStatusControlsStaleBranchAndGitSummaryAreIgnored(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{CWD: "/expected"})
	state.StatusLineBranchCWD = "/expected"
	state.StatusLineBranchPending = true
	branch := "stale"
	if state.SetStatusLineBranch("/other", &branch) {
		t.Fatal("stale branch was accepted")
	}
	if state.StatusLineBranchPending || state.StatusLineBranchSet {
		t.Fatalf("branch state after stale update = pending %v set %v", state.StatusLineBranchPending, state.StatusLineBranchSet)
	}

	state.StatusLineGitSummaryCWD = "/expected"
	state.StatusLineGitSummaryPending = true
	if state.SetStatusLineGitSummary("/other", StatusLineGitSummary{PullRequest: &StatusLinePullRequest{Number: 7}}) {
		t.Fatal("stale git summary was accepted")
	}
	if state.StatusLineGitSummaryPending || state.StatusLineGitSummary != nil {
		t.Fatalf("git summary state after stale update = pending %v summary %#v", state.StatusLineGitSummaryPending, state.StatusLineGitSummary)
	}
}

func TestStatusControlsTerminalTitlePreviewRevertAndCommit(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:         "/repo",
		ProjectName: "repo",
		StatusText:  "Working",
	})
	state.TerminalTitleConfigured = true
	state.TerminalTitleIDs = []string{"project-name"}

	result := state.PreviewTerminalTitle([]TerminalTitleItem{TerminalTitleProject, TerminalTitleStatus})
	if !state.TerminalTitleSetupActive {
		t.Fatal("preview did not mark terminal title setup active")
	}
	if !reflect.DeepEqual(state.TerminalTitleIDs, []string{"project-name", "run-state"}) {
		t.Fatalf("preview ids = %#v", state.TerminalTitleIDs)
	}
	if !result.TerminalTitleRendered || result.TerminalTitleText != "repo | Working" {
		t.Fatalf("preview title = %#v", result)
	}

	reverted := state.CancelTerminalTitleSetup()
	if state.TerminalTitleSetupActive {
		t.Fatal("cancel did not end setup")
	}
	if !reflect.DeepEqual(state.TerminalTitleIDs, []string{"project-name"}) {
		t.Fatalf("reverted ids = %#v", state.TerminalTitleIDs)
	}
	if !reverted.TerminalTitleRendered || reverted.TerminalTitleText != "repo" {
		t.Fatalf("reverted title = %#v", reverted)
	}

	state.PreviewTerminalTitle([]TerminalTitleItem{TerminalTitleStatus})
	committed := state.SetupTerminalTitle([]TerminalTitleItem{TerminalTitleAppName, TerminalTitleProject})
	if state.TerminalTitleSetupActive {
		t.Fatal("commit left setup active")
	}
	if !reflect.DeepEqual(state.TerminalTitleIDs, []string{"app-name", "project-name"}) {
		t.Fatalf("committed ids = %#v", state.TerminalTitleIDs)
	}
	if !committed.TerminalTitleRendered || committed.TerminalTitleText != "codex | repo" {
		t.Fatalf("committed title = %#v", committed)
	}
	state.CancelTerminalTitleSetup()
	if !reflect.DeepEqual(state.TerminalTitleIDs, []string{"app-name", "project-name"}) {
		t.Fatalf("cancel after commit reverted ids = %#v", state.TerminalTitleIDs)
	}
}

func TestStatusControlsHelpersAndPreviewData(t *testing.T) {
	fiveHours := int64(5 * 60)
	contextWindow := int64(32000)
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:               "/repo",
		ModelName:         "gpt-5",
		ReasoningEffort:   "none",
		ContextWindowSize: &contextWindow,
		LastTokenUsage:    StatusTokenUsage{TotalTokens: 12000},
		TotalTokenUsage:   StatusTokenUsage{InputTokens: 1000, OutputTokens: 250},
		RateLimitSnapshots: map[string]RateLimitSnapshot{
			"codex": {
				Primary: &RateLimitWindow{
					UsedPercent:        87.4,
					WindowDurationMins: &fiveHours,
				},
			},
		},
	})
	if got := StatusLineReasoningEffortLabel(state.Runtime.ReasoningEffort); got != "default" {
		t.Fatalf("reasoning label = %q", got)
	}
	if got, ok := state.StatusLineContextRemainingPercent(); !ok || got != 100 {
		t.Fatalf("remaining = %d ok=%v", got, ok)
	}
	if got, ok := state.StatusLineValueForItem(bottompane.StatusLineFiveHourLimit); !ok || got != "5h 13% left" {
		t.Fatalf("five hour = %q ok=%v", got, ok)
	}
	data := state.StatusSurfacePreviewData()
	if got, ok := data.ValueFor(StatusPreviewFiveHourLimit); !ok || got != "5h 13% left" {
		t.Fatalf("preview five hour = %q ok=%v", got, ok)
	}
	if _, ok := data.ValueFor(StatusPreviewWeeklyLimit); ok {
		t.Fatal("weekly placeholder was not suppressed when codex snapshot exists without a weekly value")
	}
}

func TestStatusSetupViewsExposeRustItemMetadataAndPreviews(t *testing.T) {
	data := NewStatusSurfacePreviewData(map[StatusSurfacePreviewItem]string{
		StatusPreviewModel:         "gpt-5",
		StatusPreviewFiveHourLimit: "5h 42% left",
		StatusPreviewProjectName:   "repo",
		StatusPreviewStatus:        "Working",
	})
	statusView := NewStatusLineSetupView([]bottompane.StatusLineItem{
		bottompane.StatusLineModelName,
		bottompane.StatusLineFiveHourLimit,
	}, false, data)
	if statusView.Title != "Status line setup" || !strings.Contains(statusView.PreviewText, "gpt-5") || !strings.Contains(statusView.PreviewText, "5h 42% left") {
		t.Fatalf("status setup view = %#v", statusView)
	}
	if len(statusView.Items) != len(AllStatusLineItems()) {
		t.Fatalf("status setup item count = %d want %d", len(statusView.Items), len(AllStatusLineItems()))
	}
	fiveHour := findStatusLineSetupItem(statusView.Items, "five-hour-limit")
	if fiveHour == nil || !strings.Contains(fiveHour.Description, "5-hour usage limit") || !fiveHour.Selected {
		t.Fatalf("five hour setup item = %#v", fiveHour)
	}

	titleView := NewTerminalTitleSetupView([]TerminalTitleItem{
		TerminalTitleProject,
		TerminalTitleSpinner,
		TerminalTitleStatus,
	}, data)
	if titleView.Title != "Terminal title setup" || !strings.Contains(titleView.PreviewText, "Action Required") || !strings.Contains(titleView.PreviewText, "repo") {
		t.Fatalf("title setup view = %#v", titleView)
	}
	if len(titleView.Items) != len(AllTerminalTitleItems()) {
		t.Fatalf("title setup item count = %d want %d", len(titleView.Items), len(AllTerminalTitleItems()))
	}
	spinner := findTerminalTitleSetupItem(titleView.Items, "activity")
	if spinner == nil || !strings.Contains(spinner.Description, "Spinner while working") || !spinner.Selected {
		t.Fatalf("spinner setup item = %#v", spinner)
	}
}

func TestStatusControlsInvalidSurfaceWarningsFireOnceAfterThreadConfigured(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:       "/repo",
		ModelName: "gpt-5",
	})
	state.StatusLineConfigured = true
	state.StatusLineIDs = []string{"model", "bad", "bad2", "bad"}
	state.TerminalTitleConfigured = true
	state.TerminalTitleIDs = []string{"project-name", "oops", "oops"}

	result := state.RefreshStatusSurfaces()
	if result.InvalidStatusLineWarning != "" || result.InvalidTerminalTitleWarning != "" {
		t.Fatalf("warnings before thread = status %q title %q", result.InvalidStatusLineWarning, result.InvalidTerminalTitleWarning)
	}

	state.Runtime.ThreadID = "thread-1"
	result = state.RefreshStatusSurfaces()
	if got, want := result.InvalidStatusLineWarning, `Ignored invalid status line items: "bad" and "bad2".`; got != want {
		t.Fatalf("invalid status warning = %q want %q", got, want)
	}
	if got, want := result.InvalidTerminalTitleWarning, `Ignored invalid terminal title item: "oops".`; got != want {
		t.Fatalf("invalid title warning = %q want %q", got, want)
	}

	result = state.RefreshStatusSurfaces()
	if result.InvalidStatusLineWarning != "" || result.InvalidTerminalTitleWarning != "" {
		t.Fatalf("warnings repeated = status %q title %q", result.InvalidStatusLineWarning, result.InvalidTerminalTitleWarning)
	}
}

func TestStatusControlsWorkspaceHeadlineRefreshLifecycleMatchesRust(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:                 "/repo",
		ModelName:           "gpt-5",
		HasCodexBackendAuth: true,
	})
	state.StatusLineConfigured = true
	state.StatusLineIDs = []string{"model", "workspace-headline"}
	selections := NewStatusSurfaceSelections(state.ConfiguredStatusLineItems(), state.ConfiguredTerminalTitleItems())
	now := time.Unix(1000, 0)

	requested, requestID := state.syncWorkspaceHeadlineState(selections, now)
	if !requested || requestID != 0 || state.WorkspaceHeadlinePendingRequestID == nil || *state.WorkspaceHeadlinePendingRequestID != 0 {
		t.Fatalf("initial workspace request = requested %v id %d pending %#v", requested, requestID, state.WorkspaceHeadlinePendingRequestID)
	}
	requested, _ = state.syncWorkspaceHeadlineState(selections, now.Add(time.Minute))
	if requested {
		t.Fatal("workspace headline requested again while previous request was pending")
	}

	update := state.SetStatusLineWorkspaceHeadline(requestID, WorkspaceHeadlineFetchResult{
		Kind:     WorkspaceHeadlineFetchAvailable,
		Headline: "  Ship parity  ",
	})
	if !update.Matched || !update.RefreshStatusLine || !update.ScheduleWorkspaceHeadlineRefresh || update.ScheduleWorkspaceHeadlineAfter != WorkspaceHeadlineRefreshInterval {
		t.Fatalf("available update = %#v", update)
	}
	if state.Runtime.WorkspaceHeadline != "Ship parity" || !strings.Contains(state.LastStatusLine.PlainText(), "Ship parity") {
		t.Fatalf("headline = %q status line %q", state.Runtime.WorkspaceHeadline, state.LastStatusLine.PlainText())
	}

	requested, _ = state.syncWorkspaceHeadlineState(selections, now.Add(time.Minute))
	if requested {
		t.Fatal("workspace headline refreshed before the Rust interval elapsed")
	}
	requested, requestID = state.syncWorkspaceHeadlineState(selections, now.Add(WorkspaceHeadlineRefreshInterval))
	if !requested || requestID != 1 {
		t.Fatalf("interval workspace request = requested %v id %d", requested, requestID)
	}

	disabled := state.SetStatusLineWorkspaceHeadline(requestID, WorkspaceHeadlineFetchResult{Kind: WorkspaceHeadlineFetchFeatureDisabled})
	if !disabled.Matched || disabled.ScheduleWorkspaceHeadlineRefresh || !state.WorkspaceMessagesDisabled || state.Runtime.WorkspaceHeadline != "" {
		t.Fatalf("disabled update = %#v disabled=%v headline=%q", disabled, state.WorkspaceMessagesDisabled, state.Runtime.WorkspaceHeadline)
	}
	requested, _ = state.syncWorkspaceHeadlineState(selections, now.Add(10*WorkspaceHeadlineRefreshInterval))
	if requested {
		t.Fatal("workspace headline requested after feature-disabled response")
	}

	state.StatusLineIDs = []string{"model"}
	selections = NewStatusSurfaceSelections(state.ConfiguredStatusLineItems(), state.ConfiguredTerminalTitleItems())
	state.Runtime.WorkspaceHeadline = "stale"
	state.WorkspaceHeadlinePendingRequestID = cloneUint64PtrChatwidget(99)
	state.syncWorkspaceHeadlineState(selections, now.Add(11*WorkspaceHeadlineRefreshInterval))
	if state.WorkspaceMessagesDisabled || state.Runtime.WorkspaceHeadline != "" || state.WorkspaceHeadlinePendingRequestID != nil || state.WorkspaceHeadlineLastRequestedSet {
		t.Fatalf("workspace state was not reset after removing item: disabled=%v headline=%q pending=%#v lastSet=%v", state.WorkspaceMessagesDisabled, state.Runtime.WorkspaceHeadline, state.WorkspaceHeadlinePendingRequestID, state.WorkspaceHeadlineLastRequestedSet)
	}
}

func TestStatusControlsWorkspaceHeadlineRequiresAuthAndMatchingRequest(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		CWD:               "/repo",
		ModelName:         "gpt-5",
		WorkspaceHeadline: "Existing headline",
	})
	state.StatusLineConfigured = true
	state.StatusLineIDs = []string{"model", "workspace-headline"}
	selections := NewStatusSurfaceSelections(state.ConfiguredStatusLineItems(), state.ConfiguredTerminalTitleItems())
	now := time.Unix(2000, 0)

	requested, _ := state.syncWorkspaceHeadlineState(selections, now)
	if requested || state.WorkspaceHeadlinePendingRequestID != nil {
		t.Fatal("workspace headline requested without codex backend auth")
	}

	state.Runtime.HasCodexBackendAuth = true
	requested, requestID := state.syncWorkspaceHeadlineState(selections, now)
	if !requested || requestID != 0 {
		t.Fatalf("workspace request after auth = requested %v id %d", requested, requestID)
	}

	stale := state.SetStatusLineWorkspaceHeadline(999, WorkspaceHeadlineFetchResult{
		Kind:     WorkspaceHeadlineFetchAvailable,
		Headline: "Stale headline",
	})
	if stale.Matched || state.WorkspaceHeadlinePendingRequestID == nil || *state.WorkspaceHeadlinePendingRequestID != requestID {
		t.Fatalf("stale update = %#v pending=%#v", stale, state.WorkspaceHeadlinePendingRequestID)
	}

	failed := state.SetStatusLineWorkspaceHeadline(requestID, WorkspaceHeadlineFetchResult{Kind: WorkspaceHeadlineFetchFailed, Error: "timeout"})
	if !failed.Matched || !failed.ScheduleWorkspaceHeadlineRefresh || state.Runtime.WorkspaceHeadline != "Existing headline" {
		t.Fatalf("failed update = %#v headline=%q", failed, state.Runtime.WorkspaceHeadline)
	}
}

func findStatusLineSetupItem(items []StatusLineSetupItem, id string) *StatusLineSetupItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func findTerminalTitleSetupItem(items []TerminalTitleSetupItem, id string) *TerminalTitleSetupItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}
