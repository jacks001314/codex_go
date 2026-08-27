package mcp

import (
	"net/http"
	"strings"

	"codex_go/auth"
	"codex_go/model"
)

func RuntimeAuthFromSnapshot(snapshot *auth.AuthDotJSON) *RuntimeAuth {
	if snapshot == nil || !RuntimeAuthUsesCodexBackend(snapshot.Mode()) {
		return nil
	}
	resolved, err := model.AuthHeadersFromAuth(*snapshot)
	if err != nil {
		return nil
	}
	headers := map[string]string{}
	for name, values := range resolved.Headers {
		if len(values) > 0 {
			headers[name] = values[len(values)-1]
		}
	}
	runtimeAuth := &RuntimeAuth{
		UsesCodexBackend: true,
		HTTPHeaders:      headers,
	}
	if resolved.SignRequest != nil {
		runtimeAuth.ApplyHTTPRequest = func(request *http.Request, body []byte) error {
			return resolved.Apply(request.Context(), request, body)
		}
	}
	return runtimeAuth
}

// ServiceTrustedAccessFromSnapshot builds a TrustedAccessContext from the
// ChatGPT auth snapshot (Rust TrustedAccessContext::new, #40992/#41005). It
// returns nil for non-ChatGPT auth so entitlement metadata is never attached
// without a verifiable account identity.
func ServiceTrustedAccessFromSnapshot(snapshot *auth.AuthDotJSON, chatgptBaseURL string, httpClient HTTPDoer) *TrustedAccessContext {
	if snapshot == nil || !RuntimeAuthUsesCodexBackend(snapshot.Mode()) {
		return nil
	}
	if strings.TrimSpace(chatgptBaseURL) == "" || httpClient == nil {
		return nil
	}
	account := &TrustedAccessAccount{
		AccountID:        auth.AccountIDFromAuthForRestrictions(snapshot),
		ChatGPTUserID:    auth.ChatGPTUserIDFromAuth(snapshot),
		UsesCodexBackend: true,
	}
	if accountEnv := auth.AccountFromAuth(snapshot); accountEnv != nil {
		account.Workspace = accountEnv.PlanType.IsWorkspaceAccount()
	}
	account.FedRAMP = boolFromAuthTokens(snapshot.Tokens, "chatgpt_account_is_fedramp", "is_fedramp_account")
	authApply := func(request *http.Request) error {
		resolved, err := model.AuthHeadersFromAuth(*snapshot)
		if err != nil {
			return err
		}
		for name, values := range resolved.Headers {
			if len(values) > 0 {
				request.Header.Set(name, values[len(values)-1])
			}
		}
		if resolved.SignRequest != nil {
			return resolved.Apply(request.Context(), request, nil)
		}
		return nil
	}
	return &TrustedAccessContext{
		ChatGPTBaseURL: chatgptBaseURL,
		HTTPDoer:       httpClient.Do,
		ApplyAuth:      authApply,
		Account:        account,
	}
}

func boolFromAuthTokens(tokens map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := tokens[key].(bool); ok {
			return value
		}
	}
	return false
}
