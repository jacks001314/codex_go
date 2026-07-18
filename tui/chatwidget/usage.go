package chatwidget

import (
	"strconv"
	"strings"
)

const (
	UsageMenuViewID       = "usage-menu"
	RateLimitResetViewID  = "rate-limit-reset"
	standardPopupHintLine = "Enter select | Esc close"
)

type UsageMenuAction string

const (
	UsageMenuActionShowTokenActivity       UsageMenuAction = "show_token_activity"
	UsageMenuActionOpenRateLimitReset      UsageMenuAction = "open_rate_limit_reset"
	UsageMenuActionConsumeRateLimitReset   UsageMenuAction = "consume_rate_limit_reset"
	UsageMenuActionRetryRateLimitReset     UsageMenuAction = "retry_rate_limit_reset"
	UsageMenuActionCloseRateLimitReset     UsageMenuAction = "close_rate_limit_reset"
	UsageMenuActionRefreshRateLimitCredits UsageMenuAction = "refresh_rate_limit_credits"
)

type SelectionItem struct {
	ID                         string
	Name                       string
	Description                string
	SelectedDescription        string
	SearchValue                string
	Disabled                   bool
	DisabledReason             string
	IsCurrent                  bool
	IsDefault                  bool
	Action                     UsageMenuAction
	DismissOnSelect            bool
	DismissParentOnChildAccept bool
}

type SelectionView struct {
	ViewID                     string
	Title                      string
	Subtitle                   string
	HeaderLines                []string
	FooterHint                 string
	Items                      []SelectionItem
	InitialSelectedIndex       int
	AllowCancel                bool
	Searchable                 bool
	SearchPlaceholder          string
	ReopenOnCancel             bool
	RefreshResetAvailability   bool
	RefreshResetAvailabilityID uint64
}

type UsageMenuState struct {
	HasChatGPTAccount                bool
	AvailableRateLimitResetCredits   *int64
	NextRefreshResetAvailabilityID   uint64
	RefreshWhenKnownResetCreditsZero bool
}

type RateLimitResetCreditsRefresh struct {
	AvailableCount int64
	Snapshots      []RateLimitSnapshot
	Failed         bool
}

type UsageMenuOpenResult struct {
	View                       SelectionView
	RefreshResetAvailability   bool
	RefreshResetAvailabilityID uint64
}

type RateLimitResetPopupResult struct {
	View                     SelectionView
	Matched                  bool
	RequestID                uint64
	RefreshCreditsAfterReset bool
}

type RateLimitResetHintResult struct {
	Matched bool
	Hint    string
}

type UsageRuntimeState struct {
	HasChatGPTAccount              bool
	HasCodexBackendAuth            bool
	PlanType                       string
	AvailableRateLimitResetCredits *int64
	RateLimitSnapshots             map[string]RateLimitSnapshot

	NextRateLimitResetRequestID        uint64
	PendingRateLimitResetRequestID     *uint64
	PendingUsageMenuRateLimitRequestID *uint64
	PendingRateLimitResetHintRequestID *uint64
	PendingRateLimitResetHint          string
}

func NewUsageMenuView(state UsageMenuState) SelectionView {
	resetEligible := state.HasChatGPTAccount
	resetActionEnabled := false
	resetDescription := "No usage limit resets available."
	switch {
	case resetEligible && state.AvailableRateLimitResetCredits != nil && *state.AvailableRateLimitResetCredits > 0:
		resetActionEnabled = true
		resetDescription = "You have " + formatInt64(*state.AvailableRateLimitResetCredits) + " " + ResetLabel(*state.AvailableRateLimitResetCredits) + " available."
	case resetEligible && state.AvailableRateLimitResetCredits == nil:
		resetActionEnabled = true
		resetDescription = "Check reset availability."
	}
	shouldRefresh := resetEligible &&
		state.RefreshWhenKnownResetCreditsZero &&
		state.AvailableRateLimitResetCredits != nil &&
		*state.AvailableRateLimitResetCredits == 0

	return SelectionView{
		ViewID:                   UsageMenuViewID,
		Title:                    "Usage",
		Subtitle:                 "View account usage or redeem an earned reset.",
		FooterHint:               standardPopupHintLine,
		RefreshResetAvailability: shouldRefresh,
		RefreshResetAvailabilityID: func() uint64 {
			if shouldRefresh {
				return state.NextRefreshResetAvailabilityID
			}
			return 0
		}(),
		AllowCancel: true,
		Items: []SelectionItem{
			{
				Name:            "Show usage",
				Description:     "View recent account token usage.",
				Action:          UsageMenuActionShowTokenActivity,
				DismissOnSelect: true,
			},
			{
				Name:            "Redeem usage limit reset",
				Description:     resetDescription,
				Disabled:        !resetActionEnabled,
				Action:          UsageMenuActionOpenRateLimitReset,
				DismissOnSelect: true,
			},
		},
	}
}

func (s *UsageRuntimeState) OpenUsageMenu() UsageMenuOpenResult {
	if s == nil {
		return UsageMenuOpenResult{View: NewUsageMenuView(UsageMenuState{})}
	}
	s.ClearPendingRateLimitResetHint()
	view := NewUsageMenuView(UsageMenuState{
		HasChatGPTAccount:                s.HasChatGPTAccount,
		AvailableRateLimitResetCredits:   cloneInt64Ptr(s.AvailableRateLimitResetCredits),
		RefreshWhenKnownResetCreditsZero: true,
	})
	result := UsageMenuOpenResult{View: view}
	if s.AvailableRateLimitResetCredits != nil && *s.AvailableRateLimitResetCredits == 0 {
		requestID := s.takeNextRateLimitResetRequestID()
		s.PendingUsageMenuRateLimitRequestID = &requestID
		result.RefreshResetAvailability = true
		result.RefreshResetAvailabilityID = requestID
		result.View.RefreshResetAvailability = true
		result.View.RefreshResetAvailabilityID = requestID
	}
	return result
}

func (s *UsageRuntimeState) FinishUsageMenuRateLimitRefresh(requestID uint64, refresh RateLimitResetCreditsRefresh) (SelectionView, bool) {
	if s == nil || !uint64PtrEqual(s.PendingUsageMenuRateLimitRequestID, requestID) {
		return SelectionView{}, false
	}
	s.PendingUsageMenuRateLimitRequestID = nil
	s.ApplyRateLimitSnapshots(refresh.Snapshots)
	if !refresh.Failed {
		s.AvailableRateLimitResetCredits = int64Ptr(refresh.AvailableCount)
	}
	return NewUsageMenuView(UsageMenuState{
		HasChatGPTAccount:              s.HasChatGPTAccount,
		AvailableRateLimitResetCredits: cloneInt64Ptr(s.AvailableRateLimitResetCredits),
	}), true
}

func RateLimitResetLoadingView() SelectionView {
	return SelectionView{
		ViewID:      RateLimitResetViewID,
		Title:       "Usage limit resets",
		Subtitle:    "Checking your available resets...",
		AllowCancel: true,
		Items: []SelectionItem{{
			Name:     "Loading...",
			Disabled: true,
		}},
	}
}

func (s *UsageRuntimeState) ShowRateLimitResetLoadingPopup() RateLimitResetPopupResult {
	if s == nil {
		return RateLimitResetPopupResult{View: RateLimitResetLoadingView()}
	}
	s.ClearPendingRateLimitResetHint()
	requestID := s.takeNextRateLimitResetRequestID()
	s.PendingRateLimitResetRequestID = &requestID
	return RateLimitResetPopupResult{
		View:      RateLimitResetLoadingView(),
		Matched:   true,
		RequestID: requestID,
	}
}

func (s *UsageRuntimeState) FinishRateLimitResetCreditsRefresh(requestID uint64, refresh RateLimitResetCreditsRefresh) RateLimitResetPopupResult {
	if s == nil || !uint64PtrEqual(s.PendingRateLimitResetRequestID, requestID) {
		return RateLimitResetPopupResult{}
	}
	s.PendingRateLimitResetRequestID = nil
	s.ApplyRateLimitSnapshots(refresh.Snapshots)
	if refresh.Failed {
		return RateLimitResetPopupResult{
			View:    RateLimitResetMessageView("Couldn't load usage limit resets. Please try again."),
			Matched: true,
		}
	}
	s.AvailableRateLimitResetCredits = int64Ptr(refresh.AvailableCount)
	if refresh.AvailableCount > 0 {
		return RateLimitResetPopupResult{
			View:    s.RateLimitResetConfirmationView(refresh.AvailableCount),
			Matched: true,
		}
	}
	return RateLimitResetPopupResult{
		View:    RateLimitResetMessageView("You don't have any usage limit resets available."),
		Matched: true,
	}
}

func RateLimitResetConfirmationView(availableCount int64, hasMonthlyWindow bool, planType string) SelectionView {
	resetDescription := "Reset your current 5-hour and weekly usage limits."
	if hasMonthlyWindow || isMonthlyResetPlan(planType) {
		resetDescription = "Reset your current monthly usage limit."
	}
	return SelectionView{
		ViewID:               RateLimitResetViewID,
		Title:                "Usage limit resets",
		Subtitle:             "You have " + formatInt64(availableCount) + " " + ResetLabel(availableCount) + " available.",
		FooterHint:           standardPopupHintLine,
		InitialSelectedIndex: 1,
		AllowCancel:          true,
		Items: []SelectionItem{
			{
				Name:            "Use a reset",
				Description:     resetDescription,
				Action:          UsageMenuActionConsumeRateLimitReset,
				DismissOnSelect: true,
			},
			{
				Name:            "Cancel",
				Action:          UsageMenuActionCloseRateLimitReset,
				DismissOnSelect: true,
			},
		},
	}
}

func (s *UsageRuntimeState) RateLimitResetConfirmationView(availableCount int64) SelectionView {
	if s == nil {
		return RateLimitResetConfirmationView(availableCount, false, "")
	}
	return RateLimitResetConfirmationView(availableCount, HasMonthlyRateLimitWindow(s.RateLimitSnapshots), s.PlanType)
}

func RateLimitResetMessageView(message string) SelectionView {
	return SelectionView{
		ViewID:      RateLimitResetViewID,
		Title:       "Usage limit resets",
		Subtitle:    message,
		AllowCancel: true,
		Items: []SelectionItem{{
			Name:            "Close",
			Action:          UsageMenuActionCloseRateLimitReset,
			DismissOnSelect: true,
		}},
	}
}

func (s *UsageRuntimeState) ShowRateLimitResetConsumingPopup() RateLimitResetPopupResult {
	if s == nil {
		return RateLimitResetPopupResult{View: RateLimitResetConsumingView()}
	}
	s.ClearPendingRateLimitResetHint()
	requestID := s.takeNextRateLimitResetRequestID()
	s.PendingRateLimitResetRequestID = &requestID
	return RateLimitResetPopupResult{
		View:      RateLimitResetConsumingView(),
		Matched:   true,
		RequestID: requestID,
	}
}

func RateLimitResetConsumingView() SelectionView {
	return SelectionView{
		ViewID:      RateLimitResetViewID,
		Title:       "Usage limit resets",
		Subtitle:    "Resetting your usage...",
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:     "Using a reset...",
			Disabled: true,
		}},
	}
}

func RateLimitResetSuccessLoadingView() SelectionView {
	return SelectionView{
		ViewID:      RateLimitResetViewID,
		Title:       "Usage limit resets",
		Subtitle:    "Usage reset. Checking your remaining resets...",
		AllowCancel: false,
		Items: []SelectionItem{{
			Name:     "Refreshing...",
			Disabled: true,
		}},
	}
}

type RateLimitResetConsumeOutcome string

const (
	RateLimitResetOutcomeReset           RateLimitResetConsumeOutcome = "reset"
	RateLimitResetOutcomeAlreadyRedeemed RateLimitResetConsumeOutcome = "alreadyRedeemed"
	RateLimitResetOutcomeNothingToReset  RateLimitResetConsumeOutcome = "nothingToReset"
	RateLimitResetOutcomeNoCredit        RateLimitResetConsumeOutcome = "noCredit"
)

type RateLimitResetConsumeResult struct {
	View                     SelectionView
	RefreshCreditsAfterReset bool
	AvailableCredits         *int64
}

func (s *UsageRuntimeState) FinishRateLimitResetConsume(requestID uint64, outcome RateLimitResetConsumeOutcome, failed bool) RateLimitResetPopupResult {
	if s == nil || !uint64PtrEqual(s.PendingRateLimitResetRequestID, requestID) {
		return RateLimitResetPopupResult{}
	}
	result := RateLimitResetConsumeResultView(outcome, failed)
	if result.AvailableCredits != nil {
		s.AvailableRateLimitResetCredits = cloneInt64Ptr(result.AvailableCredits)
	}
	if result.RefreshCreditsAfterReset {
		s.AvailableRateLimitResetCredits = nil
		return RateLimitResetPopupResult{
			View:                     result.View,
			Matched:                  true,
			RequestID:                requestID,
			RefreshCreditsAfterReset: true,
		}
	}
	s.PendingRateLimitResetRequestID = nil
	return RateLimitResetPopupResult{
		View:    result.View,
		Matched: true,
	}
}

func RateLimitResetConsumeResultView(outcome RateLimitResetConsumeOutcome, failed bool) RateLimitResetConsumeResult {
	if failed {
		return RateLimitResetConsumeResult{
			View: SelectionView{
				ViewID:      RateLimitResetViewID,
				Title:       "Usage limit resets",
				Subtitle:    "Couldn't reset usage. Please try again.",
				AllowCancel: true,
				Items: []SelectionItem{
					{
						Name:            "Try again",
						Action:          UsageMenuActionRetryRateLimitReset,
						DismissOnSelect: true,
					},
					{
						Name:            "Close",
						Action:          UsageMenuActionCloseRateLimitReset,
						DismissOnSelect: true,
					},
				},
			},
		}
	}
	switch outcome {
	case RateLimitResetOutcomeReset, RateLimitResetOutcomeAlreadyRedeemed:
		return RateLimitResetConsumeResult{
			View:                     RateLimitResetSuccessLoadingView(),
			RefreshCreditsAfterReset: true,
		}
	case RateLimitResetOutcomeNothingToReset:
		return RateLimitResetConsumeResult{
			View: RateLimitResetMessageView("Your usage does not need a reset right now."),
		}
	case RateLimitResetOutcomeNoCredit:
		zero := int64(0)
		return RateLimitResetConsumeResult{
			View:             RateLimitResetMessageView("No usage limit resets are available."),
			AvailableCredits: &zero,
		}
	default:
		return RateLimitResetConsumeResult{
			View: RateLimitResetMessageView("Couldn't reset usage. Please try again."),
		}
	}
}

func (s *UsageRuntimeState) FinishPostConsumeResetCreditsRefresh(requestID uint64, refresh RateLimitResetCreditsRefresh) RateLimitResetPopupResult {
	if s == nil || !uint64PtrEqual(s.PendingRateLimitResetRequestID, requestID) {
		return RateLimitResetPopupResult{}
	}
	s.PendingRateLimitResetRequestID = nil
	s.ApplyRateLimitSnapshots(refresh.Snapshots)
	message := "Usage reset."
	if !refresh.Failed {
		s.AvailableRateLimitResetCredits = int64Ptr(refresh.AvailableCount)
		message = "Usage reset. You have " + formatInt64(refresh.AvailableCount) + " " + ResetLabel(refresh.AvailableCount) + " left."
	}
	return RateLimitResetPopupResult{
		View:    RateLimitResetMessageView(message),
		Matched: true,
	}
}

func (s *UsageRuntimeState) StartRateLimitResetStartupCheck() uint64 {
	if s == nil {
		return 0
	}
	s.ClearPendingRateLimitResetHint()
	requestID := s.takeNextRateLimitResetRequestID()
	s.PendingRateLimitResetHintRequestID = &requestID
	return requestID
}

func (s *UsageRuntimeState) FinishRateLimitResetHintRefresh(requestID uint64, refresh RateLimitResetCreditsRefresh) RateLimitResetHintResult {
	if s == nil || !uint64PtrEqual(s.PendingRateLimitResetHintRequestID, requestID) {
		return RateLimitResetHintResult{}
	}
	s.PendingRateLimitResetHintRequestID = nil
	s.ApplyRateLimitSnapshots(refresh.Snapshots)
	if !s.HasCodexBackendAuth {
		return RateLimitResetHintResult{Matched: true}
	}
	if refresh.Failed {
		return RateLimitResetHintResult{Matched: true}
	}
	s.AvailableRateLimitResetCredits = int64Ptr(refresh.AvailableCount)
	hint, ok := RateLimitResetHint(refresh.AvailableCount)
	if ok {
		s.PendingRateLimitResetHint = hint
	}
	return RateLimitResetHintResult{
		Matched: true,
		Hint:    s.PendingRateLimitResetHint,
	}
}

func (s *UsageRuntimeState) ClearPendingRateLimitResetRequests() {
	if s == nil {
		return
	}
	s.PendingRateLimitResetRequestID = nil
	s.PendingUsageMenuRateLimitRequestID = nil
	s.AvailableRateLimitResetCredits = nil
	s.RateLimitSnapshots = nil
	s.ClearPendingRateLimitResetHint()
}

func (s *UsageRuntimeState) ClearPendingRateLimitResetHint() {
	if s == nil {
		return
	}
	s.PendingRateLimitResetHintRequestID = nil
	s.PendingRateLimitResetHint = ""
}

func (s *UsageRuntimeState) TakePendingRateLimitResetHint() (string, bool) {
	if s == nil || s.PendingRateLimitResetHint == "" {
		return "", false
	}
	hint := s.PendingRateLimitResetHint
	s.PendingRateLimitResetHint = ""
	return hint, true
}

func (s *UsageRuntimeState) ApplyRateLimitSnapshots(snapshots []RateLimitSnapshot) {
	if s == nil || len(snapshots) == 0 {
		return
	}
	if s.RateLimitSnapshots == nil {
		s.RateLimitSnapshots = map[string]RateLimitSnapshot{}
	}
	for _, snapshot := range snapshots {
		limitID := strings.TrimSpace(snapshot.LimitID)
		if limitID == "" {
			limitID = "codex"
			snapshot.LimitID = limitID
		}
		if strings.TrimSpace(snapshot.PlanType) != "" {
			s.PlanType = strings.TrimSpace(snapshot.PlanType)
		}
		s.RateLimitSnapshots[limitID] = snapshot
	}
}

func RateLimitResetHint(availableCount int64) (string, bool) {
	if availableCount <= 0 {
		return "", false
	}
	return "You have " + formatInt64(availableCount) + " " + ResetLabel(availableCount) + " available. Run /usage to use one.", true
}

func HasMonthlyRateLimitWindow(snapshots map[string]RateLimitSnapshot) bool {
	for limitID, snapshot := range snapshots {
		effectiveLimitID := strings.TrimSpace(limitID)
		if effectiveLimitID == "" {
			effectiveLimitID = strings.TrimSpace(snapshot.LimitID)
		}
		if effectiveLimitID == "" {
			effectiveLimitID = "codex"
		}
		if !strings.EqualFold(effectiveLimitID, "codex") {
			continue
		}
		if rateLimitWindowIsMonthly(snapshot.Primary) || rateLimitWindowIsMonthly(snapshot.Secondary) {
			return true
		}
	}
	return false
}

func ResetLabel(count int64) string {
	if count == 1 {
		return "usage limit reset"
	}
	return "usage limit resets"
}

func isMonthlyResetPlan(planType string) bool {
	switch strings.ToLower(strings.TrimSpace(planType)) {
	case "free", "go":
		return true
	default:
		return false
	}
}

func rateLimitWindowIsMonthly(window *RateLimitWindow) bool {
	if window == nil || window.WindowDurationMins == nil {
		return false
	}
	label, ok := LimitsDuration(*window.WindowDurationMins)
	return ok && label == "monthly"
}

func (s *UsageRuntimeState) takeNextRateLimitResetRequestID() uint64 {
	s.NextRateLimitResetRequestID++
	if s.NextRateLimitResetRequestID == 0 {
		s.NextRateLimitResetRequestID = 1
	}
	return s.NextRateLimitResetRequestID
}

func uint64PtrEqual(value *uint64, want uint64) bool {
	return value != nil && *value == want
}

func int64Ptr(value int64) *int64 {
	return &value
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
