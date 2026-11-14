package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/prompt"
	"codex_go/realtime"
)

// realtimeCallTimeout bounds the client-owned call negotiation. Rust's call
// creation is part of the voice start attempt, so a provider that never answers
// must fail the attempt instead of holding the helper open.
const realtimeCallTimeout = 30 * time.Second

// clientOwnedVoiceCall negotiates the TUI's realtime WebRTC call with this
// process's provider and credentials, then returns the start request that
// attaches the app-server through existingCall (Rust #61172). The returned
// answer is the provider's SDP answer the helper applies.
func clientOwnedVoiceCall(codexHome string, cwd func() string, client *http.Client) func(context.Context, string, string, VoiceSettings) (realtime.StartParams, string, error) {
	return func(ctx context.Context, threadID string, offerSDP string, settings VoiceSettings) (realtime.StartParams, string, error) {
		return negotiateVoiceOwnedCall(ctx, codexHome, cwd, client, threadID, offerSDP, settings)
	}
}

// negotiateVoiceOwnedCall mirrors the app-server's realtime start preparation
// with the caller's local configuration: the TUI resolves the provider and
// credential itself, creates the call, and hands the call id to the observer.
func negotiateVoiceOwnedCall(
	ctx context.Context,
	codexHome string,
	cwd func() string,
	client *http.Client,
	threadID string,
	offerSDP string,
	settings VoiceSettings,
) (realtime.StartParams, string, error) {
	var empty realtime.StartParams
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return empty, "", errors.New("start a conversation before using voice mode")
	}
	if strings.TrimSpace(offerSDP) == "" {
		return empty, "", errors.New("voice helper did not produce an SDP offer")
	}
	if settings.VoiceErr != "" {
		return empty, "", errors.New(settings.VoiceErr)
	}
	workingDir := ""
	if cwd != nil {
		workingDir = strings.TrimSpace(cwd())
	}
	loaded, err := config.LoadWithOptions(codexHome, &config.LoadOptions{CWD: workingDir})
	if err != nil {
		return empty, "", fmt.Errorf("load realtime provider configuration: %w", err)
	}
	providerID := voiceConfigString(loaded.Values, "model_provider")
	providerInfo, err := model.ProviderForConfigID(loaded.Values, providerID, voiceConfigString(loaded.Values, "openai_base_url"))
	if err != nil {
		return empty, "", err
	}
	resolved, err := auth.NewStoreWithOptions(codexHome, auth.StoreOptionsFromConfig(
		loaded.CLIAuthCredentialsStoreMode(), loaded.SecretAuthStorageEnabled(),
	)).Resolve()
	if err != nil {
		return empty, "", fmt.Errorf("load realtime credentials: %w", err)
	}
	if resolved == nil {
		return empty, "", errors.New("realtime voice needs a signed-in account or API key")
	}
	residency := ""
	if loaded.Requirements != nil && loaded.Requirements.EnforceResidency != nil {
		residency = string(*loaded.Requirements.EnforceResidency)
	}
	runtimeProvider := model.CreateRuntimeProviderWithResidency(firstNonEmptyLocal(providerID, model.OpenAIProviderID), *providerInfo, &resolved.Auth, residency)
	callProvider, err := runtimeProvider.APIProvider()
	if err != nil {
		return empty, "", err
	}
	callAuth, err := runtimeProvider.APIAuth()
	if err != nil {
		return empty, "", err
	}
	if baseURL := voiceConfigString(loaded.Values, "experimental_realtime_webrtc_call_base_url"); baseURL != "" {
		callProvider.BaseURL = baseURL
	}

	headers := callProvider.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("session-id", threadID)
	headers.Set("thread-id", threadID)
	mergeHTTPHeadersLocal(headers, callAuth.Headers)

	if client == nil {
		client = &http.Client{Timeout: realtimeCallTimeout}
	}
	session := &realtime.SessionConfig{
		ThreadID:              threadID,
		RealtimeSessionID:     threadID,
		Model:                 firstNonEmptyLocal(strings.TrimSpace(settings.Model), voiceConfigString(loaded.Values, "experimental_realtime_ws_model")),
		OutputModality:        realtime.OutputAudio,
		Version:               realtime.VersionV3,
		ClientManagedHandoffs: true,
		// The TUI sends no startup context, so the call carries the shared
		// realtime prompt the app-server would otherwise prepare.
		Prompt:    prompt.PrepareRealtime(nil, voiceConfigString(loaded.Values, "experimental_realtime_ws_backend_prompt")),
		Transport: *realtime.WebRTCTransport(offerSDP),
	}
	if settings.VoiceSet && realtime.IsKnownVoice(realtime.Voice(strings.TrimSpace(settings.Voice))) {
		session.Voice = realtime.Voice(strings.TrimSpace(settings.Voice))
	}
	backend := &realtime.TransportBackendConfig{
		WebRTCCallBaseURL: callProvider.BaseURL,
		HTTPClient:        client,
		CallHeaders:       headers,
		PrepareCall: func(ctx context.Context, request *http.Request, body []byte) error {
			return callAuth.Apply(ctx, request, body)
		},
	}
	answer, callID, err := realtime.CreateWebRTCCall(ctx, backend, session)
	if err != nil {
		return empty, "", err
	}
	clientManagedHandoffs := true
	includeStartupContext := false
	version := realtime.VersionV3
	realtimeSessionID := threadID
	params := realtime.StartParams{
		ThreadID:              threadID,
		ClientManagedHandoffs: &clientManagedHandoffs,
		OutputModality:        realtime.OutputAudio,
		IncludeStartupContext: &includeStartupContext,
		RealtimeSessionID:     &realtimeSessionID,
		Transport:             realtime.ExistingCallTransport(callID),
		Version:               &version,
	}
	if err := params.Validate(); err != nil {
		return empty, "", err
	}
	return params, answer, nil
}

// voiceConfigString reads one top-level string configuration value.
func voiceConfigString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
