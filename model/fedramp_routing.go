package model

// Credential-dependent endpoint defaults.
//
// Rust parity: model-provider/src/auth.rs::uses_fedramp_api_key and
// provider.rs::resolve_api_provider (#57178). API keys with a FedRAMP issuance
// marker route to government hosts only when configuration did not select a
// base URL; the complete key still authenticates unchanged.

import (
	"strings"

	"codex_go/auth"
)

const (
	// FedRAMPAPIBaseURL is the default OpenAI API endpoint for FedRAMP keys.
	FedRAMPAPIBaseURL = "https://gov.api.openai.com/v1"
	// FedRAMPCodexBaseURL serves Codex model metadata for FedRAMP keys.
	FedRAMPCodexBaseURL = "https://gov.chatgpt.com/backend-api/codex"
)

// UsesFedrampAPIKey reports whether the credential selected for this provider
// enables OpenAI's default FedRAMP endpoints. Command-backed providers keep
// their configured routing across token refreshes.
func UsesFedrampAPIKey(provider ProviderInfo, snapshot *auth.AuthDotJSON) (bool, error) {
	if !provider.IsOpenAI() || provider.HasCommandAuth() {
		return false, nil
	}
	// An explicit provider credential takes precedence over ambient login.
	if key, err := bearerTokenForProvider(provider); err != nil {
		return false, err
	} else if key != "" {
		return auth.IsFedrampAPIKey(key), nil
	}
	return provider.RequiresOpenAIAuth && snapshot != nil && snapshot.Mode() == "api-key" && auth.IsFedrampAPIKey(snapshot.OpenAIAPIKey), nil
}

// ResolveAPIProvider applies credential-dependent defaults without changing a
// configured base URL, including an explicitly configured commercial default.
func ResolveAPIProvider(provider ProviderInfo, snapshot *auth.AuthDotJSON) (APIProvider, error) {
	authMode := ""
	if snapshot != nil {
		authMode = snapshot.BackendMode()
	}
	resolved, err := provider.ToAPIProvider(authMode)
	if err != nil {
		return APIProvider{}, err
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		fedramp, err := UsesFedrampAPIKey(provider, snapshot)
		if err != nil {
			return APIProvider{}, err
		}
		if fedramp {
			resolved.BaseURL = FedRAMPAPIBaseURL
		}
	}
	return resolved, nil
}

// ModelDiscoveryBaseURL returns the Codex backend URL serving API-key model
// metadata for this credential: the government host for FedRAMP keys.
func ModelDiscoveryBaseURL(provider ProviderInfo, snapshot *auth.AuthDotJSON) (string, error) {
	fedramp, err := UsesFedrampAPIKey(provider, snapshot)
	if err != nil {
		return "", err
	}
	if fedramp {
		return FedRAMPCodexBaseURL, nil
	}
	return ChatGPTCodexBaseURL, nil
}
