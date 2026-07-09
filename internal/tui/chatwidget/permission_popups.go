package chatwidget

import (
	"strings"

	codextui "codex_go/internal/tui"
)

const (
	AutoReviewDenialsPopupViewID = "auto-review-denials"

	PermissionActionApproveRecentAutoReviewDenial UsageMenuAction = "approve_recent_auto_review_denial"
)

type AutoReviewDenialEntry struct {
	ID        string
	Summary   string
	Rationale string
}

type AutoReviewDenialsPopupOutcome string

const (
	AutoReviewDenialsPopupShow  AutoReviewDenialsPopupOutcome = "show"
	AutoReviewDenialsPopupInfo  AutoReviewDenialsPopupOutcome = "info"
	AutoReviewDenialsPopupError AutoReviewDenialsPopupOutcome = "error"
)

type AutoReviewDenialsPopupResult struct {
	Outcome AutoReviewDenialsPopupOutcome
	View    SelectionView
	Message string
	Hint    string
}

type AutoReviewDenialsState struct {
	Entries []AutoReviewDenialEntry
}

type AutoReviewDenialApprovalResult struct {
	Approved bool
	Entry    AutoReviewDenialEntry
	Message  string
	Hint     string
	Error    string
}

func NewFullAccessConfirmationPopupView() PermissionMenuView {
	return FullAccessConfirmationView()
}

func PermissionPopupSelectionDisabledReason(item PermissionMenuItem) string {
	return item.DisabledReason
}

func AutoReviewDenialEntriesFromSummaries(denials []codextui.AutoReviewDenial) []AutoReviewDenialEntry {
	out := make([]AutoReviewDenialEntry, 0, len(denials))
	for _, denial := range denials {
		entry := AutoReviewDenialEntry{
			ID:      strings.TrimSpace(denial.ID),
			Summary: strings.TrimSpace(denial.Summary),
		}
		if entry.ID == "" && entry.Summary == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func BuildAutoReviewDenialsPopup(threadID string, denials []AutoReviewDenialEntry) AutoReviewDenialsPopupResult {
	if len(denials) == 0 {
		return AutoReviewDenialsPopupResult{
			Outcome: AutoReviewDenialsPopupInfo,
			Message: "No recent auto-review denials in this thread.",
			Hint:    "Denials are recorded after auto-review rejects an action.",
		}
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return AutoReviewDenialsPopupResult{
			Outcome: AutoReviewDenialsPopupError,
			Message: "That thread is no longer available.",
		}
	}

	items := []SelectionItem{
		{
			Name:        "Command",
			Description: "Rationale",
			SearchValue: "",
			Disabled:    true,
		},
	}
	for _, denial := range denials {
		id := strings.TrimSpace(denial.ID)
		summary := strings.TrimSpace(denial.Summary)
		if summary == "" {
			summary = "Denied action"
		}
		rationale := strings.TrimSpace(denial.Rationale)
		if rationale == "" {
			rationale = "Auto-review did not include a rationale."
		}
		items = append(items, SelectionItem{
			ID:                  id,
			Name:                summary,
			Description:         rationale,
			SelectedDescription: rationale,
			SearchValue:         strings.TrimSpace(summary + " " + rationale),
			Action:              PermissionActionApproveRecentAutoReviewDenial,
			DismissOnSelect:     true,
		})
	}

	return AutoReviewDenialsPopupResult{
		Outcome: AutoReviewDenialsPopupShow,
		View: SelectionView{
			ViewID:               AutoReviewDenialsPopupViewID,
			Title:                "Auto-review Denials",
			Subtitle:             "Select a denied action to approve.",
			FooterHint:           standardPopupHintLine,
			Items:                items,
			InitialSelectedIndex: firstEnabledSelectionIndex(items),
			AllowCancel:          true,
			Searchable:           true,
			SearchPlaceholder:    "Type to search denials",
		},
	}
}

func (s *AutoReviewDenialsState) Take(id string) (AutoReviewDenialEntry, bool) {
	if s == nil {
		return AutoReviewDenialEntry{}, false
	}
	id = strings.TrimSpace(id)
	for index, entry := range s.Entries {
		if strings.TrimSpace(entry.ID) != id {
			continue
		}
		out := entry
		copy(s.Entries[index:], s.Entries[index+1:])
		s.Entries = s.Entries[:len(s.Entries)-1]
		return out, true
	}
	return AutoReviewDenialEntry{}, false
}

func ApproveRecentAutoReviewDenial(state *AutoReviewDenialsState, id string) AutoReviewDenialApprovalResult {
	entry, ok := state.Take(id)
	if !ok {
		return AutoReviewDenialApprovalResult{
			Error: "That auto-review denial is no longer available.",
		}
	}
	return AutoReviewDenialApprovalResult{
		Approved: true,
		Entry:    entry,
		Message:  "Approval recorded for one retry of the selected auto-review denial.",
		Hint:     "The model will see the approval context; the retry still goes through auto-review.",
	}
}
