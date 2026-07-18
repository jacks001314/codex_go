package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultRevokeTokenURL = "https://auth.openai.com/oauth/revoke"

type revokeTokenKind string

const (
	revokeAccessToken  revokeTokenKind = "access_token"
	revokeRefreshToken revokeTokenKind = "refresh_token"
)

type revokeTokenRequest struct {
	Token         string  `json:"token"`
	TokenTypeHint string  `json:"token_type_hint"`
	ClientID      *string `json:"client_id,omitempty"`
}

func LogoutWithRevoke(ctx context.Context, codexHome string, storeOptions *StoreOptions) (bool, error) {
	store := NewStoreWithOptions(codexHome, storeOptions)
	snapshot, err := store.Load()
	if err != nil {
		snapshot = nil
	}
	_ = RevokeAuthTokens(ctx, snapshot)
	return LogoutAllStores(codexHome, storeOptions)
}

func LogoutAllStores(codexHome string, storeOptions *StoreOptions) (bool, error) {
	if storeOptions != nil && storeOptions.Mode == AuthCredentialsStoreEphemeral {
		return NewStoreWithOptions(codexHome, storeOptions).Delete()
	}
	removedEphemeral, err := NewStoreWithOptions(codexHome, &StoreOptions{Mode: AuthCredentialsStoreEphemeral}).Delete()
	if err != nil {
		return false, err
	}
	removedManaged, err := NewStoreWithOptions(codexHome, storeOptions).Delete()
	if err != nil {
		return false, err
	}
	return removedEphemeral || removedManaged, nil
}

func RevokeAuthTokens(ctx context.Context, snapshot *AuthDotJSON) error {
	token, kind, ok := revocableToken(snapshot)
	if !ok {
		return nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return revokeOAuthToken(ctx, client, revokeTokenEndpoint(), token, kind)
}

func revocableToken(snapshot *AuthDotJSON) (string, revokeTokenKind, bool) {
	if snapshot == nil || snapshot.Mode() != "chatgpt" {
		return "", "", false
	}
	if token := stringFromAny(snapshot.Tokens, "refresh_token"); token != "" {
		return token, revokeRefreshToken, true
	}
	if token := stringFromAny(snapshot.Tokens, "access_token"); token != "" {
		return token, revokeAccessToken, true
	}
	return "", "", false
}

func revokeOAuthToken(ctx context.Context, client *http.Client, endpoint string, token string, kind revokeTokenKind) error {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	requestBody := revokeTokenRequest{
		Token:         strings.TrimSpace(token),
		TokenTypeHint: string(kind),
	}
	if kind == revokeRefreshToken {
		clientID := oauthClientFromEnv()
		requestBody.ClientID = &clientID
	}
	data, err := json.Marshal(&requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("failed to revoke %s: %s", kind, response.Status)
}

func revokeTokenEndpoint() string {
	if endpoint := strings.TrimSpace(os.Getenv(RevokeTokenURLEnvOverride)); endpoint != "" {
		return endpoint
	}
	if refreshEndpoint := strings.TrimSpace(os.Getenv(RefreshTokenURLEnvOverride)); refreshEndpoint != "" {
		if endpoint := deriveRevokeTokenEndpoint(refreshEndpoint); endpoint != "" {
			return endpoint
		}
	}
	return defaultRevokeTokenURL
}

func deriveRevokeTokenEndpoint(refreshEndpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(refreshEndpoint))
	if err != nil {
		return ""
	}
	parsed.Path = "/oauth/revoke"
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	return parsed.String()
}
