package chatgptapi

import "strings"

type GitPullRequest struct {
	Number         int            `json:"number"`
	URL            string         `json:"url"`
	State          string         `json:"state"`
	Merged         bool           `json:"merged"`
	Mergeable      bool           `json:"mergeable"`
	Draft          *bool          `json:"draft,omitempty"`
	Title          *string        `json:"title,omitempty"`
	Body           *string        `json:"body,omitempty"`
	Base           *string        `json:"base,omitempty"`
	Head           *string        `json:"head,omitempty"`
	BaseSHA        *string        `json:"base_sha,omitempty"`
	HeadSHA        *string        `json:"head_sha,omitempty"`
	MergeCommitSHA *string        `json:"merge_commit_sha,omitempty"`
	Comments       map[string]any `json:"comments,omitempty"`
	Diff           map[string]any `json:"diff,omitempty"`
	User           map[string]any `json:"user,omitempty"`
}

func NewGitPullRequest(number int, url string, state string, merged bool, mergeable bool) *GitPullRequest {
	return &GitPullRequest{Number: number, URL: url, State: state, Merged: merged, Mergeable: mergeable}
}

type ExternalPullRequestResponse struct {
	ID              string          `json:"id"`
	AssistantTurnID string          `json:"assistant_turn_id"`
	PullRequest     *GitPullRequest `json:"pull_request"`
	CodexUpdatedSHA *string         `json:"codex_updated_sha,omitempty"`
}

func NewExternalPullRequestResponse(id string, assistantTurnID string, pullRequest *GitPullRequest) *ExternalPullRequestResponse {
	return &ExternalPullRequestResponse{ID: id, AssistantTurnID: assistantTurnID, PullRequest: pullRequest}
}

type TaskResponse struct {
	ID                   string                        `json:"id"`
	CreatedAt            *float64                      `json:"created_at,omitempty"`
	Title                string                        `json:"title"`
	HasGeneratedTitle    *bool                         `json:"has_generated_title,omitempty"`
	CurrentTurnID        *string                       `json:"current_turn_id,omitempty"`
	HasUnreadTurn        *bool                         `json:"has_unread_turn,omitempty"`
	DenormalizedMetadata map[string]any                `json:"denormalized_metadata,omitempty"`
	Archived             bool                          `json:"archived"`
	ExternalPullRequests []ExternalPullRequestResponse `json:"external_pull_requests"`
}

func NewTaskResponse(id string, title string, archived bool, prs []ExternalPullRequestResponse) *TaskResponse {
	return &TaskResponse{ID: id, Title: title, Archived: archived, ExternalPullRequests: append([]ExternalPullRequestResponse(nil), prs...)}
}

type TaskListItem struct {
	ID                string                        `json:"id"`
	Title             string                        `json:"title"`
	HasGeneratedTitle *bool                         `json:"has_generated_title,omitempty"`
	UpdatedAt         *float64                      `json:"updated_at,omitempty"`
	CreatedAt         *float64                      `json:"created_at,omitempty"`
	TaskStatusDisplay map[string]any                `json:"task_status_display,omitempty"`
	Archived          bool                          `json:"archived"`
	HasUnreadTurn     bool                          `json:"has_unread_turn"`
	PullRequests      []ExternalPullRequestResponse `json:"pull_requests,omitempty"`
}

func NewTaskListItem(id string, title string, hasGeneratedTitle *bool, archived bool, hasUnreadTurn bool) *TaskListItem {
	return &TaskListItem{ID: id, Title: title, HasGeneratedTitle: hasGeneratedTitle, Archived: archived, HasUnreadTurn: hasUnreadTurn}
}

type PlanType string

const (
	PlanGuest                       PlanType = "guest"
	PlanFree                        PlanType = "free"
	PlanGo                          PlanType = "go"
	PlanPlus                        PlanType = "plus"
	PlanPro                         PlanType = "pro"
	PlanProLite                     PlanType = "prolite"
	PlanFreeWorkspace               PlanType = "free_workspace"
	PlanTeam                        PlanType = "team"
	PlanSelfServeBusinessUsageBased PlanType = "self_serve_business_usage_based"
	PlanBusiness                    PlanType = "business"
	PlanEnt26                       PlanType = "ent26"
	PlanEnterpriseCbpAutomation     PlanType = "enterprise_cbp_automation"
	PlanEnterpriseCbpUsageBased     PlanType = "enterprise_cbp_usage_based"
	PlanEducation                   PlanType = "education"
	PlanQuorum                      PlanType = "quorum"
	PlanK12                         PlanType = "k12"
	PlanEnterprise                  PlanType = "enterprise"
	PlanEdu                         PlanType = "edu"
	PlanEduPlus                     PlanType = "edu_plus"
	PlanEduPro                      PlanType = "edu_pro"
	PlanUnknown                     PlanType = "unknown"
)

type RateLimitReachedKind string

const (
	RateLimitReached                 RateLimitReachedKind = "rate_limit_reached"
	WorkspaceOwnerCreditsDepleted    RateLimitReachedKind = "workspace_owner_credits_depleted"
	WorkspaceMemberCreditsDepleted   RateLimitReachedKind = "workspace_member_credits_depleted"
	WorkspaceOwnerUsageLimitReached  RateLimitReachedKind = "workspace_owner_usage_limit_reached"
	WorkspaceMemberUsageLimitReached RateLimitReachedKind = "workspace_member_usage_limit_reached"
	RateLimitReachedUnknown          RateLimitReachedKind = "unknown"
)

type RateLimitReachedType struct {
	Type RateLimitReachedKind `json:"type"`
}

type RateLimitStatusPayload struct {
	PlanType              PlanType                      `json:"plan_type"`
	RateLimit             *RateLimitStatusDetails       `json:"rate_limit,omitempty"`
	Credits               *CreditStatusDetails          `json:"credits,omitempty"`
	SpendControl          *SpendControlStatusDetails    `json:"spend_control,omitempty"`
	AdditionalRateLimits  []AdditionalRateLimitDetails  `json:"additional_rate_limits,omitempty"`
	RateLimitReachedType  *RateLimitReachedType         `json:"rate_limit_reached_type,omitempty"`
	RateLimitResetCredits *RateLimitResetCreditsSummary `json:"rate_limit_reset_credits,omitempty"`
}

func NewRateLimitStatusPayload(planType PlanType) *RateLimitStatusPayload {
	if planType == "" {
		planType = PlanGuest
	}
	return &RateLimitStatusPayload{PlanType: planType}
}

type RateLimitWindowSnapshot struct {
	Used               int64   `json:"used,omitempty"`
	Limit              int64   `json:"limit,omitempty"`
	UsedPercent        float64 `json:"used_percent,omitempty"`
	LimitWindowSeconds int64   `json:"limit_window_seconds,omitempty"`
	ResetsAt           int64   `json:"reset_at,omitempty"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds,omitempty"`
	Remaining          int64   `json:"remaining,omitempty"`
}

type RateLimitStatusDetails struct {
	Allowed         bool                     `json:"allowed"`
	LimitReached    bool                     `json:"limit_reached"`
	PrimaryWindow   *RateLimitWindowSnapshot `json:"primary_window,omitempty"`
	SecondaryWindow *RateLimitWindowSnapshot `json:"secondary_window,omitempty"`
}

func NewRateLimitStatusDetails(allowed bool, limitReached bool) *RateLimitStatusDetails {
	return &RateLimitStatusDetails{Allowed: allowed, LimitReached: limitReached}
}

type AdditionalRateLimitDetails struct {
	LimitName      string                  `json:"limit_name"`
	MeteredFeature string                  `json:"metered_feature"`
	RateLimit      *RateLimitStatusDetails `json:"rate_limit,omitempty"`
}

type SpendControlStatusDetails struct {
	Reached         bool                      `json:"reached"`
	IndividualLimit *SpendControlLimitDetails `json:"individual_limit,omitempty"`
}

type SpendControlLimitDetails struct {
	Source            *string `json:"source,omitempty"`
	Limit             string  `json:"limit"`
	Used              string  `json:"used"`
	Remaining         string  `json:"remaining"`
	UsedPercent       int32   `json:"used_percent"`
	RemainingPercent  int32   `json:"remaining_percent"`
	ResetAfterSeconds int64   `json:"reset_after_seconds"`
	ResetsAt          int64   `json:"reset_at"`
}

type CreditStatusDetails struct {
	HasCredits          bool    `json:"has_credits"`
	Unlimited           bool    `json:"unlimited"`
	Balance             *string `json:"balance,omitempty"`
	ApproxLocalMessages []any   `json:"approx_local_messages,omitempty"`
	ApproxCloudMessages []any   `json:"approx_cloud_messages,omitempty"`
}

func NewCreditStatusDetails(hasCredits bool, unlimited bool) *CreditStatusDetails {
	return &CreditStatusDetails{HasCredits: hasCredits, Unlimited: unlimited}
}

type RateLimitSnapshot struct {
	LimitID              *string
	LimitName            *string
	Primary              *RateLimitWindow
	Secondary            *RateLimitWindow
	Credits              *CreditsSnapshot
	IndividualLimit      *SpendControlLimitSnapshot
	PlanType             PlanType
	RateLimitReachedType *RateLimitReachedKind
}

type RateLimitWindow struct {
	UsedPercent        float64
	WindowDurationMins *int64
	ResetsAt           *int64
}

type CreditsSnapshot struct {
	HasCredits bool
	Unlimited  bool
	Balance    *string
}

type SpendControlLimitSnapshot struct {
	Limit            string
	Used             string
	RemainingPercent int32
	ResetsAt         int64
}

func RateLimitSnapshotsFromPayload(payload *RateLimitStatusPayload) []RateLimitSnapshot {
	if payload == nil {
		return nil
	}
	planType := payload.PlanType
	if planType == "" {
		planType = PlanGuest
	}
	rateLimitReachedType := mapRateLimitReachedType(payload.RateLimitReachedType)
	snapshots := []RateLimitSnapshot{makeRateLimitSnapshot(
		stringPtr("codex"),
		nil,
		payload.RateLimit,
		payload.Credits,
		mapSpendControl(payload.SpendControl),
		planType,
		rateLimitReachedType,
	)}
	for _, additional := range payload.AdditionalRateLimits {
		limitID := strings.TrimSpace(additional.MeteredFeature)
		if limitID == "" {
			limitID = strings.TrimSpace(additional.LimitName)
		}
		snapshots = append(snapshots, makeRateLimitSnapshot(
			stringPtr(limitID),
			stringPtr(additional.LimitName),
			additional.RateLimit,
			nil,
			nil,
			planType,
			nil,
		))
	}
	return snapshots
}

func makeRateLimitSnapshot(limitID *string, limitName *string, rateLimit *RateLimitStatusDetails, credits *CreditStatusDetails, individualLimit *SpendControlLimitSnapshot, planType PlanType, reachedType *RateLimitReachedKind) RateLimitSnapshot {
	var primary *RateLimitWindow
	var secondary *RateLimitWindow
	if rateLimit != nil {
		primary = mapRateLimitWindow(rateLimit.PrimaryWindow)
		secondary = mapRateLimitWindow(rateLimit.SecondaryWindow)
	}
	return RateLimitSnapshot{
		LimitID:              cloneStringPointer(limitID),
		LimitName:            cloneStringPointer(limitName),
		Primary:              primary,
		Secondary:            secondary,
		Credits:              mapCredits(credits),
		IndividualLimit:      individualLimit,
		PlanType:             planType,
		RateLimitReachedType: reachedType,
	}
}

func mapRateLimitWindow(window *RateLimitWindowSnapshot) *RateLimitWindow {
	if window == nil {
		return nil
	}
	return &RateLimitWindow{
		UsedPercent:        window.UsedPercent,
		WindowDurationMins: windowMinutesFromSeconds(window.LimitWindowSeconds),
		ResetsAt:           int64Ptr(window.ResetsAt),
	}
}

func mapCredits(credits *CreditStatusDetails) *CreditsSnapshot {
	if credits == nil {
		return nil
	}
	return &CreditsSnapshot{
		HasCredits: credits.HasCredits,
		Unlimited:  credits.Unlimited,
		Balance:    cloneStringPointer(credits.Balance),
	}
}

func mapSpendControl(details *SpendControlStatusDetails) *SpendControlLimitSnapshot {
	if details == nil || details.IndividualLimit == nil {
		return nil
	}
	return &SpendControlLimitSnapshot{
		Limit:            details.IndividualLimit.Limit,
		Used:             details.IndividualLimit.Used,
		RemainingPercent: details.IndividualLimit.RemainingPercent,
		ResetsAt:         details.IndividualLimit.ResetsAt,
	}
}

func mapRateLimitReachedType(value *RateLimitReachedType) *RateLimitReachedKind {
	if value == nil {
		return nil
	}
	switch value.Type {
	case RateLimitReached, WorkspaceOwnerCreditsDepleted, WorkspaceMemberCreditsDepleted, WorkspaceOwnerUsageLimitReached, WorkspaceMemberUsageLimitReached:
		kind := value.Type
		return &kind
	default:
		return nil
	}
}

func windowMinutesFromSeconds(seconds int64) *int64 {
	if seconds <= 0 {
		return nil
	}
	minutes := (seconds + 59) / 60
	return &minutes
}

func int64Ptr(value int64) *int64 {
	return &value
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
