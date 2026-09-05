package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/model"
	"codex_go/network"
	"codex_go/prompt"
	"codex_go/realtime"
	"codex_go/session"
	"codex_go/turn"
)

func (r *RuntimeRouter) startRealtimeConversationAsync(params realtime.StartParams) {
	r.enqueueRealtimeOperation(params.ThreadID, func(ctx context.Context) {
		options, err := r.realtimeStartOptions(&params)
		if err == nil {
			if options == nil {
				options = &realtime.StartOptions{}
			}
			options.Context = ctx
			_, notifications, startErr := r.requireRealtime().StartWithOptions(&params, options)
			err = startErr
			if err == nil {
				r.notifyRealtime(notifications)
				return
			}
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		r.notifyRealtime([]realtime.Notification{{
			Method: realtime.NotificationError,
			Params: realtime.ErrorNotification{ThreadID: strings.TrimSpace(params.ThreadID), Message: err.Error()},
		}})
	})
}

const realtimeOperationQueueCapacity = 256

func (r *RuntimeRouter) enqueueRealtimeOperation(threadID string, operation func(context.Context)) bool {
	ctx, queue, ok := r.realtimeOperationQueue(threadID, operation)
	if !ok {
		return false
	}
	select {
	case queue <- operation:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *RuntimeRouter) tryEnqueueRealtimeOperation(threadID string, operation func(context.Context)) bool {
	ctx, queue, ok := r.realtimeOperationQueue(threadID, operation)
	if !ok {
		return false
	}
	select {
	case queue <- operation:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

func (r *RuntimeRouter) realtimeOperationQueue(threadID string, operation func(context.Context)) (context.Context, chan func(context.Context), bool) {
	if r == nil || operation == nil {
		return nil, nil, false
	}
	threadID = strings.TrimSpace(threadID)
	r.realtimeOpsMu.Lock()
	if r.realtimeOpsClosing {
		r.realtimeOpsMu.Unlock()
		return nil, nil, false
	}
	ctx := r.realtimeOpsCtx
	if ctx == nil {
		ctx = context.Background()
	}
	queue := r.realtimeOpsQueues[threadID]
	if queue == nil {
		queue = make(chan func(context.Context), realtimeOperationQueueCapacity)
		r.realtimeOpsQueues[threadID] = queue
		r.realtimeOpsWG.Add(1)
		go r.runRealtimeOperationQueue(ctx, queue)
	}
	r.realtimeOpsMu.Unlock()
	return ctx, queue, true
}

func (r *RuntimeRouter) runRealtimeOperationQueue(ctx context.Context, queue <-chan func(context.Context)) {
	defer r.realtimeOpsWG.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case operation := <-queue:
			if operation != nil {
				operation(ctx)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *RuntimeRouter) appendRealtimeAudioAsync(params realtime.AppendAudioParams) {
	r.tryEnqueueRealtimeOperation(params.ThreadID, func(ctx context.Context) {
		if ctx.Err() != nil {
			return
		}
		_, err := r.requireRealtime().AppendAudio(&params)
		r.notifyRealtimeOperationError(params.ThreadID, err)
	})
}

func (r *RuntimeRouter) appendRealtimeTextAsync(params realtime.AppendTextParams) {
	r.enqueueRealtimeOperation(params.ThreadID, func(ctx context.Context) {
		if ctx.Err() != nil {
			return
		}
		_, err := r.requireRealtime().AppendText(&params)
		r.notifyRealtimeOperationError(params.ThreadID, err)
	})
}

func (r *RuntimeRouter) appendRealtimeSpeechAsync(params realtime.AppendSpeechParams) {
	r.enqueueRealtimeOperation(params.ThreadID, func(ctx context.Context) {
		if ctx.Err() != nil {
			return
		}
		_, err := r.requireRealtime().AppendSpeech(&params)
		r.notifyRealtimeOperationError(params.ThreadID, err)
	})
}

func (r *RuntimeRouter) stopRealtimeConversationAsync(params realtime.StopParams) {
	r.enqueueRealtimeOperation(params.ThreadID, func(ctx context.Context) {
		if ctx.Err() != nil {
			return
		}
		_, notification, err := r.requireRealtime().Stop(&params, "requested")
		if errors.Is(err, realtime.ErrRealtimeNotRunning) {
			notification = realtime.NewClosedNotification(params.ThreadID, "requested")
			err = nil
		}
		if err != nil {
			r.notifyRealtimeOperationError(params.ThreadID, err)
			return
		}
		r.notifyRealtime([]realtime.Notification{notification})
	})
}

func (r *RuntimeRouter) notifyRealtimeOperationError(threadID string, err error) {
	if r == nil || err == nil {
		return
	}
	if errors.Is(err, realtime.ErrRealtimeNotRunning) {
		r.notify(NotificationError, &ErrorNotification{
			Error: TurnError{
				Message:        "conversation is not running",
				CodexErrorInfo: CodexErrorInfo("badRequest"),
			},
			ThreadID: strings.TrimSpace(threadID),
		})
		return
	}
	r.notifyRealtime([]realtime.Notification{{
		Method: realtime.NotificationError,
		Params: realtime.ErrorNotification{ThreadID: strings.TrimSpace(threadID), Message: err.Error()},
	}})
}

func (r *RuntimeRouter) realtimeStartOptions(params *realtime.StartParams) (*realtime.StartOptions, error) {
	if r == nil || params == nil || r.services.Config == nil {
		return nil, nil
	}
	cfg, record, err := r.effectiveRealtimeThreadConfig(params.ThreadID)
	if err != nil {
		return nil, err
	}

	turnParams := realtimeTurnStartParams(record)
	providerConfig, err := r.appTurnModelProviderConfig(cfg, turnParams)
	if err != nil {
		return nil, err
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerConfig.ProviderID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	snapshot, err := r.realtimeAuthSnapshot(providerInfo)
	if err != nil {
		return nil, err
	}

	websocketProvider, err := providerInfo.ToAPIProvider("api-key")
	if err != nil {
		return nil, err
	}
	if baseURL := stringConfigValue(cfg, "experimental_realtime_ws_base_url"); baseURL != "" {
		websocketProvider.BaseURL = baseURL
	}
	sidebandBaseURL := "https://api.openai.com/v1"
	if baseURL := stringConfigValue(cfg, "experimental_realtime_ws_base_url"); baseURL != "" {
		sidebandBaseURL = baseURL
	}

	runtimeProvider := model.CreateRuntimeProviderForID(providerConfig.ProviderID, *providerInfo, snapshot)
	callProvider, err := runtimeProvider.APIProvider()
	if err != nil {
		return nil, err
	}
	if baseURL := stringConfigValue(cfg, "experimental_realtime_webrtc_call_base_url"); baseURL != "" {
		callProvider, err = providerInfo.ToAPIProvider("api-key")
		if err != nil {
			return nil, err
		}
		callProvider.BaseURL = baseURL
	}
	callAuth, err := runtimeProvider.APIAuth()
	if err != nil {
		return nil, err
	}

	headers := websocketProvider.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	commonHeaders := realtimeThreadHeaders(record, params.ThreadID)
	mergeHTTPHeaders(headers, commonHeaders)
	transportType := "websocket"
	if params.Transport != nil && strings.TrimSpace(params.Transport.Type) != "" {
		transportType = strings.TrimSpace(params.Transport.Type)
	}
	if transportType == "websocket" {
		apiKey, keyErr := realtimeAPIKey(snapshot, providerInfo)
		if keyErr != nil {
			return nil, keyErr
		}
		headers.Set("Authorization", "Bearer "+apiKey)
	} else {
		headers.Del("Authorization")
	}
	callHeaders := callProvider.Headers.Clone()
	if callHeaders == nil {
		callHeaders = make(http.Header)
	}
	mergeHTTPHeaders(callHeaders, commonHeaders)
	mergeHTTPHeaders(callHeaders, callAuth.Headers)

	client, ok := r.httpClientForConfig(cfg).(*http.Client)
	if !ok || client == nil {
		client = network.NewHTTPClient(cfg.RespectSystemProxyEnabled(), 0)
	}
	backend := &realtime.TransportBackendConfig{
		WebsocketBaseURL:  websocketProvider.BaseURL,
		WebRTCCallBaseURL: callProvider.BaseURL,
		SidebandBaseURL:   sidebandBaseURL,
		HTTPClient:        client,
		Headers:           headers,
		CallHeaders:       callHeaders,
		QueryParams:       cloneRealtimeStringMap(websocketProvider.QueryParams),
		PrepareCall: func(ctx context.Context, request *http.Request, body []byte) error {
			return callAuth.Apply(ctx, request, body)
		},
	}

	instructions := r.realtimeInstructions(cfg, record, params)
	return &realtime.StartOptions{
		Backend:        backend,
		DefaultModel:   stringConfigValue(cfg, "experimental_realtime_ws_model"),
		DefaultVersion: realtimeVersionFromConfig(cfg),
		DefaultVoice:   realtimeVoiceFromConfig(cfg),
		SessionMode:    realtimeSessionModeFromConfig(cfg),
		Instructions:   &instructions,
	}, nil
}

func (r *RuntimeRouter) effectiveRealtimeThreadConfig(threadID string) (*config.Config, *session.Record, error) {
	threadID = strings.TrimSpace(threadID)
	var record *session.Record
	var err error
	if r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil && threadID != "" {
		record, err = r.threadRecord(session.ThreadID(threadID), false, true)
		if err != nil {
			return nil, nil, err
		}
	}
	params := realtimeTurnStartParams(record)
	if params == nil {
		params = &turn.TurnStartParams{ThreadID: threadID}
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, nil, err
	}
	return cfg, record, nil
}

func realtimeTurnStartParams(record *session.Record) *turn.TurnStartParams {
	if record == nil {
		return nil
	}
	params := &turn.TurnStartParams{
		ThreadID:   string(record.ID),
		CWD:        strings.TrimSpace(record.Metadata.CWD),
		Model:      strings.TrimSpace(record.Metadata.Model),
		Originator: strings.TrimSpace(record.Metadata.Originator),
		// Rust #40665: turns started by a realtime handoff are classified with a
		// "realtime" turn trigger in the Responses request metadata.
		TurnTrigger: "realtime",
		Config:      threadRecordConfigOverrides(record),
	}
	if providerID := strings.TrimSpace(record.Metadata.ModelProvider); providerID != "" {
		if params.Config == nil {
			params.Config = map[string]any{}
		}
		params.Config["model_provider"] = providerID
	}
	return params
}

func (r *RuntimeRouter) realtimeAuthSnapshot(_ *model.ProviderInfo) (*auth.AuthDotJSON, error) {
	resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	return &resolved.Auth, nil
}

func realtimeAPIKey(snapshot *auth.AuthDotJSON, provider *model.ProviderInfo) (string, error) {
	if provider == nil {
		return "", jsonRPCInvalidRequest("realtime conversation requires API key auth")
	}
	if key, err := provider.APIKey(); err != nil {
		return "", err
	} else if strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key), nil
	}
	if token := strings.TrimSpace(provider.ExperimentalBearerToken); token != "" {
		return token, nil
	}
	if snapshot != nil && snapshot.Mode() == "api-key" && strings.TrimSpace(snapshot.OpenAIAPIKey) != "" {
		return strings.TrimSpace(snapshot.OpenAIAPIKey), nil
	}
	if provider.IsOpenAI() {
		if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
			return key, nil
		}
	}
	return "", jsonRPCInvalidRequest("realtime conversation requires API key auth")
}

func (r *RuntimeRouter) realtimeInstructions(cfg *config.Config, record *session.Record, params *realtime.StartParams) string {
	var requestPrompt *prompt.RealtimeRequestPrompt
	if params != nil && params.Prompt.Set {
		requestPrompt = &prompt.RealtimeRequestPrompt{Set: true, Value: params.Prompt.Value}
	}
	instructions := prompt.PrepareRealtime(requestPrompt, stringConfigValue(cfg, "experimental_realtime_ws_backend_prompt"))
	if params == nil || params.IncludeStartupContext == nil || *params.IncludeStartupContext {
		if startupContext, ok := configStringValue(cfg, "experimental_realtime_ws_startup_context"); ok {
			instructions = joinRealtimeInstructions(instructions, startupContext)
		} else {
			instructions = joinRealtimeInstructions(instructions, r.buildRealtimeStartupContext(record))
		}
	}
	return instructions
}

func joinRealtimeInstructions(promptText, startupContext string) string {
	switch {
	case promptText == "":
		return startupContext
	case startupContext == "":
		return promptText
	default:
		return promptText + "\n\n" + startupContext
	}
}

func realtimeVersionFromConfig(cfg *config.Config) realtime.Version {
	value := strings.TrimSpace(realtimeConfigString(cfg, "version"))
	switch realtime.Version(value) {
	case realtime.VersionV1, realtime.VersionV2, realtime.VersionV3:
		return realtime.Version(value)
	default:
		return realtime.VersionV2
	}
}

func realtimeVoiceFromConfig(cfg *config.Config) realtime.Voice {
	return realtime.Voice(strings.TrimSpace(realtimeConfigString(cfg, "voice")))
}

func realtimeSessionModeFromConfig(cfg *config.Config) realtime.SessionMode {
	value := strings.TrimSpace(realtimeConfigString(cfg, "type"))
	if value == string(realtime.SessionModeTranscription) {
		return realtime.SessionModeTranscription
	}
	return realtime.SessionModeConversational
}

func realtimeConfigString(cfg *config.Config, key string) string {
	if cfg == nil || cfg.Values == nil {
		return ""
	}
	values, _ := cfg.Values["realtime"].(map[string]any)
	value, _ := values[key].(string)
	return value
}

func configStringValue(cfg *config.Config, key string) (string, bool) {
	if cfg == nil || cfg.Values == nil {
		return "", false
	}
	value, ok := cfg.Values[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func realtimeThreadHeaders(record *session.Record, threadID string) http.Header {
	headers := make(http.Header)
	threadID = strings.TrimSpace(threadID)
	sessionID := threadID
	originator := defaultInitializeOriginator
	threadSource := ""
	if record != nil {
		sessionID = firstNonEmpty(strings.TrimSpace(record.SessionID), sessionID)
		originator = firstNonEmpty(strings.TrimSpace(record.Metadata.Originator), originator)
		threadSource = strings.TrimSpace(record.Metadata.ThreadSource)
	}
	if sessionID != "" {
		headers.Set("session-id", sessionID)
	}
	if threadID != "" {
		headers.Set("thread-id", threadID)
	}
	if originator != "" {
		headers.Set("originator", originator)
	}
	// Rust #41250: voice calls span zero or many backing turns, so include the
	// saved thread source (header-safe ASCII JSON) up to the byte limit.
	if threadSource != "" && len(threadSource) <= realtimeThreadSourceMaxBytes {
		if metadata, ok := asciiJSONHeaderString(map[string]string{turnMetadataThreadSourceKey: threadSource}); ok {
			headers.Set(codexapi.ClientCodexTurnMetadataHeader, metadata)
		}
	}
	return headers
}

const realtimeThreadSourceMaxBytes = 256
const turnMetadataThreadSourceKey = "thread_source"

// asciiJSONHeaderString encodes value as ASCII-only JSON so it can be used as an
// HTTP header value; non-ASCII runes are escaped to \uXXXX (Rust
// to_ascii_json_string, #41250).
func asciiJSONHeaderString(value any) (string, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	for _, r := range string(data) {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String(), true
}

func mergeHTTPHeaders(destination http.Header, source http.Header) {
	for key, values := range source {
		destination.Del(key)
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func cloneRealtimeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (r *RuntimeRouter) handleRealtimeEvent(threadID string, event realtime.Event) {
	if r == nil || (event.Type != "handoff.requested" && event.Type != "transcript_tail.flush") {
		return
	}
	eventLock := r.realtimeEventLock(threadID)
	eventLock.Lock()
	defer eventLock.Unlock()
	transcriptDelta := formatRealtimeTranscript(event.ActiveTranscript)
	input := event.InputTranscript
	tailFlush := event.Type == "transcript_tail.flush"
	if tailFlush {
		input = "The user just ended their realtime session. Here is the remaining handoff/transcript tail. You probably do not have to do anything; acknowledge the handoff unless the transcript itself asks for something."
	}
	delegation := wrapRealtimeDelegation(input, transcriptDelta, tailFlush)
	if delegation == "" {
		return
	}
	if active := r.activeRuntimeTurnSnapshot(threadID); active != nil {
		_, _ = r.handleTurnSteer(requestWithInternalParams(MethodTurnSteer, turn.TurnSteerParams{
			ThreadID:       threadID,
			ExpectedTurnID: active.ID,
			Prompt:         delegation,
		}))
		return
	}
	_, _ = r.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   delegation,
	}))
}

func (r *RuntimeRouter) realtimeEventLock(threadID string) *sync.Mutex {
	threadID = strings.TrimSpace(threadID)
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	lock := r.realtimeEventLocks[threadID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.realtimeEventLocks[threadID] = lock
	}
	return lock
}

func formatRealtimeTranscript(entries []realtime.TranscriptEntry) string {
	if len(entries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, entry.Role+": "+entry.Text)
	}
	return strings.Join(lines, "\n")
}

func wrapRealtimeDelegation(input string, transcriptDelta string, transcriptTailFlush bool) string {
	if input == "" {
		input = transcriptDelta
	}
	if input == "" {
		return ""
	}
	var body strings.Builder
	body.WriteString("<realtime_delegation>\n")
	if transcriptTailFlush {
		body.WriteString("  <source>transcript_tail_flush</source>\n")
	}
	body.WriteString("  <input>")
	body.WriteString(escapeRealtimeXMLText(input))
	body.WriteString("</input>\n")
	if transcriptDelta != "" {
		body.WriteString("  <transcript_delta>")
		body.WriteString(escapeRealtimeXMLText(transcriptDelta))
		body.WriteString("</transcript_delta>\n")
	}
	body.WriteString("</realtime_delegation>")
	return body.String()
}

func escapeRealtimeXMLText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	return strings.ReplaceAll(value, ">", "&gt;")
}
