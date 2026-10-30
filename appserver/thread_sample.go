package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

const (
	threadSampleTimeout      = 30 * time.Second
	threadSampleMaxPrompt    = 4000
	threadSampleMaxOutput    = 64 * 1024
	threadSampleOriginator   = "thread_sample"
	threadSampleThreadSource = "thread_sample"
)

// handleThreadSampleRuntime captures one model response with the source
// thread's configuration and history. The request is a plain model sample: no
// tools are executed and the source thread's history is never written.
func (r *RuntimeRouter) handleThreadSampleRuntime(request *Request) (*ThreadSampleResponse, error) {
	if r == nil {
		return nil, invalidParams("thread/sample is unavailable")
	}
	var params ThreadSampleParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	expectedTurnID := strings.TrimSpace(params.ExpectedTurnID)
	prompt := params.Prompt
	if expectedTurnID == "" || strings.TrimSpace(prompt) == "" || len(prompt) > threadSampleMaxPrompt {
		return nil, invalidParams(fmt.Sprintf("thread/sample requires expectedTurnId and a prompt of at most %d bytes", threadSampleMaxPrompt))
	}
	sampleBase, unregisterSample := r.registerThreadSampleCancel(request)
	defer unregisterSample()
	if r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || r.threads == nil {
		return nil, invalidParams("thread/sample requires a loaded thread store")
	}
	if !r.threads.HasLiveThread(session.ThreadID(threadID)) {
		return nil, invalidParams("sample source is not loaded")
	}
	if r.threads.ActiveTurn(threadID) != nil {
		return nil, invalidParams("sample source must be idle")
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil, invalidParams("sample source is not loaded")
	}
	if boolFromMap(record.Metadata.Extra, "ephemeral") || runtimeRecordIsSubagent(record) || strings.TrimSpace(string(record.ParentThreadID)) != "" {
		return nil, invalidParams("sample source must be a persistent root thread")
	}
	if !threadSampleLatestTurnMatches(record, expectedTurnID) {
		return nil, invalidParams("sample source turn must be the latest finished turn")
	}
	historyVersion, err := threadSampleHistoryVersion(record)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	sampleTurnID := newMemoryTurnID()
	settings := r.threadSettingsForTurn(threadID)
	paramsForTurn := &turn.TurnStartParams{
		ThreadID: threadID,
		CWD:      firstNonEmpty(threadSettingsCWD(settings), strings.TrimSpace(record.Metadata.CWD)),
		Model:    firstNonEmpty(threadSettingsModel(settings), strings.TrimSpace(record.Metadata.Model)),
		Config:   threadRecordConfigOverrides(record),
	}
	applyThreadSettingsToTurnStartParams(paramsForTurn, settings)
	cfg, err := r.effectiveConfigForTurn(paramsForTurn)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	providerConfig, err := r.appTurnModelProviderConfig(cfg, paramsForTurn)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	modelInfo := r.modelInfoForRuntimeWithConfig(providerConfig.Model, cfg)
	instructions, err := appBaseInstructionsForConfig(cfg)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	instructions = firstNonEmpty(turnBaseInstructions(paramsForTurn), instructions)
	if instructions == "" && modelInfo != nil {
		instructions = modelInfo.ModelInstructions(appPersonalityForTurn(cfg, paramsForTurn))
	}
	historyItems, _ := r.historyInputItemsForTurn(threadID)
	// Rust #57287: MCP is disabled for a sample only when the inherited window
	// carries an incremental catalog on a Responses Lite model. The sample reads
	// the source record without history, so the catalog is detected on the
	// request history that the sample inherits.
	disableMCP := params.DisableMCP && modelInfo != nil && modelInfo.UseResponsesLite && historyInputItemsHaveAdditionalTools(historyItems)
	preserveParentTools := disableMCP && features.Enabled(cfg.FeatureSettings(), "prediction_preserve_parent_tools")
	inputItems := append([]any(nil), historyItems...)
	if worldStateItems, stateErr := r.modelWorldStateInputItems(threadID, modelInfo, instructions); stateErr == nil {
		inputItems = append(inputItems, worldStateItems...)
	}
	if item, itemErr := r.managedDeveloperInstructionsInputItem(cfg); itemErr == nil && item != nil {
		inputItems = append(inputItems, item)
	}
	if preserveParentTools {
		inputItems = append(inputItems, predictionMCPUnavailableNoticeInputItem())
	}
	runContext, _ := r.buildTurnRuntimeContext(context.Background(), paramsForTurn, sampleTurnID)
	var tools []any
	if runContext != nil {
		if preserveParentTools {
			// The inherited `additional_tools` item already declares the parent
			// catalog, so no new declarations are sent (Rust #57287).
			tools = nil
		} else if disableMCP {
			tools = runContext.ModelVisibleToolDefinitionsWithoutThirdParty("", cfg != nil && cfg.DisableCodeModeInProcessFallback())
		} else {
			tools = runContext.ModelVisibleToolDefinitions("", cfg != nil && cfg.DisableCodeModeInProcessFallback())
		}
	}
	effort := appReasoningEffortForTurn(cfg, paramsForTurn)
	if params.Effort != nil {
		requested := strings.TrimSpace(*params.Effort)
		if requested != "" && modelInfo != nil && modelInfo.SupportsReasoningEffortUpdates && strings.EqualFold(providerConfig.ProviderID, model.OpenAIProviderID) {
			effort = requested
		}
	}
	requestTrace := r.requestSpanTrace(request)
	// Rust #56364: callers may override the ephemeral fork's thread source;
	// thread_sample remains the default. The thread source and turn trigger are
	// carried inside the turn-metadata document, which is also what the request
	// body's client_metadata copy uses.
	threadSource := threadSampleThreadSource
	if params.ThreadSource != nil {
		if override := strings.TrimSpace(*params.ThreadSource); override != "" {
			threadSource = override
		}
	}
	turnTrigger := ""
	if params.TurnTrigger != nil {
		turnTrigger = strings.TrimSpace(*params.TurnTrigger)
	}
	clientMetadata := turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
		SessionID:            threadID,
		ThreadID:             threadID,
		TurnID:               sampleTurnID,
		RequestKind:          codexapi.ClientRequestTurn,
		ThreadSource:         threadSource,
		TurnTrigger:          turnTrigger,
		CodexVersion:         appServerVersion(),
		Model:                providerConfig.Model,
		ReasoningEffort:      effort,
		ResponsesAPIMetadata: params.ResponsesAPIClientMetadata,
	})
	interruptOnCancel := threadSampleInterruptOnCancel(cfg, &params)
	agentRequest := &model.AgentRequest{
		Prompt:            prompt,
		InterruptOnCancel: interruptOnCancel,
		Instructions:      instructions,
		InputItems:        inputItems,
		Tools:             tools,
		Model:             providerConfig.Model,
		ProviderID:        providerConfig.ProviderID,
		ThreadID:          threadID,
		TurnID:            sampleTurnID,
		Originator:        threadSampleOriginator,
		ReasoningEffort:   effort,
		Trace:             requestTrace,
		Ephemeral:         true,
		ClientMetadata:    clientMetadata,
	}
	agent := r.requireAgentForTurn(paramsForTurn)
	sampleContext, cancel := context.WithTimeout(sampleBase, threadSampleTimeout)
	defer cancel()
	response, err := agent.Run(sampleContext, agentRequest)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(sampleContext.Err(), context.Canceled) {
			return nil, fmt.Errorf("sample canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(sampleContext.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("sample timed out")
		}
		return nil, err
	}
	if response == nil || strings.TrimSpace(response.ResponseID) == "" {
		return nil, fmt.Errorf("sample completed without a response")
	}
	current, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || current == nil {
		return nil, invalidParams("sample source changed")
	}
	currentVersion, err := threadSampleHistoryVersion(current)
	if err != nil || currentVersion != historyVersion {
		return nil, invalidParams("sample source changed")
	}
	text, err := threadSampleResponseText(response)
	if err != nil {
		return nil, err
	}
	result := &ThreadSampleResponse{
		SourceTurnID: expectedTurnID,
		ResponseID:   strings.TrimSpace(response.ResponseID),
		Text:         text,
	}
	if response.Usage.InputTokens != 0 || response.Usage.CachedInputTokens != 0 || response.Usage.OutputTokens != 0 {
		inputTokens := response.Usage.InputTokens
		cachedTokens := response.Usage.CachedInputTokens
		outputTokens := response.Usage.OutputTokens
		result.InputTokens = &inputTokens
		result.CachedInputTokens = &cachedTokens
		result.OutputTokens = &outputTokens
	}
	return result, nil
}

// threadSampleInterruptOnCancel resolves Rust #57864's per-sample override:
// `interrupt_on_cancel` follows the parent configuration unless the request
// sets it explicitly.
func threadSampleInterruptOnCancel(cfg *config.Config, params *ThreadSampleParams) bool {
	enabled := cfg != nil && features.Enabled(cfg.FeatureSettings(), "interrupt_on_cancel")
	if params != nil && params.InterruptOnCancel != nil {
		return *params.InterruptOnCancel
	}
	return enabled
}

func (r *RuntimeRouter) registerThreadSampleCancel(request *Request) (context.Context, func()) {
	base, cancel := context.WithCancel(context.Background())
	if r == nil || request == nil {
		return base, func() {}
	}
	key := threadSampleCancelKey(request.ConnectionID, request.ID)
	r.threadSampleMu.Lock()
	if r.threadSampleCancels == nil {
		r.threadSampleCancels = map[string]context.CancelFunc{}
	}
	r.threadSampleCancels[key] = cancel
	r.threadSampleMu.Unlock()
	return base, func() {
		cancel()
		r.threadSampleMu.Lock()
		delete(r.threadSampleCancels, key)
		r.threadSampleMu.Unlock()
	}
}

func threadSampleCancelKey(connectionID string, requestID RequestID) string {
	encoded, _ := json.Marshal(requestID)
	return normalizeConnectionID(connectionID) + "\x00" + string(encoded)
}

func (r *RuntimeRouter) handleThreadSampleCancelRuntime(request *Request) (*ThreadSampleCancelResponse, error) {
	var params ThreadSampleCancelParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	key := threadSampleCancelKey(request.ConnectionID, params.RequestID)
	r.threadSampleMu.Lock()
	cancel := r.threadSampleCancels[key]
	r.threadSampleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Unknown or repeated cancellations are harmless no-ops.
	return &ThreadSampleCancelResponse{}, nil
}

func (r *RuntimeRouter) cancelThreadSamplesForConnection(connectionID string) {
	if r == nil {
		return
	}
	prefix := normalizeConnectionID(connectionID) + "\x00"
	r.threadSampleMu.Lock()
	var cancels []context.CancelFunc
	for key, cancel := range r.threadSampleCancels {
		if strings.HasPrefix(key, prefix) {
			cancels = append(cancels, cancel)
			delete(r.threadSampleCancels, key)
		}
	}
	r.threadSampleMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func threadSampleLatestTurnMatches(record *session.Record, expectedTurnID string) bool {
	if record == nil {
		return false
	}
	turns := record.Metadata.RolloutTurns
	if len(turns) == 0 {
		return false
	}
	latest := turns[len(turns)-1]
	if strings.TrimSpace(latest.ID) != expectedTurnID {
		return false
	}
	// Rust #56020: completed, failed, and interrupted are all finished turns.
	switch strings.ToLower(strings.TrimSpace(latest.Status)) {
	case "completed", "failed", "interrupted":
		return true
	default:
		return false
	}
}

func threadSampleHistoryVersion(record *session.Record) (string, error) {
	if record == nil {
		return "", errors.New("sample source is unavailable")
	}
	data, err := json.Marshal(record.Items)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// threadSampleResponseText accepts only assistant text. Reasoning is ignored;
// any tool call or other output item makes the sample text null, matching the
// Rust interceptor's text-only contract.
func threadSampleResponseText(response *model.AgentResponse) (*string, error) {
	if response == nil {
		return nil, nil
	}
	var builder strings.Builder
	for _, item := range response.Items {
		switch item.Type {
		case "reasoning":
			continue
		case "agent_message":
			if phase, _ := item.Data["phase"].(string); strings.EqualFold(strings.TrimSpace(phase), "commentary") {
				continue
			}
			if builder.Len()+len(item.Text) > threadSampleMaxOutput {
				return nil, fmt.Errorf("sample output exceeds limit")
			}
			builder.WriteString(item.Text)
		default:
			return nil, nil
		}
	}
	text := builder.String()
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return &text, nil
}
