package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrInvalidAccountRequest = errors.New("invalid account request")

type PlanType string

const (
	PlanUnknown                     PlanType = "unknown"
	PlanFree                        PlanType = "free"
	PlanGo                          PlanType = "go"
	PlanPlus                        PlanType = "plus"
	PlanPro                         PlanType = "pro"
	PlanProlite                     PlanType = "prolite"
	PlanTeam                        PlanType = "team"
	PlanSelfServeBusinessUsageBased PlanType = "self_serve_business_usage_based"
	PlanBusiness                    PlanType = "business"
	PlanEnterpriseCBPUsageBased     PlanType = "enterprise_cbp_usage_based"
	PlanEnterprise                  PlanType = "enterprise"
	PlanEdu                         PlanType = "edu"
)

type BedrockCredentialSource string

const (
	BedrockCredentialSourceAWSManaged   BedrockCredentialSource = "awsManaged"
	BedrockCredentialSourceCodexManaged BedrockCredentialSource = "codexManaged"
)

type AccountType string

const (
	AccountAPIKey        AccountType = "apiKey"
	AccountChatGPT       AccountType = "chatgpt"
	AccountAmazonBedrock AccountType = "amazonBedrock"
)

type Account struct {
	Type             AccountType             `json:"type"`
	Email            *string                 `json:"email,omitempty"`
	PlanType         PlanType                `json:"planType,omitempty"`
	CredentialSource BedrockCredentialSource `json:"credentialSource,omitempty"`
}

func (a *Account) MarshalJSON() ([]byte, error) {
	switch a.Type {
	case AccountAPIKey:
		return json.Marshal(struct {
			Type AccountType `json:"type"`
		}{Type: AccountAPIKey})
	case AccountChatGPT:
		plan := a.PlanType
		if plan == "" {
			plan = PlanUnknown
		}
		return json.Marshal(struct {
			Type     AccountType `json:"type"`
			Email    *string     `json:"email"`
			PlanType PlanType    `json:"planType"`
		}{Type: AccountChatGPT, Email: a.Email, PlanType: plan})
	case AccountAmazonBedrock:
		source := a.CredentialSource
		if source == "" {
			source = BedrockCredentialSourceAWSManaged
		}
		return json.Marshal(struct {
			Type             AccountType             `json:"type"`
			CredentialSource BedrockCredentialSource `json:"credentialSource"`
		}{Type: AccountAmazonBedrock, CredentialSource: source})
	default:
		type accountAlias Account
		return json.Marshal(accountAlias(*a))
	}
}

func AccountFromAuth(snapshot *AuthDotJSON) *Account {
	if snapshot == nil {
		return nil
	}
	switch snapshot.Mode() {
	case "api-key":
		return &Account{Type: AccountAPIKey}
	case "chatgpt", "chatgptAuthTokens":
		claims := ChatGPTClaimsFromJWT(stringFromAny(snapshot.Tokens, "access_token"))
		email := firstNonEmptyAccount(
			stringFromAny(snapshot.Tokens, "email"),
			claims.Email,
		)
		plan := firstNonEmptyAccount(
			stringFromAny(snapshot.Tokens, "plan_type"),
			stringFromAny(snapshot.Tokens, "chatgpt_plan_type"),
			claims.PlanType,
		)
		return &Account{
			Type:     AccountChatGPT,
			Email:    stringPtrIfNotEmpty(email),
			PlanType: planFromString(plan),
		}
	case "bedrock-api-key":
		return &Account{Type: AccountAmazonBedrock, CredentialSource: BedrockCredentialSourceCodexManaged}
	case "personal-access-token":
		return AccountFromPersonalAccessTokenMetadata(personalAccessTokenMetadataFromAuth(snapshot))
	case "agent-identity":
		return accountFromAgentIdentity(snapshot.AgentIdentity)
	default:
		return nil
	}
}

func AccountIDFromAuthForRestrictions(snapshot *AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	accountID := firstNonEmptyAccount(
		stringFromAny(snapshot.Tokens, "account_id"),
		stringFromAny(snapshot.Tokens, "chatgpt_account_id"),
		stringFromAny(snapshot.Tokens, "accountId"),
		stringFromAny(snapshot.Tokens, "chatgptAccountId"),
	)
	if accountID != "" {
		return strings.TrimSpace(accountID)
	}
	if snapshot.Mode() == "agent-identity" {
		accountID = accountIDFromAgentIdentity(snapshot.AgentIdentity)
		if accountID != "" {
			return accountID
		}
	}
	for _, key := range []string{"id_token", "access_token"} {
		claims := ChatGPTClaimsFromJWT(stringFromAny(snapshot.Tokens, key))
		if strings.TrimSpace(claims.AccountID) != "" {
			return strings.TrimSpace(claims.AccountID)
		}
	}
	return ""
}

func accountIDFromAgentIdentity(value any) string {
	record := agentIdentityRecordFromAny(value)
	if record != nil {
		return strings.TrimSpace(record.AccountID)
	}
	switch identity := value.(type) {
	case string:
		return strings.TrimSpace(ChatGPTClaimsFromJWT(identity).AccountID)
	default:
		return ""
	}
}

func accountFromAgentIdentity(value any) *Account {
	record := agentIdentityRecordFromAny(value)
	if record != nil {
		return &Account{
			Type:     AccountChatGPT,
			Email:    cloneStringPtr(record.Email),
			PlanType: planFromString(string(record.PlanType)),
		}
	}
	token, ok := value.(string)
	if !ok {
		return nil
	}
	claims := ChatGPTClaimsFromJWT(token)
	if strings.TrimSpace(claims.Email) == "" && strings.TrimSpace(claims.PlanType) == "" && strings.TrimSpace(claims.AccountID) == "" {
		return nil
	}
	return &Account{
		Type:     AccountChatGPT,
		Email:    stringPtrIfNotEmpty(claims.Email),
		PlanType: planFromString(claims.PlanType),
	}
}

func agentIdentityRecordFromAny(value any) *AgentIdentityAuthRecord {
	switch identity := value.(type) {
	case *AgentIdentityAuthRecord:
		return identity
	case AgentIdentityAuthRecord:
		record := identity
		return &record
	case map[string]any:
		record := &AgentIdentityAuthRecord{
			AgentRuntimeID:        firstNonEmptyAccount(stringFromAny(identity, "agent_runtime_id"), stringFromAny(identity, "agentRuntimeId")),
			AgentPrivateKey:       firstNonEmptyAccount(stringFromAny(identity, "agent_private_key"), stringFromAny(identity, "agentPrivateKey")),
			AccountID:             firstNonEmptyAccount(stringFromAny(identity, "account_id"), stringFromAny(identity, "accountId"), stringFromAny(identity, "chatgpt_account_id"), stringFromAny(identity, "chatgptAccountId")),
			ChatGPTUserID:         firstNonEmptyAccount(stringFromAny(identity, "chatgpt_user_id"), stringFromAny(identity, "chatgptUserId")),
			PlanType:              planFromString(firstNonEmptyAccount(stringFromAny(identity, "plan_type"), stringFromAny(identity, "planType"), stringFromAny(identity, "chatgpt_plan_type"), stringFromAny(identity, "chatgptPlanType"))),
			ChatGPTAccountFedRAMP: boolFromAny(identity, "chatgpt_account_is_fedramp") || boolFromAny(identity, "chatgptAccountIsFedramp"),
		}
		email := firstNonEmptyAccount(stringFromAny(identity, "email"), stringFromAny(identity, "account_email"), stringFromAny(identity, "accountEmail"))
		record.Email = stringPtrIfNotEmpty(email)
		taskID := firstNonEmptyAccount(stringFromAny(identity, "task_id"), stringFromAny(identity, "taskId"))
		record.TaskID = stringPtrIfNotEmpty(taskID)
		if strings.TrimSpace(record.AccountID) == "" && strings.TrimSpace(record.ChatGPTUserID) == "" && record.Email == nil && record.PlanType == "" {
			return nil
		}
		return record
	default:
		return nil
	}
}

func AgentIdentityRecordFromAuth(snapshot *AuthDotJSON) *AgentIdentityAuthRecord {
	if snapshot == nil || snapshot.Mode() != "agent-identity" {
		return nil
	}
	record := agentIdentityRecordFromAny(snapshot.AgentIdentity)
	if record == nil {
		return nil
	}
	clone := *record
	clone.Email = cloneStringPtr(record.Email)
	clone.TaskID = cloneStringPtr(record.TaskID)
	return &clone
}

func personalAccessTokenMetadataFromAuth(snapshot *AuthDotJSON) *PersonalAccessTokenMetadata {
	if snapshot == nil || snapshot.Tokens == nil {
		return nil
	}
	accountID := stringFromAny(snapshot.Tokens, "chatgpt_account_id")
	userID := stringFromAny(snapshot.Tokens, "chatgpt_user_id")
	planType := stringFromAny(snapshot.Tokens, "chatgpt_plan_type")
	emailValue, ok := snapshot.Tokens["email"]
	var email *string
	switch value := emailValue.(type) {
	case string:
		email = stringPtrIfNotEmpty(value)
	case *string:
		email = cloneStringPtr(value)
	case nil:
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		email = stringPtrIfNotEmpty(text)
	}
	if accountID == "" && userID == "" && planType == "" && !ok {
		return nil
	}
	return &PersonalAccessTokenMetadata{
		Email:                 email,
		ChatGPTUserID:         userID,
		ChatGPTAccountID:      accountID,
		ChatGPTPlanType:       planType,
		ChatGPTAccountFedRAMP: boolFromAny(snapshot.Tokens, "chatgpt_account_is_fedramp"),
	}
}

type LoginAccountParams struct {
	Type                  AccountType `json:"type"`
	APIKey                string      `json:"apiKey,omitempty"`
	CodexStreamlinedLogin bool        `json:"codexStreamlinedLogin,omitempty"`
	AccessToken           string      `json:"accessToken,omitempty"`
	ChatGPTAccountID      string      `json:"chatgptAccountId,omitempty"`
	ChatGPTPlanType       *string     `json:"chatgptPlanType,omitempty"`
}

func (p *LoginAccountParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	switch p.Type {
	case AccountAPIKey:
		return json.Marshal(struct {
			Type   AccountType `json:"type"`
			APIKey string      `json:"apiKey"`
		}{
			Type:   AccountAPIKey,
			APIKey: p.APIKey,
		})
	case AccountChatGPT:
		return json.Marshal(struct {
			Type                  AccountType `json:"type"`
			CodexStreamlinedLogin bool        `json:"codexStreamlinedLogin,omitempty"`
		}{
			Type:                  AccountChatGPT,
			CodexStreamlinedLogin: p.CodexStreamlinedLogin,
		})
	case "chatgptDeviceCode":
		return json.Marshal(struct {
			Type AccountType `json:"type"`
		}{Type: p.Type})
	case "chatgptAuthTokens":
		return json.Marshal(struct {
			Type             AccountType `json:"type"`
			AccessToken      string      `json:"accessToken"`
			ChatGPTAccountID string      `json:"chatgptAccountId"`
			ChatGPTPlanType  *string     `json:"chatgptPlanType"`
		}{
			Type:             p.Type,
			AccessToken:      p.AccessToken,
			ChatGPTAccountID: p.ChatGPTAccountID,
			ChatGPTPlanType:  cloneStringPtr(p.ChatGPTPlanType),
		})
	default:
		type loginAccountParamsAlias LoginAccountParams
		return json.Marshal((*loginAccountParamsAlias)(p))
	}
}

func (p *LoginAccountParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidAccountRequest)
	}
	switch p.Type {
	case AccountAPIKey:
		if strings.TrimSpace(p.APIKey) == "" {
			return fmt.Errorf("%w: apiKey is required", ErrInvalidAccountRequest)
		}
	case AccountChatGPT:
	case "chatgptDeviceCode":
	case "chatgptAuthTokens":
		if strings.TrimSpace(p.AccessToken) == "" {
			return fmt.Errorf("%w: accessToken is required", ErrInvalidAccountRequest)
		}
		if strings.TrimSpace(p.ChatGPTAccountID) == "" {
			return fmt.Errorf("%w: chatgptAccountId is required", ErrInvalidAccountRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported login type %q", ErrInvalidAccountRequest, p.Type)
	}
	return nil
}

type LoginAccountResponse struct {
	Type            AccountType `json:"type"`
	LoginID         string      `json:"loginId,omitempty"`
	AuthURL         string      `json:"authUrl,omitempty"`
	VerificationURL string      `json:"verificationUrl,omitempty"`
	UserCode        string      `json:"userCode,omitempty"`
}

type CancelLoginAccountParams struct {
	LoginID string `json:"loginId"`
}

func (p *CancelLoginAccountParams) Validate() error {
	if p == nil || strings.TrimSpace(p.LoginID) == "" {
		return fmt.Errorf("%w: loginId is required", ErrInvalidAccountRequest)
	}
	return nil
}

type CancelLoginAccountStatus string

const (
	CancelLoginCanceled CancelLoginAccountStatus = "canceled"
	CancelLoginNotFound CancelLoginAccountStatus = "notFound"
)

type CancelLoginAccountResponse struct {
	Status CancelLoginAccountStatus `json:"status"`
}

type LogoutAccountResponse struct{}

type ChatGPTAuthTokensRefreshReason string

const RefreshUnauthorized ChatGPTAuthTokensRefreshReason = "unauthorized"

type ChatGPTAuthTokensRefreshParams struct {
	Reason            ChatGPTAuthTokensRefreshReason `json:"reason"`
	PreviousAccountID *string                        `json:"previousAccountId"`
}

type ChatGPTAuthTokensRefreshResponse struct {
	AccessToken      string  `json:"accessToken"`
	ChatGPTAccountID string  `json:"chatgptAccountId"`
	ChatGPTPlanType  *string `json:"chatgptPlanType"`
}

func (r *ChatGPTAuthTokensRefreshResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AccessToken      string  `json:"accessToken"`
		ChatGPTAccountID string  `json:"chatgptAccountId"`
		ChatGPTPlanType  *string `json:"chatgptPlanType"`
	}{
		AccessToken:      r.AccessToken,
		ChatGPTAccountID: r.ChatGPTAccountID,
		ChatGPTPlanType:  cloneStringPtr(r.ChatGPTPlanType),
	})
}

type GetAccountParams struct {
	RefreshToken bool `json:"refreshToken,omitempty"`
}

type GetAccountResponse struct {
	Account            *Account `json:"account"`
	RequiresOpenAIAuth bool     `json:"requiresOpenaiAuth"`
}

type AccountUpdatedNotification struct {
	AuthMode *string   `json:"authMode"`
	PlanType *PlanType `json:"planType"`
}

type GetAccountRateLimitsResponse struct {
	RateLimits            RateLimitSnapshot             `json:"rateLimits"`
	RateLimitsByLimitID   map[string]RateLimitSnapshot  `json:"rateLimitsByLimitId"`
	RateLimitResetCredits *RateLimitResetCreditsSummary `json:"rateLimitResetCredits"`
}

func (r *GetAccountRateLimitsResponse) MarshalJSON() ([]byte, error) {
	rateLimits := RateLimitSnapshot{}
	var byLimitID map[string]RateLimitSnapshot
	var resetCredits *RateLimitResetCreditsSummary
	if r != nil {
		rateLimits = cloneRateLimitSnapshot(r.RateLimits)
		byLimitID = cloneRateLimitMap(r.RateLimitsByLimitID)
		resetCredits = cloneRateLimitResetCredits(r.RateLimitResetCredits)
	}
	return json.Marshal(struct {
		RateLimits            RateLimitSnapshot             `json:"rateLimits"`
		RateLimitsByLimitID   map[string]RateLimitSnapshot  `json:"rateLimitsByLimitId"`
		RateLimitResetCredits *RateLimitResetCreditsSummary `json:"rateLimitResetCredits"`
	}{
		RateLimits:            rateLimits,
		RateLimitsByLimitID:   byLimitID,
		RateLimitResetCredits: resetCredits,
	})
}

type RateLimitSnapshot struct {
	LimitID              *string                    `json:"limitId"`
	LimitName            *string                    `json:"limitName"`
	Primary              *RateLimitWindow           `json:"primary"`
	Secondary            *RateLimitWindow           `json:"secondary"`
	Credits              *CreditsSnapshot           `json:"credits"`
	IndividualLimit      *SpendControlLimitSnapshot `json:"individualLimit"`
	PlanType             *PlanType                  `json:"planType"`
	RateLimitReachedType *RateLimitReachedType      `json:"rateLimitReachedType"`
}

func EmptyRateLimitSnapshot() RateLimitSnapshot {
	return RateLimitSnapshot{}
}

type RateLimitReachedType string

const (
	RateLimitReached                 RateLimitReachedType = "rate_limit_reached"
	WorkspaceOwnerCreditsDepleted    RateLimitReachedType = "workspace_owner_credits_depleted"
	WorkspaceMemberCreditsDepleted   RateLimitReachedType = "workspace_member_credits_depleted"
	WorkspaceOwnerUsageLimitReached  RateLimitReachedType = "workspace_owner_usage_limit_reached"
	WorkspaceMemberUsageLimitReached RateLimitReachedType = "workspace_member_usage_limit_reached"
)

type RateLimitWindow struct {
	UsedPercent        int32  `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

type CreditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

type SpendControlLimitSnapshot struct {
	Limit            string `json:"limit"`
	Used             string `json:"used"`
	RemainingPercent int32  `json:"remainingPercent"`
	ResetsAt         int64  `json:"resetsAt"`
}

type RateLimitResetCreditsSummary struct {
	AvailableCount int64 `json:"availableCount"`
}

type AccountRateLimitsUpdatedNotification struct {
	RateLimits RateLimitSnapshot `json:"rateLimits"`
}

type ConsumeRateLimitResetCreditParams struct {
	IdempotencyKey string `json:"idempotencyKey"`
}

func (p *ConsumeRateLimitResetCreditParams) Validate() error {
	if p == nil || strings.TrimSpace(p.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotencyKey is required", ErrInvalidAccountRequest)
	}
	return nil
}

type ConsumeRateLimitResetCreditOutcome string

const (
	ResetCreditOutcomeReset           ConsumeRateLimitResetCreditOutcome = "reset"
	ResetCreditOutcomeNothingToReset  ConsumeRateLimitResetCreditOutcome = "nothingToReset"
	ResetCreditOutcomeNoCredit        ConsumeRateLimitResetCreditOutcome = "noCredit"
	ResetCreditOutcomeAlreadyRedeemed ConsumeRateLimitResetCreditOutcome = "alreadyRedeemed"
)

type ConsumeRateLimitResetCreditResponse struct {
	Outcome ConsumeRateLimitResetCreditOutcome `json:"outcome"`
}

type GetAccountTokenUsageResponse struct {
	Summary           AccountTokenUsageSummary       `json:"summary"`
	DailyUsageBuckets []AccountTokenUsageDailyBucket `json:"dailyUsageBuckets"`
}

type AccountTokenUsageSummary struct {
	LifetimeTokens        *int64 `json:"lifetimeTokens"`
	PeakDailyTokens       *int64 `json:"peakDailyTokens"`
	LongestRunningTurnSec *int64 `json:"longestRunningTurnSec"`
	CurrentStreakDays     *int64 `json:"currentStreakDays"`
	LongestStreakDays     *int64 `json:"longestStreakDays"`
}

type AccountTokenUsageDailyBucket struct {
	StartDate string `json:"startDate"`
	Tokens    int64  `json:"tokens"`
}

type WorkspaceMessageType string

const (
	WorkspaceMessageHeadline     WorkspaceMessageType = "headline"
	WorkspaceMessageAnnouncement WorkspaceMessageType = "announcement"
	WorkspaceMessageUnknown      WorkspaceMessageType = "unknown"
)

type WorkspaceMessage struct {
	MessageID   string               `json:"messageId"`
	MessageType WorkspaceMessageType `json:"messageType"`
	MessageBody string               `json:"messageBody"`
	CreatedAt   *int64               `json:"createdAt"`
	ArchivedAt  *int64               `json:"archivedAt"`
}

type GetWorkspaceMessagesResponse struct {
	FeatureEnabled bool               `json:"featureEnabled"`
	Messages       []WorkspaceMessage `json:"messages"`
}

func (r *GetWorkspaceMessagesResponse) MarshalJSON() ([]byte, error) {
	messages := append([]WorkspaceMessage(nil), r.Messages...)
	if messages == nil {
		messages = []WorkspaceMessage{}
	}
	return json.Marshal(struct {
		FeatureEnabled bool               `json:"featureEnabled"`
		Messages       []WorkspaceMessage `json:"messages"`
	}{
		FeatureEnabled: r.FeatureEnabled,
		Messages:       messages,
	})
}

type AddCreditsNudgeCreditType string

const (
	AddCreditsNudgeCredits    AddCreditsNudgeCreditType = "credits"
	AddCreditsNudgeUsageLimit AddCreditsNudgeCreditType = "usage_limit"
)

type SendAddCreditsNudgeEmailParams struct {
	CreditType AddCreditsNudgeCreditType `json:"creditType"`
}

func (p *SendAddCreditsNudgeEmailParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidAccountRequest)
	}
	switch p.CreditType {
	case AddCreditsNudgeCredits, AddCreditsNudgeUsageLimit:
		return nil
	default:
		return fmt.Errorf("%w: unsupported creditType %q", ErrInvalidAccountRequest, p.CreditType)
	}
}

type AddCreditsNudgeEmailStatus string

const (
	AddCreditsNudgeEmailSent           AddCreditsNudgeEmailStatus = "sent"
	AddCreditsNudgeEmailCooldownActive AddCreditsNudgeEmailStatus = "cooldown_active"
)

type SendAddCreditsNudgeEmailResponse struct {
	Status AddCreditsNudgeEmailStatus `json:"status"`
}

type AccountLoginCompletedNotification struct {
	LoginID *string `json:"loginId"`
	Success bool    `json:"success"`
	Error   *string `json:"error"`
}

type SessionWorkspaceKind string

const (
	SessionWorkspacePersonal  SessionWorkspaceKind = "personal"
	SessionWorkspaceWorkspace SessionWorkspaceKind = "workspace"
)

type SessionWorkspace struct {
	AccountID string                `json:"accountId"`
	Name      *string               `json:"name"`
	ImageURL  *string               `json:"imageUrl"`
	Kind      *SessionWorkspaceKind `json:"kind"`
}

type Session struct {
	SessionID                  string             `json:"sessionId"`
	Email                      *string            `json:"email"`
	UserID                     *string            `json:"userId"`
	DisplayName                *string            `json:"displayName"`
	ImageURL                   *string            `json:"imageUrl"`
	LastUsedAt                 int64              `json:"lastUsedAt"`
	IsActive                   bool               `json:"isActive"`
	SelectedWorkspaceAccountID *string            `json:"selectedWorkspaceAccountId"`
	Workspaces                 []SessionWorkspace `json:"workspaces"`
}

func (s *Session) MarshalJSON() ([]byte, error) {
	workspaces := append([]SessionWorkspace(nil), s.Workspaces...)
	if workspaces == nil {
		workspaces = []SessionWorkspace{}
	}
	return json.Marshal(struct {
		SessionID                  string             `json:"sessionId"`
		Email                      *string            `json:"email"`
		UserID                     *string            `json:"userId"`
		DisplayName                *string            `json:"displayName"`
		ImageURL                   *string            `json:"imageUrl"`
		LastUsedAt                 int64              `json:"lastUsedAt"`
		IsActive                   bool               `json:"isActive"`
		SelectedWorkspaceAccountID *string            `json:"selectedWorkspaceAccountId"`
		Workspaces                 []SessionWorkspace `json:"workspaces"`
	}{
		SessionID:                  s.SessionID,
		Email:                      s.Email,
		UserID:                     s.UserID,
		DisplayName:                s.DisplayName,
		ImageURL:                   s.ImageURL,
		LastUsedAt:                 s.LastUsedAt,
		IsActive:                   s.IsActive,
		SelectedWorkspaceAccountID: s.SelectedWorkspaceAccountID,
		Workspaces:                 workspaces,
	})
}

type SessionsResponse struct {
	ActiveSessionID *string   `json:"activeSessionId"`
	Sessions        []Session `json:"sessions"`
}

type AccountSessionsAddParams struct {
	SwitchToAddedAccount bool `json:"switchToAddedAccount"`
}

type AccountSessionsListParams struct {
	RefreshWorkspaceMetadata bool `json:"refreshWorkspaceMetadata"`
}

type AccountSessionsLogoutParams struct {
	SessionID string `json:"sessionId"`
}

func (p *AccountSessionsLogoutParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidAccountRequest)
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return fmt.Errorf("%w: sessionId is required", ErrInvalidAccountRequest)
	}
	return nil
}

type AccountSessionsSwitchParams struct {
	SessionID string `json:"sessionId"`
	AccountID string `json:"accountId"`
}

func (p *AccountSessionsSwitchParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidAccountRequest)
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return fmt.Errorf("%w: sessionId is required", ErrInvalidAccountRequest)
	}
	if strings.TrimSpace(p.AccountID) == "" {
		return fmt.Errorf("%w: accountId is required", ErrInvalidAccountRequest)
	}
	return nil
}

type AccountSessionsResponse = SessionsResponse
type AccountSession = Session
type AccountSessionWorkspace = SessionWorkspace
type AccountSessionWorkspaceKind = SessionWorkspaceKind

func (r *SessionsResponse) MarshalJSON() ([]byte, error) {
	sessions := append([]Session(nil), r.Sessions...)
	if sessions == nil {
		sessions = []Session{}
	}
	return json.Marshal(struct {
		ActiveSessionID *string   `json:"activeSessionId"`
		Sessions        []Session `json:"sessions"`
	}{
		ActiveSessionID: r.ActiveSessionID,
		Sessions:        sessions,
	})
}

type AccountManager struct {
	mu                  sync.Mutex
	account             *Account
	authMode            *string
	requiresOpenAIAuth  bool
	pendingLogins       map[string]LoginAccountParams
	loginSeq            int
	rateLimits          RateLimitSnapshot
	rateLimitsByLimitID map[string]RateLimitSnapshot
	resetCredits        *RateLimitResetCreditsSummary
	redeemedResetKeys   map[string]bool
	usage               GetAccountTokenUsageResponse
	workspaceMessages   GetWorkspaceMessagesResponse
	sessions            []Session
	activeSessionID     *string
	now                 func() time.Time
}

func NewAccountManager() *AccountManager {
	return &AccountManager{
		requiresOpenAIAuth: true,
		pendingLogins:      map[string]LoginAccountParams{},
		redeemedResetKeys:  map[string]bool{},
		workspaceMessages:  GetWorkspaceMessagesResponse{FeatureEnabled: false, Messages: []WorkspaceMessage{}},
		now:                time.Now,
	}
}

func (m *AccountManager) ensureLocked() {
	if m.pendingLogins == nil {
		m.pendingLogins = map[string]LoginAccountParams{}
	}
	if m.redeemedResetKeys == nil {
		m.redeemedResetKeys = map[string]bool{}
	}
	if m.workspaceMessages.Messages == nil {
		m.workspaceMessages.Messages = []WorkspaceMessage{}
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.account == nil && m.authMode == nil {
		m.requiresOpenAIAuth = true
	}
}

func (m *AccountManager) SetClock(clock func() time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if clock == nil {
		m.now = time.Now
		return
	}
	m.now = clock
}

func (m *AccountManager) SetAccount(account *Account, authMode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	m.account = cloneAccount(account)
	m.authMode = stringPtrIfNotEmpty(authMode)
	m.requiresOpenAIAuth = account == nil
}

func (m *AccountManager) GetAccount(params *GetAccountParams) *GetAccountResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	return &GetAccountResponse{Account: cloneAccount(m.account), RequiresOpenAIAuth: m.requiresOpenAIAuth}
}

func (m *AccountManager) AccountUpdated() *AccountUpdatedNotification {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	var plan *PlanType
	if m.account != nil && m.account.PlanType != "" {
		value := m.account.PlanType
		plan = &value
	}
	return &AccountUpdatedNotification{AuthMode: cloneStringPtr(m.authMode), PlanType: plan}
}

func (m *AccountManager) Login(params *LoginAccountParams) (*LoginAccountResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	switch params.Type {
	case AccountAPIKey:
		m.account = &Account{Type: AccountAPIKey}
		mode := wireAuthModeFromMode("api-key")
		m.authMode = &mode
		m.requiresOpenAIAuth = false
		return &LoginAccountResponse{Type: AccountAPIKey}, nil
	case AccountChatGPT:
		m.loginSeq++
		loginID := fmt.Sprintf("login-%d", m.loginSeq)
		m.pendingLogins[loginID] = *params
		return &LoginAccountResponse{
			Type:    AccountChatGPT,
			LoginID: loginID,
			AuthURL: "https://chatgpt.com/codex/login?state=" + loginID,
		}, nil
	case "chatgptDeviceCode":
		m.loginSeq++
		loginID := fmt.Sprintf("login-%d", m.loginSeq)
		m.pendingLogins[loginID] = *params
		return &LoginAccountResponse{
			Type:            "chatgptDeviceCode",
			LoginID:         loginID,
			VerificationURL: "https://chatgpt.com/activate",
			UserCode:        fmt.Sprintf("CODEX-%04d", m.loginSeq),
		}, nil
	case "chatgptAuthTokens":
		plan := PlanUnknown
		if params.ChatGPTPlanType != nil {
			plan = planFromString(*params.ChatGPTPlanType)
		}
		snapshot := FromChatGPTAuthTokens(params.AccessToken, params.ChatGPTAccountID, params.ChatGPTPlanType)
		account := AccountFromAuth(&snapshot)
		if plan == PlanUnknown && account != nil {
			plan = account.PlanType
		}
		if account == nil {
			account = &Account{Type: AccountChatGPT, PlanType: plan}
		}
		if account.PlanType == "" {
			account.PlanType = plan
		}
		m.account = account
		mode := "chatgptAuthTokens"
		m.authMode = &mode
		m.requiresOpenAIAuth = true
		return &LoginAccountResponse{Type: "chatgptAuthTokens"}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported login type %q", ErrInvalidAccountRequest, params.Type)
	}
}

func (m *AccountManager) ApplyAuthSnapshot(snapshot *AuthDotJSON) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	account := AccountFromAuth(snapshot)
	m.account = cloneAccount(account)
	if snapshot != nil && snapshot.Mode() != "unknown" {
		mode := wireAuthModeFromMode(snapshot.Mode())
		m.authMode = &mode
	} else {
		m.authMode = nil
	}
	m.requiresOpenAIAuth = account == nil || (snapshot != nil && snapshot.Mode() != "api-key")
}

func (m *AccountManager) CancelActiveLogins() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	m.pendingLogins = map[string]LoginAccountParams{}
}

func (m *AccountManager) CancelLogin(params *CancelLoginAccountParams) (*CancelLoginAccountResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if _, ok := m.pendingLogins[params.LoginID]; !ok {
		return &CancelLoginAccountResponse{Status: CancelLoginNotFound}, nil
	}
	delete(m.pendingLogins, params.LoginID)
	return &CancelLoginAccountResponse{Status: CancelLoginCanceled}, nil
}

func (m *AccountManager) CompleteLogin(loginID string, account *Account, errMessage string) *AccountLoginCompletedNotification {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	var errPtr *string
	success := strings.TrimSpace(errMessage) == ""
	if !success {
		errPtr = &errMessage
	} else {
		m.account = cloneAccount(account)
		mode := "chatgpt"
		m.authMode = &mode
		m.requiresOpenAIAuth = false
	}
	delete(m.pendingLogins, loginID)
	return &AccountLoginCompletedNotification{
		LoginID: stringPtrIfNotEmpty(loginID),
		Success: success,
		Error:   errPtr,
	}
}

func (m *AccountManager) Logout() *LogoutAccountResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	m.account = nil
	m.authMode = nil
	m.requiresOpenAIAuth = true
	return &LogoutAccountResponse{}
}

func (m *AccountManager) SetRateLimits(snapshot RateLimitSnapshot, byLimitID map[string]RateLimitSnapshot, resetCredits *RateLimitResetCreditsSummary) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	m.rateLimits = cloneRateLimitSnapshot(snapshot)
	m.rateLimitsByLimitID = cloneRateLimitMap(byLimitID)
	m.resetCredits = cloneRateLimitResetCredits(resetCredits)
}

func (m *AccountManager) RateLimits() *GetAccountRateLimitsResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	return &GetAccountRateLimitsResponse{
		RateLimits:            cloneRateLimitSnapshot(m.rateLimits),
		RateLimitsByLimitID:   cloneRateLimitMap(m.rateLimitsByLimitID),
		RateLimitResetCredits: cloneRateLimitResetCredits(m.resetCredits),
	}
}

func (m *AccountManager) RateLimitsUpdated() *AccountRateLimitsUpdatedNotification {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	return &AccountRateLimitsUpdatedNotification{RateLimits: cloneRateLimitSnapshot(m.rateLimits)}
}

func (m *AccountManager) ConsumeResetCredit(params *ConsumeRateLimitResetCreditParams) (*ConsumeRateLimitResetCreditResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	key := strings.TrimSpace(params.IdempotencyKey)
	if m.redeemedResetKeys[key] {
		return &ConsumeRateLimitResetCreditResponse{Outcome: ResetCreditOutcomeAlreadyRedeemed}, nil
	}
	if m.resetCredits == nil || m.resetCredits.AvailableCount <= 0 {
		return &ConsumeRateLimitResetCreditResponse{Outcome: ResetCreditOutcomeNoCredit}, nil
	}
	if m.rateLimits.Primary == nil && m.rateLimits.Secondary == nil {
		return &ConsumeRateLimitResetCreditResponse{Outcome: ResetCreditOutcomeNothingToReset}, nil
	}
	m.resetCredits.AvailableCount--
	m.redeemedResetKeys[key] = true
	resetWindow(m.rateLimits.Primary)
	resetWindow(m.rateLimits.Secondary)
	return &ConsumeRateLimitResetCreditResponse{Outcome: ResetCreditOutcomeReset}, nil
}

func (m *AccountManager) SetTokenUsage(usage GetAccountTokenUsageResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	m.usage = cloneUsage(usage)
}

func (m *AccountManager) TokenUsage() *GetAccountTokenUsageResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	usage := cloneUsage(m.usage)
	return &usage
}

func (m *AccountManager) SetWorkspaceMessages(response GetWorkspaceMessagesResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	m.workspaceMessages = cloneWorkspaceMessages(response)
}

func (m *AccountManager) WorkspaceMessages() *GetWorkspaceMessagesResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	response := cloneWorkspaceMessages(m.workspaceMessages)
	return &response
}

func (m *AccountManager) SetSessions(activeSessionID *string, sessions []Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	m.activeSessionID = cloneStringPtr(activeSessionID)
	m.sessions = cloneSessions(sessions)
}

func (m *AccountManager) Sessions() *SessionsResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	return m.sessionsResponseLocked()
}

func (m *AccountManager) ListSessions(params *AccountSessionsListParams) *AccountSessionsResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	return m.sessionsResponseLocked()
}

func (m *AccountManager) AddSession(params *AccountSessionsAddParams) (*AccountSessionsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	return m.sessionsResponseLocked(), nil
}

func (m *AccountManager) LogoutSession(params *AccountSessionsLogoutParams) (*AccountSessionsResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	index := m.sessionIndexLocked(sessionID)
	if index < 0 {
		return nil, fmt.Errorf("%w: account session %q not found", ErrInvalidAccountRequest, sessionID)
	}
	wasActive := m.activeSessionID != nil && *m.activeSessionID == sessionID
	m.sessions = append(m.sessions[:index], m.sessions[index+1:]...)
	if len(m.sessions) == 0 {
		m.activeSessionID = nil
		return m.sessionsResponseLocked(), nil
	}
	if wasActive || m.activeSessionID == nil || m.sessionIndexLocked(*m.activeSessionID) < 0 {
		m.activateFallbackSessionLocked()
	} else {
		m.applyActiveSessionFlagsLocked()
	}
	return m.sessionsResponseLocked(), nil
}

func (m *AccountManager) SwitchSession(params *AccountSessionsSwitchParams) (*AccountSessionsResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	accountID := strings.TrimSpace(params.AccountID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	index := m.sessionIndexLocked(sessionID)
	if index < 0 {
		return nil, fmt.Errorf("%w: account session %q not found", ErrInvalidAccountRequest, sessionID)
	}
	if !sessionHasWorkspaceAccount(&m.sessions[index], accountID) {
		return nil, fmt.Errorf("%w: account %q not found in session %q", ErrInvalidAccountRequest, accountID, sessionID)
	}
	m.sessions[index].SelectedWorkspaceAccountID = stringPtrIfNotEmpty(accountID)
	if m.now != nil {
		m.sessions[index].LastUsedAt = m.now().Unix()
	}
	m.setActiveSessionLocked(sessionID)
	return m.sessionsResponseLocked(), nil
}

func resetWindow(window *RateLimitWindow) {
	if window == nil {
		return
	}
	window.UsedPercent = 0
}

func cloneAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	clone := *account
	clone.Email = cloneStringPtr(account.Email)
	return &clone
}

func cloneRateLimitSnapshot(snapshot RateLimitSnapshot) RateLimitSnapshot {
	snapshot.LimitID = cloneStringPtr(snapshot.LimitID)
	snapshot.LimitName = cloneStringPtr(snapshot.LimitName)
	snapshot.Primary = cloneRateLimitWindow(snapshot.Primary)
	snapshot.Secondary = cloneRateLimitWindow(snapshot.Secondary)
	snapshot.Credits = cloneCredits(snapshot.Credits)
	snapshot.IndividualLimit = cloneSpend(snapshot.IndividualLimit)
	if snapshot.PlanType != nil {
		value := *snapshot.PlanType
		snapshot.PlanType = &value
	}
	if snapshot.RateLimitReachedType != nil {
		value := *snapshot.RateLimitReachedType
		snapshot.RateLimitReachedType = &value
	}
	return snapshot
}

func cloneRateLimitWindow(window *RateLimitWindow) *RateLimitWindow {
	if window == nil {
		return nil
	}
	clone := *window
	clone.WindowDurationMins = cloneInt64Ptr(window.WindowDurationMins)
	clone.ResetsAt = cloneInt64Ptr(window.ResetsAt)
	return &clone
}

func cloneCredits(credits *CreditsSnapshot) *CreditsSnapshot {
	if credits == nil {
		return nil
	}
	clone := *credits
	clone.Balance = cloneStringPtr(credits.Balance)
	return &clone
}

func cloneSpend(spend *SpendControlLimitSnapshot) *SpendControlLimitSnapshot {
	if spend == nil {
		return nil
	}
	clone := *spend
	return &clone
}

func cloneRateLimitMap(values map[string]RateLimitSnapshot) map[string]RateLimitSnapshot {
	if values == nil {
		return nil
	}
	cloned := make(map[string]RateLimitSnapshot, len(values))
	for key, value := range values {
		cloned[key] = cloneRateLimitSnapshot(value)
	}
	return cloned
}

func cloneRateLimitResetCredits(summary *RateLimitResetCreditsSummary) *RateLimitResetCreditsSummary {
	if summary == nil {
		return nil
	}
	clone := *summary
	return &clone
}

func cloneUsage(usage GetAccountTokenUsageResponse) GetAccountTokenUsageResponse {
	usage.Summary.LifetimeTokens = cloneInt64Ptr(usage.Summary.LifetimeTokens)
	usage.Summary.PeakDailyTokens = cloneInt64Ptr(usage.Summary.PeakDailyTokens)
	usage.Summary.LongestRunningTurnSec = cloneInt64Ptr(usage.Summary.LongestRunningTurnSec)
	usage.Summary.CurrentStreakDays = cloneInt64Ptr(usage.Summary.CurrentStreakDays)
	usage.Summary.LongestStreakDays = cloneInt64Ptr(usage.Summary.LongestStreakDays)
	if usage.DailyUsageBuckets != nil {
		buckets := make([]AccountTokenUsageDailyBucket, len(usage.DailyUsageBuckets))
		copy(buckets, usage.DailyUsageBuckets)
		usage.DailyUsageBuckets = buckets
	}
	return usage
}

func cloneWorkspaceMessages(response GetWorkspaceMessagesResponse) GetWorkspaceMessagesResponse {
	response.Messages = append([]WorkspaceMessage(nil), response.Messages...)
	for i := range response.Messages {
		response.Messages[i].CreatedAt = cloneInt64Ptr(response.Messages[i].CreatedAt)
		response.Messages[i].ArchivedAt = cloneInt64Ptr(response.Messages[i].ArchivedAt)
	}
	return response
}

func (m *AccountManager) sessionsResponseLocked() *SessionsResponse {
	m.applyActiveSessionFlagsLocked()
	return &SessionsResponse{
		ActiveSessionID: cloneStringPtr(m.activeSessionID),
		Sessions:        cloneSessions(m.sessions),
	}
}

func (m *AccountManager) sessionIndexLocked(sessionID string) int {
	for i := range m.sessions {
		if m.sessions[i].SessionID == sessionID {
			return i
		}
	}
	return -1
}

func (m *AccountManager) setActiveSessionLocked(sessionID string) {
	m.activeSessionID = stringPtrIfNotEmpty(sessionID)
	m.applyActiveSessionFlagsLocked()
}

func (m *AccountManager) activateFallbackSessionLocked() {
	if len(m.sessions) == 0 {
		m.activeSessionID = nil
		return
	}
	m.activeSessionID = stringPtrIfNotEmpty(m.sessions[0].SessionID)
	m.applyActiveSessionFlagsLocked()
}

func (m *AccountManager) applyActiveSessionFlagsLocked() {
	active := ""
	if m.activeSessionID != nil {
		active = strings.TrimSpace(*m.activeSessionID)
	}
	if active == "" && len(m.sessions) > 0 {
		for i := range m.sessions {
			if m.sessions[i].IsActive {
				active = strings.TrimSpace(m.sessions[i].SessionID)
				break
			}
		}
		if active != "" {
			m.activeSessionID = stringPtrIfNotEmpty(active)
		}
	}
	if active != "" && m.sessionIndexLocked(active) < 0 {
		active = ""
		m.activeSessionID = nil
	}
	for i := range m.sessions {
		m.sessions[i].IsActive = active != "" && m.sessions[i].SessionID == active
	}
}

func sessionHasWorkspaceAccount(session *Session, accountID string) bool {
	if session == nil {
		return false
	}
	for i := range session.Workspaces {
		if strings.TrimSpace(session.Workspaces[i].AccountID) == accountID {
			return true
		}
	}
	return false
}

func cloneSessions(sessions []Session) []Session {
	out := make([]Session, len(sessions))
	for i := range sessions {
		out[i] = sessions[i]
		out[i].Email = cloneStringPtr(sessions[i].Email)
		out[i].UserID = cloneStringPtr(sessions[i].UserID)
		out[i].DisplayName = cloneStringPtr(sessions[i].DisplayName)
		out[i].ImageURL = cloneStringPtr(sessions[i].ImageURL)
		out[i].SelectedWorkspaceAccountID = cloneStringPtr(sessions[i].SelectedWorkspaceAccountID)
		out[i].Workspaces = append([]SessionWorkspace(nil), sessions[i].Workspaces...)
		for j := range out[i].Workspaces {
			out[i].Workspaces[j].Name = cloneStringPtr(out[i].Workspaces[j].Name)
			out[i].Workspaces[j].ImageURL = cloneStringPtr(out[i].Workspaces[j].ImageURL)
			if out[i].Workspaces[j].Kind != nil {
				value := *out[i].Workspaces[j].Kind
				out[i].Workspaces[j].Kind = &value
			}
		}
	}
	return out
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func stringPtrIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func wireAuthModeFromMode(mode string) string {
	switch normalizedAuthMode(mode) {
	case "api-key":
		return "apikey"
	case "agent-identity":
		return "agentIdentity"
	case "personal-access-token":
		return "personalAccessToken"
	case "bedrock-api-key":
		return "bedrockApiKey"
	default:
		return strings.TrimSpace(mode)
	}
}

func stringFromAny(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func boolFromAny(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	switch value := values[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func firstNonEmptyAccount(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func planFromString(value string) PlanType {
	switch strings.TrimSpace(value) {
	case string(PlanFree):
		return PlanFree
	case string(PlanGo):
		return PlanGo
	case string(PlanPlus):
		return PlanPlus
	case string(PlanPro):
		return PlanPro
	case string(PlanProlite):
		return PlanProlite
	case string(PlanTeam):
		return PlanTeam
	case string(PlanSelfServeBusinessUsageBased):
		return PlanSelfServeBusinessUsageBased
	case string(PlanBusiness):
		return PlanBusiness
	case string(PlanEnterpriseCBPUsageBased):
		return PlanEnterpriseCBPUsageBased
	case string(PlanEnterprise):
		return PlanEnterprise
	case string(PlanEdu):
		return PlanEdu
	default:
		return PlanUnknown
	}
}
