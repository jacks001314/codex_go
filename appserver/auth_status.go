package appserver

import (
	"context"
	"strings"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
)

func (r *RuntimeRouter) currentAuthStatus(params *AuthStatusParams) (*AuthStatusResponse, error) {
	if params == nil {
		params = &AuthStatusParams{}
	}
	requiresOpenAIAuth := r.requiresOpenAIAuthForStatus()
	if !requiresOpenAIAuth {
		return &AuthStatusResponse{
			Authenticated:      false,
			RequiresOpenAIAuth: &requiresOpenAIAuth,
		}, nil
	}
	codexHome := r.codexHomeForRollout()
	resolved, err := r.resolveAuthWithLoginRestrictions(codexHome)
	if err != nil {
		return nil, err
	}
	if resolved == nil || (&resolved.Auth).Mode() == "unknown" {
		return &AuthStatusResponse{
			Authenticated:      false,
			RequiresOpenAIAuth: &requiresOpenAIAuth,
		}, nil
	}
	if params.RefreshToken != nil && *params.RefreshToken {
		refreshed, _ := r.refreshManagedAuthForStatus(context.Background(), &resolved.Auth)
		if refreshed != nil {
			resolved.Auth = *refreshed
		}
	}
	return authStatusFromSnapshot(&resolved.Auth, params, requiresOpenAIAuth, codexHome), nil
}

func (r *RuntimeRouter) requiresOpenAIAuthForStatus() bool {
	if r == nil || r.services.Config == nil {
		return true
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return true
	}
	providerID := strings.TrimSpace(stringFromMap(read.Config, "model_provider"))
	provider, err := model.ProviderForConfigID(read.Config, providerID, strings.TrimSpace(stringFromMap(read.Config, "openai_base_url")))
	if err != nil || provider == nil {
		return true
	}
	return provider.RequiresOpenAIAuth
}

func authStatusFromSnapshot(snapshot *auth.AuthDotJSON, params *AuthStatusParams, requiresOpenAIAuth bool, codexHome string) *AuthStatusResponse {
	mode := authStatusMode(snapshot)
	if mode == "" {
		return &AuthStatusResponse{
			Authenticated:      false,
			RequiresOpenAIAuth: &requiresOpenAIAuth,
		}
	}
	response := &AuthStatusResponse{
		AuthMethod:         &mode,
		RequiresOpenAIAuth: &requiresOpenAIAuth,
		Authenticated:      true,
		Mode:               mode,
		AccountID:          authStatusAccountID(snapshot),
	}
	if params != nil && params.IncludeToken != nil && *params.IncludeToken &&
		!auth.IsWorkloadIdentitySelected() &&
		auth.RefreshFailureForAuth(codexHome, snapshot) == nil {
		response.AuthToken = authStatusToken(snapshot)
	}
	return response
}

func (r *RuntimeRouter) refreshManagedAuthForStatus(ctx context.Context, snapshot *auth.AuthDotJSON) (*auth.AuthDotJSON, error) {
	if snapshot == nil || snapshot.Mode() != "chatgpt" {
		return nil, nil
	}
	codexHome := r.codexHomeForRollout()
	refreshed, err := auth.RefreshChatGPTTokens(ctx, &auth.RefreshChatGPTTokenOptions{
		CodexHome:    codexHome,
		RefreshToken: stringFromMap(snapshot.Tokens, "refresh_token"),
		AuthSnapshot: snapshot,
		StoreOptions: r.authStoreOptions(),
	})
	if err != nil {
		return nil, err
	}
	if refreshed != nil {
		r.requireAccount().ApplyAuthSnapshot(refreshed)
		r.noteAuthChanged()
	}
	return refreshed, nil
}

func authStatusMode(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	switch snapshot.Mode() {
	case "api-key":
		return string(AuthModeAPIKey)
	case "chatgpt":
		return string(AuthModeChatGPT)
	case "chatgptAuthTokens":
		return string(AuthModeChatGPTAuthTokens)
	case "agent-identity":
		return string(AuthModeAgentIdentity)
	case "personal-access-token":
		return string(AuthModePersonalAccessToken)
	case "bedrock-api-key":
		return string(AuthModeBedrockAPIKey)
	default:
		return ""
	}
}

func authStatusToken(snapshot *auth.AuthDotJSON) *string {
	if snapshot == nil {
		return nil
	}
	var token string
	switch snapshot.Mode() {
	case "api-key":
		token = snapshot.OpenAIAPIKey
	case "chatgpt", "chatgptAuthTokens":
		token = stringFromMap(snapshot.Tokens, "access_token")
	default:
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return &token
}

func authStatusAccountID(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	accountID := firstNonEmpty(
		strings.TrimSpace(stringFromMap(snapshot.Tokens, "account_id")),
		strings.TrimSpace(stringFromMap(snapshot.Tokens, "chatgpt_account_id")),
	)
	if accountID != "" {
		return accountID
	}
	for _, key := range []string{"access_token", "id_token"} {
		claims := auth.ChatGPTClaimsFromJWT(stringFromMap(snapshot.Tokens, key))
		if strings.TrimSpace(claims.AccountID) != "" {
			return strings.TrimSpace(claims.AccountID)
		}
	}
	return ""
}
