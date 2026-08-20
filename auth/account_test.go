package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountFromAuth(t *testing.T) {
	apiKey := AccountFromAuth(&AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"})
	if apiKey == nil || apiKey.Type != AccountAPIKey {
		t.Fatalf("apiKey account = %+v", apiKey)
	}

	chatgpt := AccountFromAuth(&AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"email":     "me@example.com",
			"plan_type": "plus",
		},
	})
	if chatgpt == nil || chatgpt.Type != AccountChatGPT || chatgpt.Email == nil || *chatgpt.Email != "me@example.com" || chatgpt.PlanType != PlanPlus {
		t.Fatalf("chatgpt account = %+v", chatgpt)
	}

	ent26 := AccountFromAuth(&AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token": fakeJWTAccount(map[string]any{
				"email": "enterprise@example.com",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_plan_type": "ent26",
				},
			}),
		},
	})
	if ent26 == nil || ent26.PlanType != PlanEnt26 || !ent26.PlanType.IsBusinessLike() || !ent26.PlanType.IsWorkspaceAccount() {
		t.Fatalf("ent26 account = %+v", ent26)
	}

	external := AccountFromAuth(&AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens: map[string]any{
			"access_token": fakeJWTAccount(map[string]any{
				"email":              "external@example.com",
				"plan_type":          "pro",
				"chatgpt_account_id": "account-1",
			}),
		},
	})
	if external == nil || external.Type != AccountChatGPT || external.Email == nil || *external.Email != "external@example.com" || external.PlanType != PlanPro {
		t.Fatalf("external chatgpt account = %+v", external)
	}

	bedrock := AccountFromAuth(&AuthDotJSON{
		AuthMode:      "bedrock-api-key",
		BedrockAPIKey: &BedrockAPIKeyAuth{APIKey: "bedrock-key", Region: "us-east-1"},
	})
	if bedrock == nil || bedrock.Type != AccountAmazonBedrock || !bedrock.UsesCodexManagedCredentials {
		t.Fatalf("bedrock account = %+v", bedrock)
	}

	pat := AccountFromAuth(&AuthDotJSON{
		AuthMode: "personal-access-token",
		Tokens: map[string]any{
			"email":                      nil,
			"chatgpt_user_id":            "user-1",
			"chatgpt_account_id":         "account-1",
			"chatgpt_plan_type":          "pro",
			"chatgpt_account_is_fedramp": false,
		},
	})
	if pat == nil || pat.Type != AccountChatGPT || pat.Email != nil || pat.PlanType != PlanPro {
		t.Fatalf("personal access token account = %+v", pat)
	}

	agentEmail := "agent@example.com"
	agent := AccountFromAuth(&AuthDotJSON{
		AuthMode: "agent-identity",
		AgentIdentity: map[string]any{
			"agent_runtime_id":           "runtime-1",
			"agent_private_key":          "private-key",
			"account_id":                 "account-agent",
			"chatgpt_user_id":            "user-agent",
			"email":                      agentEmail,
			"plan_type":                  "team",
			"chatgpt_account_is_fedramp": true,
		},
	})
	if agent == nil || agent.Type != AccountChatGPT || agent.Email == nil || *agent.Email != agentEmail || agent.PlanType != PlanTeam {
		t.Fatalf("agent identity account = %+v", agent)
	}

	agentJWT := AccountFromAuth(&AuthDotJSON{
		AuthMode:      "agent-identity",
		AgentIdentity: fakeJWTAccount(map[string]any{"email": "jwt-agent@example.com", "plan_type": "enterprise", "chatgpt_account_id": "account-jwt"}),
	})
	if agentJWT == nil || agentJWT.Type != AccountChatGPT || agentJWT.Email == nil || *agentJWT.Email != "jwt-agent@example.com" || agentJWT.PlanType != PlanEnterprise {
		t.Fatalf("agent identity jwt account = %+v", agentJWT)
	}
}

func TestPlanTypeClassificationsMatchRust(t *testing.T) {
	if !PlanBusiness.IsBusinessLike() || !PlanEnt26.IsBusinessLike() || !PlanEnterpriseCBPAutomation.IsBusinessLike() || !PlanEnterpriseCBPUsageBased.IsBusinessLike() {
		t.Fatal("business-like workspace plans must be classified together")
	}
	if PlanTeam.IsBusinessLike() || PlanEnterprise.IsBusinessLike() {
		t.Fatal("team and legacy enterprise plans are not business-like")
	}
	for _, plan := range []PlanType{PlanEdu, PlanEduPlus, PlanEduPro} {
		if !plan.IsEducationLike() {
			t.Fatalf("plan %q should be education-like", plan)
		}
	}
	if PlanEnterprise.IsEducationLike() || PlanTeam.IsEducationLike() {
		t.Fatal("enterprise and team plans are not education-like")
	}
	for _, plan := range []PlanType{PlanTeam, PlanSelfServeBusinessProlite, PlanSelfServeBusinessUsageBased} {
		if !plan.IsTeamLike() {
			t.Fatalf("plan %q should be team-like", plan)
		}
	}
	if PlanBusiness.IsTeamLike() || PlanProlite.IsTeamLike() {
		t.Fatal("enterprise and individual Pro Lite plans are not team-like")
	}
	for _, plan := range []PlanType{PlanTeam, PlanSelfServeBusinessProlite, PlanSelfServeBusinessUsageBased, PlanBusiness, PlanEnt26, PlanEnterpriseCBPAutomation, PlanEnterpriseCBPUsageBased, PlanEnterprise, PlanEdu, PlanEduPlus, PlanEduPro} {
		if !plan.IsWorkspaceAccount() {
			t.Fatalf("plan %q should be a workspace account", plan)
		}
	}
	if PlanPlus.IsWorkspaceAccount() || PlanUnknown.IsWorkspaceAccount() {
		t.Fatal("individual or unknown plans must not be workspace accounts")
	}
}

func TestPlanFromStringParsesEducationVariants(t *testing.T) {
	for input, want := range map[string]PlanType{
		"edu":       PlanEdu,
		"edu_plus":  PlanEduPlus,
		"edu_pro":   PlanEduPro,
		"education": PlanUnknown,
	} {
		if got := planFromString(input); got != want {
			t.Fatalf("planFromString(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAccountFromAuthParsesEnterpriseCBPAutomation(t *testing.T) {
	account := AccountFromAuth(&AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{"access_token": fakeJWTAccount(map[string]any{
			"email":                       "service-account@example.com",
			"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "enterprise_cbp_automation"},
		})},
	})
	if account == nil || account.PlanType != PlanEnterpriseCBPAutomation || !account.PlanType.IsBusinessLike() || !account.PlanType.IsWorkspaceAccount() {
		t.Fatalf("automation account = %+v", account)
	}
	encoded, err := json.Marshal(&AccountUpdatedNotification{PlanType: &account.PlanType})
	if err != nil {
		t.Fatalf("Marshal notification: %v", err)
	}
	if !strings.Contains(string(encoded), `"planType":"enterprise_cbp_automation"`) {
		t.Fatalf("notification JSON = %s", encoded)
	}
}

func TestAccountFromAuthParsesSelfServeBusinessProlite(t *testing.T) {
	account := AccountFromAuth(&AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"email":     "business@example.com",
			"plan_type": "self_serve_business_prolite",
		},
	})
	if account == nil || account.PlanType != PlanSelfServeBusinessProlite {
		t.Fatalf("account = %+v", account)
	}
	if !account.PlanType.IsTeamLike() || !account.PlanType.IsWorkspaceAccount() {
		t.Fatalf("plan classification = team-like %t, workspace %t", account.PlanType.IsTeamLike(), account.PlanType.IsWorkspaceAccount())
	}

	encoded, err := json.Marshal(&AccountUpdatedNotification{PlanType: &account.PlanType})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"planType":"self_serve_business_prolite"`) {
		t.Fatalf("notification = %s", encoded)
	}
}

func TestAccountIDFromAuthForRestrictionsAgentIdentity(t *testing.T) {
	fromRecord := AccountIDFromAuthForRestrictions(&AuthDotJSON{
		AuthMode: "agent-identity",
		AgentIdentity: map[string]any{
			"account_id":      "workspace-record",
			"chatgpt_user_id": "user-record",
		},
	})
	if fromRecord != "workspace-record" {
		t.Fatalf("record account id = %q", fromRecord)
	}

	fromJWT := AccountIDFromAuthForRestrictions(&AuthDotJSON{
		AuthMode:      "agent-identity",
		AgentIdentity: fakeJWTAccount(map[string]any{"chatgpt_account_id": "workspace-jwt"}),
	})
	if fromJWT != "workspace-jwt" {
		t.Fatalf("jwt account id = %q", fromJWT)
	}
}

func TestLoadPersonalAccessTokenMetadata(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/user-auth-credential/whoami" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at-test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"email":                      nil,
			"chatgpt_user_id":            "user-123",
			"chatgpt_account_id":         "account-123",
			"chatgpt_plan_type":          "pro",
			"chatgpt_account_is_fedramp": false,
		}); err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}))
	defer server.Close()
	t.Setenv(AuthAPIBaseURLEnv, server.URL+"/")

	metadata, err := LoadPersonalAccessTokenMetadata(context.Background(), " at-test-token ")
	if err != nil {
		t.Fatalf("LoadPersonalAccessTokenMetadata() error = %v", err)
	}
	if metadata.Email != nil || metadata.ChatGPTAccountID != "account-123" || metadata.ChatGPTPlanType != "pro" {
		t.Fatalf("metadata = %+v", metadata)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestLoadPersonalAccessTokenMetadataRejectsNonSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	t.Setenv(AuthAPIBaseURLEnv, server.URL)

	_, err := LoadPersonalAccessTokenMetadata(context.Background(), "at-test-token")
	if err == nil || !strings.Contains(err.Error(), "personal access token metadata request failed with status 401 Unauthorized") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoginCancelCompleteAndLogout(t *testing.T) {
	manager := NewAccountManager()
	if response := manager.GetAccount(&GetAccountParams{}); response.Account != nil || !response.RequiresOpenAIAuth {
		t.Fatalf("initial account = %+v", response)
	}

	apiKey, err := manager.Login(&LoginAccountParams{Type: AccountAPIKey, APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("Login(apiKey) error = %v", err)
	}
	if apiKey.Type != AccountAPIKey {
		t.Fatalf("apiKey response = %+v", apiKey)
	}
	if response := manager.GetAccount(&GetAccountParams{}); response.Account == nil || response.Account.Type != AccountAPIKey || response.RequiresOpenAIAuth {
		t.Fatalf("account after api key = %+v", response)
	}
	if updated := manager.AccountUpdated(); updated.AuthMode == nil || *updated.AuthMode != "apikey" {
		t.Fatalf("api key AccountUpdated() = %+v", updated)
	}

	manager.Logout()
	login, err := manager.Login(&LoginAccountParams{Type: AccountChatGPT})
	if err != nil {
		t.Fatalf("Login(chatgpt) error = %v", err)
	}
	if login.LoginID == "" || login.AuthURL == "" {
		t.Fatalf("chatgpt response = %+v", login)
	}
	cancel, err := manager.CancelLogin(&CancelLoginAccountParams{LoginID: login.LoginID})
	if err != nil {
		t.Fatalf("CancelLogin() error = %v", err)
	}
	if cancel.Status != CancelLoginCanceled {
		t.Fatalf("cancel status = %s, want canceled", cancel.Status)
	}
	notification := manager.CompleteLogin("login-2", &Account{Type: AccountChatGPT, PlanType: PlanPro}, "")
	if !notification.Success {
		t.Fatalf("CompleteLogin() = %+v, want success", notification)
	}
	if updated := manager.AccountUpdated(); updated.AuthMode == nil || *updated.AuthMode != "chatgpt" || updated.PlanType == nil || *updated.PlanType != PlanPro {
		t.Fatalf("AccountUpdated() = %+v", updated)
	}
}

func TestLoginAccountResponseMarshalRustUnionShape(t *testing.T) {
	cases := []struct {
		name string
		in   *LoginAccountResponse
		want string
	}{
		{
			name: "api key",
			in:   &LoginAccountResponse{Type: AccountAPIKey},
			want: `{"type":"apiKey"}`,
		},
		{
			name: "chatgpt",
			in:   &LoginAccountResponse{Type: AccountChatGPT, LoginID: "login-1", AuthURL: "https://chatgpt.com/codex/login"},
			want: `{"type":"chatgpt","loginId":"login-1","authUrl":"https://chatgpt.com/codex/login"}`,
		},
		{
			name: "device code",
			in:   &LoginAccountResponse{Type: "chatgptDeviceCode", LoginID: "login-2", VerificationURL: "https://chatgpt.com/activate", UserCode: "CODEX-0002"},
			want: `{"type":"chatgptDeviceCode","loginId":"login-2","verificationUrl":"https://chatgpt.com/activate","userCode":"CODEX-0002"}`,
		},
		{
			name: "external tokens",
			in:   &LoginAccountResponse{Type: "chatgptAuthTokens"},
			want: `{"type":"chatgptAuthTokens"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(data) != tc.want {
				t.Fatalf("JSON = %s, want %s", data, tc.want)
			}
		})
	}
}

func TestGetAccountResponseMarshalRustUnionShape(t *testing.T) {
	email := "user@example.com"
	cases := []struct {
		name string
		in   *GetAccountResponse
		want string
	}{
		{
			name: "none requiring openai auth",
			in:   &GetAccountResponse{RequiresOpenAIAuth: true},
			want: `{"account":null,"requiresOpenaiAuth":true}`,
		},
		{
			name: "api key",
			in:   &GetAccountResponse{Account: &Account{Type: AccountAPIKey}, RequiresOpenAIAuth: true},
			want: `{"account":{"type":"apiKey"},"requiresOpenaiAuth":true}`,
		},
		{
			name: "chatgpt with email",
			in:   &GetAccountResponse{Account: &Account{Type: AccountChatGPT, Email: &email, PlanType: PlanPro}, RequiresOpenAIAuth: true},
			want: `{"account":{"type":"chatgpt","email":"user@example.com","planType":"pro"},"requiresOpenaiAuth":true}`,
		},
		{
			name: "chatgpt without email",
			in:   &GetAccountResponse{Account: &Account{Type: AccountChatGPT, PlanType: PlanEnterprise}, RequiresOpenAIAuth: true},
			want: `{"account":{"type":"chatgpt","email":null,"planType":"enterprise"},"requiresOpenaiAuth":true}`,
		},
		{
			name: "chatgpt ent26",
			in:   &GetAccountResponse{Account: &Account{Type: AccountChatGPT, PlanType: PlanEnt26}, RequiresOpenAIAuth: true},
			want: `{"account":{"type":"chatgpt","email":null,"planType":"ent26"},"requiresOpenaiAuth":true}`,
		},
		{
			name: "chatgpt missing plan",
			in:   &GetAccountResponse{Account: &Account{Type: AccountChatGPT, Email: &email}, RequiresOpenAIAuth: true},
			want: `{"account":{"type":"chatgpt","email":"user@example.com","planType":"unknown"},"requiresOpenaiAuth":true}`,
		},
		{
			name: "amazon bedrock",
			in:   &GetAccountResponse{Account: &Account{Type: AccountAmazonBedrock, UsesCodexManagedCredentials: true}},
			want: `{"account":{"type":"amazonBedrock","usesCodexManagedCredentials":true},"requiresOpenaiAuth":false}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(data) != tc.want {
				t.Fatalf("JSON = %s, want %s", data, tc.want)
			}
		})
	}
}

func TestLoginAccountParamsMarshalRustUnionShape(t *testing.T) {
	plan := "pro"
	cases := []struct {
		name string
		in   *LoginAccountParams
		want string
	}{
		{
			name: "api key",
			in:   &LoginAccountParams{Type: AccountAPIKey, APIKey: "sk-test", AccessToken: "legacy-hidden"},
			want: `{"type":"apiKey","apiKey":"sk-test"}`,
		},
		{
			name: "chatgpt default",
			in:   &LoginAccountParams{Type: AccountChatGPT},
			want: `{"type":"chatgpt"}`,
		},
		{
			name: "chatgpt streamlined",
			in:   &LoginAccountParams{Type: AccountChatGPT, CodexStreamlinedLogin: true},
			want: `{"type":"chatgpt","codexStreamlinedLogin":true}`,
		},
		{
			name: "device code",
			in:   &LoginAccountParams{Type: "chatgptDeviceCode", AccessToken: "legacy-hidden"},
			want: `{"type":"chatgptDeviceCode"}`,
		},
		{
			name: "external tokens nullable plan",
			in:   &LoginAccountParams{Type: "chatgptAuthTokens", AccessToken: "tok", ChatGPTAccountID: "account-1"},
			want: `{"type":"chatgptAuthTokens","accessToken":"tok","chatgptAccountId":"account-1","chatgptPlanType":null}`,
		},
		{
			name: "external tokens plan",
			in:   &LoginAccountParams{Type: "chatgptAuthTokens", AccessToken: "tok", ChatGPTAccountID: "account-1", ChatGPTPlanType: &plan},
			want: `{"type":"chatgptAuthTokens","accessToken":"tok","chatgptAccountId":"account-1","chatgptPlanType":"pro"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(data) != tc.want {
				t.Fatalf("JSON = %s, want %s", data, tc.want)
			}
		})
	}
}

func TestLoginValidation(t *testing.T) {
	manager := NewAccountManager()
	_, err := manager.Login(nil)
	if !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("Login(nil) error = %v, want ErrInvalidAccountRequest", err)
	}
	_, err = manager.Login(&LoginAccountParams{Type: AccountAPIKey})
	if !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("Login(apiKey empty) error = %v, want ErrInvalidAccountRequest", err)
	}
	_, err = manager.Login(&LoginAccountParams{Type: "chatgptAuthTokens", AccessToken: "tok"})
	if !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("Login(tokens missing account) error = %v, want ErrInvalidAccountRequest", err)
	}
	if _, err = manager.CancelLogin(nil); !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("CancelLogin(nil) error = %v, want ErrInvalidAccountRequest", err)
	}
}

func TestChatGPTAuthTokensLoginUsesExternalMode(t *testing.T) {
	manager := NewAccountManager()
	plan := "pro"
	response, err := manager.Login(&LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      fakeJWTAccount(map[string]any{"email": "me@example.com", "plan_type": "plus"}),
		ChatGPTAccountID: "account-1",
		ChatGPTPlanType:  &plan,
	})
	if err != nil {
		t.Fatalf("Login(chatgptAuthTokens) error = %v", err)
	}
	if response.Type != "chatgptAuthTokens" {
		t.Fatalf("response = %+v", response)
	}
	read := manager.GetAccount(&GetAccountParams{RefreshToken: true})
	if read.Account == nil || read.Account.Type != AccountChatGPT || read.Account.Email == nil || *read.Account.Email != "me@example.com" || read.Account.PlanType != PlanPro || !read.RequiresOpenAIAuth {
		t.Fatalf("account = %+v", read)
	}
	updated := manager.AccountUpdated()
	if updated.AuthMode == nil || *updated.AuthMode != "chatgptAuthTokens" || updated.PlanType == nil || *updated.PlanType != PlanPro {
		t.Fatalf("updated = %+v", updated)
	}
}

func TestAccountManagerAuthSnapshotTracksExternalTokensAndClonesLikeRust(t *testing.T) {
	manager := NewAccountManager()
	plan := "pro"
	if _, err := manager.Login(&LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      "access-token",
		ChatGPTAccountID: "account-123",
		ChatGPTPlanType:  &plan,
	}); err != nil {
		t.Fatalf("Login(chatgptAuthTokens) error = %v", err)
	}

	snapshot := manager.AuthSnapshot()
	if snapshot == nil || snapshot.Mode() != "chatgptAuthTokens" || snapshot.Tokens["access_token"] != "access-token" || snapshot.Tokens["account_id"] != "account-123" {
		t.Fatalf("auth snapshot = %#v", snapshot)
	}
	snapshot.Tokens["access_token"] = "mutated"
	if got := manager.AuthSnapshot(); got == nil || got.Tokens["access_token"] != "access-token" {
		t.Fatalf("stored auth snapshot was mutated through clone: %#v", got)
	}

	manager.Logout()
	if got := manager.AuthSnapshot(); got != nil {
		t.Fatalf("auth snapshot after logout = %#v, want nil", got)
	}
}

func TestApplyAuthSnapshotUsesRustWireAuthMode(t *testing.T) {
	manager := NewAccountManager()
	manager.ApplyAuthSnapshot(&AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "sk-rust"})
	updated := manager.AccountUpdated()
	if updated.AuthMode == nil || *updated.AuthMode != "apikey" {
		t.Fatalf("api key updated = %+v", updated)
	}
	read := manager.GetAccount(&GetAccountParams{})
	if read.Account == nil || read.Account.Type != AccountAPIKey || read.RequiresOpenAIAuth {
		t.Fatalf("api key account = %+v", read)
	}

	manager.ApplyAuthSnapshot(&AuthDotJSON{
		AuthMode: "agent-identity",
		AgentIdentity: map[string]any{
			"account_id":      "account-agent",
			"chatgpt_user_id": "user-agent",
		},
	})
	updated = manager.AccountUpdated()
	if updated.AuthMode == nil || *updated.AuthMode != "agentIdentity" {
		t.Fatalf("agent identity updated = %+v", updated)
	}
}

func TestCancelActiveLogins(t *testing.T) {
	manager := NewAccountManager()
	login, err := manager.Login(&LoginAccountParams{Type: AccountChatGPT})
	if err != nil {
		t.Fatalf("Login(chatgpt) error = %v", err)
	}
	manager.CancelActiveLogins()
	cancel, err := manager.CancelLogin(&CancelLoginAccountParams{LoginID: login.LoginID})
	if err != nil {
		t.Fatalf("CancelLogin() error = %v", err)
	}
	if cancel.Status != CancelLoginNotFound {
		t.Fatalf("cancel status = %s, want notFound", cancel.Status)
	}
}

func TestRateLimitsAndResetCredit(t *testing.T) {
	manager := NewAccountManager()
	window := &RateLimitWindow{UsedPercent: 90}
	plan := PlanPlus
	manager.SetRateLimits(
		RateLimitSnapshot{Primary: window, PlanType: &plan},
		map[string]RateLimitSnapshot{"codex": {Primary: &RateLimitWindow{UsedPercent: 80}}},
		&RateLimitResetCreditsSummary{AvailableCount: 1},
	)
	read := manager.RateLimits()
	if read.RateLimits.Primary == nil || read.RateLimits.Primary.UsedPercent != 90 {
		t.Fatalf("RateLimits() = %+v", read)
	}
	if read.RateLimitsByLimitID["codex"].Primary.UsedPercent != 80 {
		t.Fatalf("RateLimitsByLimitID = %+v", read.RateLimitsByLimitID)
	}
	if _, err := manager.ConsumeResetCredit(nil); !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("ConsumeResetCredit(nil) error = %v, want ErrInvalidAccountRequest", err)
	}
	consume, err := manager.ConsumeResetCredit(&ConsumeRateLimitResetCreditParams{IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("ConsumeResetCredit() error = %v", err)
	}
	if consume.Outcome != ResetCreditOutcomeReset {
		t.Fatalf("outcome = %s, want reset", consume.Outcome)
	}
	read = manager.RateLimits()
	if read.RateLimits.Primary.UsedPercent != 0 || read.RateLimitResetCredits == nil || read.RateLimitResetCredits.AvailableCount != 0 {
		t.Fatalf("RateLimits() after reset = %+v", read)
	}
	consume, err = manager.ConsumeResetCredit(&ConsumeRateLimitResetCreditParams{IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("ConsumeResetCredit(second) error = %v", err)
	}
	if consume.Outcome != ResetCreditOutcomeAlreadyRedeemed {
		t.Fatalf("second outcome = %s, want alreadyRedeemed", consume.Outcome)
	}
}

func TestRateLimitsMarshalRustNullableResetCredits(t *testing.T) {
	manager := NewAccountManager()
	manager.SetRateLimits(RateLimitSnapshot{Primary: &RateLimitWindow{UsedPercent: 43}}, nil, nil)
	read := manager.RateLimits()
	if read.RateLimitResetCredits != nil {
		t.Fatalf("RateLimitResetCredits = %+v, want nil", read.RateLimitResetCredits)
	}
	data, err := json.Marshal(read)
	if err != nil {
		t.Fatalf("Marshal rate limits error = %v", err)
	}
	if !strings.Contains(string(data), `"rateLimitResetCredits":null`) || !strings.Contains(string(data), `"rateLimitsByLimitId":null`) || !strings.Contains(string(data), `"usedPercent":43`) {
		t.Fatalf("rate limits JSON = %s", data)
	}
}

func TestUsageWorkspaceMessagesAndSessionsClone(t *testing.T) {
	manager := NewAccountManager()
	nilUsage := manager.TokenUsage()
	if nilUsage.DailyUsageBuckets != nil {
		t.Fatalf("default daily usage buckets = %#v, want nil", nilUsage.DailyUsageBuckets)
	}
	data, err := json.Marshal(nilUsage)
	if err != nil {
		t.Fatalf("Marshal usage error = %v", err)
	}
	if !strings.Contains(string(data), `"dailyUsageBuckets":null`) {
		t.Fatalf("daily usage buckets JSON = %s, want null", data)
	}

	lifetime := int64(100)
	manager.SetTokenUsage(GetAccountTokenUsageResponse{
		Summary:           AccountTokenUsageSummary{LifetimeTokens: &lifetime},
		DailyUsageBuckets: []AccountTokenUsageDailyBucket{{StartDate: "2026-06-29", Tokens: 10}},
	})
	usage := manager.TokenUsage()
	*usage.Summary.LifetimeTokens = 1
	if manager.TokenUsage().Summary.LifetimeTokens == nil || *manager.TokenUsage().Summary.LifetimeTokens != 100 {
		t.Fatalf("usage clone leaked mutation")
	}

	manager.SetTokenUsage(GetAccountTokenUsageResponse{DailyUsageBuckets: []AccountTokenUsageDailyBucket{}})
	data, err = json.Marshal(manager.TokenUsage())
	if err != nil {
		t.Fatalf("Marshal empty usage error = %v", err)
	}
	if !strings.Contains(string(data), `"dailyUsageBuckets":[]`) {
		t.Fatalf("empty daily usage buckets JSON = %s, want []", data)
	}

	created := int64(10)
	manager.SetWorkspaceMessages(GetWorkspaceMessagesResponse{
		FeatureEnabled: true,
		Messages: []WorkspaceMessage{{
			MessageID:   "m1",
			MessageType: WorkspaceMessageHeadline,
			MessageBody: "hello",
			CreatedAt:   &created,
		}},
	})
	messages := manager.WorkspaceMessages()
	*messages.Messages[0].CreatedAt = 20
	if *manager.WorkspaceMessages().Messages[0].CreatedAt != 10 {
		t.Fatalf("workspace message clone leaked mutation")
	}

	active := "session-1"
	email := "me@example.com"
	kind := SessionWorkspacePersonal
	manager.SetSessions(&active, []Session{{
		SessionID: "session-1",
		Email:     &email,
		IsActive:  true,
		Workspaces: []SessionWorkspace{{
			AccountID: "account-1",
			Kind:      &kind,
		}},
	}})
	sessions := manager.Sessions()
	*sessions.Sessions[0].Email = "other@example.com"
	if *manager.Sessions().Sessions[0].Email != "me@example.com" {
		t.Fatalf("session clone leaked mutation")
	}
}

func TestAccountSessionsSwitchAndLogout(t *testing.T) {
	manager := NewAccountManager()
	active := "session-1"
	personal := SessionWorkspacePersonal
	workspace := SessionWorkspaceWorkspace
	manager.SetSessions(&active, []Session{
		{
			SessionID:                  "session-1",
			IsActive:                   true,
			SelectedWorkspaceAccountID: stringPtrIfNotEmpty("account-1"),
			Workspaces: []SessionWorkspace{{
				AccountID: "account-1",
				Kind:      &personal,
			}},
		},
		{
			SessionID: "session-2",
			Workspaces: []SessionWorkspace{{
				AccountID: "account-2",
				Kind:      &workspace,
			}},
		},
	})

	switched, err := manager.SwitchSession(&AccountSessionsSwitchParams{SessionID: "session-2", AccountID: "account-2"})
	if err != nil {
		t.Fatalf("SwitchSession() error = %v", err)
	}
	if switched.ActiveSessionID == nil || *switched.ActiveSessionID != "session-2" {
		t.Fatalf("active session after switch = %+v", switched.ActiveSessionID)
	}
	if switched.Sessions[0].IsActive || !switched.Sessions[1].IsActive {
		t.Fatalf("session active flags after switch = %+v", switched.Sessions)
	}
	if switched.Sessions[1].SelectedWorkspaceAccountID == nil || *switched.Sessions[1].SelectedWorkspaceAccountID != "account-2" {
		t.Fatalf("selected workspace after switch = %+v", switched.Sessions[1].SelectedWorkspaceAccountID)
	}
	if switched.Sessions[1].LastUsedAt == 0 {
		t.Fatalf("LastUsedAt was not refreshed on switch")
	}

	if _, err := manager.SwitchSession(&AccountSessionsSwitchParams{SessionID: "session-2", AccountID: "missing"}); !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("missing workspace switch error = %v, want ErrInvalidAccountRequest", err)
	}
	if _, err := manager.LogoutSession(&AccountSessionsLogoutParams{}); !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("empty logout session error = %v, want ErrInvalidAccountRequest", err)
	}

	loggedOut, err := manager.LogoutSession(&AccountSessionsLogoutParams{SessionID: "session-2"})
	if err != nil {
		t.Fatalf("LogoutSession() error = %v", err)
	}
	if loggedOut.ActiveSessionID == nil || *loggedOut.ActiveSessionID != "session-1" || len(loggedOut.Sessions) != 1 || !loggedOut.Sessions[0].IsActive {
		t.Fatalf("sessions after active logout = %+v", loggedOut)
	}

	empty, err := manager.LogoutSession(&AccountSessionsLogoutParams{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("LogoutSession last session error = %v", err)
	}
	if empty.ActiveSessionID != nil || len(empty.Sessions) != 0 {
		t.Fatalf("sessions after last logout = %+v", empty)
	}
}

func TestAccountManagerZeroValueIsUsable(t *testing.T) {
	var manager AccountManager
	account := manager.GetAccount(nil)
	if account == nil || !account.RequiresOpenAIAuth {
		t.Fatalf("zero value account = %+v", account)
	}
	login, err := manager.Login(&LoginAccountParams{Type: AccountChatGPT})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if login.LoginID == "" {
		t.Fatalf("Login() = %+v", login)
	}
	canceled, err := manager.CancelLogin(&CancelLoginAccountParams{LoginID: login.LoginID})
	if err != nil || canceled.Status != CancelLoginCanceled {
		t.Fatalf("CancelLogin() = %+v, %v", canceled, err)
	}
	credit, err := manager.ConsumeResetCredit(&ConsumeRateLimitResetCreditParams{IdempotencyKey: "reset-1"})
	if err != nil || credit.Outcome != ResetCreditOutcomeNoCredit {
		t.Fatalf("ConsumeResetCredit() = %+v, %v", credit, err)
	}
	active := "session-1"
	kind := SessionWorkspacePersonal
	manager.SetSessions(&active, []Session{{
		SessionID: "session-1",
		Workspaces: []SessionWorkspace{{
			AccountID: "account-1",
			Kind:      &kind,
		}},
	}})
	switched, err := manager.SwitchSession(&AccountSessionsSwitchParams{SessionID: "session-1", AccountID: "account-1"})
	if err != nil {
		t.Fatalf("SwitchSession() error = %v", err)
	}
	if switched.ActiveSessionID == nil || *switched.ActiveSessionID != "session-1" || !switched.Sessions[0].IsActive {
		t.Fatalf("sessions after switch = %+v", switched)
	}
}

func TestAccountSessionsNilParams(t *testing.T) {
	manager := NewAccountManager()
	if _, err := manager.LogoutSession(nil); !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("nil logout error = %v, want ErrInvalidAccountRequest", err)
	}
	if _, err := manager.SwitchSession(nil); !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("nil switch error = %v, want ErrInvalidAccountRequest", err)
	}
	if response := manager.ListSessions(nil); response == nil || len(response.Sessions) != 0 {
		t.Fatalf("ListSessions(nil) = %+v", response)
	}
	if response, err := manager.AddSession(nil); err != nil || response == nil {
		t.Fatalf("AddSession(nil) = %+v, %v", response, err)
	}
}

func TestNudgeValidation(t *testing.T) {
	if err := (&SendAddCreditsNudgeEmailParams{CreditType: AddCreditsNudgeCredits}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := (&SendAddCreditsNudgeEmailParams{CreditType: "bad"}).Validate(); !errors.Is(err, ErrInvalidAccountRequest) {
		t.Fatalf("bad credit type error = %v, want ErrInvalidAccountRequest", err)
	}
}

func fakeJWTAccount(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}
