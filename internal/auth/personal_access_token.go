package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultAuthAPIBaseURL   = "https://auth.openai.com/api/accounts"
	AuthAPIBaseURLEnv       = "CODEX_AUTHAPI_BASE_URL"
	personalAccessWhoamiURL = "/v1/user-auth-credential/whoami"
)

type PersonalAccessTokenMetadata struct {
	Email                 *string `json:"email"`
	ChatGPTUserID         string  `json:"chatgpt_user_id"`
	ChatGPTAccountID      string  `json:"chatgpt_account_id"`
	ChatGPTPlanType       string  `json:"chatgpt_plan_type"`
	ChatGPTAccountFedRAMP bool    `json:"chatgpt_account_is_fedramp"`
}

func AccountFromPersonalAccessToken(ctx context.Context, accessToken string) (*Account, error) {
	metadata, err := LoadPersonalAccessTokenMetadata(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	return AccountFromPersonalAccessTokenMetadata(metadata), nil
}

func AccountFromPersonalAccessTokenMetadata(metadata *PersonalAccessTokenMetadata) *Account {
	if metadata == nil {
		return nil
	}
	return &Account{
		Type:     AccountChatGPT,
		Email:    cloneStringPtr(metadata.Email),
		PlanType: planFromString(metadata.ChatGPTPlanType),
	}
}

func LoadPersonalAccessTokenMetadata(ctx context.Context, accessToken string) (*PersonalAccessTokenMetadata, error) {
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return nil, fmt.Errorf("personal access token is empty")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	endpoint := personalAccessTokenWhoamiEndpoint(authAPIBaseURLFromEnv())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to request personal access token metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("personal access token metadata request failed with status %s", response.Status)
	}
	var metadata PersonalAccessTokenMetadata
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode personal access token metadata: %w", err)
	}
	return &metadata, nil
}

func authAPIBaseURLFromEnv() string {
	baseURL := strings.TrimSpace(os.Getenv(AuthAPIBaseURLEnv))
	if baseURL == "" {
		return DefaultAuthAPIBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func personalAccessTokenWhoamiEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAuthAPIBaseURL
	}
	return baseURL + personalAccessWhoamiURL
}
