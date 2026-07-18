package chatwidget

import "strings"

const ReviewPopupViewID = "review-popup"

const (
	ReviewActionOpenBranchPicker UsageMenuAction = "review_open_branch_picker"
	ReviewActionUncommitted      UsageMenuAction = "review_uncommitted"
	ReviewActionOpenCommitPicker UsageMenuAction = "review_open_commit_picker"
	ReviewActionOpenCustomPrompt UsageMenuAction = "review_open_custom_prompt"
	ReviewActionBaseBranch       UsageMenuAction = "review_base_branch"
	ReviewActionCommit           UsageMenuAction = "review_commit"
	ReviewActionCustom           UsageMenuAction = "review_custom"
)

type ReviewTargetKind string

const (
	ReviewTargetBaseBranch  ReviewTargetKind = "base_branch"
	ReviewTargetUncommitted ReviewTargetKind = "uncommitted_changes"
	ReviewTargetCommit      ReviewTargetKind = "commit"
	ReviewTargetCustom      ReviewTargetKind = "custom"
)

type ReviewTarget struct {
	Kind         ReviewTargetKind
	Branch       string
	SHA          string
	Title        string
	Instructions string
}

type ReviewState struct {
	RecentAutoReviewDenials []string
	IsReviewMode            bool
	PreReviewTokenInfoSet   bool
	PreReviewTokenInfo      *string
}

type ReviewModeTransitionResult struct {
	Entered               bool
	Exited                bool
	RestoreTokenInfo      *string
	ClearTokenInfo        bool
	RefreshStatusSurfaces bool
	RequestRedraw         bool
}

type ReviewCommitEntry struct {
	Subject string
	SHA     string
}

type ReviewCustomPromptView struct {
	Title        string
	Placeholder  string
	InitialText  string
	ContextLabel *string
}

func NewReviewPresetView() SelectionView {
	return SelectionView{
		ViewID:      ReviewPopupViewID,
		Title:       "Select a review preset",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				Name:                       "Review against a base branch",
				Description:                "(PR Style)",
				Action:                     ReviewActionOpenBranchPicker,
				DismissParentOnChildAccept: true,
			},
			{
				Name:            "Review uncommitted changes",
				Action:          ReviewActionUncommitted,
				DismissOnSelect: true,
			},
			{
				Name:                       "Review a commit",
				Action:                     ReviewActionOpenCommitPicker,
				DismissParentOnChildAccept: true,
			},
			{
				Name:                       "Custom review instructions",
				Action:                     ReviewActionOpenCustomPrompt,
				DismissParentOnChildAccept: true,
			},
		},
	}
}

func NewReviewBranchPickerView(currentBranch string, branches []string) SelectionView {
	if currentBranch == "" {
		currentBranch = "(detached HEAD)"
	}
	items := make([]SelectionItem, 0, len(branches))
	for _, branch := range branches {
		items = append(items, SelectionItem{
			ID:              "review_base_branch:" + branch,
			Name:            currentBranch + " -> " + branch,
			SearchValue:     branch,
			Action:          ReviewActionBaseBranch,
			DismissOnSelect: true,
		})
	}
	return SelectionView{
		ViewID:            ReviewPopupViewID,
		Title:             "Select a base branch",
		FooterHint:        standardPopupHintLine,
		Items:             items,
		AllowCancel:       true,
		Searchable:        true,
		SearchPlaceholder: "Type to search branches",
	}
}

func ReviewTargetForBranch(branch string) (ReviewTarget, bool) {
	return ReviewTarget{Kind: ReviewTargetBaseBranch, Branch: branch}, true
}

func NewReviewCommitPickerView(entries []ReviewCommitEntry) SelectionView {
	items := make([]SelectionItem, 0, len(entries))
	for _, entry := range entries {
		subject := entry.Subject
		sha := entry.SHA
		items = append(items, SelectionItem{
			ID:              "review_commit:" + sha,
			Name:            subject,
			SearchValue:     subject + " " + sha,
			Action:          ReviewActionCommit,
			DismissOnSelect: true,
		})
	}
	return SelectionView{
		ViewID:            ReviewPopupViewID,
		Title:             "Select a commit to review",
		FooterHint:        standardPopupHintLine,
		Items:             items,
		AllowCancel:       true,
		Searchable:        true,
		SearchPlaceholder: "Type to search commits",
	}
}

func ReviewTargetForCommit(entry ReviewCommitEntry) (ReviewTarget, bool) {
	return ReviewTarget{Kind: ReviewTargetCommit, SHA: entry.SHA, Title: entry.Subject}, true
}

func NewReviewCustomPromptView() ReviewCustomPromptView {
	return ReviewCustomPromptView{
		Title:       "Custom review instructions",
		Placeholder: "Type instructions and press Enter",
		InitialText: "",
	}
}

func ReviewTargetForCustomPrompt(prompt string) (ReviewTarget, bool) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ReviewTarget{}, false
	}
	return ReviewTarget{Kind: ReviewTargetCustom, Instructions: prompt}, true
}

func ReviewTargetForUncommittedChanges() ReviewTarget {
	return ReviewTarget{Kind: ReviewTargetUncommitted}
}

func (s *ReviewState) EnterReviewMode(currentTokenInfo *string) ReviewModeTransitionResult {
	if s == nil {
		return ReviewModeTransitionResult{}
	}
	if !s.IsReviewMode {
		s.PreReviewTokenInfoSet = true
		s.PreReviewTokenInfo = cloneStringPtrOrNil(currentTokenInfo)
	}
	s.IsReviewMode = true
	return ReviewModeTransitionResult{
		Entered:               true,
		RefreshStatusSurfaces: true,
		RequestRedraw:         true,
	}
}

func (s *ReviewState) ExitReviewMode() ReviewModeTransitionResult {
	if s == nil || !s.IsReviewMode {
		return ReviewModeTransitionResult{}
	}
	result := ReviewModeTransitionResult{
		Exited:                true,
		RefreshStatusSurfaces: true,
		RequestRedraw:         true,
	}
	if s.PreReviewTokenInfoSet {
		if s.PreReviewTokenInfo == nil {
			result.ClearTokenInfo = true
		} else {
			result.RestoreTokenInfo = cloneStringPtrOrNil(s.PreReviewTokenInfo)
		}
	}
	s.IsReviewMode = false
	s.PreReviewTokenInfoSet = false
	s.PreReviewTokenInfo = nil
	return result
}

func (s *ReviewState) ResetForThreadChange() {
	if s == nil {
		return
	}
	s.RecentAutoReviewDenials = nil
	s.IsReviewMode = false
	s.PreReviewTokenInfoSet = false
	s.PreReviewTokenInfo = nil
}

func cloneStringPtrOrNil(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
