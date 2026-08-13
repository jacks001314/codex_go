package appserver

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codex_go/agent"
	"codex_go/applypatch"
	"codex_go/apps"
	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/eventmap"
	"codex_go/execserver"
	"codex_go/features"
	"codex_go/install"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/plugin"
	promptctx "codex_go/prompt"
	"codex_go/review"
	"codex_go/rollout"
	"codex_go/runtimeutil"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/shell"
	"codex_go/skillprovider"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
	"codex_go/utils"
)

type activeRuntimeTurn struct {
	ThreadID     string
	TurnID       string
	Cancel       context.CancelFunc
	StartedAtMS  int64
	Params       *turn.TurnStartParams
	ConnectionID string
	RunConfig    *appTurnRunConfig
	SteerCount   int
}

func (r *RuntimeRouter) attributeCommandExecutionItem(item *ThreadItem) {
	if r == nil || item == nil || threadItemWireType(item) != "commandExecution" || r.services.Plugins == nil || r.services.Config == nil {
		return
	}
	command := strings.TrimSpace(threadItemCommand(item))
	cwd := strings.TrimSpace(threadItemCWD(item))
	if command == "" || cwd == "" {
		return
	}
	installed := r.services.Plugins.Installed(&plugin.PluginInstalledParams{})
	pluginIDs := make([]string, 0, len(installed.Plugins))
	for _, summary := range installed.Plugins {
		if summary.Enabled && summary.Installed && strings.TrimSpace(summary.ID) != "" {
			pluginIDs = append(pluginIDs, summary.ID)
		}
	}
	attribution := plugin.NewTrustedPluginRoots(r.services.Config.CodexHome(), pluginIDs).
		Resolve(shell.SplitCommandLine(command), cwd)
	if attribution == nil {
		return
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	item.Data["pluginId"] = attribution.PluginID
	item.Data["scriptPath"] = attribution.ScriptPath
}

func (r *RuntimeRouter) attributeSessionCommandItems(threadID string, turnID string, items []session.Item) {
	for i := range items {
		threadItem := BuildThreadItem(items[i])
		if threadItemWireType(&threadItem) != "commandExecution" {
			continue
		}
		r.attributeCommandExecutionItem(&threadItem)
		r.emitArtifactOperationForCommandItem(threadID, turnID, &threadItem)
		items[i].Data = cloneAnyMap(threadItem.Data)
	}
}

// emitArtifactOperationForCommandItem records a codex_artifact_operation
// "started" event for a trusted primary-runtime artifact marker command
// (Rust #38057).
func (r *RuntimeRouter) emitArtifactOperationForCommandItem(threadID string, turnID string, item *ThreadItem) {
	if r == nil || r.services.Analytics == nil || item == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.ArtifactOperationEventSink)
	if !ok {
		return
	}
	pluginID := ""
	if raw, ok := item.Data["pluginId"].(string); ok {
		pluginID = strings.TrimSpace(raw)
	}
	scriptPath := ""
	if raw, ok := item.Data["scriptPath"].(string); ok {
		scriptPath = strings.TrimSpace(raw)
	}
	if pluginID == "" || scriptPath == "" {
		return
	}
	command := strings.TrimSpace(threadItemCommand(item))
	if command == "" {
		return
	}
	attribution := &plugin.PluginCommandAttribution{PluginID: pluginID, ScriptPath: scriptPath}
	operation := plugin.RecognizeArtifactOperation(attribution, shell.SplitCommandLine(command))
	if operation == nil {
		return
	}
	occurredAtMS := uint64(threadItemInt64FromData(item.Data, "startedAtMs", "started_at_ms"))
	if occurredAtMS == 0 {
		occurredAtMS = uint64(time.Now().UTC().UnixMilli())
	}
	sink.TrackArtifactOperationEvent(context.Background(), telemetry.NewArtifactOperationEvent(telemetry.ArtifactOperationEventParams{
		ThreadID:            threadID,
		TurnID:              turnID,
		ItemID:              strings.TrimSpace(item.ID),
		Lifecycle:           telemetry.ArtifactOperationLifecycleStarted,
		OccurredAtMS:        occurredAtMS,
		Runtime:             telemetry.CurrentRuntimeMetadata(),
		PluginID:            pluginID,
		ScriptPath:          operation.ScriptPath,
		Skill:               operation.PluginName,
		ArtifactType:        operation.ArtifactType,
		OperationKind:       operation.OperationKind,
		ExpectedOutputCount: operation.ExpectedOutputCount,
		OutputFormat:        operation.OutputFormat,
		ExecutionBackend:    "unified_exec",
	}))
}

func appReasoningEffortForTurn(cfg *config.Config, params *turn.TurnStartParams) string {
	if effort := stringPtrValue(params.Effort); effort != "" {
		return effort
	}
	if turnStartPlanMode(params) {
		if effort := firstNonEmpty(stringConfigValue(cfg, "plan_mode_reasoning_effort"), stringConfigValue(cfg, "planModeReasoningEffort")); effort != "" {
			return effort
		}
	}
	return firstNonEmpty(stringConfigValue(cfg, "model_reasoning_effort"), stringConfigValue(cfg, "modelReasoningEffort"))
}

func activeTurnDiffKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func (r *RuntimeRouter) responsesStreamHandler(threadID string, turnID string, params *turn.TurnStartParams) model.ResponsesStreamHandler {
	if turnStartReviewRuntime(params) {
		return func(event *model.ResponsesStreamEvent) {}
	}
	state := newResponsesStreamNotificationState(turnStartPlanMode(params), turnID)
	if params != nil {
		state.experimentalRawEvents = params.ExperimentalRawEvents
	}
	if cfg, err := r.effectiveConfigForTurn(params); err == nil {
		state.applyPatchStreamingEvents = features.Enabled(cfg.FeatureSettings(), "apply_patch_streaming_events")
	}
	return func(event *model.ResponsesStreamEvent) {
		r.notifyResponsesStreamEvent(threadID, turnID, event, state)
	}
}

type responsesStreamNotificationState struct {
	toolNames                 map[string]string
	patchInputs               map[string]string
	patchFingerprints         map[string]string
	activeResponseID          string
	activeItemID              string
	activeAgentItemID         string
	activeReasoningItemID     string
	planMode                  bool
	planItemID                string
	planParser                map[string]*proposedPlanStreamParser
	planStarted               bool
	startedAgentItems         map[string]bool
	agentItemPhases           map[string]string
	experimentalRawEvents     bool
	applyPatchStreamingEvents bool
	retrying                  bool
}

func newResponsesStreamNotificationState(planMode bool, turnID string) *responsesStreamNotificationState {
	return &responsesStreamNotificationState{
		toolNames:         map[string]string{},
		patchInputs:       map[string]string{},
		patchFingerprints: map[string]string{},
		planMode:          planMode,
		planItemID:        safeIdentifier(turnID) + "-plan",
		planParser:        map[string]*proposedPlanStreamParser{},
		startedAgentItems: map[string]bool{},
		agentItemPhases:   map[string]string{},
	}
}

func (s *responsesStreamNotificationState) rememberOutputItem(event *model.ResponsesStreamEvent) {
	if s == nil || event == nil {
		return
	}
	s.rememberResponse(event)
	itemID := firstNonEmpty(event.ItemID, streamAgentItemID(event.Item), event.CallID)
	if itemID == "" {
		return
	}
	s.activeItemID = itemID
	if event.Item != nil {
		switch event.Item.Type {
		case "message", "agent_message":
			s.activeAgentItemID = itemID
		case "reasoning":
			s.activeReasoningItemID = itemID
		}
	}
}

func (s *responsesStreamNotificationState) rememberResponse(event *model.ResponsesStreamEvent) {
	if s == nil || event == nil {
		return
	}
	if responseID := strings.TrimSpace(event.ResponseID); responseID != "" {
		s.activeResponseID = responseID
	}
}

func (s *responsesStreamNotificationState) reasoningItemID(values ...string) string {
	fallback := "reasoning"
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			fallback = value
			return strings.TrimSpace(value)
		}
	}
	if s != nil {
		if strings.TrimSpace(s.activeReasoningItemID) != "" {
			return strings.TrimSpace(s.activeReasoningItemID)
		}
		if strings.TrimSpace(s.activeItemID) != "" {
			return strings.TrimSpace(s.activeItemID)
		}
	}
	if len(values) > 0 {
		fallback = values[len(values)-1]
	}
	return "reasoning-" + safeIdentifier(fallback)
}

func (s *responsesStreamNotificationState) agentMessageItemID(turnID string, values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if s != nil && strings.TrimSpace(s.activeAgentItemID) != "" {
		return strings.TrimSpace(s.activeAgentItemID)
	}
	return "agent-message-" + safeIdentifier(turnID)
}

func streamAgentItemID(item *model.AgentItem) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(item.ID, item.CallID)
}

func turnStartReviewRuntime(params *turn.TurnStartParams) bool {
	if params == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(params.Originator), "review")
}

func turnStartPlanMode(params *turn.TurnStartParams) bool {
	if params == nil || params.CollaborationMode == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(stringFromAny(params.CollaborationMode["mode"])), string(ModeKindPlan))
}

const collaborationModeDefaultInstructions = `# Collaboration Mode: Default

You are now in Default mode. Any previous instructions for other modes (e.g. Plan mode) are no longer active.

Your active mode changes only when new developer instructions with a different ` + "`<collaboration_mode>...</collaboration_mode>`" + ` change it; user requests or tool descriptions do not change mode by themselves. Known mode names are Plan and Default.

## request_user_input availability

Use the ` + "`request_user_input`" + ` tool only when it is listed in the available tools for this turn.

In Default mode, strongly prefer making reasonable assumptions and executing the user's request rather than stopping to ask questions. If you absolutely must ask a question because the answer cannot be discovered from local context and a reasonable assumption would be risky, ask the user directly with a concise plain-text question. Never write a multiple choice question as a textual assistant message.`

func collaborationModeDeveloperInstructions(params *turn.TurnStartParams) string {
	if params == nil || params.CollaborationMode == nil {
		return ""
	}
	settings := collaborationModeSettings(params.CollaborationMode)
	if raw, ok := settings["developer_instructions"]; ok && raw != nil {
		return strings.TrimSpace(stringFromAny(raw))
	}
	if raw, ok := settings["developerInstructions"]; ok && raw != nil {
		return strings.TrimSpace(stringFromAny(raw))
	}
	if strings.EqualFold(strings.TrimSpace(stringFromAny(params.CollaborationMode["mode"])), string(ModeKindDefault)) {
		return collaborationModeDefaultInstructions
	}
	return ""
}

type collaborationModeWorldStateSnapshot struct {
	Mode  string `json:"mode"`
	Model string `json:"model"`
}

func collaborationModeInstructions(params *turn.TurnStartParams, info *model.ModelInfo) (string, bool) {
	if params == nil || params.CollaborationMode == nil {
		return "", false
	}
	mode := strings.ToLower(strings.TrimSpace(stringFromAny(params.CollaborationMode["mode"])))
	if info != nil && info.ModelMessages != nil && info.ModelMessages.CollaborationModes != nil {
		var catalogValue *string
		switch mode {
		case string(ModeKindDefault):
			catalogValue = info.ModelMessages.CollaborationModes.Default
		case string(ModeKindPlan):
			catalogValue = info.ModelMessages.CollaborationModes.Plan
		}
		if catalogValue != nil {
			return *catalogValue, true
		}
	}
	if legacy := collaborationModeDeveloperInstructions(params); legacy != "" {
		return legacy, true
	}
	return "", false
}

func collaborationModeSnapshot(params *turn.TurnStartParams, info *model.ModelInfo) (collaborationModeWorldStateSnapshot, bool) {
	if params == nil || params.CollaborationMode == nil {
		return collaborationModeWorldStateSnapshot{}, false
	}
	mode := strings.ToLower(strings.TrimSpace(stringFromAny(params.CollaborationMode["mode"])))
	if mode == "" {
		return collaborationModeWorldStateSnapshot{}, false
	}
	modelID := ""
	if info != nil {
		modelID = strings.TrimSpace(info.Slug)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(params.Model)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(stringFromAny(collaborationModeSettings(params.CollaborationMode)["model"]))
	}
	return collaborationModeWorldStateSnapshot{Mode: mode, Model: modelID}, true
}

func (s *responsesStreamNotificationState) planParserFor(itemID string) *proposedPlanStreamParser {
	if s == nil {
		return newProposedPlanStreamParser()
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		itemID = "agent-message"
	}
	parser := s.planParser[itemID]
	if parser == nil {
		parser = newProposedPlanStreamParser()
		s.planParser[itemID] = parser
	}
	return parser
}

func (r *RuntimeRouter) notifyPlanModeAgentDelta(threadID string, turnID string, itemID string, delta string, state *responsesStreamNotificationState) {
	if r == nil || state == nil || delta == "" {
		return
	}
	parser := state.planParserFor(itemID)
	r.notifyPlanModeSegments(threadID, turnID, itemID, parser.push(delta), state)
}

func (r *RuntimeRouter) finishPlanModeAgentItem(threadID string, turnID string, itemID string, state *responsesStreamNotificationState) {
	if r == nil || state == nil {
		return
	}
	parser := state.planParserFor(itemID)
	r.notifyPlanModeSegments(threadID, turnID, itemID, parser.finish(), state)
	delete(state.planParser, itemID)
}

func (r *RuntimeRouter) notifyPlanModeSegments(threadID string, turnID string, itemID string, segments []proposedPlanSegment, state *responsesStreamNotificationState) {
	for _, segment := range segments {
		switch segment.Kind {
		case proposedPlanSegmentNormal:
			if segment.Text == "" {
				continue
			}
			r.notifyAgentMessageStarted(threadID, turnID, itemID, state)
			r.notify(NotificationAgentMessageDelta, &AgentMessageDeltaNotification{
				ThreadID: threadID,
				TurnID:   turnID,
				ItemID:   itemID,
				Delta:    segment.Text,
			})
		case proposedPlanSegmentStart:
			r.notifyPlanItemStarted(threadID, turnID, state)
		case proposedPlanSegmentDelta:
			if segment.Text == "" {
				continue
			}
			r.notifyPlanItemStarted(threadID, turnID, state)
			r.notify(NotificationPlanDelta, &PlanDeltaNotification{
				ThreadID: threadID,
				TurnID:   turnID,
				ItemID:   state.planItemID,
				Delta:    segment.Text,
			})
		}
	}
}

func (r *RuntimeRouter) notifyAgentMessageStarted(threadID string, turnID string, itemID string, state *responsesStreamNotificationState) {
	if r == nil || state == nil {
		return
	}
	itemID = firstNonEmpty(itemID, "agent-message-"+safeIdentifier(turnID))
	if state.startedAgentItems[itemID] {
		return
	}
	state.startedAgentItems[itemID] = true
	item := ThreadItem{
		ID:         itemID,
		Type:       "agent_message",
		Role:       "assistant",
		TurnID:     turnID,
		CreatedAt:  time.Now().UTC().UnixMilli(),
		ResponseID: strings.TrimSpace(state.activeResponseID),
	}
	r.notify(NotificationItemStarted, &ItemStartedNotification{
		Item:        threadItemPayload(item),
		ThreadID:    threadID,
		TurnID:      turnID,
		StartedAtMS: time.Now().UTC().UnixMilli(),
	})
}

func resourceOrHostSkillInvocationID(skill promptctx.InstructionsSkillMetadata, repoURL string, repoRoot string) string {
	if isResourceBackedSkill(skill) {
		id := strings.TrimSpace(firstNonEmpty(skill.ResourceID, skill.PackageID, skill.LocatorPath))
		if id == "" {
			return ""
		}
		digest := sha1.Sum([]byte(id))
		return fmt.Sprintf("%x", digest)
	}
	return skillInvocationID(repoURL, repoRoot, skill.Path, skill.Name)
}

func isResourceBackedSkill(skill promptctx.InstructionsSkillMetadata) bool {
	kind := strings.ToLower(strings.TrimSpace(skill.LocatorKind))
	return kind == "executor package" || kind == "orchestrator package" ||
		strings.TrimSpace(skill.PackageID) != "" || strings.TrimSpace(skill.ResourceID) != ""
}

func (r *RuntimeRouter) notifyPlanItemStarted(threadID string, turnID string, state *responsesStreamNotificationState) {
	if r == nil || state == nil || state.planStarted {
		return
	}
	state.planStarted = true
	item := ThreadItem{
		ID:         state.planItemID,
		Type:       "plan",
		TurnID:     turnID,
		CreatedAt:  time.Now().UTC().UnixMilli(),
		ResponseID: strings.TrimSpace(state.activeResponseID),
	}
	r.notify(NotificationItemStarted, &ItemStartedNotification{
		Item:        threadItemPayload(item),
		ThreadID:    threadID,
		TurnID:      turnID,
		StartedAtMS: time.Now().UTC().UnixMilli(),
	})
}

func (r *RuntimeRouter) notifyResponsesStreamEvent(threadID string, turnID string, event *model.ResponsesStreamEvent, state *responsesStreamNotificationState) {
	if r == nil || event == nil {
		return
	}
	state.rememberResponse(event)
	switch event.Kind {
	case model.ResponsesStreamEventRetrying:
		state.retrying = true
		attempt := event.RetryAttempt
		message := fmt.Sprintf("Reconnecting... %d/%d", attempt, event.RetryMax)
		details := strings.TrimSpace(event.RetryError)
		r.notify(NotificationError, &ErrorNotification{
			Error: TurnError{
				Message:           message,
				CodexErrorInfo:    codexErrorInfoWithHTTPStatus("responseStreamDisconnected", event.RetryHTTPStatus),
				AdditionalDetails: stringPtrIfNotEmpty(details),
			},
			WillRetry: true,
			ThreadID:  threadID,
			TurnID:    turnID,
		})
	case model.ResponsesStreamEventOutputAdded:
		state.rememberOutputItem(event)
		state.rememberTool(event)
		if streamAgentItemLooksLikeMCP(event.Item) || streamAgentItemIsToolSearch(event.Item) || streamAgentItemLooksLikeCollaboration(event.Item) {
			return
		}
		item := threadItemFromStreamAgentItem(event.Item, turnID, event.ResponseID, time.Now().UTC())
		if item.ID == "" {
			return
		}
		if item.Type == "agent_message" {
			phase := streamAgentMessagePhase(event.Item)
			state.agentItemPhases[item.ID] = phase
			r.requireRealtime().BeginCodexOutput(threadID, item.ID, phase)
		}
		if state.planMode && item.Type == "agent_message" {
			if event.Item != nil && event.Item.Text != "" {
				r.notifyPlanModeAgentDelta(threadID, turnID, item.ID, event.Item.Text, state)
			}
			return
		}
		r.notify(NotificationItemStarted, &ItemStartedNotification{
			Item:        threadItemPayload(item),
			ThreadID:    threadID,
			TurnID:      turnID,
			StartedAtMS: time.Now().UTC().UnixMilli(),
		})
	case model.ResponsesStreamEventOutputDone:
		state.rememberOutputItem(event)
		if event.Item != nil && (event.Item.Type == "message" || event.Item.Type == "agent_message") {
			itemID := firstNonEmpty(event.ItemID, event.Item.ID, "agent-message-"+safeIdentifier(turnID))
			phase := state.agentItemPhases[itemID]
			r.notifyRealtime(r.requireRealtime().CompleteCodexOutput(threadID, itemID, phase, event.Item.Text))
			delete(state.agentItemPhases, itemID)
		}
		if len(event.RawItem) == 0 {
			return
		}
		if state.planMode && event.Item != nil && event.Item.Type == "agent_message" {
			r.finishPlanModeAgentItem(threadID, turnID, firstNonEmpty(event.ItemID, event.Item.ID, "agent-message-"+safeIdentifier(turnID)), state)
		}
		if !state.experimentalRawEvents {
			return
		}
		r.notify(NotificationRawResponseItemCompleted, &RawResponseItemCompletedNotification{
			ThreadID: threadID,
			TurnID:   turnID,
			Item:     event.RawItem,
		})
	case model.ResponsesStreamEventOutputText:
		// Some Responses-compatible providers omit item_id on output_text.delta.
		// Rust associates those deltas with the most recently added assistant
		// message item; retaining that identity prevents commentary and final
		// messages from being merged or rendered twice by the TUI.
		itemID := state.agentMessageItemID(turnID, event.ItemID)
		if event.Delta == "" {
			return
		}
		if state.planMode {
			r.notifyPlanModeAgentDelta(threadID, turnID, itemID, event.Delta, state)
			return
		}
		r.notifyRealtime(r.requireRealtime().StreamCodexOutput(threadID, itemID, event.Delta))
		r.notify(NotificationAgentMessageDelta, &AgentMessageDeltaNotification{
			ThreadID: threadID,
			TurnID:   turnID,
			ItemID:   itemID,
			Delta:    event.Delta,
		})
	case model.ResponsesStreamEventToolInputDelta:
		itemID := firstNonEmpty(event.ItemID, event.CallID, "tool-call-"+safeIdentifier(turnID))
		if event.Delta == "" {
			return
		}
		if r.notifyApplyPatchInputDelta(threadID, turnID, itemID, event.Delta, event, state) {
			return
		}
		r.notify(NotificationMCPToolCallProgress, &MCPToolCallProgressNotification{
			ThreadID: threadID,
			TurnID:   turnID,
			ItemID:   itemID,
			Message:  event.Delta,
		})
	case model.ResponsesStreamEventPlanDelta:
		if event.PlanDelta == nil || event.PlanDelta.Delta == "" {
			return
		}
		r.notifyPlanItemStarted(threadID, turnID, state)
		r.notify(NotificationPlanDelta, &PlanDeltaNotification{
			ThreadID: threadID,
			TurnID:   turnID,
			ItemID:   firstNonEmpty(event.PlanDelta.ItemID, event.ItemID, "plan-"+safeIdentifier(turnID)),
			Delta:    event.PlanDelta.Delta,
		})
	case model.ResponsesStreamEventReasoningSummaryTextDelta:
		if event.ReasoningDelta == nil || event.ReasoningDelta.SummaryIndex == nil || event.ReasoningDelta.Delta == "" {
			return
		}
		r.notify(NotificationReasoningSummaryTextDelta, &ReasoningSummaryTextDeltaNotification{
			ThreadID:     threadID,
			TurnID:       turnID,
			ItemID:       state.reasoningItemID(event.ReasoningDelta.ItemID, event.ItemID),
			Delta:        event.ReasoningDelta.Delta,
			SummaryIndex: *event.ReasoningDelta.SummaryIndex,
		})
	case model.ResponsesStreamEventReasoningTextDelta:
		if event.ReasoningDelta == nil || event.ReasoningDelta.ContentIndex == nil || event.ReasoningDelta.Delta == "" {
			return
		}
		r.notify(NotificationReasoningTextDelta, &ReasoningTextDeltaNotification{
			ThreadID:     threadID,
			TurnID:       turnID,
			ItemID:       state.reasoningItemID(event.ReasoningDelta.ItemID, event.ItemID),
			Delta:        event.ReasoningDelta.Delta,
			ContentIndex: *event.ReasoningDelta.ContentIndex,
		})
	case model.ResponsesStreamEventReasoningSummaryPartAdded:
		if event.ReasoningPart == nil {
			return
		}
		r.notify(NotificationReasoningSummaryPartAdded, &ReasoningSummaryPartAddedNotification{
			ThreadID:     threadID,
			TurnID:       turnID,
			ItemID:       state.reasoningItemID(event.ReasoningPart.ItemID, event.ItemID),
			SummaryIndex: event.ReasoningPart.SummaryIndex,
		})
	case model.ResponsesStreamEventModelReroute:
		if event.Reroute == nil {
			return
		}
		r.notify(NotificationModelRerouted, &ModelReroutedNotification{
			ThreadID:  threadID,
			TurnID:    turnID,
			FromModel: event.Reroute.FromModel,
			ToModel:   event.Reroute.ToModel,
			Reason:    modelRerouteReason(event.Reroute.Reason),
		})
	case model.ResponsesStreamEventModelVerify:
		if event.Verification == nil {
			return
		}
		r.notify(NotificationModelVerification, &ModelVerificationNotification{
			ThreadID:      threadID,
			TurnID:        turnID,
			Verifications: modelVerifications(event.Verification.Verifications),
		})
	case model.ResponsesStreamEventModeration:
		r.notify(NotificationTurnModerationMetadata, &TurnModerationMetadataNotification{
			ThreadID: threadID,
			TurnID:   turnID,
			Metadata: event.ModerationMetadata,
		})
	case model.ResponsesStreamEventSafetyBuffer:
		return
	case model.ResponsesStreamEventCompleted:
		if state.retrying {
			state.retrying = false
		}
		if !state.experimentalRawEvents {
			return
		}
		var usage *TokenUsageBreakdown
		if event.Usage != nil {
			usage = tokenUsageBreakdownFromAgentUsage(*event.Usage)
		}
		r.notify(NotificationRawResponseCompleted, &RawResponseCompletedNotification{
			ThreadID:   threadID,
			TurnID:     turnID,
			ResponseID: strings.TrimSpace(event.ResponseID),
			Usage:      usage,
		})
	}
}

func streamAgentItemLooksLikeCollaboration(item *model.AgentItem) bool {
	if item == nil || (item.Type != "function_call" && item.Type != "custom_tool_call") {
		return false
	}
	namespace := strings.TrimSpace(item.Namespace)
	if namespace == agent.MultiAgentV1Namespace || namespace == agent.MultiAgentV2Namespace {
		return true
	}
	name := strings.TrimSpace(item.Name)
	return strings.HasPrefix(name, agent.MultiAgentV1Namespace+".") || strings.HasPrefix(name, agent.MultiAgentV2Namespace+".")
}

func streamAgentMessagePhase(item *model.AgentItem) string {
	if item == nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(
		stringFromMap(item.Data, "phase"),
		stringFromMap(item.Data, "messagePhase"),
		stringFromMap(item.Data, "message_phase"),
	))
}

func (s *responsesStreamNotificationState) rememberTool(event *model.ResponsesStreamEvent) {
	if s == nil || event == nil || event.Item == nil {
		return
	}
	if strings.TrimSpace(event.Item.Name) == "" {
		return
	}
	for _, key := range []string{event.Item.ID, event.Item.CallID, event.ItemID, event.CallID} {
		if strings.TrimSpace(key) == "" {
			continue
		}
		s.toolNames[key] = event.Item.Name
	}
}

func (s *responsesStreamNotificationState) toolNameFor(event *model.ResponsesStreamEvent, itemID string) string {
	if s == nil {
		return ""
	}
	for _, key := range []string{itemID, event.ItemID, event.CallID} {
		if name := s.toolNames[key]; strings.TrimSpace(name) != "" {
			return name
		}
	}
	return ""
}

func modelRerouteReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case string(ModelRerouteReasonHighRiskCyberActivity), "high_risk_cyber_activity":
		return string(ModelRerouteReasonHighRiskCyberActivity)
	default:
		return strings.TrimSpace(reason)
	}
}

func modelVerifications(values []string) []ModelVerification {
	out := make([]ModelVerification, 0, len(values))
	for _, value := range values {
		switch strings.TrimSpace(value) {
		case string(ModelVerificationTrustedAccessForCyber), "trusted_access_for_cyber":
			out = append(out, ModelVerificationTrustedAccessForCyber)
		case "":
			continue
		default:
			out = append(out, ModelVerification(strings.TrimSpace(value)))
		}
	}
	if out == nil {
		return []ModelVerification{}
	}
	return out
}

func (r *RuntimeRouter) notifyApplyPatchInputDelta(threadID string, turnID string, itemID string, delta string, event *model.ResponsesStreamEvent, state *responsesStreamNotificationState) bool {
	if state == nil || strings.TrimSpace(itemID) == "" || delta == "" {
		return false
	}
	if state.toolNameFor(event, itemID) != "apply_patch" {
		return false
	}
	if !state.applyPatchStreamingEvents {
		return true
	}
	state.patchInputs[itemID] += delta
	input := state.patchInputs[itemID]
	action, err := applypatch.Parse(state.patchInputs[itemID])
	changes := []map[string]any{}
	if err == nil && action != nil && !action.IsEmpty() {
		changes = applyPatchActionFileChangeMaps(action)
	} else {
		changes = partialApplyPatchFileChangeMaps(input)
	}
	if len(changes) == 0 || !state.rememberPatchFingerprint(itemID, changes) {
		return true
	}
	r.notify(NotificationFileChangePatchUpdated, &FileChangePatchUpdatedNotification{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
		Changes:  fileChangeMapsAny(changes),
	})
	return true
}

func (s *responsesStreamNotificationState) rememberPatchFingerprint(itemID string, changes []map[string]any) bool {
	if s == nil {
		return true
	}
	if s.patchFingerprints == nil {
		s.patchFingerprints = map[string]string{}
	}
	data, err := json.Marshal(changes)
	if err != nil {
		return true
	}
	fingerprint := string(data)
	if s.patchFingerprints[itemID] == fingerprint {
		return false
	}
	s.patchFingerprints[itemID] = fingerprint
	return true
}

func applyPatchActionFileChangeAny(action *applypatch.Action) []any {
	return fileChangeMapsAny(applyPatchActionFileChangeMaps(action))
}

func fileChangeMapsAny(changes []map[string]any) []any {
	out := make([]any, 0, len(changes))
	for _, change := range changes {
		out = append(out, change)
	}
	return out
}

func applyPatchActionFileChangeMaps(action *applypatch.Action) []map[string]any {
	if action == nil {
		return []map[string]any{}
	}
	changes := make([]map[string]any, 0, len(action.Hunks))
	for index := range action.Hunks {
		change := &action.Hunks[index]
		changes = append(changes, map[string]any{
			"path": change.Path,
			"kind": applyPatchActionChangeKindData(change),
			"diff": applyPatchActionStreamingDiff(change),
		})
	}
	return changes
}

func applyPatchActionFileChangeMapsForCWD(action *applypatch.Action, cwd string) []map[string]any {
	changes := applyPatchActionFileChangeMaps(action)
	if len(changes) == 0 {
		return changes
	}
	for _, change := range changes {
		if path, ok := change["path"].(string); ok {
			change["path"] = appserverWorkspacePath(cwd, path)
		}
		if kind, ok := change["kind"].(map[string]any); ok {
			if movePath, ok := kind["move_path"].(string); ok && strings.TrimSpace(movePath) != "" {
				kind["move_path"] = appserverWorkspacePath(cwd, movePath)
			}
		}
	}
	return changes
}

func appserverWorkspacePath(cwd string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		base = "."
	}
	abs, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		return path
	}
	return filepath.Clean(abs)
}

func partialApplyPatchFileChangeMaps(input string) []map[string]any {
	hunks := partialApplyPatchChanges(input)
	if len(hunks) == 0 {
		return []map[string]any{}
	}
	return applyPatchActionFileChangeMaps(&applypatch.Action{Hunks: hunks})
}

func partialApplyPatchChanges(input string) []applypatch.Change {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n"), "\n")
	hunks := make([]applypatch.Change, 0)
	inPatch := false
	var current *applypatch.Change
	var content strings.Builder
	flush := func() {
		if current == nil || strings.TrimSpace(current.Path) == "" {
			current = nil
			content.Reset()
			return
		}
		change := *current
		switch change.Kind {
		case applypatch.ChangeAdd, applypatch.ChangeDelete:
			change.Content = content.String()
		default:
			change.UnifiedDiff = content.String()
		}
		hunks = append(hunks, change)
		current = nil
		content.Reset()
	}
	for _, line := range lines {
		switch {
		case !inPatch:
			if line == "*** Begin Patch" {
				inPatch = true
			}
		case line == "*** End Patch":
			flush()
			return hunks
		case strings.HasPrefix(line, "*** Add File: "):
			flush()
			current = &applypatch.Change{Kind: applypatch.ChangeAdd, Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
		case strings.HasPrefix(line, "*** Delete File: "):
			flush()
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if path != "" {
				hunks = append(hunks, applypatch.Change{Kind: applypatch.ChangeDelete, Path: path})
			}
		case strings.HasPrefix(line, "*** Update File: "):
			flush()
			current = &applypatch.Change{Kind: applypatch.ChangeUpdate, Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
		case strings.HasPrefix(line, "*** Move to: "):
			if current != nil && current.Kind == applypatch.ChangeUpdate {
				current.MovePath = strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
			}
		default:
			appendPartialPatchLine(current, &content, line)
		}
	}
	flush()
	return hunks
}

func appendPartialPatchLine(change *applypatch.Change, content *strings.Builder, line string) {
	if change == nil || content == nil {
		return
	}
	switch change.Kind {
	case applypatch.ChangeAdd:
		if strings.HasPrefix(line, "+") {
			content.WriteString(strings.TrimPrefix(line, "+"))
			content.WriteByte('\n')
		}
	case applypatch.ChangeUpdate:
		if line == "*** End of File" || strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ") {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
}

func applyPatchActionChangeKindData(change *applypatch.Change) map[string]any {
	if change == nil {
		return map[string]any{"type": "update", "move_path": nil}
	}
	switch change.Kind {
	case applypatch.ChangeAdd, applypatch.ChangeDelete:
		return map[string]any{"type": string(change.Kind)}
	default:
		kind := map[string]any{"type": string(applypatch.ChangeUpdate), "move_path": nil}
		if strings.TrimSpace(change.MovePath) != "" {
			kind["move_path"] = change.MovePath
		}
		return kind
	}
}

func applyPatchActionChangeDiff(change *applypatch.Change) string {
	if change == nil {
		return ""
	}
	switch change.Kind {
	case applypatch.ChangeAdd, applypatch.ChangeDelete:
		return change.Content
	default:
		if strings.TrimSpace(change.MovePath) == "" {
			return change.UnifiedDiff
		}
		return fmt.Sprintf("%s\n\nMoved to: %s", strings.TrimRight(change.UnifiedDiff, "\n"), change.MovePath)
	}
}

func applyPatchActionStreamingDiff(change *applypatch.Change) string {
	if change == nil {
		return ""
	}
	switch change.Kind {
	case applypatch.ChangeAdd:
		return change.Content
	case applypatch.ChangeDelete:
		return ""
	default:
		return change.UnifiedDiff
	}
}

func (r *RuntimeRouter) startReviewRuntimeAsync(params *review.StartParams, response *review.StartResponse, connectionID string) {
	if r == nil || params == nil || response == nil || !r.reviewRuntimeAvailable() {
		return
	}
	turnParams := r.reviewRuntimeTurnStartParams(params, response)
	if turnParams == nil || strings.TrimSpace(turnParams.ThreadID) == "" {
		return
	}
	turnID := strings.TrimSpace(response.Turn.ID)
	if turnID == "" {
		turnID = "review-" + strings.TrimSpace(params.ThreadID)
	}
	runtime, err := r.buildTurnRuntime(turnParams, turnID)
	if err != nil {
		r.emitTurnRuntimeError(turnParams.ThreadID, turnID, err)
		return
	}
	if runtime == nil {
		return
	}
	startedAt := response.Turn.StartedAt
	if startedAt == 0 {
		startedAt = time.Now().UTC().Unix()
	}
	record := &turn.TurnRecord{
		ID:        turnID,
		ThreadID:  turnParams.ThreadID,
		Status:    turn.TurnStatusInProgress,
		Prompt:    promptFromTurnStart(turnParams),
		StartedAt: startedAt,
	}
	paramsCopy := cloneTurnStartParams(turnParams)
	ctx, cancel := context.WithCancel(context.Background())
	startedAtMS := time.Unix(startedAt, 0).UTC().UnixMilli()
	if err := r.registerTrackedActiveRuntimeTurn(turnParams.ThreadID, turnID, cancel, startedAtMS, paramsCopy); err != nil {
		cancel()
		r.emitTurnRuntimeError(turnParams.ThreadID, turnID, err)
		return
	}
	r.updateActiveRuntimeTurnAnalytics(turnParams.ThreadID, turnID, connectionID, nil)
	go func() {
		defer r.threads.TurnWorkerDone()
		r.runReviewRuntime(ctx, paramsCopy, record, runtime, connectionID)
	}()
}

func (r *RuntimeRouter) reviewRuntimeAvailable() bool {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return false
	}
	return r.agentConfigured() || r.services.TurnRuntime != nil
}

func (r *RuntimeRouter) reviewRuntimeTurnStartParams(params *review.StartParams, response *review.StartResponse) *turn.TurnStartParams {
	if params == nil || response == nil {
		return nil
	}
	threadID := strings.TrimSpace(response.ReviewThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(params.ThreadID)
	}
	baseInstructions := review.ReviewPrompt
	turnParams := &turn.TurnStartParams{
		ThreadID:         threadID,
		BaseInstructions: &baseInstructions,
		Originator:       "review",
		ApprovalPolicy:   string(sandbox.ApprovalNever),
		Config:           map[string]any{},
	}
	if record, err := r.threadRecord(session.ThreadID(threadID), true, false); err == nil && record != nil {
		turnParams.CWD = strings.TrimSpace(record.Metadata.CWD)
		turnParams.Model = strings.TrimSpace(record.Metadata.Model)
		if providerID := strings.TrimSpace(record.Metadata.ModelProvider); providerID != "" {
			turnParams.Config["model_provider"] = providerID
		}
	}
	turnParams.Prompt = review.PromptForTargetInDir(params.Target.ToTarget(), turnParams.CWD)
	if override := r.reviewModelOverride(); override != nil {
		turnParams.Model = *override
	}
	if len(turnParams.Config) == 0 {
		turnParams.Config = nil
	}
	return turnParams
}

func (r *RuntimeRouter) runTurnRuntime(ctx context.Context, params *turn.TurnStartParams, record *turn.TurnRecord, runtime *turn.Runtime, connectionID string) {
	if r == nil || params == nil || record == nil || runtime == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	threadID := params.ThreadID
	turnID := record.ID
	startedAt := time.Unix(record.StartedAt, 0).UTC()
	if record.StartedAt == 0 {
		startedAt = time.Now().UTC()
	}
	startedAtMS := startedAt.UnixMilli()
	appTurn := appTurnFromTurnRecord(record, nil, TurnStatusInProgress, nil, nil)
	appTurn.Items = []ThreadItem{}
	appTurn.ItemsView = TurnItemsNotLoaded
	r.beginStateThreadGoalTurn(threadID, turnID, startedAtMS, turnStartPlanMode(params), connectionID)
	promptPersisted := false

	runConfig, err := r.appTurnConfig(ctx, threadID, turnID, params, startedAtMS, runtime)
	if err != nil {
		if ctx.Err() != nil {
			r.persistRuntimeTurnPrompt(threadID, turnID, params, startedAt)
		}
		r.clearActiveRuntimeTurn(threadID, turnID)
		r.finishTurnWithError(threadID, turnID, startedAtMS, err)
		return
	}
	preparedToolMode, warning := runtime.PrepareToolMode(runConfig.ToolMode, runConfig.DisableCodeModeFallback)
	runConfig.ToolMode = preparedToolMode
	if warning != "" {
		r.notify(NotificationWarning, &WarningNotification{ThreadID: stringPtrIfNotEmpty(threadID), Message: warning})
	}
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnStarted(threadID))
	r.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: appTurn})
	_ = r.appendRuntimeTurnStarted(threadID, turnID, startedAt)
	// Rust records a full context window and compacts before the next user turn.
	// Do this before persisting/sending the new prompt so the prompt is retained
	// and the sampling request sees the compacted history.
	if status := r.compactTokenStatusForTurn(threadID, runConfig.Model, params); status.ShouldCompact {
		_, compactErr := r.compactThread(ctx, &runtimeCompactRequest{
			ThreadID: threadID, TurnID: turnID, ConnectionID: connectionID,
			Trigger: compact.TriggerAuto, Reason: compact.ReasonContextWindowExceeded,
			Phase: compact.PhasePreTurn, ActiveContextTokensBefore: int64(status.ActiveContextTokens),
		})
		if compactErr != nil {
			r.clearActiveRuntimeTurn(threadID, turnID)
			if ctx.Err() != nil {
				r.persistRuntimeTurnPrompt(threadID, turnID, params, startedAt)
			}
			r.finishTurnWithErrorAnalytics(threadID, turnID, startedAtMS, compactErr, &turnCompletionAnalyticsContext{ConnectionID: connectionID, Params: params, RunConfig: runConfig})
			return
		}
		// appTurnConfig contains the session history; reload it after compaction.
		if runConfig, err = r.appTurnConfig(ctx, threadID, turnID, params, startedAtMS, runtime); err != nil {
			r.clearActiveRuntimeTurn(threadID, turnID)
			if ctx.Err() != nil {
				r.persistRuntimeTurnPrompt(threadID, turnID, params, startedAt)
			}
			r.finishTurnWithError(threadID, turnID, startedAtMS, err)
			return
		}
	}
	r.updateActiveRuntimeTurnAnalytics(threadID, turnID, connectionID, runConfig)
	promptPersisted = r.persistRuntimeTurnPrompt(threadID, turnID, params, startedAt)
	agentPrompt := promptFromTurnStart(params)
	inputItems := append([]any(nil), runConfig.InputItems...)
	inputItems = append(inputItems, params.AdditionalInputItems...)
	// Rust 6f647caa9b: async hook results recorded before the user prompt
	// appear ahead of the new prompt in conversation history.
	inputItems = append(inputItems, r.asyncHookContextInputItems(threadID)...)
	if turnStartUsesStructuredUserInput(params) {
		if item := userMessageInputItemFromTurnUserInputs(params.Prompt, params.Input); item != nil {
			inputItems = append(inputItems, item)
			agentPrompt = ""
		}
	}
	samplingFollowUp, tokenBudgetDelivery := r.autoCompactFallbackFollowUp(threadID, runConfig)
	samplingCompaction := r.midTurnSamplingCompaction(threadID, turnID, connectionID, params, runConfig, startedAt)
	result, err := runtime.Run(ctx, &turn.AgentLoopRequest{
		Prompt:                       agentPrompt,
		Instructions:                 runConfig.Instructions,
		InputItems:                   inputItems,
		HostedTools:                  append([]any(nil), runConfig.HostedTools...),
		SteerMailbox:                 r.requireSteerMailbox(),
		Model:                        runConfig.Model,
		ToolMode:                     runConfig.ToolMode,
		DisableCodeModeFallback:      runConfig.DisableCodeModeFallback,
		ProviderID:                   runConfig.ProviderID,
		TaskKind:                     model.AgentTaskRegular,
		ThreadID:                     threadID,
		TurnID:                       turnID,
		Originator:                   runConfig.Originator,
		Store:                        runConfig.Store,
		PreviousResponseID:           runConfig.PreviousResponseID,
		ParallelToolCalls:            runConfig.ParallelToolCalls,
		ReasoningEffort:              runConfig.ReasoningEffort,
		ReasoningSummary:             runConfig.ReasoningSummary,
		ConcurrentReasoningSummaries: runConfig.ConcurrentReasoningSummaries,
		ModelVerbosity:               runConfig.ModelVerbosity,
		IncludeTimingMetrics:         runConfig.IncludeTimingMetrics,
		BetaFeaturesHeader:           runConfig.BetaFeaturesHeader,
		ItemIDsEnabled:               runConfig.ItemIDsEnabled,
		PromptCacheKey:               runConfig.PromptCacheKey,
		ServiceTier:                  runConfig.ServiceTier,
		ClientMetadata:               cloneStringMap(runConfig.ClientMetadata),
		AttestationProvider:          runConfig.AttestationProvider,
		OutputSchema:                 params.OutputSchema,
		PostToolInputItems:           runConfig.PostToolInputItems,
		OnToolStarted:                r.runtimeToolStartedNotifier(threadID, turnID, firstNonEmpty(params.CWD, r.services.DefaultCWD), runConfig.UnifiedExecEnabled),
		OnToolCompleted:              r.runtimeToolCompletedNotifier(threadID, turnID, firstNonEmpty(params.CWD, r.services.DefaultCWD), runConfig.UnifiedExecEnabled),
		EmitCodeModeNestedLifecycle:  true,
		OnWarning: func(message string) {
			r.notify(NotificationWarning, &WarningNotification{ThreadID: stringPtrIfNotEmpty(threadID), Message: message})
		},
		SamplingFollowUp:                samplingFollowUp,
		SamplingCompaction:              samplingCompaction,
		ExecutedToolCallMetadataEnabled: runConfig.ExecutedToolCallMetadataEnabled,
	})
	if err != nil {
		steerCount := r.activeRuntimeTurnSteerCount(threadID, turnID)
		if ctx.Err() != nil {
			r.clearActiveRuntimeTurn(threadID, turnID)
			return
		}
		if isContextWindowExceededError(err) {
			_ = r.persistContextWindowExceededStatus(threadID)
		}
		r.clearActiveRuntimeTurn(threadID, turnID)
		r.finishTurnWithErrorAnalytics(threadID, turnID, startedAtMS, err, &turnCompletionAnalyticsContext{
			ConnectionID: connectionID,
			Params:       params,
			RunConfig:    runConfig,
			SteerCount:   steerCount,
		})
		return
	}
	steerCount := r.activeRuntimeTurnSteerCount(threadID, turnID)
	r.unifiedExecPersistMu.Lock()
	if !r.consumeCompletedRuntimeTurn(threadID, turnID) {
		r.unifiedExecPersistMu.Unlock()
		return
	}
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.notifyTurnPlanUpdates(threadID, turnID, result)
	_ = r.persistLastResponseID(threadID, result)
	items := append([]session.Item(nil), runConfig.SessionItems...)
	if runConfig.ExtraSessionItems != nil {
		items = append(items, runConfig.ExtraSessionItems()...)
	}
	items = append(items, r.drainPendingUnifiedExecItems(threadID, turnID)...)
	items = append(items, r.sessionItemsForTurn(turnID, params, result, startedAt)...)
	if promptPersisted {
		items = withoutRuntimeUserPromptItem(items, turnID)
	}
	r.attributeSessionCommandItems(threadID, turnID, items)
	if len(items) > 0 {
		if _, err := r.runtimeAppendItems(session.ThreadID(threadID), items); err != nil {
			r.unifiedExecPersistMu.Unlock()
			r.finishTurnWithErrorAnalytics(threadID, turnID, startedAtMS, err, &turnCompletionAnalyticsContext{
				ConnectionID: connectionID,
				Params:       params,
				RunConfig:    runConfig,
				Result:       result,
				SteerCount:   steerCount,
			})
			return
		}
		_ = r.appendRuntimeRollout(threadID, items, startedAt)
	}
	r.unifiedExecPersistMu.Unlock()
	threadItems := make([]ThreadItem, 0, len(items))
	for _, item := range items {
		if sessionItemIsHiddenThreadItem(&item) {
			continue
		}
		if item.Type == "tool_output" {
			r.notifyTurnDiffFromSessionItem(threadID, turnID, &item)
		}
		threadItem := BuildThreadItem(item)
		r.attributeCommandExecutionItem(&threadItem)
		threadItems = append(threadItems, threadItem)
		payload := threadItemPayload(threadItem)
		if shouldNotifyRuntimeItemCompleted(threadItem) {
			r.notify(NotificationItemCompleted, &ItemCompletedNotification{
				Item:          payload,
				ThreadID:      threadID,
				TurnID:        turnID,
				CompletedAtMS: item.CreatedAt.UTC().UnixMilli(),
			})
			r.emitCommandExecutionAnalyticsEvent(ctx, connectionID, threadID, turnID, &threadItem, runConfig)
			r.emitFileChangeAnalyticsEvent(ctx, connectionID, threadID, turnID, &threadItem, runConfig)
			r.emitMCPToolCallAnalyticsEvent(ctx, connectionID, threadID, turnID, &threadItem, runConfig)
			r.emitDynamicToolCallAnalyticsEvent(ctx, connectionID, threadID, turnID, &threadItem, runConfig)
			r.emitCollabAgentToolCallAnalyticsEvent(ctx, connectionID, threadID, turnID, &threadItem, runConfig)
			r.emitWebSearchAnalyticsEvent(ctx, connectionID, threadID, turnID, &threadItem, runConfig)
			r.emitImageGenerationAnalyticsEvent(ctx, connectionID, threadID, turnID, &threadItem, runConfig)
		}
	}
	if usage := tokenUsageFromAgentLoopResult(result); usage != nil {
		usage.ModelContextWindow = positiveInt64Ptr(r.effectiveModelContextWindowForModel(runConfig.Model, params))
		lastUsage := lastAgentResponseUsage(result)
		status, tokenInfo, statusErr := r.persistCompactTokenStatus(threadID, runConfig.Model, params, result.Usage, lastUsage)
		if tokenInfo != nil {
			usage.Total = tokenInfo.Total
			usage.Last = tokenInfo.Last
		}
		r.notify(NotificationThreadTokenUsageUpdated, &ThreadTokenUsageUpdatedNotification{
			ThreadID:   threadID,
			TurnID:     turnID,
			TokenUsage: *usage,
		})
		if statusErr == nil && status != nil {
			_ = r.persistAutoCompactFallbackOutcome(threadID, turnID, result, status, tokenBudgetDelivery)
			fallbackRecorded, fallbackErr := r.recordAutoCompactFallbackPrompt(threadID, turnID, status)
			if !fallbackRecorded && fallbackErr == nil && status.ShouldCompact {
				if _, compactErr := r.autoCompactThreadAfterTurn(threadID, turnID, connectionID, status); compactErr != nil {
					// Surface compaction failures instead of silently leaving
					// the thread over the limit (Rust reports the error and
					// the next turn re-attempts compaction).
					r.persistCompactionFailure(threadID, compactErr)
					r.notify(NotificationWarning, &WarningNotification{
						ThreadID: stringPtrIfNotEmpty(threadID),
						Message:  "Automatic context compaction failed: " + compactErr.Error(),
					})
				}
			}
		}
	}
	completedAt := time.Now().UTC()
	completedAtUnix := completedAt.Unix()
	durationMS := completedAt.UnixMilli() - startedAtMS
	r.finishStateThreadGoalTurn(threadID, turnID, completedAt, model.AgentUsageTotalTokens(result.Usage), nil)
	_ = r.appendRuntimeTurnComplete(threadID, turnID, completedAt, durationMS)
	r.completeTurnRecord(threadID, turnID, TurnStatusCompleted)
	completedTurn := completedTurnNotificationTurn(turnID, TurnStatusCompleted, nil, &record.StartedAt, &completedAtUnix, &durationMS)
	if summary := finalAgentMessageSummary(threadItems); len(summary) > 0 {
		completedTurn.Items = summary
		completedTurn.ItemsView = TurnItemsSummary
	}
	r.notifyTurnCompletedOnce(&TurnCompletedNotification{ThreadID: threadID, Turn: completedTurn})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnCompleted(threadID))
	r.deliverRuntimeAgentCompletion(threadID, agent.AgentMessageStatus{Kind: agent.AgentMessageStatusCompleted, Message: lastAgentMessageFromThreadItems(threadItems)})
	r.emitCodexTurnAnalyticsEvent(ctx, connectionID, params, record, runConfig, result, TurnStatusCompleted, startedAt, completedAt, durationMS, steerCount, nil, nil, nil, nil)
	r.emitAcceptedLineFingerprintsAnalyticsEvent(ctx, threadID, turnID, runConfig, completedAt)
	r.clearActiveDiffTracker(threadID, turnID)
}

func (r *RuntimeRouter) notifyCompactionActivity(threadID string, active bool) {
	message := "Context compaction completed"
	if active {
		message = "Compacting context..."
	}
	r.notify(NotificationWarning, &WarningNotification{Message: message, ThreadID: stringPtrIfNotEmpty(threadID)})
}

func (r *RuntimeRouter) persistAutoCompactFallbackOutcome(threadID string, turnID string, result *turn.AgentLoopResult, status *compact.TokenStatus, delivery *tokenBudgetDeliveryState) error {
	if r == nil || result == nil || result.SamplingFollowUps == 0 || status == nil || delivery == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return err
	}
	extra := ensureRecordExtra(cloneAnyMap(record.Metadata.Extra))
	if delivery.reminderDelivered {
		extra["token_budget_reminder_delivered"] = true
		extra["token_budget_reminder_turn_id"] = strings.TrimSpace(turnID)
		extra["token_budget_reminder_active_context_tokens"] = status.ActiveContextTokens
	}
	if delivery.fallbackDelivered {
		extra["auto_compact_fallback_delivered"] = true
		extra["auto_compact_fallback_turn_id"] = strings.TrimSpace(turnID)
		extra["auto_compact_fallback_follow_up_count"] = result.SamplingFollowUps
		extra["auto_compact_fallback_active_context_tokens"] = status.ActiveContextTokens
		outcome := "reserve_remaining"
		if status.ShouldCompact {
			outcome = "reserve_exhausted"
		}
		extra["auto_compact_fallback_outcome"] = outcome
	}
	record.Metadata.Extra = extra
	if runtimeRecordEphemeral(record) {
		r.saveEphemeralThreadRecord(record)
		return nil
	}
	return r.runtimeSaveThreadRecord(record)
}

type tokenBudgetDeliveryState struct {
	reminderDelivered bool
	fallbackDelivered bool
}

// autoCompactFallbackFollowUp mirrors Rust token_budget::maybe_record: the
// token-budget reminder fires once per window when the base window remaining
// drops to the reminder threshold, and the auto-compact fallback prompt fires
// once per window when the base window is exactly exhausted without forcing
// compaction. Delivery state is shared with persistAutoCompactFallbackOutcome
// so the persisted per-window markers distinguish the two.
func (r *RuntimeRouter) autoCompactFallbackFollowUp(threadID string, runConfig *appTurnRunConfig) (turn.SamplingFollowUp, *tokenBudgetDeliveryState) {
	if r == nil || runConfig == nil {
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return nil, nil
	}
	extra := cloneAnyMap(record.Metadata.Extra)
	prompt := strings.TrimSpace(stringFromAny(extra["auto_compact_fallback_prompt"]))
	buffer := intFromAny(extra["auto_compact_fallback_buffer_tokens"])
	limit := compactTokenLimitFromMetadata(extra)
	reminderThreshold := intFromAny(extra["token_budget_reminder_threshold_tokens"])
	reminderTemplate := strings.TrimSpace(stringFromAny(extra["token_budget_reminder_message_template"]))
	fallbackConfigured := prompt != "" && buffer > 0 && limit > 0 && !boolFromAny(extra["auto_compact_fallback_delivered"])
	reminderConfigured := reminderThreshold > 0 && reminderTemplate != "" && !boolFromAny(extra["token_budget_reminder_delivered"])
	if !fallbackConfigured && !reminderConfigured {
		return nil, nil
	}
	delivery := &tokenBudgetDeliveryState{}
	return func(ctx *turn.SamplingFollowUpContext) []any {
		if ctx == nil {
			return nil
		}
		status := compact.Evaluate(compact.Policy{Enabled: true, TokenLimit: limit, FallbackBufferTokens: buffer}, int(model.AgentUsageTotalTokens(ctx.Usage)))
		if status.BaseWindowTokensRemaining == nil {
			return nil
		}
		remaining := *status.BaseWindowTokensRemaining
		var items []any
		// The reminder fires even when compaction is already due (Rust records
		// it before the roll-over check).
		if reminderConfigured && !delivery.reminderDelivered && remaining <= reminderThreshold {
			if item := model.DeveloperMessageInputItem(strings.ReplaceAll(reminderTemplate, "{n_remaining}", strconv.Itoa(remaining))); item != nil {
				items = append(items, item)
				delivery.reminderDelivered = true
			}
		}
		if fallbackConfigured && !delivery.fallbackDelivered && !status.ShouldCompact && remaining == 0 {
			if item := model.DeveloperMessageInputItem(prompt); item != nil {
				items = append(items, item)
				delivery.fallbackDelivered = true
			}
		}
		return items
	}, delivery
}

// midTurnSamplingCompaction mirrors Rust's mid-turn roll-over: when a turn
// still needs follow-up and the auto-compact token limit is reached, the
// in-flight conversation is compacted in place and the sampling loop continues
// against the compacted history instead of executing the pending tool calls.
// The active context is measured from the last sampled response (Rust's
// context_window_token_status uses the last response plus items recorded after
// it), not the accumulated turn usage.
func (r *RuntimeRouter) midTurnSamplingCompaction(threadID string, turnID string, connectionID string, params *turn.TurnStartParams, runConfig *appTurnRunConfig, startedAt time.Time) turn.SamplingCompaction {
	if r == nil || runConfig == nil || params == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	return func(ctx *turn.SamplingCompactionContext) (*turn.SamplingCompactionResult, error) {
		if ctx == nil || ctx.Result == nil || ctx.Response == nil {
			return nil, nil
		}
		record, err := r.threadRecord(session.ThreadID(threadID), true, true)
		if err != nil || record == nil {
			return nil, err
		}
		extra := cloneAnyMap(record.Metadata.Extra)
		policy := r.compactPolicyForTurn(runConfig.Model, params, extra)
		if policy.Scope == compact.ScopeBodyAfterPrefix && policy.PrefillTokens <= 0 {
			policy.PrefillTokens = compact.EstimateTokens(compactItemsFromSessionItems(record.Items))
		}
		status := compact.Evaluate(policy, int(model.AgentUsageTotalTokens(ctx.Response.Usage)))
		newContextWindowRequested := r.takeNewContextWindowRequest(threadID)
		if !status.ShouldCompact && !newContextWindowRequested {
			return nil, nil
		}
		if newContextWindowRequested && !status.ShouldCompact {
			// Rust's token-budget new_context roll-over restarts the context
			// window without summarizing the conversation history.
			if err := r.resetContextWindow(threadID, turnID); err != nil {
				return nil, err
			}
			return &turn.SamplingCompactionResult{
				Compacted:   true,
				ResetWindow: true,
			}, nil
		}
		// The number of conversation input items is captured before the
		// compaction replaces the persisted history, so the world-state
		// prefix can be split from the original conversation items.
		historyItems, _ := r.historyInputItemsForTurn(threadID)
		// The conversation to compact is the persisted thread history plus
		// the items the current turn accumulated before they are persisted.
		history := compactItemsFromSessionItems(record.Items)
		seen := make(map[string]struct{}, len(history))
		for i := range history {
			if history[i].ID != "" {
				seen[history[i].ID] = struct{}{}
			}
		}
		for _, item := range compactItemsFromSessionItems(r.sessionItemsForTurn(turnID, params, ctx.Result, startedAt)) {
			if item.ID != "" {
				if _, ok := seen[item.ID]; ok {
					continue
				}
				seen[item.ID] = struct{}{}
			}
			history = append(history, item)
		}
		_, compactedItems, err := r.compactThreadWithHistory(context.Background(), &runtimeCompactRequest{
			ThreadID:                  threadID,
			TurnID:                    turnID,
			ConnectionID:              connectionID,
			Trigger:                   compact.TriggerAuto,
			Reason:                    compactReasonFromStatus(&status),
			Phase:                     compact.PhaseMidTurn,
			ActiveContextTokensBefore: int64(status.ActiveContextTokens),
			History:                   history,
		}, nil)
		if err != nil {
			return nil, err
		}
		if len(compactedItems) == 0 {
			return nil, nil
		}
		// Preserve the turn's non-conversation prefix items (world state,
		// skills, ...) so the compacted context continues with the same
		// injected state. The compacted history ends with the last real user
		// message, so the prefix is placed ahead of it.
		prefix := runConfig.InputItems
		if len(historyItems) <= len(prefix) {
			prefix = append([]any(nil), prefix[len(historyItems):]...)
		} else {
			prefix = nil
		}
		compactedInputItems := session.InputItemsFromItems(compactedItems, &session.HistoryBuildOptions{
			IncludeToolOutputs: true,
			CWD:                strings.TrimSpace(record.Metadata.CWD),
		})
		replacement := append(prefix, compactedInputItems...)
		return &turn.SamplingCompactionResult{
			Compacted:          true,
			InputItems:         replacement,
			PreviousResponseID: "",
		}, nil
	}
}

func (r *RuntimeRouter) runReviewRuntime(ctx context.Context, params *turn.TurnStartParams, record *turn.TurnRecord, runtime *turn.Runtime, connectionID string) {
	if r == nil || params == nil || record == nil || runtime == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	threadID := params.ThreadID
	turnID := record.ID
	startedAt := time.Unix(record.StartedAt, 0).UTC()
	if record.StartedAt == 0 {
		startedAt = time.Now().UTC()
	}
	startedAtMS := startedAt.UnixMilli()
	r.beginStateThreadGoalTurn(threadID, turnID, startedAtMS, turnStartPlanMode(params), connectionID)
	runConfig, err := r.appTurnConfig(ctx, threadID, turnID, params, startedAtMS, runtime)
	if err != nil {
		r.clearActiveRuntimeTurn(threadID, turnID)
		r.finishTurnWithError(threadID, turnID, startedAtMS, err)
		return
	}
	r.updateActiveRuntimeTurnAnalytics(threadID, turnID, connectionID, runConfig)
	inputItems := append(append([]any(nil), runConfig.InputItems...), params.AdditionalInputItems...)
	inputItems = append(inputItems, r.asyncHookContextInputItems(threadID)...)
	result, err := runtime.Run(ctx, &turn.AgentLoopRequest{
		Prompt:                       promptFromTurnStart(params),
		Instructions:                 runConfig.Instructions,
		InputItems:                   inputItems,
		HostedTools:                  append([]any(nil), runConfig.HostedTools...),
		SteerMailbox:                 r.requireSteerMailbox(),
		Model:                        runConfig.Model,
		ToolMode:                     runConfig.ToolMode,
		DisableCodeModeFallback:      runConfig.DisableCodeModeFallback,
		ProviderID:                   runConfig.ProviderID,
		TaskKind:                     model.AgentTaskReview,
		ThreadID:                     threadID,
		TurnID:                       turnID,
		Originator:                   runConfig.Originator,
		Store:                        runConfig.Store,
		PreviousResponseID:           runConfig.PreviousResponseID,
		ParallelToolCalls:            runConfig.ParallelToolCalls,
		ReasoningEffort:              runConfig.ReasoningEffort,
		ReasoningSummary:             runConfig.ReasoningSummary,
		ConcurrentReasoningSummaries: runConfig.ConcurrentReasoningSummaries,
		ModelVerbosity:               runConfig.ModelVerbosity,
		IncludeTimingMetrics:         runConfig.IncludeTimingMetrics,
		BetaFeaturesHeader:           runConfig.BetaFeaturesHeader,
		ItemIDsEnabled:               runConfig.ItemIDsEnabled,
		PromptCacheKey:               runConfig.PromptCacheKey,
		ServiceTier:                  runConfig.ServiceTier,
		ClientMetadata:               cloneStringMap(runConfig.ClientMetadata),
		AttestationProvider:          runConfig.AttestationProvider,
		PostToolInputItems:           runConfig.PostToolInputItems,
		DisableHostedImageGeneration: true,
		OnToolStarted:                r.runtimeToolStartedNotifier(threadID, turnID, firstNonEmpty(params.CWD, r.services.DefaultCWD), runConfig.UnifiedExecEnabled),
		OnToolCompleted:              r.runtimeToolCompletedNotifier(threadID, turnID, firstNonEmpty(params.CWD, r.services.DefaultCWD), runConfig.UnifiedExecEnabled),
		EmitCodeModeNestedLifecycle:  true,
		OnWarning: func(message string) {
			r.notify(NotificationWarning, &WarningNotification{ThreadID: stringPtrIfNotEmpty(threadID), Message: message})
		},
		ExecutedToolCallMetadataEnabled: runConfig.ExecutedToolCallMetadataEnabled,
	})
	if err != nil {
		steerCount := r.activeRuntimeTurnSteerCount(threadID, turnID)
		if ctx.Err() != nil {
			r.clearActiveRuntimeTurn(threadID, turnID)
			return
		}
		r.clearActiveRuntimeTurn(threadID, turnID)
		r.finishReviewRuntimeFallbackCompleted(threadID, turnID, record.StartedAt, startedAtMS, &turnCompletionAnalyticsContext{
			ConnectionID: connectionID,
			Params:       params,
			RunConfig:    runConfig,
			SteerCount:   steerCount,
		})
		return
	}
	steerCount := r.activeRuntimeTurnSteerCount(threadID, turnID)
	r.unifiedExecPersistMu.Lock()
	if !r.consumeCompletedRuntimeTurn(threadID, turnID) {
		r.unifiedExecPersistMu.Unlock()
		return
	}
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.notifyTurnPlanUpdates(threadID, turnID, result)
	_ = r.persistLastResponseID(threadID, result)
	output := reviewOutputFromAgentLoopResult(result)
	completedAt := time.Now().UTC()
	items := append([]session.Item(nil), runConfig.SessionItems...)
	if runConfig.ExtraSessionItems != nil {
		items = append(items, runConfig.ExtraSessionItems()...)
	}
	items = append(items, r.drainPendingUnifiedExecItems(threadID, turnID)...)
	items = append(items, reviewRuntimeSessionItems(turnID, output, result, completedAt)...)
	if len(items) > 0 {
		if _, err := r.runtimeAppendItems(session.ThreadID(threadID), items); err != nil {
			r.unifiedExecPersistMu.Unlock()
			r.finishTurnWithErrorAnalytics(threadID, turnID, startedAtMS, err, &turnCompletionAnalyticsContext{
				ConnectionID: connectionID,
				Params:       params,
				RunConfig:    runConfig,
				Result:       result,
				SteerCount:   steerCount,
			})
			return
		}
		_ = r.appendRuntimeRollout(threadID, items, startedAt)
	}
	r.unifiedExecPersistMu.Unlock()
	if usage := tokenUsageFromAgentLoopResult(result); usage != nil {
		usage.ModelContextWindow = positiveInt64Ptr(r.effectiveModelContextWindowForModel(runConfig.Model, params))
		r.notify(NotificationThreadTokenUsageUpdated, &ThreadTokenUsageUpdatedNotification{
			ThreadID:   threadID,
			TurnID:     turnID,
			TokenUsage: *usage,
		})
	}
	r.notifyReviewRuntimeItems(threadID, turnID, items)
	completedAtUnix := completedAt.Unix()
	durationMS := completedAt.UnixMilli() - startedAtMS
	r.finishStateThreadGoalTurn(threadID, turnID, completedAt, model.AgentUsageTotalTokens(result.Usage), nil)
	_ = r.appendRuntimeTurnComplete(threadID, turnID, completedAt, durationMS)
	r.completeTurnRecord(threadID, turnID, TurnStatusCompleted)
	completedTurn := completedTurnNotificationTurn(turnID, TurnStatusCompleted, nil, &record.StartedAt, &completedAtUnix, &durationMS)
	r.notifyTurnCompletedOnce(&TurnCompletedNotification{ThreadID: threadID, Turn: completedTurn})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnCompleted(threadID))
	r.deliverRuntimeAgentCompletion(threadID, agent.AgentMessageStatus{Kind: agent.AgentMessageStatusCompleted, Message: reviewFinalAgentMessage(result)})
	r.emitCodexTurnAnalyticsEvent(ctx, connectionID, params, record, runConfig, result, TurnStatusCompleted, startedAt, completedAt, durationMS, steerCount, nil, nil, nil, nil)
	r.clearActiveDiffTracker(threadID, turnID)
}

func reviewOutputFromAgentLoopResult(result *turn.AgentLoopResult) *review.OutputEvent {
	text := reviewFinalAgentMessage(result)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return review.ParseOutputEvent(text)
}

func reviewFinalAgentMessage(result *turn.AgentLoopResult) string {
	if result == nil {
		return ""
	}
	responses := result.ModelResponses()
	for i := len(responses) - 1; i >= 0; i-- {
		response := responses[i]
		if response == nil {
			continue
		}
		for j := len(response.Items) - 1; j >= 0; j-- {
			item := response.Items[j]
			itemType := strings.TrimSpace(item.Type)
			if itemType != "agent_message" && itemType != "message" {
				continue
			}
			if strings.TrimSpace(item.Text) != "" {
				return item.Text
			}
		}
		if strings.TrimSpace(response.Message) != "" {
			return response.Message
		}
	}
	return ""
}

func reviewRuntimeSessionItems(turnID string, output *review.OutputEvent, result *turn.AgentLoopResult, createdAt time.Time) []session.Item {
	userMessage, assistantMessage := review.ReviewRolloutMessages(output)
	response := (*model.AgentResponse)(nil)
	usage := model.AgentUsage{}
	var profile *turn.Profile
	if result != nil {
		response = result.Response
		usage = result.Usage
		profile = result.TimingProfile
	}
	metadata := appResponseMetadata(turnID, response, &usage, profile)
	exitedReviewText := review.FallbackMessage
	if output != nil {
		exitedReviewText = review.RenderOutputText(output)
	}
	responseID := ""
	if response != nil {
		responseID = response.ResponseID
	}
	return []session.Item{
		{
			ID:        review.ReviewRolloutUserMessageID,
			Type:      "message",
			Role:      "user",
			Text:      userMessage,
			Content:   []session.ContentPart{{Type: "input_text", Text: userMessage}},
			CreatedAt: createdAt,
			Metadata: appTurnMetadata(turnID, map[string]any{
				"kind": "review_rollout_user",
			}),
		},
		{
			ID:        turnID,
			Type:      "exitedReviewMode",
			Text:      exitedReviewText,
			CreatedAt: createdAt,
			Metadata: appTurnMetadata(turnID, map[string]any{
				"kind":   "review_exit",
				"review": exitedReviewText,
			}),
		},
		{
			ID:         review.ReviewRolloutAssistantMessageID,
			Type:       "agent_message",
			Role:       "assistant",
			Text:       assistantMessage,
			CreatedAt:  createdAt,
			Metadata:   metadata,
			ResponseID: responseID,
		},
	}
}

func (r *RuntimeRouter) notifyReviewRuntimeItems(threadID string, turnID string, items []session.Item) {
	if r == nil {
		return
	}
	for i := range items {
		item := items[i]
		threadItem := BuildThreadItem(item)
		payload := threadItemPayload(threadItem)
		switch threadItemWireType(&threadItem) {
		case "exitedReviewMode":
			completedAtMS := item.CreatedAt.UTC().UnixMilli()
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item:        payload,
				ThreadID:    threadID,
				TurnID:      turnID,
				StartedAtMS: completedAtMS,
			})
			r.notify(NotificationItemCompleted, &ItemCompletedNotification{
				Item:          payload,
				ThreadID:      threadID,
				TurnID:        turnID,
				CompletedAtMS: completedAtMS,
			})
		case "agentMessage":
			if strings.TrimSpace(threadItem.Text) != "" {
				r.notify(NotificationAgentMessageDelta, &AgentMessageDeltaNotification{
					ThreadID: threadID,
					TurnID:   turnID,
					ItemID:   threadItem.ID,
					Delta:    threadItem.Text,
				})
			}
			r.notify(NotificationItemCompleted, &ItemCompletedNotification{
				Item:          payload,
				ThreadID:      threadID,
				TurnID:        turnID,
				CompletedAtMS: item.CreatedAt.UTC().UnixMilli(),
			})
		}
	}
}

func shouldNotifyRuntimeItemCompleted(item ThreadItem) bool {
	if evented, _ := item.Data["unified_exec_evented"].(bool); evented {
		return false
	}
	if notified, _ := item.Data["lifecycleNotified"].(bool); notified {
		return false
	}
	if item.Type == "tool_search_call" || item.Type == "tool_search_output" {
		return false
	}
	switch threadItemWireType(&item) {
	case "commandExecution":
		return threadItemCommandStatus(&item) != CommandExecutionInProgress
	case "fileChange":
		return threadItemFileChangeStatus(&item) != PatchApplyInProgress
	case "mcpToolCall":
		return threadItemMCPStatus(&item) != "inProgress"
	default:
		return true
	}
}

func completedTurnNotificationTurn(turnID string, status TurnStatus, appErr *TurnError, startedAt *int64, completedAt *int64, durationMS *int64) Turn {
	return Turn{
		ID:          turnID,
		Items:       []ThreadItem{},
		ItemsView:   TurnItemsNotLoaded,
		Status:      status,
		Error:       appErr,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		DurationMS:  durationMS,
	}
}

func finalAgentMessageSummary(items []ThreadItem) []ThreadItem {
	for i := len(items) - 1; i >= 0; i-- {
		if normalizeThreadItemType(items[i].Type) != "agentMessage" || strings.TrimSpace(items[i].Text) == "" {
			continue
		}
		return []ThreadItem{items[i]}
	}
	return nil
}

func lastAgentMessageFromThreadItems(items []ThreadItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		if normalizeThreadItemType(items[i].Type) == "agentMessage" && strings.TrimSpace(items[i].Text) != "" {
			return strings.TrimSpace(items[i].Text)
		}
	}
	return ""
}

func (r *RuntimeRouter) deliverRuntimeAgentCompletion(threadID string, status agent.AgentMessageStatus) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil || strings.TrimSpace(record.Metadata.AgentPath) == "" || record.ParentThreadID == "" {
		return
	}
	rootID, _ := r.runtimeAgentIdentity(threadID)
	parentPath := "/root"
	if parent, parentErr := r.threadRecord(record.ParentThreadID, true, false); parentErr == nil && parent != nil && strings.TrimSpace(parent.Metadata.AgentPath) != "" {
		parentPath = strings.TrimSpace(parent.Metadata.AgentPath)
	}
	message, ok := agent.FormatInterAgentCompletionMessage(parentPath, record.Metadata.AgentPath, status)
	if ok {
		input := map[string]any{
			"type": "agent_message", "author": record.Metadata.AgentPath, "recipient": parentPath,
			"content": []any{map[string]any{"type": "input_text", "text": message}},
		}
		parentID := string(record.ParentThreadID)
		if active := r.activeRuntimeTurnSnapshot(parentID); active != nil {
			_ = r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: parentID, TurnID: active.ID, InputItems: []any{input}})
		} else {
			r.enqueueRuntimeAgentMessage(parentID, input)
		}
	}
	r.notifyRuntimeAgentActivity(rootID, "Wait completed.")
}

func (r *RuntimeRouter) notifyTurnPlanUpdates(threadID string, turnID string, result *turn.AgentLoopResult) {
	if r == nil || result == nil {
		return
	}
	for i := range result.ToolExecutions {
		update, ok := turnPlanUpdatedNotification(threadID, turnID, &result.ToolExecutions[i])
		if !ok {
			continue
		}
		r.notify(NotificationTurnPlanUpdated, update)
	}
}

func (r *RuntimeRouter) runtimeToolStartedNotifier(threadID string, turnID string, cwd string, unifiedExecEnabled bool) turn.ToolStartedCallback {
	return func(ctx context.Context, invocation *tool.Invocation, startedAt time.Time) {
		if r == nil || invocation == nil {
			return
		}
		if r.networkApproval != nil {
			r.networkApproval.registerActiveCall(threadID, turnID, invocation)
		}
		if item, ok := collaborationStartedThreadItem(invocation, threadID, turnID, startedAt); ok {
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item: threadItemPayload(item), ThreadID: threadID, TurnID: turnID, StartedAtMS: startedAt.UTC().UnixMilli(),
			})
			return
		}
		if item, ok := commandExecutionStartedThreadItem(invocation, turnID, cwd, startedAt); ok {
			// Unified exec owns the canonical command lifecycle for every command,
			// including commands delegated from code mode. Emitting the dispatcher
			// callback as well produces a duplicate item/started notification.
			if unifiedExecEnabled {
				return
			}
			r.attributeCommandExecutionItem(&item)
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item:        threadItemPayload(item),
				ThreadID:    threadID,
				TurnID:      turnID,
				StartedAtMS: startedAt.UTC().UnixMilli(),
			})
			return
		}
		if item, ok := fileChangeStartedThreadItem(invocation, turnID, cwd, startedAt); ok {
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item:        threadItemPayload(item),
				ThreadID:    threadID,
				TurnID:      turnID,
				StartedAtMS: startedAt.UTC().UnixMilli(),
			})
			return
		}
		if isWebSearchInvocation(invocation) {
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item: ThreadItemPayload{
					"id":     firstNonEmpty(invocation.CallID, "web-search-"+safeIdentifier(turnID)),
					"type":   "webSearch",
					"query":  "",
					"action": map[string]any{"type": "other"},
				},
				ThreadID:    threadID,
				TurnID:      turnID,
				StartedAtMS: startedAt.UTC().UnixMilli(),
			})
			return
		}
		if isImageGenerationInvocation(invocation) {
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item: ThreadItemPayload{
					"id":     firstNonEmpty(invocation.CallID, "image-generation-"+safeIdentifier(turnID)),
					"type":   "imageGeneration",
					"status": "in_progress",
				},
				ThreadID:    threadID,
				TurnID:      turnID,
				StartedAtMS: startedAt.UTC().UnixMilli(),
			})
			return
		}
		if item, ok := mcpToolCallStartedThreadItem(invocation, turnID, startedAt); ok {
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item:        threadItemPayload(item),
				ThreadID:    threadID,
				TurnID:      turnID,
				StartedAtMS: startedAt.UTC().UnixMilli(),
			})
			return
		}
		if !isClockSleepInvocation(invocation) {
			return
		}
		durationMS, ok := clockSleepDurationMS(invocation)
		if !ok {
			return
		}
		r.notify(NotificationItemStarted, &ItemStartedNotification{
			Item: ThreadItemPayload{
				"id":         firstNonEmpty(invocation.CallID, "sleep-"+safeIdentifier(turnID)),
				"type":       "sleep",
				"durationMs": durationMS,
			},
			ThreadID:    threadID,
			TurnID:      turnID,
			StartedAtMS: startedAt.UTC().UnixMilli(),
		})
	}
}

func (r *RuntimeRouter) runtimeToolCompletedNotifier(threadID string, turnID string, cwd string, unifiedExecEnabled bool) turn.ToolCompletedCallback {
	return func(_ context.Context, execution *turn.ToolExecutionResult) {
		if r == nil || execution == nil || execution.Invocation == nil {
			return
		}
		if item, ok := collaborationCompletedThreadItem(execution, threadID, turnID); ok {
			completedAt := execution.FinishedAt
			if completedAt.IsZero() {
				completedAt = time.Now().UTC()
			}
			payload := threadItemPayload(item)
			if threadItemWireType(&item) == "subAgentActivity" {
				r.notify(NotificationItemStarted, &ItemStartedNotification{
					Item: payload, ThreadID: threadID, TurnID: turnID, StartedAtMS: completedAt.UTC().UnixMilli(),
				})
			}
			r.notify(NotificationItemCompleted, &ItemCompletedNotification{
				Item: payload, ThreadID: threadID, TurnID: turnID, CompletedAtMS: completedAt.UTC().UnixMilli(),
			})
			return
		}
		if unifiedExecEnabled || execution.Invocation.Source != "code_mode" {
			return
		}
		item, ok := commandExecutionStartedThreadItem(execution.Invocation, turnID, cwd, execution.StartedAt)
		if !ok {
			return
		}
		status := CommandExecutionCompleted
		output := ""
		exitCode := 0
		hasExitCode := false
		if execution.Output != nil {
			output = firstNonEmpty(stringFromAny(execution.Output.Data["hook_response"]), execution.Output.Body)
			if execution.Output.Data != nil {
				if _, ok := execution.Output.Data["exit_code"]; ok {
					exitCode = intFromAny(execution.Output.Data["exit_code"])
					hasExitCode = true
				}
			}
			if !execution.Output.Success {
				status = CommandExecutionFailed
			}
		}
		if hasExitCode && exitCode != 0 {
			status = CommandExecutionFailed
		}
		item.Data["status"] = string(status)
		item.Data["aggregatedOutput"] = output
		item.Data["aggregated_output"] = output
		if hasExitCode {
			item.Data["exitCode"] = exitCode
			item.Data["exit_code"] = exitCode
		}
		completedAt := execution.FinishedAt
		if completedAt.IsZero() {
			completedAt = time.Now().UTC()
		}
		item.Data["completedAtMs"] = completedAt.UTC().UnixMilli()
		item.Data["completed_at_ms"] = completedAt.UTC().UnixMilli()
		r.attributeCommandExecutionItem(&item)
		r.notify(NotificationItemCompleted, &ItemCompletedNotification{
			Item:          threadItemPayload(item),
			ThreadID:      threadID,
			TurnID:        turnID,
			CompletedAtMS: completedAt.UTC().UnixMilli(),
		})
	}
}

func collaborationStartedThreadItem(invocation *tool.Invocation, senderThreadID string, turnID string, startedAt time.Time) (ThreadItem, bool) {
	if invocation == nil || !appCollaborationToolName(invocation.ToolName) {
		return ThreadItem{}, false
	}
	name := strings.TrimSpace(invocation.ToolName.Name)
	versionV2 := strings.TrimSpace(invocation.ToolName.Namespace) == agent.MultiAgentV2Namespace
	if versionV2 && name != "wait_agent" {
		return ThreadItem{}, false
	}
	toolKind, ok := appCollabToolKind(name)
	if !ok {
		return ThreadItem{}, false
	}
	return appCollabAgentThreadItem(invocation, nil, senderThreadID, turnID, string(CollabAgentToolCallInProgress), toolKind, startedAt), true
}

func collaborationCompletedThreadItem(execution *turn.ToolExecutionResult, senderThreadID string, turnID string) (ThreadItem, bool) {
	if execution == nil || execution.Invocation == nil || !appCollaborationToolName(execution.Invocation.ToolName) {
		return ThreadItem{}, false
	}
	name := strings.TrimSpace(execution.Invocation.ToolName.Name)
	versionV2 := strings.TrimSpace(execution.Invocation.ToolName.Namespace) == agent.MultiAgentV2Namespace
	if versionV2 && name != "wait_agent" {
		if activity, ok := appSubAgentActivityFromExecution(execution); ok {
			return ThreadItem{
				ID: firstNonEmpty(execution.Invocation.CallID, "sub-agent-activity-"+safeIdentifier(turnID)), Type: "subAgentActivity", TurnID: turnID,
				CreatedAt: execution.FinishedAt.UTC().UnixMilli(), Data: activity,
			}, true
		}
		return ThreadItem{}, false
	}
	toolKind, ok := appCollabToolKind(name)
	if !ok {
		return ThreadItem{}, false
	}
	status := string(CollabAgentToolCallCompleted)
	if execution.Output == nil || !execution.Output.Success {
		status = string(CollabAgentToolCallFailed)
	}
	return appCollabAgentThreadItem(execution.Invocation, execution.Output, senderThreadID, turnID, status, toolKind, execution.FinishedAt), true
}

func appCollaborationToolName(name tool.ToolName) bool {
	namespace := strings.TrimSpace(name.Namespace)
	return namespace == agent.MultiAgentV1Namespace || namespace == agent.MultiAgentV2Namespace
}

func appCollabToolKind(name string) (CollabAgentTool, bool) {
	switch strings.TrimSpace(name) {
	case "spawn_agent":
		return CollabAgentToolSpawnAgent, true
	case "send_input", "send_message", "followup_task":
		return CollabAgentToolSendInput, true
	case "resume_agent":
		return CollabAgentToolResumeAgent, true
	case "wait_agent":
		return CollabAgentToolWait, true
	case "close_agent", "interrupt_agent":
		return CollabAgentToolCloseAgent, true
	default:
		return "", false
	}
}

func appCollabAgentThreadItem(invocation *tool.Invocation, output *tool.Output, senderThreadID string, turnID string, status string, kind CollabAgentTool, at time.Time) ThreadItem {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	args := map[string]any{}
	if invocation != nil && strings.TrimSpace(invocation.Payload.Arguments) != "" {
		_ = json.Unmarshal([]byte(invocation.Payload.Arguments), &args)
	}
	receivers := appCollabReceiverThreadIDs(args, output)
	prompt := firstNonEmpty(stringFromAny(args["prompt"]), stringFromAny(args["message"]))
	if looksLikeEncryptedCollaborationText(prompt) {
		prompt = ""
	}
	states := map[string]any{}
	if result := appCollabResultMap(output); result != nil {
		if raw, ok := result["status"].(map[string]any); ok {
			for key, value := range raw {
				states[key] = appCollabAgentStateFromAny(value)
			}
		}
	}
	data := map[string]any{
		"tool": string(kind), "status": status, "senderThreadId": senderThreadID,
		"receiverThreadIds": receivers, "agentsStates": states,
	}
	if prompt != "" {
		data["prompt"] = prompt
	}
	return ThreadItem{ID: firstNonEmpty(invocation.CallID, "collab-"+safeIdentifier(turnID)), Type: "collabAgentToolCall", TurnID: turnID, CreatedAt: at.UTC().UnixMilli(), Data: data}
}

func appCollabReceiverThreadIDs(args map[string]any, output *tool.Output) []string {
	values := []string{}
	if targets, ok := args["targets"].([]any); ok {
		for _, target := range targets {
			if value := strings.TrimSpace(stringFromAny(target)); value != "" {
				values = append(values, value)
			}
		}
	}
	for _, key := range []string{"target", "id"} {
		if value := strings.TrimSpace(stringFromAny(args[key])); value != "" {
			values = append(values, value)
		}
	}
	if result := appCollabResultMap(output); result != nil {
		if id := strings.TrimSpace(stringFromAny(result["agent_id"])); id != "" {
			values = []string{id}
		}
	}
	return values
}

func appCollabResultMap(output *tool.Output) map[string]any {
	if output == nil || output.Data == nil || output.Data["result"] == nil {
		return nil
	}
	if result, ok := output.Data["result"].(map[string]any); ok {
		return result
	}
	data, err := json.Marshal(output.Data["result"])
	if err != nil {
		return nil
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil
	}
	return result
}

func appCollabAgentStateFromAny(value any) map[string]any {
	if status, ok := value.(agent.AgentMessageStatus); ok {
		state := map[string]any{"status": appCollabStatus(status.Kind)}
		if strings.TrimSpace(status.Message) != "" {
			state["message"] = status.Message
		}
		return state
	}
	data, _ := json.Marshal(value)
	var status agent.AgentMessageStatus
	if json.Unmarshal(data, &status) == nil && status.Kind != "" {
		return appCollabAgentStateFromAny(status)
	}
	return map[string]any{"status": string(CollabAgentStatusNotFound)}
}

func appCollabStatus(kind agent.AgentMessageStatusKind) string {
	switch kind {
	case agent.AgentMessageStatusPendingInit:
		return string(CollabAgentStatusPendingInit)
	case agent.AgentMessageStatusRunning:
		return string(CollabAgentStatusRunning)
	case agent.AgentMessageStatusInterrupted:
		return string(CollabAgentStatusInterrupted)
	case agent.AgentMessageStatusCompleted:
		return string(CollabAgentStatusCompleted)
	case agent.AgentMessageStatusErrored:
		return string(CollabAgentStatusErrored)
	case agent.AgentMessageStatusShutdown:
		return string(CollabAgentStatusShutdown)
	default:
		return string(CollabAgentStatusNotFound)
	}
}

func appSubAgentActivityFromExecution(execution *turn.ToolExecutionResult) (map[string]any, bool) {
	if execution == nil || execution.Output == nil || execution.Output.Data == nil {
		return nil, false
	}
	raw, ok := execution.Output.Data["subAgentActivity"].(map[string]any)
	if !ok {
		return nil, false
	}
	kind := strings.TrimSpace(stringFromAny(raw["kind"]))
	if kind == "" {
		return nil, false
	}
	return map[string]any{
		"kind":          kind,
		"agentThreadId": firstNonEmpty(stringFromAny(raw["agent_thread_id"]), stringFromAny(raw["agentThreadId"])),
		"agentPath":     firstNonEmpty(stringFromAny(raw["agent_path"]), stringFromAny(raw["agentPath"])),
	}, true
}

func looksLikeEncryptedCollaborationText(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "gAAAA")
}

func (r *RuntimeRouter) runtimeUnifiedExecEventSink(threadID string, turnID string) tool.UnifiedExecEventSink {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if r == nil || threadID == "" || turnID == "" {
		return nil
	}
	return func(event tool.UnifiedExecEvent) {
		callID := strings.TrimSpace(event.CallID)
		if callID == "" {
			return
		}
		processID := strconv.Itoa(event.ProcessID)
		switch event.Kind {
		case tool.UnifiedExecEventBegin:
			if active := r.activeRuntimeTurnStateSnapshot(threadID, turnID); active != nil && active.RunConfig != nil {
				r.rememberUnifiedExecAnalytics(threadID, turnID, callID, active.ConnectionID, active.RunConfig)
			}
			startedAt := event.StartedAt
			if startedAt.IsZero() {
				startedAt = time.Now()
			}
			item := unifiedExecThreadItem(event, CommandExecutionInProgress)
			r.attributeCommandExecutionItem(&item)
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				ThreadID:    threadID,
				TurnID:      turnID,
				Item:        threadItemPayload(item),
				StartedAtMS: startedAt.UTC().UnixMilli(),
			})
		case tool.UnifiedExecEventOutputDelta:
			if event.Output == "" {
				return
			}
			r.notify(NotificationCommandExecutionOutputDelta, &CommandExecutionOutputDeltaNotification{
				ThreadID: threadID,
				TurnID:   turnID,
				ItemID:   callID,
				Delta:    event.Output,
			})
		case tool.UnifiedExecEventTerminalInteraction:
			r.notify(NotificationTerminalInteraction, &TerminalInteractionNotification{
				ThreadID:  threadID,
				TurnID:    turnID,
				ItemID:    callID,
				ProcessID: processID,
				Stdin:     event.Input,
			})
		case tool.UnifiedExecEventEnd:
			status := CommandExecutionCompleted
			if event.ExitCode != 0 {
				status = CommandExecutionFailed
			}
			item := unifiedExecThreadItem(event, status)
			r.attributeCommandExecutionItem(&item)
			// Rust publishes ExecCommandEnd to the client-facing event stream
			// before rollout persistence can delay any other work. In particular,
			// the foreground unified-exec collector waits for this sink to return;
			// doing store/rollout I/O first can deadlock a real app-server turn
			// after outputDelta and leave the TUI without item/completed.
			r.notify(NotificationItemCompleted, &ItemCompletedNotification{
				ThreadID:      threadID,
				TurnID:        turnID,
				Item:          threadItemPayload(item),
				CompletedAtMS: time.Now().UTC().UnixMilli(),
			})
			if analytics, ok := r.takeUnifiedExecAnalytics(threadID, turnID, callID); ok {
				r.emitCommandExecutionAnalyticsEvent(context.Background(), analytics.ConnectionID, threadID, turnID, &item, analytics.RunConfig)
			}
			r.enqueuePendingUnifiedExecItem(threadID, turnID, unifiedExecSessionItem(turnID, item, event))
			// A background process can finish after its originating turn has
			// already drained the pending queue. Persist that late completion on
			// the watcher path; foreground completions remain owned by turn
			// finalization and never block the tool result.
			if r.activeRuntimeTurnStateSnapshot(threadID, turnID) == nil {
				r.persistPendingUnifiedExecItemsAfterTurn(threadID, turnID)
			}
		}
	}
}

func unifiedExecAnalyticsKey(threadID string, turnID string, callID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID) + "\x00" + strings.TrimSpace(callID)
}

func (r *RuntimeRouter) rememberUnifiedExecAnalytics(threadID string, turnID string, callID string, connectionID string, runConfig *appTurnRunConfig) {
	if r == nil || runConfig == nil {
		return
	}
	r.unifiedExecAnalyticsMu.Lock()
	defer r.unifiedExecAnalyticsMu.Unlock()
	if r.unifiedExecAnalytics == nil {
		r.unifiedExecAnalytics = map[string]unifiedExecAnalyticsContext{}
	}
	r.unifiedExecAnalytics[unifiedExecAnalyticsKey(threadID, turnID, callID)] = unifiedExecAnalyticsContext{
		ConnectionID: normalizeConnectionID(connectionID),
		RunConfig:    runConfig,
	}
}

func (r *RuntimeRouter) takeUnifiedExecAnalytics(threadID string, turnID string, callID string) (unifiedExecAnalyticsContext, bool) {
	if r == nil {
		return unifiedExecAnalyticsContext{}, false
	}
	r.unifiedExecAnalyticsMu.Lock()
	defer r.unifiedExecAnalyticsMu.Unlock()
	key := unifiedExecAnalyticsKey(threadID, turnID, callID)
	analytics, ok := r.unifiedExecAnalytics[key]
	delete(r.unifiedExecAnalytics, key)
	return analytics, ok
}

func unifiedExecSessionItem(turnID string, item ThreadItem, event tool.UnifiedExecEvent) session.Item {
	createdAt := time.Time{}
	if !event.StartedAt.IsZero() {
		createdAt = event.StartedAt.Add(event.Duration).UTC()
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	data := cloneAnyMap(item.Data)
	data["unified_exec_evented"] = true
	return session.Item{
		ID:        item.ID,
		Type:      "commandExecution",
		CallID:    item.ID,
		Text:      event.Output,
		CreatedAt: createdAt,
		Data:      data,
		Metadata:  appTurnMetadata(turnID, map[string]any{"source": string(CommandExecutionSourceUnifiedExecStartup)}),
	}
}

func pendingUnifiedExecItemsKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func (r *RuntimeRouter) enqueuePendingUnifiedExecItem(threadID string, turnID string, item session.Item) {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" || strings.TrimSpace(item.ID) == "" {
		return
	}
	key := pendingUnifiedExecItemsKey(threadID, turnID)
	r.unifiedExecPendingMu.Lock()
	defer r.unifiedExecPendingMu.Unlock()
	if r.unifiedExecPending == nil {
		r.unifiedExecPending = map[string][]session.Item{}
	}
	pending := r.unifiedExecPending[key]
	for index := range pending {
		if pending[index].ID == item.ID {
			pending[index] = cloneRuntimeSessionItem(item)
			r.unifiedExecPending[key] = pending
			return
		}
	}
	r.unifiedExecPending[key] = append(pending, cloneRuntimeSessionItem(item))
}

func (r *RuntimeRouter) drainPendingUnifiedExecItems(threadID string, turnID string) []session.Item {
	if r == nil {
		return nil
	}
	key := pendingUnifiedExecItemsKey(threadID, turnID)
	r.unifiedExecPendingMu.Lock()
	pending := cloneRuntimeSessionItems(r.unifiedExecPending[key])
	delete(r.unifiedExecPending, key)
	r.unifiedExecPendingMu.Unlock()
	return pending
}

func (r *RuntimeRouter) persistPendingUnifiedExecItemsAfterTurn(threadID string, turnID string) {
	if r == nil || !r.hasRuntimeThreadStore() {
		r.clearPendingUnifiedExecItems(threadID, turnID)
		return
	}
	r.unifiedExecPersistMu.Lock()
	defer r.unifiedExecPersistMu.Unlock()
	items := r.drainPendingUnifiedExecItems(threadID, turnID)
	if len(items) == 0 {
		return
	}
	if _, err := r.runtimeAppendItems(session.ThreadID(threadID), items); err != nil {
		return
	}
	createdAt := items[0].CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_ = r.appendRuntimeRollout(threadID, items, createdAt)
}

func (r *RuntimeRouter) clearPendingUnifiedExecItems(threadID string, turnID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	r.unifiedExecPendingMu.Lock()
	defer r.unifiedExecPendingMu.Unlock()
	if turnID != "" {
		delete(r.unifiedExecPending, pendingUnifiedExecItemsKey(threadID, turnID))
		return
	}
	prefix := threadID + "\x00"
	for key := range r.unifiedExecPending {
		if strings.HasPrefix(key, prefix) {
			delete(r.unifiedExecPending, key)
		}
	}
}

func unifiedExecThreadItem(event tool.UnifiedExecEvent, status CommandExecutionStatus) ThreadItem {
	command := strings.TrimSpace(event.HookCommand)
	if command == "" {
		command = strings.Join(event.Command, " ")
	}
	commandArgs := append([]string(nil), event.Command...)
	if len(commandArgs) == 0 {
		commandArgs = shell.SplitCommandLine(command)
	}
	data := map[string]any{
		"command":        command,
		"cwd":            event.CWD,
		"processId":      strconv.Itoa(event.ProcessID),
		"process_id":     event.ProcessID,
		"source":         string(CommandExecutionSourceUnifiedExecStartup),
		"status":         string(status),
		"commandActions": commandExecutionActions(commandArgs, command, event.CWD),
	}
	if !event.StartedAt.IsZero() {
		data["startedAtMs"] = event.StartedAt.UTC().UnixMilli()
		data["started_at_ms"] = event.StartedAt.UTC().UnixMilli()
	}
	if status != CommandExecutionInProgress {
		completedAt := event.StartedAt.Add(event.Duration)
		if completedAt.IsZero() {
			completedAt = time.Now()
		}
		data["completedAtMs"] = completedAt.UTC().UnixMilli()
		data["completed_at_ms"] = completedAt.UTC().UnixMilli()
		data["aggregatedOutput"] = event.Output
		data["exitCode"] = event.ExitCode
		data["exit_code"] = event.ExitCode
		data["durationMs"] = event.Duration.Milliseconds()
		data["duration_ms"] = event.Duration.Milliseconds()
	}
	return ThreadItem{
		ID:        event.CallID,
		Type:      "commandExecution",
		TurnID:    "",
		CreatedAt: event.StartedAt.UTC().UnixMilli(),
		Data:      data,
	}
}

func commandExecutionStartedThreadItem(invocation *tool.Invocation, turnID string, cwd string, startedAt time.Time) (ThreadItem, bool) {
	if invocation == nil || !tool.IsShellCommandToolName(invocation.ToolName) || invocation.Payload.Kind != tool.PayloadFunction {
		return ThreadItem{}, false
	}
	var args struct {
		Cmd     string `json:"cmd"`
		Command string `json:"command"`
		CWD     string `json:"cwd"`
		Workdir string `json:"workdir"`
	}
	if strings.TrimSpace(invocation.Payload.Arguments) != "" {
		if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &args); err != nil {
			return ThreadItem{}, false
		}
	}
	command := strings.TrimSpace(firstNonEmpty(args.Cmd, args.Command))
	if command == "" {
		return ThreadItem{}, false
	}
	itemCWD := firstNonEmpty(args.CWD, args.Workdir, cwd)
	if itemCWD != "" {
		legacy := utils.NewLegacyAppPathString(itemCWD)
		if _, absolute := legacy.InferAbsolutePathConvention(); !absolute {
			if abs, err := filepath.Abs(itemCWD); err == nil {
				itemCWD = abs
			}
		}
	}
	callID := firstNonEmpty(invocation.CallID, "command-"+safeIdentifier(turnID))
	return ThreadItem{
		ID:        callID,
		Type:      "commandExecution",
		TurnID:    turnID,
		CreatedAt: startedAt.UTC().UnixMilli(),
		Data: map[string]any{
			"command":        command,
			"cwd":            itemCWD,
			"source":         string(CommandExecutionSourceAgent),
			"status":         string(CommandExecutionInProgress),
			"commandActions": commandExecutionActions(shell.SplitCommandLine(command), command, itemCWD),
		},
	}, true
}

func commandExecutionActions(commandArgs []string, command string, cwd string) []map[string]any {
	paths := shell.ReadPaths(commandArgs)
	if len(paths) == 0 {
		return []map[string]any{{"type": "unknown", "command": command}}
	}
	actions := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		resolved, err := utils.ResolveExecutorPath(cwd, path)
		if err != nil {
			continue
		}
		actions = append(actions, map[string]any{
			"type":    "read",
			"command": command,
			"name":    utils.CrossPlatformBase(path),
			"path":    resolved.Value,
		})
	}
	return actions
}

func fileChangeStartedThreadItem(invocation *tool.Invocation, turnID string, cwd string, startedAt time.Time) (ThreadItem, bool) {
	if invocation == nil || invocation.ToolName.Key() != tool.DefaultApplyPatchToolName || invocation.Payload.Kind != tool.PayloadCustom {
		return ThreadItem{}, false
	}
	action, err := applypatch.Parse(invocation.Payload.Input)
	if err != nil || action == nil || action.IsEmpty() {
		return ThreadItem{}, false
	}
	callID := firstNonEmpty(invocation.CallID, "patch-"+safeIdentifier(turnID))
	return ThreadItem{
		ID:        callID,
		Type:      "fileChange",
		Name:      tool.DefaultApplyPatchToolName,
		TurnID:    turnID,
		CreatedAt: startedAt.UTC().UnixMilli(),
		Data: map[string]any{
			"fileChange": true,
			"status":     string(PatchApplyInProgress),
			"changes":    applyPatchActionFileChangeMapsForCWD(action, cwd),
		},
	}, true
}

func turnPlanUpdatedNotification(threadID string, turnID string, execution *turn.ToolExecutionResult) (*TurnPlanUpdatedNotification, bool) {
	if execution == nil || execution.Invocation == nil || execution.Output == nil {
		return nil, false
	}
	if execution.Invocation.ToolName.Key() != "update_plan" && execution.Output.ToolName.Key() != "update_plan" {
		return nil, false
	}
	if execution.Output.Data == nil {
		return nil, false
	}
	if marker, ok := execution.Output.Data["planUpdate"].(bool); ok && !marker {
		return nil, false
	}
	plan := turnPlanStepsFromAny(execution.Output.Data["plan"])
	if len(plan) == 0 {
		return nil, false
	}
	return &TurnPlanUpdatedNotification{
		ThreadID:    threadID,
		TurnID:      turnID,
		Explanation: threadItemStringPtrFromAnyMap(execution.Output.Data, "explanation"),
		Plan:        plan,
	}, true
}

func turnPlanStepsFromAny(value any) []TurnPlanStep {
	switch typed := value.(type) {
	case nil:
		return nil
	case []tool.PlanItem:
		out := make([]TurnPlanStep, 0, len(typed))
		for i := range typed {
			if step, ok := turnPlanStepFromPlanItem(&typed[i]); ok {
				out = append(out, step)
			}
		}
		return out
	case []any:
		out := make([]TurnPlanStep, 0, len(typed))
		for _, item := range typed {
			if step, ok := turnPlanStepFromAny(item); ok {
				out = append(out, step)
			}
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var decoded []map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil
		}
		out := make([]TurnPlanStep, 0, len(decoded))
		for i := range decoded {
			if step, ok := turnPlanStepFromMap(decoded[i]); ok {
				out = append(out, step)
			}
		}
		return out
	}
}

func turnPlanStepFromAny(value any) (TurnPlanStep, bool) {
	switch typed := value.(type) {
	case tool.PlanItem:
		return turnPlanStepFromPlanItem(&typed)
	case map[string]any:
		return turnPlanStepFromMap(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return TurnPlanStep{}, false
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return TurnPlanStep{}, false
		}
		return turnPlanStepFromMap(decoded)
	}
}

func turnPlanStepFromPlanItem(item *tool.PlanItem) (TurnPlanStep, bool) {
	if item == nil {
		return TurnPlanStep{}, false
	}
	step := strings.TrimSpace(item.Step)
	if step == "" {
		return TurnPlanStep{}, false
	}
	return TurnPlanStep{Step: step, Status: turnPlanStepStatus(item.Status)}, true
}

func turnPlanStepFromMap(item map[string]any) (TurnPlanStep, bool) {
	if item == nil {
		return TurnPlanStep{}, false
	}
	step := strings.TrimSpace(threadItemStringFromAnyMap(item, "step"))
	if step == "" {
		return TurnPlanStep{}, false
	}
	return TurnPlanStep{Step: step, Status: turnPlanStepStatus(threadItemStringFromAnyMap(item, "status"))}, true
}

func turnPlanStepStatus(value any) TurnPlanStepStatus {
	switch strings.TrimSpace(fmt.Sprint(value)) {
	case "pending":
		return TurnPlanStepPending
	case "inProgress", "in_progress":
		return TurnPlanStepInProgress
	case "completed":
		return TurnPlanStepCompleted
	default:
		return TurnPlanStepPending
	}
}

func (r *RuntimeRouter) reserveRuntimeThread(threadID string) error {
	if r == nil {
		return fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	return r.threads.ReserveTurn(threadID)
}

func (r *RuntimeRouter) registerActiveRuntimeTurn(threadID string, turnID string, cancel context.CancelFunc, startedAtMS int64, params *turn.TurnStartParams) error {
	if r == nil {
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	if r.diagnosticsGauges != nil {
		defer r.diagnosticsGauges.track("app.turns.active")()
	}
	return r.threads.RegisterTurn(threadID, turnID, cancel, startedAtMS, params)
}

func (r *RuntimeRouter) registerTrackedActiveRuntimeTurn(threadID string, turnID string, cancel context.CancelFunc, startedAtMS int64, params *turn.TurnStartParams) error {
	if r == nil {
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	return r.threads.RegisterTrackedTurn(threadID, turnID, cancel, startedAtMS, params)
}

func (r *RuntimeRouter) updateActiveRuntimeTurnAnalytics(threadID string, turnID string, connectionID string, runConfig *appTurnRunConfig) {
	if r == nil {
		return
	}
	r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		if strings.TrimSpace(connectionID) != "" {
			active.ConnectionID = normalizeConnectionID(connectionID)
		}
		active.RunConfig = runConfig
	})
}

func (r *RuntimeRouter) noteAcceptedTurnSteer(threadID string, turnID string) {
	if r == nil {
		return
	}
	r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) { active.SteerCount++ })
}

func (r *RuntimeRouter) activeRuntimeTurnSteerCount(threadID string, turnID string) int {
	if r == nil {
		return 0
	}
	active := r.threads.ActiveTurn(threadID)
	if active == nil || active.TurnID != turnID {
		return 0
	}
	return active.SteerCount
}

func (r *RuntimeRouter) activeRuntimeTurnIsReview(threadID string, turnID string) bool {
	if r == nil {
		return false
	}
	active := r.threads.ActiveTurn(threadID)
	if active == nil || active.TurnID != turnID || active.Params == nil {
		return false
	}
	return turnStartReviewRuntime(active.Params)
}

func (r *RuntimeRouter) consumeCompletedRuntimeTurn(threadID string, turnID string) bool {
	if r == nil {
		return false
	}
	if _, ok := r.threads.ConsumeTurn(threadID, turnID, false); !ok {
		return false
	}
	if r.networkApproval != nil {
		r.networkApproval.clearActiveCallsForTurn(threadID, turnID)
	}
	return true
}

func (r *RuntimeRouter) cancelActiveRuntimeTurn(threadID string, turnID string) (*activeRuntimeTurn, bool) {
	if r == nil {
		return nil, false
	}
	active, ok := r.threads.ConsumeTurn(threadID, turnID, true)
	if !ok {
		return nil, false
	}
	if active.Cancel != nil {
		active.Cancel()
	}
	if r.networkApproval != nil {
		r.networkApproval.cancelPendingForTurn(threadID, turnID)
		r.networkApproval.clearActiveCallsForTurn(threadID, turnID)
	}
	r.clearPendingUnifiedExecItems(threadID, turnID)
	return active, true
}

func (r *RuntimeRouter) cancelActiveRuntimeTurnTracked(threadID string, turnID string) (*activeRuntimeTurn, bool) {
	if r == nil {
		return nil, false
	}
	active, ok := r.threads.ConsumeTurnTracked(threadID, turnID, true)
	if !ok {
		return nil, false
	}
	if active.Cancel != nil {
		active.Cancel()
	}
	if r.networkApproval != nil {
		r.networkApproval.cancelPendingForTurn(threadID, turnID)
		r.networkApproval.clearActiveCallsForTurn(threadID, turnID)
	}
	r.clearPendingUnifiedExecItems(threadID, turnID)
	return active, true
}

func (r *RuntimeRouter) clearActiveRuntimeTurn(threadID string, turnID string) {
	if r == nil {
		return
	}
	if _, ok := r.threads.ConsumeTurn(threadID, turnID, true); !ok {
		return
	}
	if r.diagnosticsGauges != nil {
		if v := r.diagnosticsGauges.gauge("app.turns.active"); v.Load() > 0 {
			v.Add(^uint64(0))
		}
	}
	if r.networkApproval != nil {
		r.networkApproval.cancelPendingForTurn(threadID, turnID)
		r.networkApproval.clearActiveCallsForTurn(threadID, turnID)
	}
	r.clearPendingUnifiedExecItems(threadID, turnID)
}

func (r *RuntimeRouter) ensureActiveDiffTrackerLocked(threadID string, turnID string) *runtimeutil.DiffTracker {
	return r.threads.ensureDiffLocked(threadID, turnID)
}

func (r *RuntimeRouter) activeDiffTracker(threadID string, turnID string) *runtimeutil.DiffTracker {
	if r == nil {
		return nil
	}
	return r.threads.DiffTracker(threadID, turnID)
}

func (r *RuntimeRouter) activeUnifiedDiffSnapshot(threadID string, turnID string) string {
	if r == nil {
		return ""
	}
	return r.threads.DiffSnapshot(threadID, turnID)
}

func (r *RuntimeRouter) clearActiveDiffTracker(threadID string, turnID string) {
	if r == nil {
		return
	}
	r.threads.ClearDiff(threadID, turnID)
	r.clearToolItemReviewSummaries(threadID, turnID)
}

func (r *RuntimeRouter) hasRuntimeThreadStore() bool {
	return r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil
}

func (r *RuntimeRouter) appendRuntimeRollout(threadID string, items []session.Item, now time.Time) error {
	return r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return rollout.AppendSessionItems(recorder, items, now)
	})
}

func (r *RuntimeRouter) persistRuntimeTurnPrompt(threadID string, turnID string, params *turn.TurnStartParams, createdAt time.Time) bool {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return false
	}
	item, ok := runtimeUserPromptSessionItem(turnID, params, createdAt)
	if !ok {
		return false
	}
	if _, err := r.runtimeAppendItem(session.ThreadID(threadID), item); err != nil {
		return false
	}
	_ = r.appendRuntimeRollout(threadID, []session.Item{item}, createdAt)
	threadItem := BuildThreadItem(item)
	r.notify(NotificationItemStarted, &ItemStartedNotification{
		Item:        threadItemPayload(threadItem),
		ThreadID:    threadID,
		TurnID:      turnID,
		StartedAtMS: createdAt.UTC().UnixMilli(),
	})
	r.notify(NotificationItemCompleted, &ItemCompletedNotification{
		Item:          threadItemPayload(threadItem),
		ThreadID:      threadID,
		TurnID:        turnID,
		CompletedAtMS: createdAt.UTC().UnixMilli(),
	})
	return true
}

func (r *RuntimeRouter) appendRuntimeTurnStarted(threadID string, turnID string, startedAt time.Time) error {
	return r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendTurnStarted(turnID, startedAt)
	})
}

func (r *RuntimeRouter) appendRuntimeTurnComplete(threadID string, turnID string, completedAt time.Time, durationMS int64) error {
	return r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendTurnComplete(turnID, completedAt, durationMS)
	})
}

func (r *RuntimeRouter) appendRuntimeTurnError(threadID string, message string, now time.Time) error {
	return r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendTurnError(message, now)
	})
}

func (r *RuntimeRouter) appendRuntimeTurnAborted(threadID string, turnID string, reason string, completedAt time.Time, durationMS int64) error {
	return r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendTurnAborted(turnID, reason, completedAt, durationMS)
	})
}

func (r *RuntimeRouter) withRuntimeRollout(threadID string, apply func(*rollout.Recorder) error) error {
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false); ok {
		return nil
	}
	open := func() (*rollout.Recorder, error) { return r.openRuntimeRollout(threadID) }
	if handled, err := r.threads.WithRolloutRecorder(session.ThreadID(threadID), open, apply); handled {
		return err
	}
	recorder, err := open()
	if err != nil || recorder == nil {
		return err
	}
	applyErr := apply(recorder)
	closeErr := recorder.Close()
	if applyErr != nil {
		return applyErr
	}
	return closeErr
}

func (r *RuntimeRouter) openRuntimeRollout(threadID string) (*rollout.Recorder, error) {
	codexHome := r.codexHomeForRollout()
	if codexHome == "" {
		return nil, nil
	}
	var record *session.Record
	var readErr error
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		record, readErr = r.services.ThreadRouter.readThreadRecord(session.ThreadID(threadID), true, true)
	}
	path, err := rollout.FindThreadPath(codexHome, threadID, false)
	if err == nil {
		if recorder, replaced, replaceErr := r.replaceRuntimeSeedRollout(record, path); replaceErr != nil || replaced {
			return recorder, replaceErr
		}
		recorder, resumeErr := rollout.Resume(path)
		if resumeErr == nil {
			r.services.ThreadRouter.configureThreadHistoryRecorder(recorder, session.ThreadID(threadID))
		}
		return recorder, resumeErr
	}
	if r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, nil
	}
	if readErr != nil || record == nil || record.Archived {
		return nil, nil
	}
	recordForRollout := record
	if runtimeSeedRolloutMarked(record) {
		recordForRollout = runtimeSeedRolloutRecord(record)
		r.clearRuntimeSeedRolloutMarker(record)
	}
	if err := r.services.ThreadRouter.createThreadRollout(recordForRollout, recordForRollout.CreatedAt); err != nil {
		return nil, err
	}
	path, err = rollout.FindThreadPath(codexHome, threadID, false)
	if err != nil {
		return nil, nil
	}
	recorder, resumeErr := rollout.Resume(path)
	if resumeErr == nil {
		r.services.ThreadRouter.configureThreadHistoryRecorder(recorder, session.ThreadID(threadID))
	}
	return recorder, resumeErr
}

func (r *RuntimeRouter) replaceRuntimeSeedRollout(record *session.Record, path string) (*rollout.Recorder, bool, error) {
	if r == nil || r.services.ThreadRouter == nil || record == nil || record.Archived || !runtimeSeedRolloutMarked(record) {
		return nil, false, nil
	}
	rolloutRecord, err := rollout.RecordFromPath(path, false)
	if err != nil || !runtimeSeedRolloutMatches(record, rolloutRecord) {
		return nil, false, nil
	}
	recordForRollout := runtimeSeedRolloutRecord(record)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, true, err
	}
	recorder, err := r.services.ThreadRouter.newThreadRolloutRecorder(recordForRollout, recordForRollout.CreatedAt)
	if err != nil {
		return nil, true, err
	}
	r.clearRuntimeSeedRolloutMarker(record)
	return recorder, true, nil
}

func runtimeSeedRolloutMarked(record *session.Record) bool {
	return record != nil && boolFromMap(record.Metadata.Extra, runtimeSeedRolloutExtraKey)
}

func runtimeSeedRolloutRecord(record *session.Record) *session.Record {
	clone := cloneRuntimeSessionRecord(record)
	if clone == nil {
		return nil
	}
	clone.Items = nil
	clone.Metadata.RolloutTurns = nil
	clone.Metadata.Extra = ensureRecordExtra(clone.Metadata.Extra)
	delete(clone.Metadata.Extra, runtimeSeedRolloutExtraKey)
	return clone
}

func runtimeSeedRolloutMatches(record *session.Record, rolloutRecord *session.Record) bool {
	if record == nil || rolloutRecord == nil || len(record.Items) != 1 || len(rolloutRecord.Items) != 1 || len(rolloutRecord.Metadata.RolloutTurns) != 0 {
		return false
	}
	seed := &record.Items[0]
	item := &rolloutRecord.Items[0]
	return seed.ID == item.ID &&
		seed.Role == item.Role &&
		seed.Text == item.Text &&
		runtimeSessionItemTurnID(seed, 0) == runtimeSessionItemTurnID(item, 0)
}

func (r *RuntimeRouter) clearRuntimeSeedRolloutMarker(record *session.Record) {
	if r == nil || record == nil || !runtimeSeedRolloutMarked(record) {
		return
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	delete(record.Metadata.Extra, runtimeSeedRolloutExtraKey)
	_ = r.runtimeSaveThreadRecord(record)
}

func (r *RuntimeRouter) appendRuntimeCompacted(threadID string, message string, replacement []session.Item, now time.Time) error {
	items := make([]rollout.Item, 0, len(replacement))
	for i := range replacement {
		item := rollout.ItemFromSessionItem(&replacement[i])
		if item == nil {
			continue
		}
		items = append(items, *item)
	}
	return r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendCompacted(message, items, now)
	})
}

func (r *RuntimeRouter) codexHomeForRollout() string {
	if r == nil {
		return ""
	}
	if r.services.Config != nil {
		if home := strings.TrimSpace(r.services.Config.CodexHome()); home != "" {
			return home
		}
	}
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		return codexHomeFromSessionStore(r.services.ThreadRouter.store)
	}
	return strings.TrimSpace(r.services.DefaultCWD)
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func stringPointerIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

type turnCompletionAnalyticsContext struct {
	ConnectionID                         string
	Params                               *turn.TurnStartParams
	RunConfig                            *appTurnRunConfig
	Result                               *turn.AgentLoopResult
	SteerCount                           int
	TurnError                            CodexErrorInfo
	CodexErrorKind                       *string
	CodexErrorHTTPStatusCode             *uint16
	ExplicitClientInterruptRequestedAtMS *uint64
}

func analyticsContextFromActiveRuntimeTurn(active *activeRuntimeTurn) *turnCompletionAnalyticsContext {
	if active == nil || active.Params == nil || active.RunConfig == nil {
		return nil
	}
	return &turnCompletionAnalyticsContext{
		ConnectionID: active.ConnectionID,
		Params:       cloneTurnStartParams(active.Params),
		RunConfig:    active.RunConfig,
		SteerCount:   active.SteerCount,
	}
}

func (r *RuntimeRouter) finishTurnWithError(threadID string, turnID string, startedAtMS int64, err error) {
	r.finishTurnWithErrorAnalytics(threadID, turnID, startedAtMS, err, nil)
}

func (r *RuntimeRouter) finishTurnWithErrorAnalytics(threadID string, turnID string, startedAtMS int64, err error, analytics *turnCompletionAnalyticsContext) {
	if r == nil {
		return
	}
	if err == nil {
		err = fmt.Errorf("turn failed")
	}
	now := time.Now().UTC()
	completedAt := now.Unix()
	durationMS := now.UnixMilli() - startedAtMS
	errorFields := turnAnalyticsErrorFieldsFromError(err)
	appErr := &TurnError{Message: err.Error(), CodexErrorInfo: errorFields.TurnError}
	if analytics != nil {
		analytics.TurnError = errorFields.TurnError
		analytics.CodexErrorKind = errorFields.CodexErrorKind
		analytics.CodexErrorHTTPStatusCode = errorFields.HTTPStatusCode
	}
	_ = r.appendRuntimeTurnError(threadID, err.Error(), now)
	tokenDelta := int64(0)
	if analytics != nil && analytics.Result != nil {
		tokenDelta = model.AgentUsageTotalTokens(analytics.Result.Usage)
	}
	r.finishStateThreadGoalTurn(threadID, turnID, now, tokenDelta, errorFields.TurnError)
	_ = r.appendRuntimeTurnComplete(threadID, turnID, now, durationMS)
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.completeTurnRecord(threadID, turnID, TurnStatusFailed)
	r.notify(NotificationError, &ErrorNotification{
		Error:     *appErr,
		WillRetry: false,
		ThreadID:  threadID,
		TurnID:    turnID,
	})
	r.notifyTurnCompletedOnce(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     completedTurnNotificationTurn(turnID, TurnStatusFailed, appErr, nil, &completedAt, &durationMS),
	})
	r.notifyThreadStatus(r.requireThreadStatus().NoteSystemError(threadID))
	r.deliverRuntimeAgentCompletion(threadID, agent.AgentMessageStatus{Kind: agent.AgentMessageStatusErrored, Message: err.Error()})
	r.emitTurnCompletionAnalytics(context.Background(), analytics, turnID, TurnStatusFailed, startedAtMS, now, durationMS)
	r.clearActiveDiffTracker(threadID, turnID)
}

func (r *RuntimeRouter) finishTurnInterrupted(threadID string, turnID string, startedAtMS int64) {
	r.finishTurnInterruptedAnalytics(threadID, turnID, startedAtMS, nil)
}

func (r *RuntimeRouter) finishTurnInterruptedAnalytics(threadID string, turnID string, startedAtMS int64, analytics *turnCompletionAnalyticsContext) {
	if r == nil {
		return
	}
	// Rust 509565820f: interrupting a turn stops active code-mode cells when
	// the code_mode_interrupt feature is enabled, keeping the session alive.
	if r.codeModeInterruptEnabledForThread(threadID) {
		if runtime := r.codeModeRuntimeForThread(threadID); runtime != nil {
			runtime.InterruptActiveCells()
		}
	}
	now := time.Now().UTC()
	completedAt := now.Unix()
	durationMS := now.UnixMilli() - startedAtMS
	r.persistInterruptedTurnMarker(threadID, turnID, now)
	r.finishStateThreadGoalTurn(threadID, turnID, now, 0, nil)
	_ = r.appendRuntimeTurnAborted(threadID, turnID, "interrupted", now, durationMS)
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.completeTurnRecord(threadID, turnID, TurnStatusInterrupted)
	r.notifyTurnCompletedOnce(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     completedTurnNotificationTurn(turnID, TurnStatusInterrupted, nil, nil, &completedAt, &durationMS),
	})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnInterrupted(threadID))
	r.deliverRuntimeAgentCompletion(threadID, agent.AgentMessageStatus{Kind: agent.AgentMessageStatusInterrupted})
	r.emitTurnCompletionAnalytics(context.Background(), analytics, turnID, TurnStatusInterrupted, startedAtMS, now, durationMS)
	r.clearActiveDiffTracker(threadID, turnID)
}

func (r *RuntimeRouter) finishReviewRuntimeInterrupted(threadID string, turnID string, startedAtMS int64, analytics *turnCompletionAnalyticsContext) {
	if r == nil {
		return
	}
	now := time.Now().UTC()
	items := reviewRuntimeSessionItems(turnID, nil, nil, now)
	if len(items) > 0 {
		if _, err := r.runtimeAppendItems(session.ThreadID(threadID), items); err == nil {
			_ = r.appendRuntimeRollout(threadID, items, time.UnixMilli(startedAtMS).UTC())
			r.notifyReviewRuntimeItems(threadID, turnID, items)
		}
	}
	completedAt := now.Unix()
	durationMS := now.UnixMilli() - startedAtMS
	r.finishStateThreadGoalTurn(threadID, turnID, now, 0, nil)
	_ = r.appendRuntimeTurnAborted(threadID, turnID, "interrupted", now, durationMS)
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.completeTurnRecord(threadID, turnID, TurnStatusInterrupted)
	r.notifyTurnCompletedOnce(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     completedTurnNotificationTurn(turnID, TurnStatusInterrupted, nil, nil, &completedAt, &durationMS),
	})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnInterrupted(threadID))
	r.emitTurnCompletionAnalytics(context.Background(), analytics, turnID, TurnStatusInterrupted, startedAtMS, now, durationMS)
	r.clearActiveDiffTracker(threadID, turnID)
}

func (r *RuntimeRouter) finishReviewRuntimeFallbackCompleted(threadID string, turnID string, recordStartedAt int64, startedAtMS int64, analytics *turnCompletionAnalyticsContext) {
	if r == nil {
		return
	}
	now := time.Now().UTC()
	items := reviewRuntimeSessionItems(turnID, nil, nil, now)
	if len(items) > 0 {
		if _, err := r.runtimeAppendItems(session.ThreadID(threadID), items); err == nil {
			_ = r.appendRuntimeRollout(threadID, items, time.UnixMilli(startedAtMS).UTC())
			r.notifyReviewRuntimeItems(threadID, turnID, items)
		}
	}
	completedAt := now.Unix()
	durationMS := now.UnixMilli() - startedAtMS
	r.finishStateThreadGoalTurn(threadID, turnID, now, 0, nil)
	_ = r.appendRuntimeTurnComplete(threadID, turnID, now, durationMS)
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.completeTurnRecord(threadID, turnID, TurnStatusCompleted)
	completedTurn := completedTurnNotificationTurn(turnID, TurnStatusCompleted, nil, &recordStartedAt, &completedAt, &durationMS)
	r.notifyTurnCompletedOnce(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     completedTurn,
	})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnCompleted(threadID))
	r.emitTurnCompletionAnalytics(context.Background(), analytics, turnID, TurnStatusCompleted, startedAtMS, now, durationMS)
	r.clearActiveDiffTracker(threadID, turnID)
}

func (r *RuntimeRouter) emitTurnCompletionAnalytics(ctx context.Context, analytics *turnCompletionAnalyticsContext, turnID string, status TurnStatus, startedAtMS int64, completedAt time.Time, durationMS int64) {
	if r == nil || analytics == nil || analytics.Params == nil || analytics.RunConfig == nil {
		return
	}
	startedAt := time.UnixMilli(startedAtMS).UTC()
	record := &turn.TurnRecord{ID: turnID}
	r.emitCodexTurnAnalyticsEvent(ctx, analytics.ConnectionID, analytics.Params, record, analytics.RunConfig, analytics.Result, status, startedAt, completedAt, durationMS, analytics.SteerCount, analytics.TurnError, analytics.CodexErrorKind, analytics.CodexErrorHTTPStatusCode, analytics.ExplicitClientInterruptRequestedAtMS)
}

type turnAnalyticsErrorFields struct {
	TurnError      CodexErrorInfo
	CodexErrorKind *string
	HTTPStatusCode *uint16
}

func turnAnalyticsErrorFieldsFromError(err error) turnAnalyticsErrorFields {
	if err == nil {
		return turnAnalyticsErrorFields{}
	}
	var apiErr *codexapi.APIError
	if errors.As(err, &apiErr) {
		return turnAnalyticsErrorFieldsFromAPIError(apiErr)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return turnAnalyticsErrorFields{TurnError: "other", CodexErrorKind: stringPtrIfNotEmpty("timeout")}
	case errors.Is(err, context.Canceled):
		return turnAnalyticsErrorFields{TurnError: "other", CodexErrorKind: stringPtrIfNotEmpty("interrupted")}
	case errors.Is(err, turn.ErrInvalidTurnRequest),
		errors.Is(err, ErrInvalidRequest),
		errors.Is(err, ErrJSONRPCInvalidRequest),
		errors.Is(err, model.ErrInvalidAgentRequest),
		errors.Is(err, session.ErrInvalidThreadID),
		errors.Is(err, tool.ErrToolInvalidCall),
		errors.Is(err, tool.ErrToolNotFound):
		return turnAnalyticsErrorFields{TurnError: "badRequest", CodexErrorKind: stringPtrIfNotEmpty("invalid_request")}
	case errors.Is(err, sandbox.ErrInvalidSandboxRunRequest),
		errors.Is(err, sandbox.ErrInvalidPermissionProfileRequest),
		errors.Is(err, sandbox.ErrInvalidWindowsSandboxRequest):
		return turnAnalyticsErrorFields{TurnError: "sandboxError", CodexErrorKind: stringPtrIfNotEmpty("sandbox")}
	default:
		return turnAnalyticsErrorFields{}
	}
}

func turnAnalyticsErrorFieldsFromAPIError(err *codexapi.APIError) turnAnalyticsErrorFields {
	if err == nil {
		return turnAnalyticsErrorFields{}
	}
	details := err.Details()
	status := uint16PtrFromHTTPStatus(details.Status)
	fields := func(info CodexErrorInfo, kind string) turnAnalyticsErrorFields {
		return turnAnalyticsErrorFields{TurnError: info, CodexErrorKind: stringPtrIfNotEmpty(kind), HTTPStatusCode: status}
	}
	switch details.Kind {
	case codexapi.ErrorContextWindowExceeded:
		return fields("contextWindowExceeded", "context_window_exceeded")
	case codexapi.ErrorQuotaExceeded:
		return fields("usageLimitExceeded", "quota_exceeded")
	case codexapi.ErrorUsageNotIncluded:
		return fields("usageLimitExceeded", "usage_not_included")
	case codexapi.ErrorRateLimit:
		return fields("usageLimitExceeded", "usage_limit_reached")
	case codexapi.ErrorServerOverloaded:
		return fields("serverOverloaded", "server_overloaded")
	case codexapi.ErrorCyberPolicy:
		return fields("cyberPolicy", "cyber_policy")
	case codexapi.ErrorInvalidRequest:
		return fields("badRequest", "invalid_request")
	case codexapi.ErrorRetryable:
		return fields(codexErrorInfoWithHTTPStatus("responseTooManyFailedAttempts", status), "retry_limit")
	case codexapi.ErrorTransport:
		return fields(codexErrorInfoWithHTTPStatus("httpConnectionFailed", status), "connection_failed")
	case codexapi.ErrorStream:
		return fields(codexErrorInfoWithHTTPStatus("responseStreamConnectionFailed", status), "response_stream_failed")
	case codexapi.ErrorAPI:
		return turnAnalyticsErrorFieldsFromAPIStatus(details.Status)
	default:
		return turnAnalyticsErrorFields{}
	}
}

func turnAnalyticsErrorFieldsFromAPIStatus(status int) turnAnalyticsErrorFields {
	httpStatus := uint16PtrFromHTTPStatus(status)
	fields := func(info CodexErrorInfo, kind string) turnAnalyticsErrorFields {
		return turnAnalyticsErrorFields{TurnError: info, CodexErrorKind: stringPtrIfNotEmpty(kind), HTTPStatusCode: httpStatus}
	}
	switch status {
	case http.StatusBadRequest:
		return fields("badRequest", "invalid_request")
	case http.StatusUnauthorized, http.StatusForbidden:
		return fields("unauthorized", "unexpected_status")
	case http.StatusTooManyRequests:
		return fields("usageLimitExceeded", "usage_limit_reached")
	case http.StatusServiceUnavailable:
		return fields("serverOverloaded", "server_overloaded")
	default:
		if status >= 500 {
			return fields("internalServerError", "internal_server_error")
		}
		if status > 0 {
			return fields("other", "unexpected_status")
		}
		return turnAnalyticsErrorFields{}
	}
}

func codexErrorInfoWithHTTPStatus(kind string, status *uint16) CodexErrorInfo {
	value := any(nil)
	if status != nil {
		value = *status
	}
	return map[string]any{kind: map[string]any{"httpStatusCode": value}}
}

func uint16PtrFromHTTPStatus(status int) *uint16 {
	if status <= 0 || status > 65535 {
		return nil
	}
	value := uint16(status)
	return &value
}

func (r *RuntimeRouter) persistInterruptedTurnMarker(threadID string, turnID string, now time.Time) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return
	}
	item := interruptedTurnMarkerSessionItem(turnID, now)
	if _, err := r.runtimeAppendItem(session.ThreadID(threadID), item); err != nil {
		return
	}
	_ = r.appendRuntimeRollout(threadID, []session.Item{item}, now)
}

func interruptedTurnMarkerSessionItem(turnID string, now time.Time) session.Item {
	const guidance = "The user interrupted the previous turn on purpose. Any running unified exec processes may still be running in the background. If any tools/commands were aborted, they may have partially executed."
	text := "<turn_aborted>\n" + guidance + "\n</turn_aborted>"
	return session.Item{
		ID:        "turn-aborted-" + safeIdentifier(turnID),
		Type:      "message",
		Role:      "user",
		Text:      text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: now,
		Metadata: map[string]any{
			"kind":   "turn_aborted",
			"turnId": turnID,
		},
	}
}

func (r *RuntimeRouter) completeTurnRecord(threadID string, turnID string, status TurnStatus) {
	if r == nil || r.services.Turns == nil {
		return
	}
	_ = r.services.Turns.Complete(&turn.TurnCompleteParams{
		ThreadID: threadID,
		TurnID:   turnID,
		Status:   string(status),
	})
}

func (r *RuntimeRouter) emitTurnRuntimeError(threadID string, turnID string, err error) {
	if r == nil || err == nil {
		return
	}
	r.finishTurnWithError(threadID, turnID, time.Now().UTC().UnixMilli(), err)
}

func (r *RuntimeRouter) notifyThreadStatus(notification *ThreadStatusNotification) {
	if r == nil || notification == nil {
		return
	}
	r.notify(NotificationThreadStatusChanged, &ThreadStatusChangedNotification{
		ThreadID: notification.ThreadID,
		Status:   notification.Status,
	})
}

func (r *RuntimeRouter) notifyTurnDiffFromSessionItem(threadID string, turnID string, item *session.Item) {
	if r == nil || item == nil || item.Data == nil {
		return
	}
	if !sessionItemHasSuccessfulFileChange(item) {
		return
	}
	changes := runtimeFileChangesFromAny(firstNonNil(item.Data["appliedChanges"], item.Data["applied_changes"]))
	if len(changes) == 0 {
		return
	}
	tracker := r.activeDiffTracker(threadID, turnID)
	if tracker == nil {
		return
	}
	hadDiff := tracker.UnifiedDiff() != nil
	tracker.Track(runtimeFileChangeEnvironmentID(item), changes, true)
	diff := tracker.UnifiedDiff()
	if !hadDiff && diff == nil {
		return
	}
	unifiedDiff := ""
	if diff != nil {
		unifiedDiff = *diff
	}
	r.notify(NotificationTurnDiffUpdated, &TurnDiffUpdatedNotification{
		ThreadID: threadID,
		TurnID:   turnID,
		Diff:     unifiedDiff,
	})
}

func sessionItemHasSuccessfulFileChange(item *session.Item) bool {
	if item == nil || item.Data == nil {
		return false
	}
	if success, ok := item.Data["success"].(bool); ok && !success {
		return false
	}
	if status, ok := item.Data["status"].(string); ok && strings.TrimSpace(status) != "" && status != string(PatchApplyCompleted) {
		return false
	}
	if marker, ok := item.Data["fileChange"].(bool); ok && marker {
		return true
	}
	if marker, ok := item.Data["file_change"].(bool); ok && marker {
		return true
	}
	return strings.TrimSpace(item.Name) == "apply_patch"
}

func runtimeFileChangeEnvironmentID(item *session.Item) string {
	if item == nil || item.Data == nil {
		return ""
	}
	for _, key := range []string{"environmentId", "environment_id"} {
		if value, ok := item.Data[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func runtimeFileChangesFromAny(value any) []runtimeutil.FileChange {
	switch typed := value.(type) {
	case nil:
		return nil
	case []runtimeutil.FileChange:
		return append([]runtimeutil.FileChange(nil), typed...)
	case []map[string]any:
		changes := make([]runtimeutil.FileChange, 0, len(typed))
		for _, change := range typed {
			if converted, ok := runtimeFileChangeFromMap(change); ok {
				changes = append(changes, converted)
			}
		}
		return changes
	case []any:
		changes := make([]runtimeutil.FileChange, 0, len(typed))
		for _, change := range typed {
			if converted, ok := runtimeFileChangeFromAny(change); ok {
				changes = append(changes, converted)
			}
		}
		return changes
	default:
		if converted, ok := runtimeFileChangeFromAny(value); ok {
			return []runtimeutil.FileChange{converted}
		}
		return nil
	}
}

func runtimeFileChangeFromAny(value any) (runtimeutil.FileChange, bool) {
	switch typed := value.(type) {
	case runtimeutil.FileChange:
		return typed, true
	case map[string]any:
		return runtimeFileChangeFromMap(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return runtimeutil.FileChange{}, false
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return runtimeutil.FileChange{}, false
		}
		return runtimeFileChangeFromMap(decoded)
	}
}

func runtimeFileChangeFromMap(change map[string]any) (runtimeutil.FileChange, bool) {
	if change == nil {
		return runtimeutil.FileChange{}, false
	}
	path := threadItemStringFromAnyMap(change, "path")
	if strings.TrimSpace(path) == "" {
		return runtimeutil.FileChange{}, false
	}
	out := runtimeutil.FileChange{
		Path:       path,
		OldContent: threadItemStringFromAnyMap(change, "oldContent", "old_content"),
		NewContent: threadItemStringFromAnyMap(change, "newContent", "new_content"),
	}
	switch threadItemPatchChangeKindType(threadItemStringFromAnyMap(change, "kind")) {
	case "add":
		out.Kind = runtimeutil.ChangeAdd
	case "delete":
		out.Kind = runtimeutil.ChangeDelete
	default:
		out.Kind = runtimeutil.ChangeUpdate
	}
	if movePath := threadItemStringFromAnyMap(change, "movePath", "move_path"); strings.TrimSpace(movePath) != "" {
		out.MovePath = &movePath
	}
	if overwritten, ok := change["overwrittenContent"].(string); ok {
		out.OverwrittenContent = &overwritten
	} else if overwritten, ok := change["overwritten_content"].(string); ok {
		out.OverwrittenContent = &overwritten
	}
	return out, true
}

func (r *RuntimeRouter) sessionItemsForTurn(turnID string, params *turn.TurnStartParams, result *turn.AgentLoopResult, createdAt time.Time) []session.Item {
	if params == nil {
		return nil
	}
	items := []session.Item{}
	if item, ok := runtimeUserPromptSessionItem(turnID, params, createdAt); ok {
		items = append(items, item)
	}
	if result == nil {
		return items
	}
	responses := result.ModelResponses()
	executionIndex := 0
	if len(responses) > 0 {
		planMode := turnStartPlanMode(params)
		planItemAdded := false
		for responseIndex, response := range responses {
			if response == nil {
				continue
			}
			usage := response.Usage
			if response == result.Response && usage == (model.AgentUsage{}) {
				usage = result.Usage
			}
			metadata := appResponseMetadata(turnID, response, &usage, result.TimingProfile)
			fallbackAssistantID := fallbackAssistantSessionItemID(turnID, responseIndex)
			toolItemCount := 0
			for i := range response.Items {
				item := response.Items[i]
				if isAppToolItem(&item) {
					toolItemCount++
					continue
				}
				if item.Type == "image_generation_call" {
					if imageItem, ok := r.sessionItemForImageGeneration(params.ThreadID, &item, createdAt, metadata, response.ResponseID); ok {
						items = append(items, imageItem)
						if instructions, ok := imageGenerationInstructionsSessionItem(turnID, &imageItem, metadata); ok {
							items = append(items, instructions)
						}
					}
					continue
				}
				if item.Type == "web_search_call" {
					action, query := appWebSearchActionFromAgentItem(&item)
					raw, _ := json.Marshal(&item)
					items = append(items, session.Item{
						ID:         firstNonEmpty(item.ID, item.CallID, "web-search-"+safeIdentifier(turnID)),
						Type:       "webSearch",
						Text:       query,
						CreatedAt:  createdAt,
						Data:       map[string]any{"query": query, "action": action},
						Metadata:   metadata,
						Raw:        raw,
						ResponseID: response.ResponseID,
					})
					continue
				}
				text := firstNonEmpty(item.Text, response.Message)
				if strings.TrimSpace(text) == "" && item.Type != "reasoning" {
					continue
				}
				itemType := firstNonEmpty(item.Type, "agent_message")
				if itemType == "message" {
					itemType = "agent_message"
				}
				if planMode && itemType == "agent_message" {
					visibleText, planText, hasPlan := splitProposedPlanText(text)
					if strings.TrimSpace(visibleText) != "" {
						raw, _ := json.Marshal(&item)
						items = append(items, session.Item{
							ID:         firstNonEmpty(item.ID, fallbackAssistantID),
							Type:       "agent_message",
							Role:       "assistant",
							Text:       visibleText,
							CreatedAt:  createdAt,
							Data:       cloneAnyMap(item.Data),
							Metadata:   cloneAnyMap(metadata),
							Raw:        raw,
							ResponseID: response.ResponseID,
						})
					}
					if hasPlan && !planItemAdded {
						planItemAdded = true
						items = append(items, session.Item{
							ID:         safeIdentifier(turnID) + "-plan",
							Type:       "plan",
							Text:       planText,
							CreatedAt:  createdAt,
							Metadata:   cloneAnyMap(metadata),
							ResponseID: response.ResponseID,
						})
					}
					continue
				}
				role := "assistant"
				if itemType == "reasoning" {
					role = ""
				}
				raw, _ := json.Marshal(&item)
				items = append(items, session.Item{
					ID:         firstNonEmpty(item.ID, fallbackAssistantID),
					Type:       itemType,
					Role:       role,
					Text:       text,
					CreatedAt:  createdAt,
					Data:       cloneAnyMap(item.Data),
					Metadata:   cloneAnyMap(metadata),
					Raw:        raw,
					ResponseID: response.ResponseID,
				})
			}
			if len(response.Items) == 0 && strings.TrimSpace(response.Message) != "" {
				if planMode {
					visibleText, planText, hasPlan := splitProposedPlanText(response.Message)
					if strings.TrimSpace(visibleText) != "" {
						items = append(items, session.Item{
							ID:         fallbackAssistantID,
							Type:       "agent_message",
							Role:       "assistant",
							Text:       visibleText,
							CreatedAt:  createdAt,
							Metadata:   cloneAnyMap(metadata),
							ResponseID: response.ResponseID,
						})
					}
					if hasPlan && !planItemAdded {
						planItemAdded = true
						items = append(items, session.Item{
							ID:         safeIdentifier(turnID) + "-plan",
							Type:       "plan",
							Text:       planText,
							CreatedAt:  createdAt,
							Metadata:   cloneAnyMap(metadata),
							ResponseID: response.ResponseID,
						})
					}
				} else {
					items = append(items, session.Item{
						ID:         fallbackAssistantID,
						Type:       "agent_message",
						Role:       "assistant",
						Text:       response.Message,
						CreatedAt:  createdAt,
						Metadata:   cloneAnyMap(metadata),
						ResponseID: response.ResponseID,
					})
				}
			}
			toolExecutions := toolExecutionsForResponse(result.ToolExecutions, executionIndex, toolItemCount)
			for i := range toolExecutions {
				if isWebSearchExecution(&toolExecutions[i]) || isImageGenerationExecution(&toolExecutions[i]) {
					continue
				}
				if item, ok := sessionItemForClockSleepExecution(turnID, &toolExecutions[i], createdAt); ok {
					items = append(items, item)
					continue
				}
				if item, ok := sessionItemForAppToolCall(turnID, &toolExecutions[i], createdAt, metadata); ok {
					items = append(items, item)
				}
			}
			for i := range toolExecutions {
				if isClockSleepExecution(&toolExecutions[i]) {
					continue
				}
				if item, ok := sessionItemForWebSearchExecution(turnID, &toolExecutions[i], createdAt, metadata); ok {
					items = append(items, item)
					continue
				}
				if item, ok := sessionItemForStandaloneImageGenerationExecution(turnID, &toolExecutions[i], createdAt, metadata); ok {
					items = append(items, item)
					if instructions, ok := imageGenerationInstructionsSessionItem(turnID, &item, metadata); ok {
						items = append(items, instructions)
					}
					continue
				}
				if item, ok := sessionItemForAppToolOutput(turnID, &toolExecutions[i], createdAt, metadata); ok {
					items = append(items, item)
				}
				if item, ok := sessionItemForAppCollaborationPresentation(turnID, &toolExecutions[i], createdAt, metadata); ok {
					items = append(items, item)
				}
			}
			executionIndex += len(toolExecutions)
		}
	}
	for executionIndex < len(result.ToolExecutions) {
		items = append(items, sessionItemsForAppToolExecution(turnID, &result.ToolExecutions[executionIndex], createdAt)...)
		executionIndex++
	}
	return items
}

func (r *RuntimeRouter) sessionItemForImageGeneration(threadID string, item *model.AgentItem, createdAt time.Time, metadata map[string]any, responseID string) (session.Item, bool) {
	if item == nil || item.Type != "image_generation_call" {
		return session.Item{}, false
	}
	itemID := strings.TrimSpace(item.ID)
	if itemID == "" {
		itemID = "image-generation-" + safeIdentifier(threadID)
	}
	data := cloneAnyMap(item.Data)
	if data == nil {
		data = map[string]any{}
	}
	result := firstNonEmpty(stringFromMap(data, "result"), item.Text)
	status := model.NormalizeImageGenerationStatus(firstNonEmpty(item.Status, stringFromMap(data, "status")), result)
	revisedPrompt := firstNonEmpty(stringFromMap(data, "revisedPrompt"), stringFromMap(data, "revised_prompt"))
	data["status"] = status
	data["result"] = result
	if revisedPrompt != "" {
		data["revisedPrompt"] = revisedPrompt
		data["revised_prompt"] = revisedPrompt
	}
	if strings.TrimSpace(result) != "" {
		if codexHome := r.codexHomeForImageGeneration(); codexHome != "" {
			if savedPath, err := eventmap.SaveImageGenerationResult(codexHome, threadID, itemID, result); err == nil {
				data["savedPath"] = savedPath
				data["saved_path"] = savedPath
			}
		}
	}
	raw, _ := json.Marshal(item)
	return session.Item{
		ID:         itemID,
		Type:       "imageGeneration",
		Status:     status,
		Text:       revisedPrompt,
		CreatedAt:  createdAt,
		Data:       data,
		Metadata:   cloneAnyMap(metadata),
		Raw:        raw,
		ResponseID: responseID,
	}, true
}

func (r *RuntimeRouter) codexHomeForImageGeneration() string {
	if r == nil {
		return ""
	}
	if r.services.Config != nil {
		if codexHome := strings.TrimSpace(r.services.Config.CodexHome()); codexHome != "" {
			return codexHome
		}
	}
	if r.services.ThreadRouter != nil {
		return strings.TrimSpace(codexHomeFromSessionStore(r.services.ThreadRouter.store))
	}
	return ""
}

const (
	imageGenerationInstructionsKind = "image_generation_instructions"
	skillInstructionsKind           = "skill_instructions"
)

func skillInstructionSessionItemsForTurn(turnID string, inputItems []any, createdAt time.Time) []session.Item {
	if len(inputItems) == 0 {
		return nil
	}
	items := make([]session.Item, 0, len(inputItems))
	for i, item := range inputItems {
		role, text, ok := skillInstructionInputItemText(item)
		if !ok {
			continue
		}
		metadata := appTurnMetadata(turnID, map[string]any{"kind": skillInstructionsKind})
		items = append(items, session.Item{
			ID:        fmt.Sprintf("skill-instructions-%s-%d", safeIdentifier(turnID), i+1),
			Type:      "message",
			Role:      role,
			Text:      text,
			Content:   []session.ContentPart{{Type: defaultSessionContentTypeForRole(role), Text: text}},
			CreatedAt: createdAt,
			Data:      map[string]any{"kind": skillInstructionsKind},
			Metadata:  metadata,
		})
	}
	return items
}

func skillInstructionInputItemText(item any) (string, string, bool) {
	raw, ok := item.(map[string]any)
	if !ok {
		return "", "", false
	}
	if strings.TrimSpace(stringFromAny(raw["type"])) != "message" {
		return "", "", false
	}
	role := strings.TrimSpace(stringFromAny(raw["role"]))
	if role == "" {
		role = contextfrag.RoleUser
	}
	text := strings.TrimSpace(textFromInputItemContent(raw["content"]))
	if text == "" || !strings.Contains(text, "<skill>") || !strings.Contains(text, "</skill>") {
		return "", "", false
	}
	return role, text, true
}

func textFromInputItemContent(content any) string {
	switch typed := content.(type) {
	case []map[string]any:
		parts := make([]string, 0, len(typed))
		for _, block := range typed {
			if text := strings.TrimSpace(stringFromAny(block["text"])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, raw := range typed {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(stringFromAny(block["text"])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func defaultSessionContentTypeForRole(role string) string {
	if strings.TrimSpace(role) == contextfrag.RoleDeveloper {
		return "input_text"
	}
	return "input_text"
}

func imageGenerationInstructionsSessionItem(turnID string, imageItem *session.Item, responseMetadata map[string]any) (session.Item, bool) {
	if imageItem == nil || imageItem.Type != "imageGeneration" {
		return session.Item{}, false
	}
	savedPath := firstNonEmpty(stringFromMap(imageItem.Data, "savedPath"), stringFromMap(imageItem.Data, "saved_path"))
	hint := imageGenerationInstructionsText(savedPath)
	if hint == "" {
		return session.Item{}, false
	}
	itemID := "image-generation-instructions-" + safeIdentifier(firstNonEmpty(imageItem.ID, turnID))
	metadata := appTurnMetadata(turnID, cloneAnyMap(responseMetadata))
	metadata["kind"] = imageGenerationInstructionsKind
	return session.Item{
		ID:        itemID,
		Type:      "message",
		Role:      "developer",
		Text:      hint,
		CreatedAt: imageItem.CreatedAt,
		Data: map[string]any{
			"kind":                imageGenerationInstructionsKind,
			"imageGenerationId":   imageItem.ID,
			"image_generation_id": imageItem.ID,
			"savedPath":           savedPath,
			"saved_path":          savedPath,
		},
		Metadata:   metadata,
		ResponseID: imageItem.ResponseID,
	}, true
}

func imageGenerationInstructionsText(savedPath string) string {
	savedPath = strings.TrimSpace(savedPath)
	if savedPath == "" {
		return ""
	}
	outputDir := filepath.Dir(savedPath)
	outputPath := filepath.Join(outputDir, "<image_id>.png")
	hint := fmt.Sprintf("Generated images are saved to %s as %s by default.\nIf you need to use a generated image at another path, copy it and leave the original in place unless the user explicitly asks you to delete it.", outputDir, outputPath)
	if len(hint) > 1024 {
		return ""
	}
	return hint
}

func sessionItemIsImageGenerationInstructions(item *session.Item) bool {
	if item == nil {
		return false
	}
	if stringValueFromMap(item.Data, "kind") == imageGenerationInstructionsKind {
		return true
	}
	if stringValueFromMap(item.Metadata, "kind") == imageGenerationInstructionsKind {
		return true
	}
	return false
}

func sessionItemIsSkillInstructions(item *session.Item) bool {
	if item == nil {
		return false
	}
	if stringValueFromMap(item.Data, "kind") == skillInstructionsKind {
		return true
	}
	if stringValueFromMap(item.Metadata, "kind") == skillInstructionsKind {
		return true
	}
	return false
}

func sessionItemIsHiddenContextInstruction(item *session.Item) bool {
	return sessionItemIsImageGenerationInstructions(item) || sessionItemIsSkillInstructions(item)
}

func sessionItemIsHiddenThreadItem(item *session.Item) bool {
	if sessionItemIsHiddenContextInstruction(item) {
		return true
	}
	if item == nil {
		return false
	}
	if hidden, _ := item.Data["hiddenFromThread"].(bool); hidden {
		return true
	}
	hidden, _ := item.Metadata["hiddenFromThread"].(bool)
	return hidden
}

func runtimeUserPromptSessionItem(turnID string, params *turn.TurnStartParams, createdAt time.Time) (session.Item, bool) {
	prompt := promptFromTurnStart(params)
	content := sessionContentFromTurnStart(params)
	if strings.TrimSpace(prompt) == "" && len(content) == 0 {
		return session.Item{}, false
	}
	clientID := ""
	if params != nil {
		clientID = params.ClientUserMessageID
	}
	return session.Item{
		ID:        runtimeUserPromptSessionItemID(turnID),
		Type:      "message",
		Role:      "user",
		Text:      prompt,
		Content:   content,
		CreatedAt: createdAt,
		Data:      runtimeUserPromptThreadItemData(params),
		Metadata: appTurnMetadata(turnID, map[string]any{
			"clientId":            clientID,
			"client_user_message": clientID,
		}),
	}, true
}

func runtimeUserPromptThreadItemData(params *turn.TurnStartParams) map[string]any {
	if params == nil {
		return nil
	}
	return runtimeUserInputThreadItemData(params.Prompt, params.Input)
}

func runtimeUserInputThreadItemData(prompt string, inputs []turn.TurnUserInput) map[string]any {
	content := threadUserInputContent(prompt, inputs)
	if len(content) == 0 {
		return nil
	}
	return map[string]any{"content": content}
}

func threadUserInputContentFromTurnStart(params *turn.TurnStartParams) []map[string]any {
	if params == nil {
		return nil
	}
	return threadUserInputContent(params.Prompt, params.Input)
}

func threadUserInputContent(prompt string, inputs []turn.TurnUserInput) []map[string]any {
	content := []map[string]any{}
	if text := strings.TrimSpace(prompt); text != "" {
		content = append(content, map[string]any{
			"type":          "text",
			"text":          text,
			"text_elements": []turn.TextElement{},
		})
	}
	for i := range inputs {
		input := inputs[i]
		inputType := strings.TrimSpace(input.Type)
		if text := strings.TrimSpace(input.Text); text != "" {
			elements := append([]turn.TextElement(nil), input.TextElements...)
			if elements == nil {
				elements = []turn.TextElement{}
			}
			content = append(content, map[string]any{
				"type":          "text",
				"text":          text,
				"text_elements": elements,
			})
			continue
		}
		if imageURL := strings.TrimSpace(input.URL); imageURL != "" && (inputType == "" || strings.EqualFold(inputType, "image")) {
			entry := map[string]any{"type": "image", "url": imageURL}
			if input.Detail != nil {
				entry["detail"] = *input.Detail
			}
			content = append(content, entry)
			continue
		}
		if path := strings.TrimSpace(input.Path); path != "" && (inputType == "" || strings.EqualFold(inputType, "localImage")) {
			entry := map[string]any{"type": "localImage", "path": path}
			if input.Detail != nil {
				entry["detail"] = *input.Detail
			}
			content = append(content, entry)
			continue
		}
		if inputType != "" {
			entry := map[string]any{"type": inputType}
			if input.Name != "" {
				entry["name"] = input.Name
			}
			if input.Path != "" {
				entry["path"] = input.Path
			}
			content = append(content, entry)
		}
	}
	if len(content) == 0 {
		return nil
	}
	return content
}

func runtimeUserPromptSessionItemID(turnID string) string {
	return "user-" + safeIdentifier(turnID)
}

func withoutRuntimeUserPromptItem(items []session.Item, turnID string) []session.Item {
	if len(items) == 0 {
		return items
	}
	promptID := runtimeUserPromptSessionItemID(turnID)
	out := items[:0]
	removed := false
	for _, item := range items {
		if !removed && item.ID == promptID {
			removed = true
			continue
		}
		out = append(out, item)
	}
	return out
}

func fallbackAssistantSessionItemID(turnID string, responseIndex int) string {
	base := "assistant-" + safeIdentifier(turnID)
	if responseIndex <= 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, responseIndex+1)
}

func toolExecutionsForResponse(executions []turn.ToolExecutionResult, start int, count int) []turn.ToolExecutionResult {
	if start < 0 || count <= 0 || start >= len(executions) {
		return nil
	}
	end := start + count
	if end > len(executions) {
		end = len(executions)
	}
	return executions[start:end]
}

func tokenUsageFromAgentLoopResult(result *turn.AgentLoopResult) *TokenUsage {
	if result == nil {
		return nil
	}
	return tokenUsageFromAgentUsage(result.Usage)
}

func tokenUsageFromAgentUsage(usage model.AgentUsage) *TokenUsage {
	if usage.InputTokens == 0 && usage.CachedInputTokens == 0 && usage.CacheWriteInputTokens == 0 && usage.OutputTokens == 0 && usage.ReasoningOutputTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}
	return &TokenUsage{
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		TotalTokens:           model.AgentUsageTotalTokens(usage),
	}
}

func tokenUsageBreakdownFromAgentUsage(usage model.AgentUsage) *TokenUsageBreakdown {
	return &TokenUsageBreakdown{
		TotalTokens:           model.AgentUsageTotalTokens(usage),
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
	}
}

func (r *RuntimeRouter) persistLastResponseID(threadID string, result *turn.AgentLoopResult) error {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || result == nil || result.Response == nil {
		return nil
	}
	responseID := strings.TrimSpace(result.Response.ResponseID)
	if responseID == "" {
		return nil
	}
	if record, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), true); ok {
		record.Metadata.LastResponseID = responseID
		r.saveEphemeralThreadRecord(record)
		return nil
	}
	_, err := r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{
		LastResponseID: stringPointerIfNotEmpty(responseID),
	}, true)
	return err
}

func (r *RuntimeRouter) persistCompactTokenStatus(threadID string, modelID string, params *turn.TurnStartParams, aggregateUsage model.AgentUsage, lastUsage model.AgentUsage) (*compact.TokenStatus, *TokenUsage, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || strings.TrimSpace(threadID) == "" {
		return nil, nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return nil, nil, err
	}
	extra := cloneAnyMap(record.Metadata.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	policy := r.compactPolicyForTurn(modelID, params, extra)
	status := compact.Evaluate(policy, int(model.AgentUsageTotalTokens(lastUsage)))
	extra["token_status"] = compactTokenStatusMap(status)
	lastMap := tokenUsageMetadataMap(lastUsage)
	total := tokenUsageBreakdownFromMetadata(extra["total_token_usage"])
	if total == nil {
		total = &TokenUsageBreakdown{}
	}
	// Rust records every completed model response. AgentLoopResult.Usage is the
	// sum of those responses, while lastUsage represents the active context.
	addAgentUsageToBreakdown(total, aggregateUsage)
	totalMap := tokenUsageBreakdownMetadataMap(total)
	window := r.effectiveModelContextWindowForModel(modelID, params)
	extra["last_token_usage"] = lastMap
	extra["total_token_usage"] = totalMap
	extra["model_context_window"] = window
	extra["token_usage_info"] = map[string]any{
		"total_token_usage": totalMap, "last_token_usage": lastMap, "model_context_window": window,
	}
	// BodyAfterPrefix scope: the first server-observed request input in a
	// window becomes the carried-prefix baseline (mirrors Rust
	// ensure_server_observed_prefill_from_usage). Estimates written after
	// compaction are replaced by the next real usage sample.
	if policy.Scope == compact.ScopeBodyAfterPrefix && !boolFromAny(extra["auto_compact_window_prefill_server_observed"]) && lastUsage.InputTokens > 0 {
		extra["auto_compact_window_prefill"] = lastUsage.InputTokens
		extra["auto_compact_window_prefill_server_observed"] = true
	}
	info := &TokenUsage{Total: total, Last: tokenUsageBreakdownFromAgentUsage(lastUsage), ModelContextWindow: positiveInt64Ptr(window)}
	if runtimeRecordEphemeral(record) {
		record.Metadata.Extra = extra
		r.saveEphemeralThreadRecord(record)
		return &status, info, nil
	}
	_, err = r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{Extra: extra}, true)
	if err != nil {
		return nil, nil, err
	}
	return &status, info, nil
}

func addAgentUsageToBreakdown(total *TokenUsageBreakdown, usage model.AgentUsage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.CacheWriteInputTokens += usage.CacheWriteInputTokens
	total.OutputTokens += usage.OutputTokens
	total.ReasoningOutputTokens += usage.ReasoningOutputTokens
	total.TotalTokens += model.AgentUsageTotalTokens(usage)
}

func tokenUsageBreakdownMetadataMap(usage *TokenUsageBreakdown) map[string]any {
	if usage == nil {
		return nil
	}
	return map[string]any{
		"inputTokens": usage.InputTokens, "cachedInputTokens": usage.CachedInputTokens,
		"cacheWriteInputTokens": usage.CacheWriteInputTokens, "outputTokens": usage.OutputTokens,
		"reasoningOutputTokens": usage.ReasoningOutputTokens, "totalTokens": usage.TotalTokens,
	}
}

func positiveInt64Ptr(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func (r *RuntimeRouter) effectiveModelContextWindowForModel(modelID string, params *turn.TurnStartParams) int64 {
	cfg, _ := r.effectiveConfigForTurn(params)
	info := r.modelInfoForRuntimeWithConfig(modelID, cfg)
	if info == nil {
		return 0
	}
	window := info.ContextWindow
	if window <= 0 {
		window = info.MaxContextWindow
	}
	percent := info.EffectiveContextWindowPercent
	if percent <= 0 {
		percent = 95
	}
	return window * int64(percent) / 100
}

func lastAgentResponseUsage(result *turn.AgentLoopResult) model.AgentUsage {
	if result == nil {
		return model.AgentUsage{}
	}
	for i := len(result.Responses) - 1; i >= 0; i-- {
		if result.Responses[i] != nil {
			return result.Responses[i].Usage
		}
	}
	if result.Response != nil {
		return result.Response.Usage
	}
	return result.Usage
}

func isContextWindowExceededError(err error) bool {
	var apiErr *codexapi.APIError
	return errors.As(err, &apiErr) && apiErr != nil && apiErr.Kind == codexapi.ErrorContextWindowExceeded
}

func (r *RuntimeRouter) compactTokenStatusForThread(threadID string) compact.TokenStatus {
	if r == nil {
		return compact.TokenStatus{}
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return compact.TokenStatus{}
	}
	return compactTokenStatusFromMetadata(record.Metadata.Extra)
}

func (r *RuntimeRouter) compactTokenStatusForTurn(threadID string, modelID string, params *turn.TurnStartParams) compact.TokenStatus {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return compact.TokenStatus{}
	}
	extra := record.Metadata.Extra
	stored := compactTokenStatusFromMetadata(extra)
	active := stored.ActiveContextTokens
	if usage, ok := extra["last_token_usage"].(map[string]any); ok {
		// Threads may carry usage persisted by the exec runner (snake_case)
		// or by the runtime (camelCase); accept both like execStoredTokenUsage.
		active = intFromAny(firstMapValue(usage, "totalTokens", "total_tokens"))
	}
	// Resumed threads may predate token usage persistence. Rust derives the
	// current context from the loaded session, so estimate it from history
	// instead of treating the resumed context as zero.
	if active <= 0 {
		active = compact.EstimateTokens(compactItemsFromSessionItems(record.Items))
	} else {
		// Rust adds an estimate of any local items recorded after the last
		// model-generated item (for example a persisted prompt from an
		// interrupted turn).
		active = compact.EstimateActiveContextTokens(compactItemsFromSessionItems(record.Items), active)
	}
	policy := r.compactPolicyForTurn(modelID, params, extra)
	if policy.Scope == compact.ScopeBodyAfterPrefix && policy.PrefillTokens <= 0 {
		// Rust estimates the carried prefix from the resumed history when no
		// server-observed baseline exists yet for the current window.
		policy.PrefillTokens = compact.EstimateTokens(compactItemsFromSessionItems(record.Items))
	}
	return compact.Evaluate(policy, active)
}

// compactPolicyForTurn mirrors Rust ModelInfo::auto_compact_token_limit and
// TurnContext::model_context_window. Explicit config overrides the catalog
// auto-compact limit; the model's resolved window still supplies the hard cap.
func (r *RuntimeRouter) compactPolicyForTurn(modelID string, params *turn.TurnStartParams, extra map[string]any) compact.Policy {
	cfg, _ := r.effectiveConfigForTurn(params)
	info := r.modelInfoForRuntimeWithConfig(modelID, cfg)
	policy := compact.Policy{Enabled: true, FallbackBufferTokens: intFromAny(extra["auto_compact_fallback_buffer_tokens"])}
	if info == nil {
		return policy
	}
	resolvedWindow := info.ContextWindow
	if resolvedWindow <= 0 {
		resolvedWindow = info.MaxContextWindow
	}
	if resolvedWindow > 0 {
		percent := info.EffectiveContextWindowPercent
		if percent <= 0 {
			percent = 95
		}
		policy.WindowTokens = int(resolvedWindow * int64(percent) / 100)
		policy.TokenLimit = int(resolvedWindow * 9 / 10)
	}
	if info.AutoCompactTokenLimit > 0 && (policy.TokenLimit == 0 || info.AutoCompactTokenLimit < int64(policy.TokenLimit)) {
		policy.TokenLimit = int(info.AutoCompactTokenLimit)
	}
	if cfg != nil {
		if limit := intFromAny(cfg.Values["model_auto_compact_token_limit"]); limit > 0 {
			policy.TokenLimit = limit
		}
		if stringConfigValue(cfg, "model_auto_compact_token_limit_scope") == string(AutoCompactTokenLimitScopeBodyAfterPrefix) {
			policy.Scope = compact.ScopeBodyAfterPrefix
			if prefill := intFromAny(extra["auto_compact_window_prefill"]); prefill > 0 {
				policy.PrefillTokens = prefill
			}
		}
	}
	return policy
}

func (r *RuntimeRouter) persistContextWindowExceededStatus(threadID string) error {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return err
	}
	extra := ensureRecordExtra(cloneAnyMap(record.Metadata.Extra))
	status := compactTokenStatusFromMetadata(extra)
	status.ShouldCompact = true
	status.Reason = compact.ReasonContextWindowExceeded
	status.NewContextWindowRequired = true
	extra["token_status"] = compactTokenStatusMap(status)
	record.Metadata.Extra = extra
	if runtimeRecordEphemeral(record) {
		r.saveEphemeralThreadRecord(record)
		return nil
	}
	return r.runtimeSaveThreadRecord(record)
}

func (r *RuntimeRouter) recordAutoCompactFallbackPrompt(threadID string, turnID string, status *compact.TokenStatus) (bool, error) {
	if r == nil || status == nil || status.ShouldCompact || status.BaseWindowTokensRemaining == nil || *status.BaseWindowTokensRemaining != 0 {
		return false, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return false, err
	}
	extra := ensureRecordExtra(cloneAnyMap(record.Metadata.Extra))
	prompt := strings.TrimSpace(stringFromAny(extra["auto_compact_fallback_prompt"]))
	if prompt == "" || boolFromAny(extra["auto_compact_fallback_delivered"]) {
		return false, nil
	}
	now := time.Now().UTC()
	item := session.Item{
		ID: "auto-compact-fallback-" + safeIdentifier(turnID), Type: "message", Role: "developer", Text: prompt, CreatedAt: now,
		Metadata: map[string]any{"turnId": turnID, "kind": "auto_compact_fallback_prompt"},
	}
	record.Items = append(record.Items, item)
	extra["auto_compact_fallback_delivered"] = true
	record.Metadata.Extra = extra
	if runtimeRecordEphemeral(record) {
		r.saveEphemeralThreadRecord(record)
		return true, nil
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return false, err
	}
	_ = r.services.ThreadRouter.appendThreadRollout(record.ID, []session.Item{item}, now)
	return true, nil
}

func (r *RuntimeRouter) autoCompactThreadAfterTurn(threadID string, turnID string, connectionID string, status *compact.TokenStatus) (*ContextCompactedNotification, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || strings.TrimSpace(threadID) == "" {
		return nil, nil
	}
	reason := compactReasonFromStatus(status)
	var activeContextTokensBefore int64
	if status != nil {
		activeContextTokensBefore = int64(status.ActiveContextTokens)
	}
	return r.compactThread(context.Background(), &runtimeCompactRequest{
		ThreadID:                  strings.TrimSpace(threadID),
		TurnID:                    strings.TrimSpace(turnID),
		ConnectionID:              connectionID,
		Trigger:                   compact.TriggerAuto,
		Reason:                    reason,
		Phase:                     compact.PhaseMidTurn,
		ActiveContextTokensBefore: activeContextTokensBefore,
	})
}

// persistCompactionFailure records a failed automatic compaction on the thread
// metadata so the failure is observable and the next turn still re-attempts
// compaction. Errors are best-effort: the thread store may be unavailable.
func (r *RuntimeRouter) persistCompactionFailure(threadID string, compactErr error) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return
	}
	extra := ensureRecordExtra(cloneAnyMap(record.Metadata.Extra))
	extra["compaction_error"] = compactErr.Error()
	extra["compaction_error_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	status := compactTokenStatusFromMetadata(extra)
	status.ShouldCompact = true
	status.NewContextWindowRequired = true
	extra["token_status"] = compactTokenStatusMap(status)
	record.Metadata.Extra = extra
	if runtimeRecordEphemeral(record) {
		r.saveEphemeralThreadRecord(record)
		return
	}
	_, _ = r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{Extra: extra}, true)
}

type runtimeCompactRequest struct {
	ThreadID                  string
	TurnID                    string
	ConnectionID              string
	Trigger                   compact.Trigger
	Reason                    compact.Reason
	Phase                     compact.Phase
	Prompt                    string
	ActiveContextTokensBefore int64
	// History overrides the persisted thread history for mid-turn
	// compaction, which must include the items accumulated by the current
	// turn before they are persisted.
	History []compact.Item
}

func (r *RuntimeRouter) compactThread(ctx context.Context, params *runtimeCompactRequest) (*ContextCompactedNotification, error) {
	notification, _, err := r.compactThreadWithHistory(ctx, params, nil)
	return notification, err
}

func (r *RuntimeRouter) compactThreadWithHistory(ctx context.Context, params *runtimeCompactRequest, historyOverride []compact.Item) (*ContextCompactedNotification, []session.Item, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil, nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil || record == nil {
		return nil, nil, err
	}
	startedAt := time.Now().UTC()
	history := historyOverride
	if len(history) == 0 {
		history = compactItemsFromSessionItems(record.Items)
	}
	request := &compact.Request{
		ThreadID:  strings.TrimSpace(params.ThreadID),
		TurnID:    strings.TrimSpace(params.TurnID),
		Trigger:   params.Trigger,
		Reason:    params.Reason,
		Phase:     params.Phase,
		Prompt:    strings.TrimSpace(params.Prompt),
		History:   history,
		StartedAt: startedAt,
	}
	if request.Trigger == "" {
		request.Trigger = compact.TriggerManual
	}
	if request.Reason == "" {
		request.Reason = compact.ReasonUserRequested
	}
	if request.Phase == "" {
		request.Phase = compact.PhaseStandaloneTurn
	}
	if request.TurnID == "" {
		request.TurnID = string(newThreadID())
	}
	hookCtx := r.compactHookContext(record, request)
	initialContext, err := r.runPreCompactHooks(ctx, hookCtx)
	if err != nil {
		r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, nil, err, startedAt, time.Now().UTC(), params.ActiveContextTokensBefore)
		return nil, nil, err
	}
	environmentContext, err := r.compactEnvironmentContext(ctx, record, request)
	if err != nil {
		r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, nil, err, startedAt, time.Now().UTC(), params.ActiveContextTokensBefore)
		return nil, nil, err
	}
	initialContext = append(initialContext, environmentContext...)
	compactionItem := session.Item{
		ID:        string(newThreadID()),
		Type:      "contextCompaction",
		CreatedAt: startedAt,
		Metadata: appTurnMetadata(request.TurnID, map[string]any{
			"kind":    "context_compaction",
			"compact": true,
		}),
	}
	r.notifyContextCompactionItemStarted(request.ThreadID, request.TurnID, compactionItem)
	compacted, err := compact.CompactRemotely(ctx, request, &compact.RemoteOptions{
		Runner:               r.compactRunnerForRecord(record),
		MaxSummaryChars:      4000,
		InitialContext:       initialContext,
		InjectBeforeLastUser: true,
		FallbackToLocal:      true,
	})
	if err != nil {
		r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, nil, err, startedAt, time.Now().UTC(), params.ActiveContextTokensBefore)
		return nil, nil, err
	}
	if compacted == nil {
		return nil, nil, nil
	}
	if err := r.runPostCompactHooks(ctx, hookCtx); err != nil {
		r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, compacted, err, startedAt, time.Now().UTC(), params.ActiveContextTokensBefore)
		return nil, nil, err
	}
	now := time.Now().UTC()
	compactedItems := sessionItemsFromCompactItems(compacted.NewHistory, now)
	record.Items = compactedItems
	compactionItem.CreatedAt = now
	record.Items = append(record.Items, compactionItem)
	record.UpdatedAt = now
	record.RecencyAt = now
	extra := cloneAnyMap(record.Metadata.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	extra["compacted_at"] = now.Format(time.RFC3339Nano)
	if request.Trigger == compact.TriggerAuto {
		extra["auto_compacted_at"] = now.Format(time.RFC3339Nano)
	}
	extra["compaction_summary"] = compacted.Summary
	extra["compaction_reason"] = string(request.Reason)
	extra["compaction_trigger"] = string(request.Trigger)
	extra["compaction_phase"] = string(request.Phase)
	extra["compaction_status"] = string(compacted.Status)
	// BodyAfterPrefix scope: Rust recompute_token_usage re-estimates the
	// carried prefix from the compacted history after the window advances, but
	// set_estimated_prefill is a no-op for a server-observed baseline. Retain
	// the observed value; only replace estimated baselines with the compacted
	// estimate.
	if _, ok := extra["auto_compact_window_prefill"]; ok && !boolFromAny(extra["auto_compact_window_prefill_server_observed"]) {
		extra["auto_compact_window_prefill"] = compact.EstimateTokens(compactItemsFromSessionItems(record.Items))
		extra["auto_compact_window_prefill_server_observed"] = false
	}
	// Rust AutoCompactWindow::advance re-arms the token-budget deliveries for
	// the new window; clear the persisted markers so a later window can deliver
	// the reminder and fallback prompt again.
	extra["auto_compact_fallback_delivered"] = false
	extra["token_budget_reminder_delivered"] = false
	// A successful pre-turn compaction satisfies the full-context requirement.
	if request.Phase == compact.PhasePreTurn {
		status := compactTokenStatusFromMetadata(extra)
		status.ShouldCompact = false
		status.NewContextWindowRequired = false
		status.Reason = ""
		extra["token_status"] = compactTokenStatusMap(status)
	}
	if compacted.Source != "" {
		extra["compaction_source"] = string(compacted.Source)
	}
	if strings.TrimSpace(compacted.ResponseID) != "" {
		extra["compaction_response_id"] = strings.TrimSpace(compacted.ResponseID)
	}
	if strings.TrimSpace(compacted.Model) != "" {
		extra["compaction_model"] = strings.TrimSpace(compacted.Model)
	}
	if strings.TrimSpace(compacted.ProviderID) != "" {
		extra["compaction_provider_id"] = strings.TrimSpace(compacted.ProviderID)
	}
	if compacted.Usage != nil {
		extra["compaction_token_usage"] = compactUsageMetadataMap(compacted.Usage)
	}
	if request.TurnID != "" {
		extra["compacted_turn_id"] = request.TurnID
	}
	record.Metadata.Extra = extra
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, nil, err
	}
	_ = r.appendRuntimeCompacted(request.ThreadID, compacted.Summary, record.Items, now)
	r.notifyContextCompactionItemCompleted(request.ThreadID, request.TurnID, compactionItem)
	r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, compacted, nil, startedAt, now, params.ActiveContextTokensBefore)
	// Rust c2bcb9a26b: restart Guardian review sessions after parent history
	// rewrites, seeding them with the latest reusable compaction when the
	// guardian_reuse_parent_compaction feature is enabled.
	r.resetGuardianAfterParentCompaction(request.ThreadID, compacted)
	return &ContextCompactedNotification{
		ThreadID:    request.ThreadID,
		TurnID:      request.TurnID,
		Summary:     compacted.Summary,
		ItemCount:   len(record.Items),
		Trigger:     string(request.Trigger),
		Reason:      string(request.Reason),
		Phase:       string(request.Phase),
		Status:      string(compacted.Status),
		Source:      string(compacted.Source),
		ResponseID:  compacted.ResponseID,
		Model:       compacted.Model,
		ProviderID:  compacted.ProviderID,
		CompletedAt: compacted.CompletedAt.Format(time.RFC3339Nano),
		TokenUsage:  compactUsageNotificationValue(compacted.Usage),
	}, compactedItems, nil
}

// resetContextWindow mirrors Rust's token-budget new_context roll-over: the
// context window accounting restarts without replacing the conversation
// history. The compaction lifecycle (item started/completed, rollout marker)
// is emitted for parity with the summarization paths.
func (r *RuntimeRouter) resetContextWindow(threadID string, turnID string) error {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return err
	}
	now := time.Now().UTC()
	compactionItem := session.Item{
		ID:        string(newThreadID()),
		Type:      "contextCompaction",
		CreatedAt: now,
		Metadata: appTurnMetadata(turnID, map[string]any{
			"kind":    "context_compaction",
			"compact": true,
		}),
	}
	r.notifyContextCompactionItemStarted(threadID, turnID, compactionItem)
	record.Items = append(record.Items, compactionItem)
	record.UpdatedAt = now
	record.RecencyAt = now
	extra := ensureRecordExtra(cloneAnyMap(record.Metadata.Extra))
	extra["compacted_at"] = now.Format(time.RFC3339Nano)
	extra["auto_compacted_at"] = now.Format(time.RFC3339Nano)
	extra["compaction_summary"] = "A new context window will start without summarizing conversation history."
	extra["compaction_reason"] = string(compact.ReasonTokenLimit)
	extra["compaction_trigger"] = string(compact.TriggerAuto)
	extra["compaction_phase"] = string(compact.PhaseMidTurn)
	extra["compaction_status"] = string(compact.StatusCompleted)
	extra["compaction_source"] = string(compact.SourceLocal)
	// The new window re-arms the token-budget deliveries and clears the
	// carried prefix baseline (Rust start_new_context_window advances the
	// window and clears prefill).
	extra["auto_compact_fallback_delivered"] = false
	extra["token_budget_reminder_delivered"] = false
	delete(extra, "auto_compact_window_prefill")
	delete(extra, "auto_compact_window_prefill_server_observed")
	status := compactTokenStatusFromMetadata(extra)
	status.ShouldCompact = false
	status.NewContextWindowRequired = false
	status.Reason = ""
	extra["token_status"] = compactTokenStatusMap(status)
	record.Metadata.Extra = extra
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return err
	}
	_ = r.appendRuntimeCompacted(threadID, "A new context window will start without summarizing conversation history.", record.Items, now)
	r.notifyContextCompactionItemCompleted(threadID, turnID, compactionItem)
	return nil
}

func (r *RuntimeRouter) compactEnvironmentContext(ctx context.Context, record *session.Record, request *compact.Request) ([]compact.Item, error) {
	if r == nil || request == nil {
		return nil, nil
	}
	params := &turn.TurnStartParams{ThreadID: request.ThreadID}
	if record != nil {
		params.CWD = record.Metadata.CWD
	}
	if active := r.activeRuntimeTurnStateSnapshot(request.ThreadID, request.TurnID); active != nil && active.Params != nil {
		clone := *active.Params
		params = &clone
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, err
	}
	if !cfg.IncludeEnvironmentContext() {
		return nil, nil
	}
	current, err := r.environmentCurrentTime(ctx, request.ThreadID, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to read current time: %w", err)
	}
	text := r.turnEnvironmentContextTextAt(r.environmentContextParams(request.ThreadID, params), current.In(time.Local), localTimezoneName())
	return []compact.Item{{
		ID: "environment-context-" + safeIdentifier(request.TurnID), Type: "message", Role: "user", Kind: "environment_context", Text: text, Created: current.UTC(),
	}}, nil
}

func (r *RuntimeRouter) notifyContextCompactionItemStarted(threadID string, turnID string, item session.Item) {
	threadItem := BuildThreadItem(item)
	r.notify(NotificationItemStarted, &ItemStartedNotification{
		ThreadID:    strings.TrimSpace(threadID),
		TurnID:      strings.TrimSpace(turnID),
		Item:        threadItemPayload(threadItem),
		StartedAtMS: item.CreatedAt.UTC().UnixMilli(),
	})
}

func (r *RuntimeRouter) notifyContextCompactionItemCompleted(threadID string, turnID string, item session.Item) {
	threadItem := BuildThreadItem(item)
	r.notify(NotificationItemCompleted, &ItemCompletedNotification{
		ThreadID:      strings.TrimSpace(threadID),
		TurnID:        strings.TrimSpace(turnID),
		Item:          threadItemPayload(threadItem),
		CompletedAtMS: item.CreatedAt.UTC().UnixMilli(),
	})
}

func compactUsageNotificationValue(usage *compact.Usage) any {
	if usage == nil {
		return nil
	}
	return compactUsageMetadataMap(usage)
}

func compactUsageMetadataMap(usage *compact.Usage) map[string]any {
	if usage == nil {
		return nil
	}
	return map[string]any{
		"inputTokens":           usage.InputTokens,
		"cachedInputTokens":     usage.CachedInputTokens,
		"cacheWriteInputTokens": usage.CacheWriteInputTokens,
		"outputTokens":          usage.OutputTokens,
		"reasoningOutputTokens": usage.ReasoningOutputTokens,
		"totalTokens":           usage.InputTokens + usage.OutputTokens,
	}
}

func (r *RuntimeRouter) compactRunnerForRecord(record *session.Record) compact.RemoteRunner {
	if r == nil {
		return nil
	}
	if r.services.CompactRunner != nil {
		return r.services.CompactRunner
	}
	agent := r.agentSnapshot()
	if agent == nil || record == nil {
		return nil
	}
	if _, ok := agent.(*model.ResponsesAgentRunner); !ok {
		return nil
	}
	providerID := strings.TrimSpace(record.Metadata.ModelProvider)
	if providerID == "" && record.Metadata.Extra != nil {
		providerID = firstNonEmpty(
			stringFromMap(record.Metadata.Extra, "model_provider"),
			stringFromMap(record.Metadata.Extra, "modelProvider"),
			stringFromMap(record.Metadata.Extra, "provider"),
		)
	}
	if !r.providerSupportsRemoteCompact(providerID) {
		return nil
	}
	return &agentCompactRunner{
		agent:      agent,
		model:      firstNonEmpty(record.Metadata.Model, defaultRemoteCompactModel),
		providerID: firstNonEmpty(providerID, model.OpenAIProviderID),
	}
}

func (r *RuntimeRouter) providerSupportsRemoteCompact(providerID string) bool {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = model.OpenAIProviderID
	}
	values := map[string]any{}
	openAIBaseURL := ""
	if r != nil && r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil {
			values = read.Config
			openAIBaseURL = strings.TrimSpace(stringFromMap(read.Config, "openai_base_url"))
		}
	}
	info, err := model.ProviderForConfigID(values, providerID, openAIBaseURL)
	if err != nil || info == nil {
		return false
	}
	return info.SupportsRemoteCompaction()
}

func compactReasonFromStatus(status *compact.TokenStatus) compact.Reason {
	if status != nil && status.Reason != "" {
		return status.Reason
	}
	return compact.ReasonTokenLimit
}

func compactTokenLimitFromMetadata(extra map[string]any) int {
	if extra == nil {
		return 0
	}
	for _, key := range []string{"auto_compact_token_limit", "autoCompactTokenLimit", "token_limit", "tokenLimit"} {
		switch value := extra[key].(type) {
		case int:
			return value
		case int64:
			return int(value)
		case float64:
			return int(value)
		case json.Number:
			if parsed, err := value.Int64(); err == nil {
				return int(parsed)
			}
		}
	}
	return 0
}

func compactTokenStatusMap(status compact.TokenStatus) map[string]any {
	out := map[string]any{
		"activeContextTokens":      status.ActiveContextTokens,
		"autoCompactScopeTokens":   status.AutoCompactScopeTokens,
		"autoCompactScopeLimit":    status.AutoCompactScopeLimit,
		"shouldCompact":            status.ShouldCompact,
		"reason":                   string(status.Reason),
		"newContextWindowRequired": status.NewContextWindowRequired,
	}
	if status.TokensUntilCompaction != nil {
		out["tokensUntilCompaction"] = *status.TokensUntilCompaction
	}
	if status.BaseWindowTokensRemaining != nil {
		out["baseWindowTokensRemaining"] = *status.BaseWindowTokensRemaining
	}
	return out
}

func compactTokenStatusFromMetadata(extra map[string]any) compact.TokenStatus {
	raw, ok := extra["token_status"].(map[string]any)
	if !ok {
		return compact.TokenStatus{}
	}
	status := compact.TokenStatus{
		ActiveContextTokens:      intFromAny(raw["activeContextTokens"]),
		AutoCompactScopeTokens:   intFromAny(raw["autoCompactScopeTokens"]),
		AutoCompactScopeLimit:    intFromAny(raw["autoCompactScopeLimit"]),
		ShouldCompact:            boolFromAny(raw["shouldCompact"]),
		Reason:                   compact.Reason(stringFromAny(raw["reason"])),
		NewContextWindowRequired: boolFromAny(raw["newContextWindowRequired"]),
	}
	if tokens, ok := intPtrFromAny(raw["tokensUntilCompaction"]); ok {
		status.TokensUntilCompaction = tokens
	}
	if tokens, ok := intPtrFromAny(raw["baseWindowTokensRemaining"]); ok {
		status.BaseWindowTokensRemaining = tokens
	}
	return status
}

func tokenUsageMetadataMap(usage model.AgentUsage) map[string]any {
	return map[string]any{
		"inputTokens":           usage.InputTokens,
		"cachedInputTokens":     usage.CachedInputTokens,
		"cacheWriteInputTokens": usage.CacheWriteInputTokens,
		"outputTokens":          usage.OutputTokens,
		"reasoningOutputTokens": usage.ReasoningOutputTokens,
		"totalTokens":           model.AgentUsageTotalTokens(usage),
	}
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		parsed, _ := v.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func intPtrFromAny(value any) (*int, bool) {
	switch v := value.(type) {
	case int:
		return &v, true
	case int64:
		parsed := int(v)
		return &parsed, true
	case float64:
		parsed := int(v)
		return &parsed, true
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			out := int(parsed)
			return &out, true
		}
	}
	return nil, false
}

func boolFromAny(value any) bool {
	out, _ := value.(bool)
	return out
}

func stringFromAny(value any) string {
	out, _ := value.(string)
	return out
}

func appResponseMetadata(turnID string, response *model.AgentResponse, usage *model.AgentUsage, profile *turn.Profile) map[string]any {
	metadata := appTurnMetadata(turnID, map[string]any{
		"model":      "",
		"providerId": "",
	})
	if response != nil {
		metadata["model"] = response.Model
		metadata["providerId"] = response.ProviderID
		metadata["responseId"] = response.ResponseID
		metadata["response_id"] = response.ResponseID
		for key, value := range model.AgentResponseMetadata(response) {
			metadata[key] = value
		}
	}
	if usage != nil {
		if tokenUsage := tokenUsageFromAgentUsage(*usage); tokenUsage != nil {
			metadata["tokenUsage"] = map[string]any{
				"inputTokens":           tokenUsage.InputTokens,
				"cachedInputTokens":     tokenUsage.CachedInputTokens,
				"cacheWriteInputTokens": tokenUsage.CacheWriteInputTokens,
				"outputTokens":          tokenUsage.OutputTokens,
				"reasoningOutputTokens": tokenUsage.ReasoningOutputTokens,
				"totalTokens":           tokenUsage.TotalTokens,
			}
			metadata["usage"] = map[string]any{
				"input_tokens":             tokenUsage.InputTokens,
				"cached_input_tokens":      tokenUsage.CachedInputTokens,
				"cache_write_input_tokens": tokenUsage.CacheWriteInputTokens,
				"output_tokens":            tokenUsage.OutputTokens,
				"reasoning_output_tokens":  tokenUsage.ReasoningOutputTokens,
				"total_tokens":             tokenUsage.TotalTokens,
			}
		}
	}
	addAppTimingProfileMetadata(metadata, profile)
	return metadata
}

func addAppTimingProfileMetadata(metadata map[string]any, profile *turn.Profile) {
	if metadata == nil || profile == nil {
		return
	}
	metadata["timingProfile"] = appTimingProfileCamelMap(profile)
	metadata["timing_profile"] = appTimingProfileSnakeMap(profile)
}

func appTimingProfileCamelMap(profile *turn.Profile) map[string]any {
	if profile == nil {
		return nil
	}
	return map[string]any{
		"beforeFirstSamplingMs":      profile.BeforeFirstSamplingMS,
		"samplingMs":                 profile.SamplingMS,
		"betweenSamplingOverheadMs":  profile.BetweenSamplingOverheadMS,
		"toolBlockingMs":             profile.ToolBlockingMS,
		"pendingIdleAfterSamplingMs": profile.PendingIdleAfterSamplingMS,
		"samplingRequestCount":       profile.SamplingRequestCount,
		"samplingRetryCount":         profile.SamplingRetryCount,
		"totalMs":                    profile.TotalMS,
	}
}

func appTimingProfileSnakeMap(profile *turn.Profile) map[string]any {
	if profile == nil {
		return nil
	}
	return map[string]any{
		"before_first_sampling_ms":       profile.BeforeFirstSamplingMS,
		"sampling_ms":                    profile.SamplingMS,
		"between_sampling_overhead_ms":   profile.BetweenSamplingOverheadMS,
		"tool_blocking_ms":               profile.ToolBlockingMS,
		"pending_idle_after_sampling_ms": profile.PendingIdleAfterSamplingMS,
		"sampling_request_count":         profile.SamplingRequestCount,
		"sampling_retry_count":           profile.SamplingRetryCount,
		"total_ms":                       profile.TotalMS,
	}
}

func sessionItemsForAppToolExecution(turnID string, execution *turn.ToolExecutionResult, createdAt time.Time) []session.Item {
	if item, ok := sessionItemForClockSleepExecution(turnID, execution, createdAt); ok {
		return []session.Item{item}
	}
	if item, ok := sessionItemForWebSearchExecution(turnID, execution, createdAt, nil); ok {
		return []session.Item{item}
	}
	if item, ok := sessionItemForStandaloneImageGenerationExecution(turnID, execution, createdAt, nil); ok {
		return []session.Item{item}
	}
	items := []session.Item{}
	if item, ok := sessionItemForAppToolCall(turnID, execution, createdAt, nil); ok {
		items = append(items, item)
	}
	if item, ok := sessionItemForAppToolOutput(turnID, execution, createdAt, nil); ok {
		items = append(items, item)
	}
	if item, ok := sessionItemForAppCollaborationPresentation(turnID, execution, createdAt, nil); ok {
		items = append(items, item)
	}
	return items
}

func sessionItemForAppCollaborationPresentation(turnID string, execution *turn.ToolExecutionResult, createdAt time.Time, responseMetadata map[string]any) (session.Item, bool) {
	if execution == nil || execution.Invocation == nil || !isAppCollaborationExecution(execution) {
		return session.Item{}, false
	}
	threadID := ""
	if execution.Invocation.Context != nil {
		threadID = firstNonEmpty(stringFromAny(execution.Invocation.Context["thread_id"]), stringFromAny(execution.Invocation.Context["threadId"]))
	}
	item, ok := collaborationCompletedThreadItem(execution, threadID, turnID)
	if !ok {
		return session.Item{}, false
	}
	metadata := appTimingMetadata(appTurnMetadata(turnID, cloneAnyMap(responseMetadata)), execution.StartedAt, execution.FinishedAt)
	metadata["lifecycleNotified"] = true
	return session.Item{
		ID: item.ID, Type: item.Type, Status: item.Status, CreatedAt: time.UnixMilli(item.CreatedAt).UTC(),
		Data: cloneAnyMap(item.Data), Metadata: metadata,
	}, true
}

func sessionItemForClockSleepExecution(turnID string, execution *turn.ToolExecutionResult, createdAt time.Time) (session.Item, bool) {
	if !isClockSleepExecution(execution) {
		return session.Item{}, false
	}
	durationMS, ok := clockSleepDurationMS(execution.Invocation)
	if !ok {
		return session.Item{}, false
	}
	callID := appToolExecutionCallID(execution, createdAt)
	itemCreatedAt := execution.FinishedAt
	if itemCreatedAt.IsZero() {
		itemCreatedAt = createdAt
	}
	metadata := appTimingMetadata(appTurnMetadata(turnID, nil), execution.StartedAt, execution.FinishedAt)
	delete(metadata, "durationMs")
	delete(metadata, "duration_ms")
	return session.Item{
		ID:        callID,
		Type:      "sleep",
		Text:      fmt.Sprintf("%d", durationMS),
		CreatedAt: itemCreatedAt,
		Data: map[string]any{
			"durationMs": durationMS,
		},
		Metadata: metadata,
	}, true
}

func isClockSleepExecution(execution *turn.ToolExecutionResult) bool {
	return execution != nil && execution.Invocation != nil && isClockSleepInvocation(execution.Invocation)
}

func isClockSleepInvocation(invocation *tool.Invocation) bool {
	return invocation != nil && invocation.ToolName.Key() == "clock.sleep"
}

func sessionItemForWebSearchExecution(turnID string, execution *turn.ToolExecutionResult, createdAt time.Time, responseMetadata map[string]any) (session.Item, bool) {
	if !isWebSearchExecution(execution) {
		return session.Item{}, false
	}
	callID := appToolExecutionCallID(execution, createdAt)
	action := webSearchActionForExecution(execution)
	query := webSearchQueryFromAction(action)
	itemCreatedAt := createdAt
	if execution != nil {
		if execution.FinishedAt.IsZero() && execution.Output != nil && !execution.Output.CompletedAt.IsZero() {
			itemCreatedAt = execution.Output.CompletedAt
		} else if !execution.FinishedAt.IsZero() {
			itemCreatedAt = execution.FinishedAt
		}
	}
	metadata := appTimingMetadata(appTurnMetadata(turnID, cloneAnyMap(responseMetadata)), execution.StartedAt, execution.FinishedAt)
	metadata["toolName"] = "web.run"
	results := webSearchResultsForExecution(execution)
	if execution.Output != nil {
		metadata["success"] = execution.Output.Success
		if strings.TrimSpace(execution.Output.Error) != "" {
			metadata["error"] = execution.Output.Error
		}
	}
	data := map[string]any{
		"query":  query,
		"action": action,
	}
	if results != nil {
		data["results"] = results
	}
	return session.Item{
		ID:        callID,
		Type:      "webSearch",
		Text:      query,
		CreatedAt: itemCreatedAt,
		Data:      data,
		Metadata:  metadata,
	}, true
}

func isWebSearchExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil {
		return false
	}
	if isWebSearchInvocation(execution.Invocation) {
		return true
	}
	if execution.Output != nil {
		if execution.Output.ToolName.Namespace == turn.WebSearchNamespace && execution.Output.ToolName.Name == turn.WebSearchRunTool {
			return true
		}
		if execution.Output.Data != nil {
			if marker, ok := execution.Output.Data["web_search"].(bool); ok && marker {
				return true
			}
		}
	}
	return false
}

func isWebSearchInvocation(invocation *tool.Invocation) bool {
	return invocation != nil && invocation.ToolName.Namespace == turn.WebSearchNamespace && invocation.ToolName.Name == turn.WebSearchRunTool
}

func sessionItemForStandaloneImageGenerationExecution(turnID string, execution *turn.ToolExecutionResult, createdAt time.Time, responseMetadata map[string]any) (session.Item, bool) {
	if !isImageGenerationExecution(execution) {
		return session.Item{}, false
	}
	callID := appToolExecutionCallID(execution, createdAt)
	itemCreatedAt := createdAt
	if execution != nil {
		if execution.FinishedAt.IsZero() && execution.Output != nil && !execution.Output.CompletedAt.IsZero() {
			itemCreatedAt = execution.Output.CompletedAt
		} else if !execution.FinishedAt.IsZero() {
			itemCreatedAt = execution.FinishedAt
		}
	}
	data := map[string]any{
		"status": "failed",
		"result": "",
	}
	if execution != nil && execution.Output != nil {
		if execution.Output.Success {
			data["status"] = "completed"
		}
		for key, value := range execution.Output.Data {
			data[key] = value
		}
	}
	status := firstNonEmpty(stringFromMap(data, "status"), "completed")
	result := stringFromMap(data, "result")
	revisedPrompt := firstNonEmpty(stringFromMap(data, "revisedPrompt"), stringFromMap(data, "revised_prompt"), imageGenerationPromptForExecution(execution))
	data["status"] = status
	data["result"] = result
	if revisedPrompt != "" {
		data["revisedPrompt"] = revisedPrompt
		data["revised_prompt"] = revisedPrompt
	}
	metadata := appTimingMetadata(appTurnMetadata(turnID, cloneAnyMap(responseMetadata)), execution.StartedAt, execution.FinishedAt)
	metadata["toolName"] = turn.ImageGenerationNamespace + "." + turn.ImageGenerationToolName
	if execution.Output != nil {
		metadata["success"] = execution.Output.Success
		if strings.TrimSpace(execution.Output.Error) != "" {
			metadata["error"] = execution.Output.Error
		}
	}
	return session.Item{
		ID:        callID,
		Type:      "imageGeneration",
		Status:    status,
		Text:      revisedPrompt,
		CreatedAt: itemCreatedAt,
		Data:      data,
		Metadata:  metadata,
	}, true
}

func isImageGenerationExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil {
		return false
	}
	if isImageGenerationInvocation(execution.Invocation) {
		return true
	}
	if execution.Output != nil {
		if execution.Output.ToolName.Namespace == turn.ImageGenerationNamespace && execution.Output.ToolName.Name == turn.ImageGenerationToolName {
			return true
		}
		if execution.Output.Data != nil {
			if marker, ok := execution.Output.Data["image_generation"].(bool); ok && marker {
				return true
			}
		}
	}
	return false
}

func isImageGenerationInvocation(invocation *tool.Invocation) bool {
	return invocation != nil && invocation.ToolName.Namespace == turn.ImageGenerationNamespace && invocation.ToolName.Name == turn.ImageGenerationToolName
}

func imageGenerationPromptForExecution(execution *turn.ToolExecutionResult) string {
	if execution == nil || execution.Invocation == nil {
		return ""
	}
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(execution.Invocation.Payload.Arguments), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Prompt)
}

func webSearchActionForExecution(execution *turn.ToolExecutionResult) any {
	if execution != nil && execution.Output != nil && execution.Output.Data != nil {
		for _, key := range []string{"web_search_action", "webSearchAction", "action"} {
			if value, ok := execution.Output.Data[key]; ok {
				if action := threadItemWebSearchActionFromAny(value); action != nil {
					return action
				}
			}
		}
	}
	return map[string]any{"type": "other"}
}

func webSearchResultsForExecution(execution *turn.ToolExecutionResult) []any {
	if execution == nil || execution.Output == nil || execution.Output.Data == nil {
		return nil
	}
	for _, key := range []string{"web_search_results", "webSearchResults", "results"} {
		if values, ok := execution.Output.Data[key].([]any); ok {
			return append([]any(nil), values...)
		}
	}
	return nil
}

func webSearchQueryFromAction(action any) string {
	normalized := threadItemWebSearchActionFromAny(action)
	data, ok := normalized.(map[string]any)
	if !ok {
		return ""
	}
	actionType, _ := data["type"].(string)
	if actionType != "search" {
		return ""
	}
	if query := threadItemStringFromAnyMap(data, "query"); strings.TrimSpace(query) != "" {
		return strings.TrimSpace(query)
	}
	switch queries := data["queries"].(type) {
	case []string:
		if len(queries) > 0 {
			return strings.TrimSpace(queries[0])
		}
	case []any:
		for _, value := range queries {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func clockSleepDurationMS(invocation *tool.Invocation) (int64, bool) {
	if invocation == nil {
		return 0, false
	}
	var payload struct {
		DurationMS int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &payload); err != nil {
		return 0, false
	}
	if payload.DurationMS <= 0 {
		return 0, false
	}
	return payload.DurationMS, true
}

func sessionItemForAppToolCall(turnID string, execution *turn.ToolExecutionResult, createdAt time.Time, responseMetadata map[string]any) (session.Item, bool) {
	if execution == nil || execution.Invocation == nil {
		return session.Item{}, false
	}
	callID := appToolExecutionCallID(execution, createdAt)
	callData := appToolInvocationData(execution.Invocation)
	if appToolOutputIsMCP(execution.Output) {
		if server := stringFromMap(execution.Output.Data, "server"); server != "" {
			callData["server"] = server
		}
		if toolName := stringFromMap(execution.Output.Data, "tool"); toolName != "" {
			callData["tool"] = toolName
		}
		markMCPToolData(callData, execution.Invocation.ToolName)
	}
	if appToolOutputIsDynamic(execution.Output) {
		markDynamicToolData(callData, execution.Invocation.ToolName)
	}
	if appToolOutputIsFileChange(execution.Output) {
		markFileChangeToolData(callData, execution.Output, true)
	}
	metadata := appTimingMetadata(appTurnMetadata(turnID, cloneAnyMap(responseMetadata)), execution.StartedAt, time.Time{})
	metadata["toolName"] = execution.Invocation.ToolName.Key()
	metadata["payloadKind"] = string(execution.Invocation.Payload.Kind)
	metadata["source"] = execution.Invocation.Source
	if isAppCollaborationExecution(execution) {
		metadata["hiddenFromThread"] = true
	}
	if unifiedExecExecutionEvented(execution) {
		metadata["hiddenFromThread"] = true
	}
	if hiddenApplyPatchValidationExecution(execution) {
		metadata["hiddenFromThread"] = true
	}
	return session.Item{
		ID:        "tool-call-" + safeIdentifier(turnID) + "-" + safeIdentifier(callID),
		Type:      appToolSessionItemType(execution.Invocation.Payload.Kind),
		Name:      execution.Invocation.ToolName.Key(),
		CallID:    callID,
		Text:      appToolInvocationText(execution.Invocation),
		Data:      callData,
		CreatedAt: createdAt,
		Metadata:  metadata,
	}, true
}

func sessionItemForAppToolOutput(turnID string, execution *turn.ToolExecutionResult, createdAt time.Time, responseMetadata map[string]any) (session.Item, bool) {
	if execution == nil || execution.Invocation == nil || execution.Output == nil {
		return session.Item{}, false
	}
	callID := appToolExecutionCallID(execution, createdAt)
	outputCreatedAt := execution.Output.CompletedAt
	if outputCreatedAt.IsZero() {
		outputCreatedAt = createdAt
	}
	outputMetadata := appTimingMetadata(appTurnMetadata(turnID, cloneAnyMap(responseMetadata)), execution.StartedAt, execution.FinishedAt)
	outputMetadata["toolName"] = execution.Invocation.ToolName.Key()
	outputMetadata["success"] = execution.Output.Success
	if strings.TrimSpace(execution.Output.Error) != "" {
		outputMetadata["error"] = execution.Output.Error
	}
	if isAppCollaborationExecution(execution) {
		outputMetadata["hiddenFromThread"] = true
	}
	if unifiedExecExecutionEvented(execution) {
		outputMetadata["hiddenFromThread"] = true
	}
	if hiddenApplyPatchValidationExecution(execution) {
		outputMetadata["hiddenFromThread"] = true
	}
	return session.Item{
		ID:        "tool-output-" + safeIdentifier(turnID) + "-" + safeIdentifier(callID),
		Type:      "tool_output",
		Name:      execution.Invocation.ToolName.Key(),
		CallID:    callID,
		Text:      execution.Output.Body,
		Data:      appToolOutputData(execution.Invocation, execution.Output),
		CreatedAt: outputCreatedAt,
		Metadata:  outputMetadata,
	}, true
}

func unifiedExecExecutionEvented(execution *turn.ToolExecutionResult) bool {
	if execution == nil || execution.Output == nil || execution.Output.Data == nil {
		return false
	}
	evented, _ := execution.Output.Data["unified_exec_evented"].(bool)
	return evented
}

func hiddenApplyPatchValidationExecution(execution *turn.ToolExecutionResult) bool {
	return execution != nil && execution.Invocation != nil &&
		execution.Invocation.ToolName.Key() == tool.DefaultApplyPatchToolName &&
		!appToolOutputIsFileChange(execution.Output)
}

func isAppCollaborationExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil {
		return false
	}
	if execution.Invocation != nil && appCollaborationToolName(execution.Invocation.ToolName) {
		return true
	}
	return execution.Output != nil && appCollaborationToolName(execution.Output.ToolName)
}

func appToolExecutionCallID(execution *turn.ToolExecutionResult, createdAt time.Time) string {
	if execution == nil || execution.Invocation == nil {
		return fmt.Sprintf("tool-%d", createdAt.UnixNano())
	}
	return firstNonEmpty(execution.Invocation.CallID, fmt.Sprintf("tool-%d", createdAt.UnixNano()))
}

func appTurnFromTurnRecord(record *turn.TurnRecord, items []ThreadItem, status TurnStatus, appErr *TurnError, completedAt *int64) Turn {
	if record == nil {
		return Turn{Items: []ThreadItem{}, ItemsView: TurnItemsFull, Status: status}
	}
	startedAt := record.StartedAt
	return Turn{
		ID:          record.ID,
		Items:       append([]ThreadItem(nil), items...),
		ItemsView:   TurnItemsFull,
		Status:      status,
		StartedAt:   &startedAt,
		CompletedAt: completedAt,
		Error:       appErr,
	}
}

func promptFromTurnStart(params *turn.TurnStartParams) string {
	if params == nil {
		return ""
	}
	if strings.TrimSpace(params.Prompt) != "" {
		return strings.TrimSpace(params.Prompt)
	}
	parts := make([]string, 0, len(params.Input))
	for i := range params.Input {
		if strings.TrimSpace(params.Input[i].Text) != "" {
			parts = append(parts, strings.TrimSpace(params.Input[i].Text))
		}
	}
	return strings.Join(parts, "\n")
}

func turnStartUsesStructuredUserInput(params *turn.TurnStartParams) bool {
	if params == nil {
		return false
	}
	for i := range params.Input {
		if strings.TrimSpace(firstNonEmpty(params.Input[i].URL, params.Input[i].Path)) != "" {
			return true
		}
	}
	return false
}

func providerFromTurnStart(params *turn.TurnStartParams) string {
	if params == nil || params.Config == nil {
		return ""
	}
	for _, key := range []string{"model_provider", "modelProvider", "provider"} {
		if value, ok := params.Config[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type appTurnRunConfig struct {
	Model                           string
	AutoReviewModelOverride         string
	ToolMode                        string
	DisableCodeModeFallback         bool
	ProviderID                      string
	Instructions                    string
	Originator                      string
	ClientMetadata                  map[string]string
	SessionID                       string
	ThreadSource                    string
	SubagentSource                  string
	ParentThreadID                  string
	ParentTurnID                    string
	Ephemeral                       bool
	WorkspaceKind                   string
	NumInputImages                  int
	IsFirstTurn                     bool
	ApprovalPolicy                  string
	ApprovalsReviewer               string
	SandboxPolicy                   string
	SandboxNetworkAccess            bool
	CollaborationMode               string
	Personality                     string
	InputItems                      []any
	HostedTools                     []any
	SessionItems                    []session.Item
	ExtraSessionItems               func() []session.Item
	PostToolInputItems              turn.ToolPostExecutionInputItems
	PreviousResponseID              string
	ParallelToolCalls               bool
	ReasoningEffort                 string
	ReasoningSummary                string
	ConcurrentReasoningSummaries    bool
	ModelVerbosity                  string
	IncludeTimingMetrics            bool
	BetaFeaturesHeader              string
	ItemIDsEnabled                  bool
	PromptCacheKey                  string
	ServiceTier                     string
	Store                           bool
	AttestationProvider             codexapi.AttestationProvider
	UnifiedExecEnabled              bool
	ExecutedToolCallMetadataEnabled bool
}

type responsesMetadataLineage struct {
	SessionID          string
	ForkedFromThreadID string
	ParentThreadID     string
	SubagentHeader     string
	SubagentKind       string
	ThreadSource       string
}

func (r *RuntimeRouter) appTurnConfig(ctx context.Context, threadID string, turnID string, params *turn.TurnStartParams, startedAtMS int64, turnRuntime *turn.Runtime) (*appTurnRunConfig, error) {
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, err
	}
	// Rust 9e301c8c9a: responses_api_metadata is validated while loading config
	// (bounded entries, ASCII identifier keys, no reserved Codex keys).
	if err := codexapi.ValidateResponsesAPIMetadata(cfg.ResponsesAPIMetadata()); err != nil {
		return nil, err
	}
	modelProviderConfig, err := r.appTurnModelProviderConfig(cfg, params)
	if err != nil {
		return nil, err
	}
	modelInfo := r.modelInfoForRuntimeWithConfig(modelProviderConfig.Model, cfg)
	baseInstructionsExplicit := params != nil && params.BaseInstructions != nil
	instructions, err := appBaseInstructionsForConfig(cfg)
	if err != nil {
		return nil, err
	}
	instructions = firstNonEmpty(turnBaseInstructions(params), instructions)
	personality := appPersonalityForTurn(cfg, params)
	if instructions == "" && !baseInstructionsExplicit {
		instructions = modelInfo.ModelInstructions(personality)
	}
	historyItems, previousResponseID := r.historyInputItemsForTurn(threadID)
	inputItems := append([]any(nil), historyItems...)
	modelPersonalityItems, err := r.modelPersonalityWorldStateInputItems(
		threadID,
		modelInfo,
		personality,
		instructions,
		features.Enabled(cfg.FeatureSettings(), "personality"),
	)
	if err != nil {
		return nil, err
	}
	inputItems = append(inputItems, modelPersonalityItems...)
	tokenBudget, err := cfg.TokenBudgetConfigWithDefaults(modelTokenBudgetDefaults(modelInfo))
	if err != nil {
		return nil, err
	}
	if item, err := r.contextWindowGuidanceWorldStateInputItem(
		threadID,
		tokenBudget.Enabled && modelInfo != nil && modelInfo.ContextWindow > 0,
		tokenBudget.GuidanceMessage,
	); err != nil {
		return nil, err
	} else if item != nil {
		inputItems = append(inputItems, item)
	}
	realtimeStateSessionItems := []session.Item{}
	if item, err := r.realtimeWorldStateInputItem(threadID, cfg); err != nil {
		return nil, err
	} else if item != nil {
		inputItems = append(inputItems, item)
		if persisted, ok := realtimeWorldStateSessionItemForTurn(turnID, item, time.UnixMilli(startedAtMS).UTC()); ok {
			realtimeStateSessionItems = append(realtimeStateSessionItems, persisted)
		}
	}
	instructions, additionalInputItems := instructionsAndInputItemsWithAdditionalContext(instructions, params.AdditionalContext)
	if item, err := r.turnEnvironmentContextInputItemForTurn(ctx, threadID, params, cfg); err != nil {
		return nil, err
	} else if item != nil {
		inputItems = append(inputItems, item)
	}
	inputItems = append(inputItems, additionalInputItems...)
	collaborationModeSessionItems := []session.Item{}
	if item, err := r.collaborationModeWorldStateInputItem(threadID, params, modelInfo); err != nil {
		return nil, err
	} else if item != nil {
		inputItems = append(inputItems, item)
		if persisted, ok := collaborationModeWorldStateSessionItemForTurn(turnID, item, time.UnixMilli(startedAtMS).UTC()); ok {
			collaborationModeSessionItems = append(collaborationModeSessionItems, persisted)
		}
	}
	if item, err := r.multiAgentModeInputItem(threadID, params); err != nil {
		return nil, err
	} else if item != nil {
		inputItems = append(inputItems, item)
	}
	if item, err := r.multiAgentUsageHintInputItem(threadID, cfg); err != nil {
		return nil, err
	} else if item != nil {
		inputItems = append(inputItems, item)
	}
	if item, err := r.deferredToolsWorldStateInputItem(threadID, turnRuntime, features.Enabled(cfg.FeatureSettings(), "deferred_tool_world_state")); err != nil {
		return nil, err
	} else if item != nil {
		inputItems = append(inputItems, item)
	}
	inputItems = append(inputItems, r.recommendedPluginInputItems(cfg)...)
	inputItems = append(inputItems, r.explicitAppInputItems(threadID, params, cfg)...)
	currentTimeState := r.newCurrentTimeReminderTurnState(threadID)
	currentTimeInputItems, currentTimeSessionItems, err := r.currentTimeReminderInputItems(ctx, threadID, turnID, cfg, time.UnixMilli(startedAtMS).UTC(), currentTimeState)
	if err != nil {
		return nil, err
	}
	inputItems = append(inputItems, currentTimeInputItems...)
	instructions = r.instructionsWithPluginContext(threadID, cfg, params, instructions)
	var skillInputItems []any
	var postToolInputItems turn.ToolPostExecutionInputItems
	instructions, skillInputItems, postToolInputItems, err = r.instructionsWithSkillsContextForTurn(ctx, threadID, turnID, cfg, params, instructions)
	if err != nil {
		return nil, err
	}
	sessionItems := append([]session.Item(nil), currentTimeSessionItems...)
	sessionItems = append(sessionItems, realtimeStateSessionItems...)
	sessionItems = append(sessionItems, collaborationModeSessionItems...)
	sessionItems = append(sessionItems, skillInstructionSessionItemsForTurn(turnID, skillInputItems, time.UnixMilli(startedAtMS).UTC())...)
	var extraSessionItemsMu sync.Mutex
	extraSessionItems := []session.Item{}
	appendExtraSessionItems := func(items []session.Item) {
		if len(items) == 0 {
			return
		}
		extraSessionItemsMu.Lock()
		defer extraSessionItemsMu.Unlock()
		extraSessionItems = append(extraSessionItems, items...)
	}
	extraSessionItemsSnapshot := func() []session.Item {
		extraSessionItemsMu.Lock()
		defer extraSessionItemsMu.Unlock()
		return append([]session.Item(nil), extraSessionItems...)
	}
	postToolInputItems = r.currentTimePostToolInputItems(threadID, turnID, cfg, currentTimeState, postToolInputItems, appendExtraSessionItems)
	postToolInputItems = r.networkApprovalPostToolInputItems(threadID, turnID, postToolInputItems, appendExtraSessionItems)
	postToolInputItems = r.execPolicyPostToolInputItems(threadID, turnID, postToolInputItems, appendExtraSessionItems)
	inputItems = append(inputItems, skillInputItems...)
	extraMetadata := turn.MergeClientMetadata(cfg.ResponsesAPIClientMetadata(), params.ResponsesAPIMetadata)
	serviceTier := r.appServiceTierForTurn(cfg, params, modelProviderConfig.Model)
	hostedTools, err := r.hostedToolsForTurn(params, turnRuntime)
	if err != nil {
		return nil, err
	}
	cwd := firstNonEmpty(turnCWD(params), r.services.DefaultCWD)
	permissionProfile, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil {
		return nil, err
	}
	approvalPolicy := turnApprovalPolicyForTurn(cfg, params)
	protectedModel := autoReviewRequiredForModel(cfg, modelProviderConfig.Model)
	if protectedModel && permissionProfile != nil && permissionProfile.Profile != nil && permissionProfile.Profile.Disabled {
		// Rust 208f05b233: downgrade Full Access to workspace-write for
		// auto-review-protected models.
		workspace := sandbox.WorkspaceWritePermissionProfile()
		permissionProfile = &config.SandboxPermissionProfileResolution{ID: sandbox.BuiltInPermissionProfileWorkspace, Profile: &workspace}
	}
	if protectedModel {
		approvalPolicy = sandbox.ApprovalOnRequest
	}
	approvalsReviewer := turnApprovalsReviewerForTurn(cfg, params)
	if protectedModel {
		approvalsReviewer = string(config.ApprovalsReviewerAutoReview)
	}
	installationID := ""
	if r != nil && r.services.Config != nil {
		if codexHome := strings.TrimSpace(r.services.Config.CodexHome()); codexHome != "" {
			if id, err := install.ResolveInstallationID(codexHome); err == nil {
				installationID = id
			}
		}
	}
	lineage := r.responsesMetadataLineage(threadID)
	threadSnapshot := r.analyticsThreadSnapshot(threadID)
	unifiedExecEnabled := features.Enabled(cfg.FeatureSettings(), "unified_exec")
	if permissionProfile != nil && permissionProfile.Profile != nil && !permissionProfile.Profile.Disabled && runtime.GOOS != "linux" {
		unifiedExecEnabled = false
	}
	toolMode := ""
	autoReviewModelOverride := ""
	nodeReplAutoReviewRequired := false
	nodeReplDisabled := false
	if modelInfo != nil {
		toolMode = modelInfo.ToolMode
		autoReviewModelOverride = strings.TrimSpace(modelInfo.AutoReviewModelOverride)
		nodeReplAutoReviewRequired = modelInfo.NodeReplAutoReviewRequired
		nodeReplDisabled = modelInfo.NodeReplDisabled
	}
	toolMode = model.ResolveToolMode(toolMode, cfg.FeatureSettings())
	return &appTurnRunConfig{
		Model:                           modelProviderConfig.Model,
		AutoReviewModelOverride:         autoReviewModelOverride,
		ToolMode:                        toolMode,
		DisableCodeModeFallback:         cfg.DisableCodeModeInProcessFallback(),
		ProviderID:                      modelProviderConfig.ProviderID,
		Instructions:                    instructions,
		Originator:                      strings.TrimSpace(params.Originator),
		SessionID:                       firstNonEmpty(lineage.SessionID, threadSnapshot.SessionID, threadID),
		ThreadSource:                    lineage.ThreadSource,
		SubagentSource:                  lineage.SubagentKind,
		ParentThreadID:                  lineage.ParentThreadID,
		ParentTurnID:                    strings.TrimSpace(params.ParentTurnID),
		Ephemeral:                       threadSnapshot.Ephemeral,
		WorkspaceKind:                   strings.TrimSpace(extraMetadata["workspace_kind"]),
		NumInputImages:                  countTurnStartInputImages(params),
		IsFirstTurn:                     threadSnapshot.IsFirstTurn,
		ApprovalPolicy:                  string(approvalPolicy),
		ApprovalsReviewer:               approvalsReviewer,
		SandboxPolicy:                   analyticsSandboxPolicy(permissionProfile, cwd),
		SandboxNetworkAccess:            analyticsSandboxNetworkAccess(permissionProfile),
		CollaborationMode:               analyticsCollaborationMode(params),
		Personality:                     analyticsOptionalModeString(personality),
		InputItems:                      inputItems,
		HostedTools:                     hostedTools,
		SessionItems:                    sessionItems,
		ExtraSessionItems:               extraSessionItemsSnapshot,
		PostToolInputItems:              postToolInputItems,
		PreviousResponseID:              previousResponseID,
		ParallelToolCalls:               r.modelSupportsParallelToolCalls(modelProviderConfig.Model),
		ReasoningEffort:                 appReasoningEffortForTurn(cfg, params),
		ReasoningSummary:                stringPtrValue(params.Summary),
		ConcurrentReasoningSummaries:    features.Enabled(cfg.FeatureSettings(), "concurrent_reasoning_summaries"),
		ModelVerbosity:                  firstNonEmpty(stringConfigValue(cfg, "model_verbosity"), stringConfigValue(cfg, "modelVerbosity")),
		IncludeTimingMetrics:            appIncludeTimingMetrics(cfg),
		BetaFeaturesHeader:              features.ModelClientBetaFeaturesHeader(cfg.FeatureSettings()),
		ItemIDsEnabled:                  cfg.FeatureSettings()["item_ids"],
		PromptCacheKey:                  threadID,
		ServiceTier:                     serviceTier,
		Store:                           modelProviderConfig.Store,
		AttestationProvider:             r.appServerAttestationProvider(),
		UnifiedExecEnabled:              unifiedExecEnabled,
		ExecutedToolCallMetadataEnabled: features.Enabled(cfg.FeatureSettings(), "executed_tool_call_metadata"),
		ClientMetadata: turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
			InstallationID:             installationID,
			SessionID:                  firstNonEmpty(lineage.SessionID, threadID),
			ThreadID:                   threadID,
			TurnID:                     turnID,
			WindowID:                   threadID + ":1",
			RequestKind:                codexapi.ClientRequestTurn,
			ForkedFromThreadID:         lineage.ForkedFromThreadID,
			ParentThreadID:             lineage.ParentThreadID,
			ParentTurnID:               params.ParentTurnID,
			SubagentHeader:             lineage.SubagentHeader,
			SubagentKind:               lineage.SubagentKind,
			ThreadSource:               lineage.ThreadSource,
			SandboxMode:                permissionProfilePolicyTag(permissionProfile, cwd),
			AutoReviewEnabled:          autoReviewEnabledForTurn(cfg, params),
			NodeReplAutoReviewRequired: &nodeReplAutoReviewRequired,
			NodeReplDisabled:           &nodeReplDisabled,
			Extra:                      extraMetadata,
			ResponsesAPIMetadata:       cfg.ResponsesAPIMetadata(),
			StartedAtMS:                startedAtMS,
			UseResponsesLite:           r.modelUsesResponsesLite(modelProviderConfig.Model),
		}),
	}, nil
}

// autoReviewEnabledForTurn mirrors Rust routes_approval_policy_to_guardian
// (Rust f2a6f2585c): auto-review is enabled when the effective approval policy
// routes to Guardian (on-request or granular) with the auto_review reviewer.
func autoReviewEnabledForTurn(cfg *config.Config, params *turn.TurnStartParams) *bool {
	enabled := false
	policy := turnApprovalPolicyForTurn(cfg, params)
	reviewer := strings.TrimSpace(turnApprovalsReviewerForTurn(cfg, params))
	if (policy == sandbox.ApprovalOnRequest || policy == sandbox.ApprovalGranular) && strings.EqualFold(reviewer, string(config.ApprovalsReviewerAutoReview)) {
		enabled = true
	}
	return &enabled
}

// turnEnvironmentContextInputItem mirrors Rust's per-turn world-state context.
// In particular, the shell reported here must be the same primary environment
// shell that exec_command will use, otherwise models can emit syntax for the
// host shell (for example a POSIX heredoc) and hand it to remote PowerShell.
func (r *RuntimeRouter) turnEnvironmentContextInputItemForTurn(ctx context.Context, threadID string, params *turn.TurnStartParams, cfg *config.Config) (any, error) {
	if cfg != nil && !cfg.IncludeEnvironmentContext() {
		return nil, nil
	}
	current, err := r.environmentCurrentTime(ctx, threadID, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to read current time: %w", err)
	}
	params = r.environmentContextParams(threadID, params)
	text := r.turnEnvironmentContextTextAt(params, current.In(time.Local), localTimezoneName())
	return model.UserMessageInputItem(text), nil
}

func (r *RuntimeRouter) turnEnvironmentContextInputItem(params *turn.TurnStartParams) any {
	params = r.environmentContextParams("", params)
	return model.UserMessageInputItem(r.turnEnvironmentContextTextAt(params, runtimeRouterClockTime(r).In(time.Local), localTimezoneName()))
}

func (r *RuntimeRouter) environmentCurrentTime(ctx context.Context, threadID string, cfg *config.Config) (time.Time, error) {
	if cfg != nil {
		if reminder := cfg.CurrentTimeReminder(); reminder != nil && reminder.Enabled && reminder.ClockSource == config.CurrentTimeSourceExternal {
			return r.requestCurrentTime(ctx, threadID)
		}
	}
	return runtimeRouterClockTime(r), nil
}

func runtimeRouterClockTime(r *RuntimeRouter) time.Time {
	if r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.now != nil {
		if now := r.services.ThreadRouter.now(); !now.IsZero() {
			return now
		}
	}
	return time.Now()
}

func (r *RuntimeRouter) environmentContextParams(threadID string, params *turn.TurnStartParams) *turn.TurnStartParams {
	if params == nil {
		params = &turn.TurnStartParams{}
	} else {
		clone := *params
		params = &clone
	}
	if strings.TrimSpace(params.CWD) == "" && strings.TrimSpace(threadID) != "" {
		if record, err := r.threadRecord(session.ThreadID(threadID), true, true); err == nil && record != nil {
			params.CWD = strings.TrimSpace(record.Metadata.CWD)
		}
	}
	return params
}

func (r *RuntimeRouter) turnEnvironmentContextTextAt(params *turn.TurnStartParams, now time.Time, timezone string) string {
	environments := r.unifiedExecEnvironmentsForTurn(params)
	defaultShellName := r.defaultEnvironmentShellName()
	var b strings.Builder
	b.WriteString("<environment_context>\n")
	if len(environments) <= 1 {
		var environment tool.UnifiedExecEnvironment
		if len(environments) == 1 {
			environment = environments[0]
		}
		cwd := strings.TrimSpace(firstNonEmpty(environment.CWD, turnCWD(params), r.services.DefaultCWD, "."))
		shellName := unifiedEnvironmentShellName(environment, defaultShellName)
		fmt.Fprintf(&b, "  <cwd>%s</cwd>\n", escapeEnvironmentXML(cwd))
		fmt.Fprintf(&b, "  <shell>%s</shell>\n", escapeEnvironmentXML(shellName))
	} else {
		b.WriteString("  <environments>\n")
		for i, environment := range environments {
			fmt.Fprintf(&b, "    <environment id=\"%s\" primary=\"%t\">\n", escapeEnvironmentXML(environment.ID), i == 0)
			fmt.Fprintf(&b, "      <cwd>%s</cwd>\n", escapeEnvironmentXML(strings.TrimSpace(environment.CWD)))
			if shellName := unifiedEnvironmentShellName(environment, ""); shellName != "" {
				fmt.Fprintf(&b, "      <shell>%s</shell>\n", escapeEnvironmentXML(shellName))
			} else {
				b.WriteString("      <status>starting</status>\n")
			}
			b.WriteString("    </environment>\n")
		}
		b.WriteString("  </environments>\n")
	}
	fmt.Fprintf(&b, "  <current_date>%s</current_date>\n", escapeEnvironmentXML(now.Format("2006-01-02")))
	fmt.Fprintf(&b, "  <timezone>%s</timezone>\n", escapeEnvironmentXML(timezone))
	b.WriteString("</environment_context>")
	return b.String()
}

func (r *RuntimeRouter) defaultEnvironmentShellName() string {
	if r != nil && r.services.Environment != nil {
		if info := r.services.Environment.defaultShell; strings.TrimSpace(info.Name) != "" {
			return strings.TrimSpace(info.Name)
		} else if strings.TrimSpace(info.Path) != "" {
			return string(tool.DetectShellType(info.Path))
		}
	}
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "sh"
}

func unifiedEnvironmentShellName(environment tool.UnifiedExecEnvironment, fallback string) string {
	if environment.Shell != nil && environment.Shell.Type != "" {
		return string(environment.Shell.Type)
	}
	return strings.TrimSpace(fallback)
}

func escapeEnvironmentXML(value string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(value)); err != nil {
		return value
	}
	return b.String()
}

type appTurnAnalyticsThreadSnapshot struct {
	SessionID   string
	Ephemeral   bool
	IsFirstTurn bool
}

func (r *RuntimeRouter) analyticsThreadSnapshot(threadID string) appTurnAnalyticsThreadSnapshot {
	threadID = strings.TrimSpace(threadID)
	snapshot := appTurnAnalyticsThreadSnapshot{SessionID: threadID, IsFirstTurn: true}
	if r == nil || threadID == "" {
		return snapshot
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return snapshot
	}
	if sessionID := strings.TrimSpace(record.SessionID); sessionID != "" {
		snapshot.SessionID = sessionID
	}
	snapshot.Ephemeral = runtimeRecordEphemeral(record)
	snapshot.IsFirstTurn = len(record.Items) == 0 && len(record.Metadata.RolloutTurns) == 0
	return snapshot
}

func turnApprovalsReviewerForTurn(cfg *config.Config, params *turn.TurnStartParams) string {
	if params != nil && params.ApprovalsReviewer != nil {
		if value := strings.TrimSpace(*params.ApprovalsReviewer); value != "" {
			if autoReviewProtectedModel(cfg, params) {
				// Rust 208f05b233: models listed in auto_review.required_on_models
				// always use the auto_review reviewer.
				return string(config.ApprovalsReviewerAutoReview)
			}
			return value
		}
	}
	value := firstNonEmpty(
		stringConfigValue(cfg, "approvals_reviewer"),
		stringConfigValue(cfg, "approvalsReviewer"),
	)
	if autoReviewProtectedModel(cfg, params) {
		return string(config.ApprovalsReviewerAutoReview)
	}
	if value == "" {
		return "user"
	}
	return value
}

// autoReviewProtectedModel reports whether the effective turn model is listed in
// auto_review.required_on_models (Rust 208f05b233).
func autoReviewProtectedModel(cfg *config.Config, params *turn.TurnStartParams) bool {
	if params == nil {
		return false
	}
	model := strings.TrimSpace(params.Model)
	if model == "" && cfg != nil {
		model = stringConfigValue(cfg, "model")
	}
	return autoReviewRequiredForModel(cfg, model)
}

func analyticsSandboxPolicy(resolution *config.SandboxPermissionProfileResolution, cwd string) string {
	if resolution == nil || resolution.Profile == nil {
		return ""
	}
	profile := resolution.Profile
	if profile.Disabled {
		return "full_access"
	}
	policy := profile.SandboxPolicy
	if policy == nil {
		if profile.AllowsNetwork() {
			return "full_access"
		}
		return "read_only"
	}
	if policy.HasFullDiskWriteAccess() {
		if profile.AllowsNetwork() {
			return "full_access"
		}
		return "external_sandbox"
	}
	if len(policy.GetWritableRootsWithCWD(cwd)) == 0 {
		return "read_only"
	}
	return "workspace_write"
}

func analyticsSandboxNetworkAccess(resolution *config.SandboxPermissionProfileResolution) bool {
	return resolution != nil && resolution.Profile != nil && resolution.Profile.AllowsNetwork()
}

// autoReviewRequiredForModel mirrors Rust's auto_review_required_for_model
// (codex-rs/config/src/config_requirements.rs, Rust 208f05b233): models listed
// in auto_review.required_on_models (or their exact provider-alias suffix) must
// run with on-request approvals and the auto_review reviewer.
func autoReviewRequiredForModel(cfg *config.Config, model string) bool {
	if cfg == nil || cfg.Requirements == nil {
		return false
	}
	return cfg.Requirements.AutoReviewRequiredForModel(model)
}

// permissionProfilePolicyTag mirrors Rust's permission_profile_policy_tag
// (codex-rs/core/src/sandbox_tags.rs, Rust 4ca25a2c4e): it derives the
// `sandbox_mode` turn-metadata value from the effective permission profile and
// the working directory.
func permissionProfilePolicyTag(resolution *config.SandboxPermissionProfileResolution, cwd string) string {
	if resolution == nil {
		return "danger-full-access"
	}
	return permissionProfilePolicyTagFromProfile(resolution.Profile, cwd)
}

func permissionProfilePolicyTagFromProfile(profile *sandbox.PermissionProfile, cwd string) string {
	if profile == nil {
		return "danger-full-access"
	}
	if profile.Disabled {
		return "danger-full-access"
	}
	policy := profile.SandboxPolicy
	if policy == nil {
		if profile.AllowsNetwork() {
			return "danger-full-access"
		}
		return "read-only"
	}
	if policy.HasFullDiskWriteAccess() {
		return "danger-full-access"
	}
	if len(policy.GetWritableRootsWithCWD(cwd)) == 0 {
		return "read-only"
	}
	return "workspace-write"
}

func analyticsCollaborationMode(params *turn.TurnStartParams) string {
	if params != nil && params.CollaborationMode != nil {
		switch strings.ToLower(strings.TrimSpace(stringFromAny(params.CollaborationMode["mode"]))) {
		case strings.ToLower(string(ModeKindPlan)):
			return "plan"
		}
	}
	return "default"
}

func analyticsOptionalModeString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") {
		return ""
	}
	return value
}

func countTurnStartInputImages(params *turn.TurnStartParams) int {
	if params == nil {
		return 0
	}
	return countTurnUserInputImages(params.Input)
}

func countTurnSteerInputImages(params *turn.TurnSteerParams) int {
	if params == nil {
		return 0
	}
	return countTurnUserInputImages(params.Input)
}

func countTurnUserInputImages(inputs []turn.TurnUserInput) int {
	count := 0
	for i := range inputs {
		input := inputs[i]
		if strings.TrimSpace(input.URL) != "" || strings.TrimSpace(input.Path) != "" {
			count++
		}
	}
	return count
}

func (r *RuntimeRouter) emitCodexTurnAnalyticsEvent(ctx context.Context, connectionID string, params *turn.TurnStartParams, record *turn.TurnRecord, runConfig *appTurnRunConfig, result *turn.AgentLoopResult, status TurnStatus, startedAt time.Time, completedAt time.Time, durationMS int64, steerCount int, turnError CodexErrorInfo, codexErrorKind *string, codexErrorHTTPStatusCode *uint16, explicitClientInterruptRequestedAtMS *uint64) {
	if r == nil || r.services.Analytics == nil || params == nil || record == nil || runConfig == nil {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	event := telemetry.NewCodexTurnEvent(telemetry.CodexTurnEventInput{
		ThreadID:                             params.ThreadID,
		SessionID:                            firstNonEmpty(runConfig.SessionID, params.ThreadID),
		TurnID:                               record.ID,
		AppServerClient:                      client,
		ThreadOriginator:                     runConfig.Originator,
		Runtime:                              telemetry.CurrentRuntimeMetadata(),
		Ephemeral:                            runConfig.Ephemeral,
		ThreadSource:                         stringPtrIfNotEmpty(runConfig.ThreadSource),
		InitializationMode:                   "new",
		SubagentSource:                       stringPtrIfNotEmpty(runConfig.SubagentSource),
		ParentThreadID:                       stringPtrIfNotEmpty(runConfig.ParentThreadID),
		Model:                                stringPtrIfNotEmpty(runConfig.Model),
		ModelProvider:                        runConfig.ProviderID,
		SandboxPolicy:                        stringPtrIfNotEmpty(runConfig.SandboxPolicy),
		ReasoningEffort:                      stringPtrIfNotEmpty(analyticsOptionalModeString(runConfig.ReasoningEffort)),
		ReasoningSummary:                     stringPtrIfNotEmpty(analyticsOptionalModeString(runConfig.ReasoningSummary)),
		ServiceTier:                          runConfig.ServiceTier,
		ApprovalPolicy:                       firstNonEmpty(runConfig.ApprovalPolicy, string(sandbox.ApprovalOnRequest)),
		ApprovalsReviewer:                    firstNonEmpty(runConfig.ApprovalsReviewer, "user"),
		SandboxNetworkAccess:                 runConfig.SandboxNetworkAccess,
		CollaborationMode:                    stringPtrIfNotEmpty(firstNonEmpty(runConfig.CollaborationMode, "default")),
		Personality:                          stringPtrIfNotEmpty(runConfig.Personality),
		WorkspaceKind:                        stringPtrIfNotEmpty(runConfig.WorkspaceKind),
		NumInputImages:                       runConfig.NumInputImages,
		IsFirstTurn:                          runConfig.IsFirstTurn,
		Status:                               stringPtrIfNotEmpty(string(status)),
		ExplicitClientInterruptRequestedAtMS: explicitClientInterruptRequestedAtMS,
		TurnError:                            turnError,
		CodexErrorKind:                       codexErrorKind,
		CodexErrorHTTPStatusCode:             codexErrorHTTPStatusCode,
		SteerCount:                           intPtrAppserver(steerCount),
		RunningBackgroundProcessCount:        intPtrAppserver(r.requireThreadExtras().CountBackgroundTerminals(params.ThreadID)),
		ToolCounts:                           analyticsTurnToolCounts(result),
		TokenUsage:                           analyticsTurnTokenUsage(result),
		TimingProfile:                        analyticsTurnTimingProfile(result),
		DurationMS:                           uint64PtrFromNonNegativeInt64(durationMS),
		StartedAt:                            uint64PtrFromUnixSeconds(startedAt),
		CompletedAt:                          uint64PtrFromUnixSeconds(completedAt),
	})
	r.services.Analytics.TrackCodexTurnEvent(ctx, event)
}

func (r *RuntimeRouter) analyticsAppServerClient(connectionID string) (telemetry.CodexAppServerClientMetadata, bool) {
	if r == nil {
		return telemetry.CodexAppServerClientMetadata{}, false
	}
	info, ok := r.connectionClientInfo(connectionID)
	if !ok {
		return telemetry.CodexAppServerClientMetadata{}, false
	}
	transport := r.services.AnalyticsRPCTransport
	if transport == "" {
		transport = telemetry.AppServerRPCTransportInProcess
	}
	return telemetry.CodexAppServerClientMetadata{
		ProductClientID:       initializeOriginator(info),
		ClientName:            stringPtrIfNotEmpty(strings.TrimSpace(info.Name)),
		ClientVersion:         stringPtrIfNotEmpty(strings.TrimSpace(info.Version)),
		RPCTransport:          transport,
		ExperimentalAPIEnable: r.connectionExperimentalAPI(connectionID),
	}, true
}

func analyticsTurnToolCounts(result *turn.AgentLoopResult) *telemetry.CodexTurnToolCounts {
	counts := &telemetry.CodexTurnToolCounts{}
	if result == nil {
		return counts
	}
	for i := range result.ToolExecutions {
		execution := &result.ToolExecutions[i]
		if execution.Invocation == nil {
			continue
		}
		counts.Total++
		name := execution.Invocation.ToolName
		key := name.Key()
		switch {
		case tool.IsShellCommandToolName(name):
			counts.ShellCommand++
		case key == tool.DefaultApplyPatchToolName:
			counts.FileChange++
		case name.Namespace == turn.WebSearchNamespace && name.Name == turn.WebSearchRunTool:
			counts.WebSearch++
		case strings.HasPrefix(name.Namespace, "mcp__") || strings.HasPrefix(key, "mcp__"):
			counts.MCPToolCall++
		case name.Namespace == turn.ImageGenerationNamespace && name.Name == turn.ImageGenerationToolName:
			counts.ImageGeneration++
		case strings.Contains(key, "image_generation") || strings.Contains(key, "imageGeneration"):
			counts.ImageGeneration++
		case name.Namespace != "":
			counts.DynamicToolCall++
		}
	}
	return counts
}

func analyticsTurnTokenUsage(result *turn.AgentLoopResult) *telemetry.CodexTurnTokenUsage {
	usage := tokenUsageFromAgentLoopResult(result)
	if usage == nil {
		return nil
	}
	return &telemetry.CodexTurnTokenUsage{
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		TotalTokens:           usage.TotalTokens,
	}
}

func analyticsTurnTimingProfile(result *turn.AgentLoopResult) telemetry.CodexTurnTimingProfile {
	if result == nil || result.TimingProfile == nil {
		return telemetry.CodexTurnTimingProfile{}
	}
	profile := result.TimingProfile
	return telemetry.CodexTurnTimingProfile{
		BeforeFirstSamplingMS:     profile.BeforeFirstSamplingMS,
		SamplingMS:                profile.SamplingMS,
		BetweenSamplingOverheadMS: profile.BetweenSamplingOverheadMS,
		ToolBlockingMS:            profile.ToolBlockingMS,
		AfterLastSamplingMS:       profile.PendingIdleAfterSamplingMS,
		SamplingRequestCount:      profile.SamplingRequestCount,
		SamplingRetryCount:        profile.SamplingRetryCount,
	}
}

func intPtrAppserver(value int) *int {
	return &value
}

func uint64PtrFromNonNegativeInt64(value int64) *uint64 {
	if value < 0 {
		return nil
	}
	out := uint64(value)
	return &out
}

func uint64PtrFromUnixSeconds(value time.Time) *uint64 {
	if value.IsZero() {
		return nil
	}
	unix := value.UTC().Unix()
	if unix < 0 {
		return nil
	}
	out := uint64(unix)
	return &out
}

func (r *RuntimeRouter) steerClientMetadata(params *turn.TurnSteerParams) map[string]string {
	if r == nil || params == nil || len(params.ResponsesAPIMetadata) == 0 {
		return nil
	}
	active := r.activeRuntimeTurnStateSnapshot(params.ThreadID, params.ExpectedTurnID)
	if active == nil {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil {
		cfg = nil
	}
	extraMetadata := turn.MergeClientMetadata(nil, params.ResponsesAPIMetadata)
	if cfg != nil {
		extraMetadata = turn.MergeClientMetadata(cfg.ResponsesAPIClientMetadata(), params.ResponsesAPIMetadata)
	}
	installationID := ""
	if r.services.Config != nil {
		if codexHome := strings.TrimSpace(r.services.Config.CodexHome()); codexHome != "" {
			if id, err := install.ResolveInstallationID(codexHome); err == nil {
				installationID = id
			}
		}
	}
	modelID := ""
	if cfg != nil {
		modelID = stringConfigValue(cfg, "model")
	}
	nodeReplAutoReviewRequired := false
	nodeReplDisabled := false
	if cfg != nil && modelID != "" {
		if info := r.modelInfoForRuntimeWithConfig(modelID, cfg); info != nil {
			nodeReplAutoReviewRequired = info.NodeReplAutoReviewRequired
			nodeReplDisabled = info.NodeReplDisabled
		}
	}
	lineage := r.responsesMetadataLineage(params.ThreadID)
	parentTurnID := ""
	if active.Params != nil {
		parentTurnID = active.Params.ParentTurnID
	}
	return turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
		InstallationID:             installationID,
		SessionID:                  firstNonEmpty(lineage.SessionID, params.ThreadID),
		ThreadID:                   params.ThreadID,
		TurnID:                     params.ExpectedTurnID,
		WindowID:                   params.ThreadID + ":1",
		RequestKind:                codexapi.ClientRequestTurn,
		ForkedFromThreadID:         lineage.ForkedFromThreadID,
		ParentThreadID:             lineage.ParentThreadID,
		ParentTurnID:               parentTurnID,
		SubagentHeader:             lineage.SubagentHeader,
		SubagentKind:               lineage.SubagentKind,
		ThreadSource:               lineage.ThreadSource,
		NodeReplAutoReviewRequired: &nodeReplAutoReviewRequired,
		NodeReplDisabled:           &nodeReplDisabled,
		Extra:                      extraMetadata,
		ResponsesAPIMetadata:       cfg.ResponsesAPIMetadata(),
		StartedAtMS:                active.StartedAtMS,
		UseResponsesLite:           r.modelUsesResponsesLite(modelID),
	})
}

func (r *RuntimeRouter) responsesMetadataLineage(threadID string) responsesMetadataLineage {
	threadID = strings.TrimSpace(threadID)
	lineage := responsesMetadataLineage{SessionID: threadID}
	if r == nil || threadID == "" {
		return lineage
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return lineage
	}
	if sessionID := strings.TrimSpace(record.SessionID); sessionID != "" {
		lineage.SessionID = sessionID
	}
	lineage.ThreadSource = strings.TrimSpace(record.Metadata.ThreadSource)
	source := strings.TrimSpace(record.Metadata.Source)
	lineage.SubagentHeader, lineage.SubagentKind = codexapi.ClientSubagentMetadataFromSource(source)
	if runtimeSessionSourceIsSubagent(source) || lineage.SubagentHeader != "" || lineage.SubagentKind != "" {
		lineage.ParentThreadID = firstNonEmpty(strings.TrimSpace(string(record.ParentThreadID)), runtimeParentThreadIDFromSessionSource(source))
		lineage.ForkedFromThreadID = strings.TrimSpace(string(record.ForkedFromID))
		return lineage
	}
	lineage.ForkedFromThreadID = strings.TrimSpace(string(record.ForkedFromID))
	return lineage
}

func runtimeSessionSourceIsSubagent(source string) bool {
	normalized := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(normalized, "subagent:") ||
		strings.HasPrefix(normalized, "subagent_") ||
		strings.HasPrefix(normalized, "subagent-")
}

func runtimeParentThreadIDFromSessionSource(source string) string {
	normalized := strings.ToLower(strings.TrimSpace(source))
	for _, prefix := range []string{"subagent_thread_spawn_", "subagent-thread-spawn-"} {
		if !strings.HasPrefix(normalized, prefix) {
			continue
		}
		rest := strings.TrimSpace(source[len(prefix):])
		if rest == "" {
			return ""
		}
		if idx := strings.LastIndex(rest, "_d"); idx > 0 {
			return rest[:idx]
		}
		if idx := strings.LastIndex(rest, "-d"); idx > 0 {
			return rest[:idx]
		}
		return rest
	}
	return ""
}

func (r *RuntimeRouter) activeRuntimeTurnStateSnapshot(threadID string, turnID string) *activeRuntimeTurn {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return nil
	}
	active := r.threads.ActiveTurn(threadID)
	if active == nil || active.TurnID != turnID {
		return nil
	}
	return active
}

func (r *RuntimeRouter) historyInputItemsForTurn(threadID string) ([]any, string) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || strings.TrimSpace(threadID) == "" {
		return nil, ""
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return nil, ""
	}
	previousResponseID := firstNonEmpty(record.Metadata.LastResponseID, record.Metadata.PreviousResponseID)
	items := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: strings.TrimSpace(record.Metadata.CWD)})
	return items, previousResponseID
}

func modelInputTextMessage(role string, text string) map[string]any {
	role = strings.TrimSpace(role)
	if role == "" {
		role = contextfrag.RoleUser
	}
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{{
			"type": "input_text",
			"text": strings.TrimSpace(text),
		}},
	}
}

func instructionsAndInputItemsWithAdditionalContext(instructions string, values map[string]turn.AdditionalContextEntry) (string, []any) {
	if len(values) == 0 {
		return strings.TrimSpace(instructions), nil
	}
	developer := []string{}
	inputItems := []any{}
	for _, key := range sortedAdditionalContextKeys(values) {
		entry := values[key]
		role := contextfrag.RoleUser
		if entry.Kind == turn.AdditionalContextApplication {
			role = contextfrag.RoleDeveloper
		}
		rendered := contextfrag.Render(contextfrag.NewAdditionalContextFragment(role, key, entry.Value))
		if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
			continue
		}
		if rendered.Role == contextfrag.RoleDeveloper {
			developer = append(developer, rendered.Content)
			continue
		}
		inputItems = append(inputItems, map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": rendered.Content,
			}},
		})
	}
	if len(developer) > 0 {
		sections := append(developer, strings.TrimSpace(instructions))
		instructions = strings.Join(nonEmpty(sections), "\n\n")
	}
	return strings.TrimSpace(instructions), inputItems
}

func (r *RuntimeRouter) recommendedPluginInputItems(cfg *config.Config) []any {
	candidates := pluginInstallRecommendationCandidates(r.pluginInstallCandidatesForTurn(cfg))
	if len(candidates) == 0 {
		return nil
	}
	plugins := make([]contextfrag.RecommendedPlugin, 0, len(candidates))
	for _, candidate := range candidates {
		plugins = append(plugins, contextfrag.RecommendedPlugin{ID: candidate.ID, Name: candidate.Name})
	}
	rendered := contextfrag.Render(contextfrag.NewRecommendedPluginsInstructions(plugins))
	if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
		return nil
	}
	return []any{map[string]any{
		"type": "message",
		"role": "user",
		"content": []map[string]any{{
			"type": "input_text",
			"text": rendered.Content,
		}},
	}}
}

func (r *RuntimeRouter) explicitAppInputItems(threadID string, params *turn.TurnStartParams, cfg *config.Config) []any {
	mentioned := plugin.CollectExplicitAppIDs(pluginUserInputFromTurn(params))
	if len(mentioned) == 0 {
		return nil
	}
	appsByID := r.appsForExplicitMentions(threadID, cfg)
	items := make([]any, 0, len(mentioned))
	for _, id := range sortedBoolKeys(mentioned) {
		app, ok := appsByID[id]
		if !ok {
			installURL := apps.ConnectorInstallURL(id, id)
			app = apps.AppEntry{ID: id, Name: id, InstallURL: &installURL}
		}
		rendered := contextfrag.Render(contextfrag.NewAppInstructions(appInstructionsData(&app)))
		if item := renderedFragmentInputItem(rendered); item != nil {
			items = append(items, item)
		}
	}
	return items
}

func (r *RuntimeRouter) appsForExplicitMentions(threadID string, cfg *config.Config) map[string]apps.AppEntry {
	out := map[string]apps.AppEntry{}
	if r == nil {
		return out
	}
	service := r.requireApps()
	if r.services.Plugins != nil {
		service.SetPluginConnectors(appPluginConnectorsFromCapabilities(r.services.Plugins.EnabledCapabilities()))
	}
	var configValues map[string]any
	if cfg != nil {
		configValues = cfg.Values
		service.SetConfigValues(configValues)
	} else if r.services.Config != nil {
		read, err := r.services.Config.Read(&config.ConfigReadParams{})
		if err == nil && read != nil {
			configValues = read.Config
			service.SetConfigValues(read.Config)
		}
	}
	r.configureAppDirectoryProvider(service, configValues)
	r.configureAppAccessibleProvider(service)
	response, err := service.List(&apps.AppListParams{ThreadID: stringPtrIfNotEmpty(threadID)})
	if err != nil || response == nil {
		return out
	}
	for _, app := range response.Data {
		id := strings.TrimSpace(app.ID)
		if id == "" {
			continue
		}
		out[id] = app
	}
	return out
}

func appInstructionsData(app *apps.AppEntry) contextfrag.AppInstructionsData {
	if app == nil {
		return contextfrag.AppInstructionsData{}
	}
	description := ""
	if app.Description != nil {
		description = *app.Description
	}
	installURL := ""
	if app.InstallURL != nil {
		installURL = *app.InstallURL
	}
	return contextfrag.AppInstructionsData{
		ID:                 app.ID,
		Name:               app.Name,
		Description:        description,
		InstallURL:         installURL,
		IsAccessible:       app.IsAccessible,
		IsEnabled:          appEnabledForRuntime(app),
		PluginDisplayNames: append([]string(nil), app.PluginDisplayNames...),
	}
}

func appEnabledForRuntime(app *apps.AppEntry) bool {
	if app == nil {
		return false
	}
	if app.EnabledExplicit {
		return app.IsEnabled
	}
	if app.IsEnabled || !app.Enabled {
		return app.IsEnabled
	}
	return app.Enabled
}

func (r *RuntimeRouter) pluginInstallCandidatesForTurn(cfg *config.Config) []plugin.DiscoverableInfo {
	return r.pluginInstallCandidatesForTurnContext(context.Background(), cfg)
}

func (r *RuntimeRouter) pluginInstallCandidatesForTurnContext(ctx context.Context, cfg *config.Config) []plugin.DiscoverableInfo {
	if r == nil || r.services.Plugins == nil {
		return nil
	}
	r.configureSuggestedPluginProviderForTurn(cfg)
	return plugin.ListDiscoverablePlugins(r.services.Plugins.DiscoverableInstallCandidatesContext(ctx), r.pluginDiscoverableConfigForTurn(cfg))
}

func pluginInstallRecommendationCandidates(candidates []plugin.DiscoverableInfo) []plugin.DiscoverableInfo {
	out := make([]plugin.DiscoverableInfo, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.ToolType) == "connector" {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func (r *RuntimeRouter) pluginDiscoverableConfigForTurn(cfg *config.Config) *plugin.DiscoverableConfig {
	if r == nil {
		return nil
	}
	out := &plugin.DiscoverableConfig{}
	values := map[string]any{}
	if cfg != nil && cfg.Values != nil {
		values = cfg.Values
	} else if r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil && read.Config != nil {
			values = read.Config
		}
	}
	for _, item := range disabledToolSuggestEntries(values) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		id = strings.TrimSpace(id)
		if id != "" {
			out.DisabledPluginIDs = append(out.DisabledPluginIDs, id)
		}
	}
	if r.services.Apps != nil {
		for id, app := range r.appsForExplicitMentions("", &config.Config{Values: values}) {
			if app.IsAccessible && strings.TrimSpace(id) != "" {
				out.LoadedAppConnectorIDs = append(out.LoadedAppConnectorIDs, id)
			}
		}
	}
	if len(out.ConfiguredPluginIDs) == 0 && len(out.DisabledPluginIDs) == 0 && len(out.LoadedAppConnectorIDs) == 0 {
		return nil
	}
	sort.Strings(out.DisabledPluginIDs)
	sort.Strings(out.LoadedAppConnectorIDs)
	return out
}

func (r *RuntimeRouter) configureSuggestedPluginProviderForTurn(cfg *config.Config) {
	if r == nil || r.services.Plugins == nil {
		return
	}
	if cfg == nil && r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil {
			cfg = &config.Config{Values: read.Config}
		}
	}
	if !suggestedPluginsEnabledForConfig(cfg) || r.services.Config == nil {
		r.services.Plugins.SetSuggestedPluginProviderWithKey(nil, "")
		return
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	resolved, err := r.resolveAuthWithLoginRestrictions(codexHome)
	if err != nil || resolved == nil || (&resolved.Auth).BackendMode() != "chatgpt" {
		r.services.Plugins.SetSuggestedPluginProviderWithKey(nil, "")
		return
	}
	accessToken := suggestedPluginAccessTokenFromAuth(&resolved.Auth)
	if accessToken == "" {
		r.services.Plugins.SetSuggestedPluginProviderWithKey(nil, "")
		return
	}
	var client plugin.HTTPDoer
	if r.services.HTTPClient != nil {
		client = r.services.HTTPClient
	}
	baseURL := cfg.ChatGPTBaseURL()
	accountID := suggestedPluginAccountIDFromAuth(&resolved.Auth)
	provider := plugin.NewHTTPSuggestedPluginProvider(baseURL, accessToken, accountID, client)
	r.services.Plugins.SetSuggestedPluginProviderWithKey(provider, strings.TrimRight(strings.TrimSpace(baseURL), "/"))
}

func suggestedPluginAccessTokenFromAuth(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil || snapshot.Tokens == nil {
		return ""
	}
	return stringFromAnyMap(snapshot.Tokens, "access_token")
}

func suggestedPluginAccountIDFromAuth(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil || snapshot.Tokens == nil {
		return ""
	}
	return firstNonEmpty(
		stringFromAnyMap(snapshot.Tokens, "chatgpt_account_id"),
		stringFromAnyMap(snapshot.Tokens, "account_id"),
	)
}

func stringFromAnyMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func suggestedPluginsEnabledForConfig(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	settings := cfg.FeatureSettings()
	return features.Enabled(settings, "apps") &&
		features.Enabled(settings, "plugins") &&
		(features.Enabled(settings, "tool_suggest") || features.Enabled(settings, "recommended_plugins"))
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key != "" && value {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func sortedAdditionalContextKeys(values map[string]turn.AdditionalContextEntry) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func (r *RuntimeRouter) effectiveConfigForTurn(params *turn.TurnStartParams) (*config.Config, error) {
	cfg := &config.Config{Values: map[string]any{}}
	if r == nil || r.services.Config == nil {
		applyRuntimeConfigOverrides(cfg, turnConfigOverrides(params))
		return cfg, nil
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		applyRuntimeConfigOverrides(cfg, turnConfigOverrides(params))
		return cfg, nil
	}
	loaded, err := config.LoadWithOptions(codexHome, &config.LoadOptions{CWD: turnCWD(params)})
	if err != nil {
		return nil, err
	}
	if loaded != nil {
		cfg = loaded
	}
	if cfg.Requirements == nil {
		if requirements := r.services.Config.Requirements(); requirements != nil {
			cfg.Requirements = requirements.Requirements
		}
	}
	applyRuntimeConfigOverrides(cfg, turnConfigOverrides(params))
	return cfg, nil
}

func turnConfigOverrides(params *turn.TurnStartParams) map[string]any {
	if params == nil {
		return nil
	}
	return params.Config
}

func applyRuntimeConfigOverrides(cfg *config.Config, overrides map[string]any) {
	if cfg == nil || len(overrides) == 0 {
		return
	}
	if cfg.Values == nil {
		cfg.Values = map[string]any{}
	}
	configOverrides := make([]config.Override, 0, len(overrides))
	for key, value := range overrides {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		configOverrides = append(configOverrides, config.Override{
			Path:  config.CanonicalizeKey(key),
			Value: value,
		})
	}
	config.ApplyOverrides(cfg.Values, configOverrides)
}

type currentTimeReminderTurnState struct {
	mu          sync.Mutex
	hasLast     bool
	lastTimeUTC time.Time
}

func (r *RuntimeRouter) newCurrentTimeReminderTurnState(threadID string) *currentTimeReminderTurnState {
	state := &currentTimeReminderTurnState{}
	if last, ok := r.lastCurrentTimeReminderTime(threadID); ok {
		state.hasLast = true
		state.lastTimeUTC = last.UTC()
	}
	return state
}

func (s *currentTimeReminderTurnState) due(now time.Time, intervalSeconds uint64) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasLast {
		return true
	}
	if intervalSeconds == 0 {
		return true
	}
	return now.UTC().Sub(s.lastTimeUTC) >= time.Duration(intervalSeconds)*time.Second
}

func (s *currentTimeReminderTurnState) noteDelivered(now time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasLast = true
	s.lastTimeUTC = now.UTC()
}

func (r *RuntimeRouter) currentTimeReminderInputItems(ctx context.Context, threadID string, turnID string, cfg *config.Config, createdAt time.Time, state *currentTimeReminderTurnState) ([]any, []session.Item, error) {
	if cfg == nil {
		return nil, nil, nil
	}
	reminder := cfg.CurrentTimeReminder()
	if reminder == nil || !reminder.Enabled {
		return nil, nil, nil
	}
	now := runtimeRouterClockTime(r).UTC()
	location := "UTC"
	if reminder.ClockSource == config.CurrentTimeSourceExternal {
		current, err := r.requestCurrentTime(ctx, threadID)
		if err != nil {
			return nil, nil, err
		}
		now = current
		location = "external"
	}
	if !state.due(now, reminder.ReminderIntervalSeconds) {
		return nil, nil, nil
	}
	rendered := contextfrag.Render(&contextfrag.CurrentTimeReminder{Now: now, Location: location})
	if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
		return nil, nil, nil
	}
	state.noteDelivered(now)
	sessionItem := currentTimeReminderSessionItem(turnID, rendered, now, location, createdAt)
	return []any{modelInputTextMessage(rendered.Role, rendered.Content)}, []session.Item{sessionItem}, nil
}

func (r *RuntimeRouter) currentTimePostToolInputItems(threadID string, turnID string, cfg *config.Config, state *currentTimeReminderTurnState, base turn.ToolPostExecutionInputItems, appendSessionItems func([]session.Item)) turn.ToolPostExecutionInputItems {
	if cfg == nil {
		return base
	}
	reminder := cfg.CurrentTimeReminder()
	if reminder == nil || !reminder.Enabled {
		return base
	}
	return func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any {
		items := []any{}
		if base != nil {
			items = append(items, base(ctx, invocation, output)...)
		}
		createdAt := time.Now().UTC()
		if output != nil && !output.CompletedAt.IsZero() {
			createdAt = output.CompletedAt.UTC()
		}
		currentInputItems, currentSessionItems, err := r.currentTimeReminderInputItems(ctx, threadID, turnID, cfg, createdAt, state)
		if err != nil {
			return items
		}
		if len(currentSessionItems) > 0 && appendSessionItems != nil {
			appendSessionItems(currentSessionItems)
		}
		return append(items, currentInputItems...)
	}
}

func (r *RuntimeRouter) lastCurrentTimeReminderTime(threadID string) (time.Time, bool) {
	if r == nil || r.services.ThreadRouter == nil || strings.TrimSpace(threadID) == "" {
		return time.Time{}, false
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return time.Time{}, false
	}
	for i := len(record.Items) - 1; i >= 0; i-- {
		if deliveredAt, ok := currentTimeReminderSessionItemTime(&record.Items[i]); ok {
			return deliveredAt, true
		}
	}
	return time.Time{}, false
}

func currentTimeReminderSessionItemTime(item *session.Item) (time.Time, bool) {
	if item == nil || !sessionItemIsCurrentTimeReminder(item) {
		return time.Time{}, false
	}
	unixSeconds := int64FromAnyValue(firstMapValue(item.Data, "current_time_at", "currentTimeAt"))
	if unixSeconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(unixSeconds, 0).UTC(), true
}

func sessionItemIsCurrentTimeReminder(item *session.Item) bool {
	if item == nil {
		return false
	}
	if stringValueFromMap(item.Data, "kind") == "current_time_reminder" {
		return true
	}
	if stringValueFromMap(item.Metadata, "kind") == "current_time_reminder" {
		return true
	}
	return false
}

func stringValueFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	text, _ := values[key].(string)
	return strings.TrimSpace(text)
}

func currentTimeReminderSessionItem(turnID string, rendered *contextfrag.RenderedFragment, now time.Time, source string, createdAt time.Time) session.Item {
	text := ""
	role := contextfrag.RoleDeveloper
	if rendered != nil {
		text = strings.TrimSpace(rendered.Content)
		if strings.TrimSpace(rendered.Role) != "" {
			role = strings.TrimSpace(rendered.Role)
		}
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	return session.Item{
		ID:        "current-time-" + safeIdentifier(turnID) + "-" + strconv.FormatInt(now.Unix(), 10),
		Type:      "message",
		Role:      role,
		Text:      text,
		CreatedAt: createdAt,
		Data: map[string]any{
			"kind":            "current_time_reminder",
			"current_time_at": now.Unix(),
			"source":          source,
		},
		Metadata: appTurnMetadata(turnID, map[string]any{
			"kind": "current_time_reminder",
		}),
	}
}

func (r *RuntimeRouter) instructionsWithPluginContext(threadID string, cfg *config.Config, params *turn.TurnStartParams, instructions string) string {
	if r == nil || r.services.Plugins == nil {
		return strings.TrimSpace(instructions)
	}
	capabilities := r.services.Plugins.EnabledCapabilities()
	if len(capabilities) == 0 {
		return strings.TrimSpace(instructions)
	}
	sections := []string{}
	// Generic plugin usage guidance is gated by the selected model's capability
	// (Rust e4e0c7070e): codex-auto-review and other opted-out models still get
	// explicit-mention injections below, but not the blanket guidance.
	modelInfo := r.modelInfoForRuntimeWithConfig(params.Model, cfg)
	if modelInfo == nil || modelInfo.IncludePluginUsageInstructions {
		available := contextfrag.Render(contextfrag.NewAvailablePluginsInstructions(contextPluginSummaries(capabilities)))
		if available != nil && strings.TrimSpace(available.Content) != "" {
			sections = append(sections, available.Content)
		}
	}
	mentioned := plugin.CollectExplicitPluginMentions(pluginUserInputFromTurn(params), capabilities)
	if len(mentioned) > 0 {
		mcpTools, _ := r.mcpRuntimeInputsForServiceWithRequired(threadID, cfg, r.mcpServiceForThread(threadID, cfg), r.requiredMCPServersForTurn(threadID, cfg, params))
		appsByID := r.appsForExplicitMentions(threadID, cfg)
		for _, text := range plugin.BuildPluginInjections(
			mentioned,
			pluginToolInfosForRuntime(mcpTools, capabilities),
			pluginAppInfosForRuntime(appsByID),
		) {
			rendered := contextfrag.Render(contextfrag.NewPluginInstructions(text))
			if rendered != nil && strings.TrimSpace(rendered.Content) != "" {
				sections = append(sections, rendered.Content)
			}
		}
	}
	sections = append(sections, strings.TrimSpace(instructions))
	return strings.Join(nonEmpty(sections), "\n\n")
}

func pluginToolInfosForRuntime(tools []mcp.RuntimeToolInfo, capabilities []plugin.CapabilitySummary) []plugin.ToolInfo {
	if len(tools) == 0 {
		return nil
	}
	pluginNamesByServer := map[string][]string{}
	for i := range capabilities {
		displayName := firstNonEmpty(capabilities[i].DisplayName, capabilities[i].Name, capabilities[i].ConfigName, capabilities[i].RemotePluginID)
		if strings.TrimSpace(displayName) == "" {
			continue
		}
		for _, serverName := range capabilities[i].MCPServers {
			serverName = strings.TrimSpace(serverName)
			if serverName == "" {
				continue
			}
			pluginNamesByServer[serverName] = mergeRuntimePluginDisplayNames(pluginNamesByServer[serverName], []string{displayName})
		}
	}
	out := make([]plugin.ToolInfo, 0, len(tools))
	for i := range tools {
		serverName := strings.TrimSpace(tools[i].ServerName)
		if serverName == "" {
			continue
		}
		out = append(out, plugin.ToolInfo{
			ServerName:         serverName,
			PluginDisplayNames: mergeRuntimePluginDisplayNames(tools[i].PluginDisplayNames, pluginNamesByServer[serverName]),
		})
	}
	return out
}

func pluginAppInfosForRuntime(appsByID map[string]apps.AppEntry) []plugin.AppInfo {
	if len(appsByID) == 0 {
		return nil
	}
	ids := make([]string, 0, len(appsByID))
	for id := range appsByID {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]plugin.AppInfo, 0, len(ids))
	for _, id := range ids {
		app := appsByID[id]
		out = append(out, plugin.AppInfo{
			ID:                 id,
			DisplayName:        firstNonEmpty(strings.TrimSpace(app.Name), id),
			Enabled:            appEnabledForRuntime(&app),
			PluginDisplayNames: append([]string(nil), app.PluginDisplayNames...),
		})
	}
	return out
}

func (r *RuntimeRouter) instructionsWithSkillsContext(threadID string, cfg *config.Config, params *turn.TurnStartParams, instructions string) (string, []any, turn.ToolPostExecutionInputItems, error) {
	return r.instructionsWithSkillsContextForTurn(context.Background(), threadID, "", cfg, params, instructions)
}

func (r *RuntimeRouter) instructionsWithSkillsContextForTurn(ctx context.Context, threadID string, turnID string, cfg *config.Config, params *turn.TurnStartParams, instructions string) (string, []any, turn.ToolPostExecutionInputItems, error) {
	if r == nil || r.services.Skills == nil {
		return strings.TrimSpace(instructions), nil, nil, nil
	}
	listParams := &SkillsListParams{}
	if params != nil {
		sessionConfig := &config.Config{Values: map[string]any{}}
		applyRuntimeConfigOverrides(sessionConfig, turnConfigOverrides(params))
		listParams.Config = skillConfigEntriesFromValues(sessionConfig.Values)
	}
	if cwd := turnCWD(params); cwd != "" {
		listParams.CWDs = []string{cwd}
	}
	response, err := r.services.Skills.List(listParams)
	if err != nil {
		return "", nil, nil, err
	}
	hostWarnings := make([]string, 0, len(response.Errors))
	for _, skillErr := range response.Errors {
		hostWarnings = append(hostWarnings, fmt.Sprintf("Failed to load skill at %s: %s", skillErr.Path, skillErr.Message))
	}
	r.notifySkillWarnings(threadID, hostWarnings)
	skillEntries := cloneSkills(response.Skills)
	pluginSkillEntries := []SkillsListEntry(nil)
	if (r.services.WorkspaceCodexPluginsEnabled == nil || *r.services.WorkspaceCodexPluginsEnabled) && (cfg == nil || features.Enabled(cfg.FeatureSettings(), "plugins")) {
		pluginSkillEntries, err = r.pluginSkillEntriesForRuntime()
		if err != nil {
			return "", nil, nil, err
		}
	}
	pluginSkillEntries, err = r.services.Skills.applyConfigEntries(pluginSkillEntries, listParams.Config)
	if err != nil {
		return "", nil, nil, err
	}
	skillEntries = append(skillEntries, pluginSkillEntries...)
	hostSkillEntries := cloneSkills(skillEntries)
	executorSandboxContexts, err := r.executorSkillSandboxContextsForTurn(cfg, turnCWD(params), params)
	if err != nil {
		return "", nil, nil, err
	}
	executorSkillProviders := r.executorSkillProviderForThreadWithSandbox(threadID, executorSandboxContexts)
	selectedCapabilitySkillEntries, selectedCapabilitySkillWarnings, err := r.selectedCapabilitySkillEntriesForRuntimeWithSandbox(ctx, threadID, executorSandboxContexts)
	if err != nil {
		return "", nil, nil, err
	}
	selectedCapabilitySkillEntries, err = r.services.Skills.applyConfigEntries(selectedCapabilitySkillEntries, listParams.Config)
	if err != nil {
		return "", nil, nil, err
	}
	skillEntries = append(skillEntries, selectedCapabilitySkillEntries...)
	r.notifySkillWarnings(threadID, selectedCapabilitySkillWarnings)
	hostSkillMetadata := promptHostSkillMetadataFromEntries(hostSkillEntries)
	selectedCapabilitySkillMetadata := promptSkillMetadataFromEntries(selectedCapabilitySkillEntries)
	skillMetadata := append(append([]promptctx.InstructionsSkillMetadata(nil), hostSkillMetadata...), selectedCapabilitySkillMetadata...)
	orchestratorMetadata := []promptctx.InstructionsSkillMetadata(nil)
	if cfg == nil || cfg.OrchestratorSkillsEnabled() {
		var orchestratorWarnings []string
		orchestratorMetadata, orchestratorWarnings = r.orchestratorSkillMetadataForRuntime(threadID)
		skillMetadata = append(skillMetadata, orchestratorMetadata...)
		r.notifySkillWarnings(threadID, orchestratorWarnings)
	}
	customMetadata, customWarnings := r.customSkillMetadataForRuntime(ctx, turnID)
	skillMetadata = append(skillMetadata, customMetadata...)
	r.notifySkillWarnings(threadID, customWarnings)
	r.runSkillShadowSelection(threadID, turnID, cfg, params, hostSkillMetadata, orchestratorMetadata)
	hostSkillMetadata = selectSkillMetadata(cfg, params, hostSkillMetadata)
	selectedCapabilitySkillMetadata = selectSkillMetadata(cfg, params, selectedCapabilitySkillMetadata)
	orchestratorMetadata = selectSkillMetadata(cfg, params, orchestratorMetadata)
	customMetadata = selectSkillMetadata(cfg, params, customMetadata)
	skillMetadata = append(append(append(append([]promptctx.InstructionsSkillMetadata(nil), hostSkillMetadata...), selectedCapabilitySkillMetadata...), orchestratorMetadata...), customMetadata...)
	r.maybePromptAndInstallSkillMCPDependencies(ctx, threadID, turnID, cfg, params, skillEntries, skillMetadata)
	modelID := firstNonEmpty(turnParamModel(params), stringConfigValue(cfg, "model"), defaultModelForAppTurn())
	postToolInputItems := r.implicitSkillInvocationEventProvider(threadID, turnID, modelID, params, skillMetadata)
	if cfg != nil && !cfg.IncludeSkillInstructions() {
		skillInputItems := r.explicitSkillInputItemsForTurnWithProvider(threadID, turnID, modelID, params, skillMetadata, executorSkillProviders)
		return strings.TrimSpace(instructions), skillInputItems, postToolInputItems, nil
	}
	includeUsageInstructions := r.includeSkillsUsageInstructionsForTurn(cfg, params)
	executorMetadata := append(append(append([]promptctx.InstructionsSkillMetadata(nil), orchestratorMetadata...), selectedCapabilitySkillMetadata...), customMetadata...)
	hostAvailable, executorAvailable := promptctx.RenderCombinedAvailableSkills(
		hostSkillMetadata,
		executorMetadata,
		promptctx.AvailableSkillsRenderOptions{
			Budget:                   promptctx.DefaultSkillMetadataBudget(r.skillContextWindowForTurn(cfg, params)),
			IncludeUsageInstructions: includeUsageInstructions,
		},
	)
	for _, available := range []*promptctx.AvailableSkills{hostAvailable, executorAvailable} {
		if available != nil && available.WarningMessage != nil && strings.TrimSpace(*available.WarningMessage) != "" {
			r.notify(NotificationWarning, &WarningNotification{
				ThreadID: stringPtrIfNotEmpty(threadID),
				Message:  strings.TrimSpace(*available.WarningMessage),
			})
		}
	}
	skillInputItems := r.explicitSkillInputItemsForTurnWithProvider(threadID, turnID, modelID, params, skillMetadata, executorSkillProviders)
	rendered := make([]string, 0, 3)
	if hostAvailable != nil {
		rendered = append(rendered, hostAvailable.Body)
	}
	if executorAvailable != nil {
		rendered = append(rendered, executorAvailable.Body)
	}
	rendered = append(rendered, instructions)
	return strings.Join(nonEmpty(rendered), "\n\n"), skillInputItems, postToolInputItems, nil
}

func (r *RuntimeRouter) notifySkillWarnings(threadID string, warnings []string) {
	if r == nil {
		return
	}
	warnings = turnBoundedSkillWarnings(warnings)
	if len(warnings) == 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	for _, message := range warnings {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		r.skillWarningsMu.Lock()
		seen := r.skillWarnings[threadID]
		if seen == nil {
			seen = map[string]struct{}{}
			r.skillWarnings[threadID] = seen
		}
		_, duplicate := seen[message]
		if !duplicate {
			seen[message] = struct{}{}
		}
		r.skillWarningsMu.Unlock()
		if duplicate {
			continue
		}
		r.notify(NotificationWarning, &WarningNotification{ThreadID: stringPtrIfNotEmpty(threadID), Message: message})
	}
}

func turnBoundedSkillWarnings(warnings []string) []string {
	const maxWarnings = 4
	const maxBytes = 256
	out := make([]string, 0, maxWarnings)
	for _, warning := range warnings {
		if len(out) >= maxWarnings {
			break
		}
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		if len(warning) > maxBytes {
			end := maxBytes
			for end > 0 && !utf8.RuneStart(warning[end]) {
				end--
			}
			warning = warning[:end]
		}
		out = append(out, warning)
	}
	return out
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (r *RuntimeRouter) pluginSkillEntriesForRuntime() ([]SkillsListEntry, error) {
	entries, _, err := r.pluginSkillEntriesAndErrorsForRuntime()
	return entries, err
}

func (r *RuntimeRouter) pluginSkillEntriesAndErrorsForRuntime() ([]SkillsListEntry, []SkillErrorInfo, error) {
	if r == nil || r.services.Plugins == nil {
		return nil, nil, nil
	}
	roots := r.services.Plugins.EnabledSkillRoots()
	entries := make([]SkillsListEntry, 0)
	errors := make([]SkillErrorInfo, 0)
	for _, root := range roots {
		discovered, discoveredErrors, err := discover(SkillsRoot{Path: root.Root, Scope: "plugin", PluginID: root.PluginID, RemotePluginID: root.RemotePluginID, PluginRoot: filepath.Dir(root.Root)})
		if err != nil {
			return nil, nil, err
		}
		errors = append(errors, discoveredErrors...)
		for _, entry := range discovered {
			prefixed := cloneSkill(entry)
			prefixPluginSkillNames(&prefixed, root.PluginNamespace)
			entries = append(entries, prefixed)
		}
	}
	return entries, errors, nil
}

func (r *RuntimeRouter) selectedCapabilitySkillEntriesForRuntime(threadID string) ([]SkillsListEntry, []string, error) {
	return r.selectedCapabilitySkillEntriesForRuntimeWithSandbox(context.Background(), threadID, nil)
}

func (r *RuntimeRouter) selectedCapabilitySkillEntriesForRuntimeWithSandbox(ctx context.Context, threadID string, sandboxContexts map[string]*execserver.FileSystemSandboxContext) ([]SkillsListEntry, []string, error) {
	if r == nil || strings.TrimSpace(threadID) == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil, nil, err
	}
	entries := make([]SkillsListEntry, 0)
	warnings := make([]string, 0)
	for _, raw := range record.Metadata.SelectedCapabilityRoots {
		var selected SelectedCapabilityRoot
		if err := json.Unmarshal(raw, &selected); err != nil {
			continue
		}
		if selected.Location.Type != CapabilityRootLocationEnvironment {
			continue
		}
		environmentID := firstNonEmpty(strings.TrimSpace(selected.Location.EnvironmentID), "local")
		sandboxContext, sandboxErr := executorSkillSandboxContextForEnvironment(sandboxContexts, environmentID)
		if sandboxErr != nil {
			warnings = append(warnings, fmt.Sprintf("executor skills unavailable for environment `%s`: filesystem sandbox context is missing", environmentID))
			continue
		}
		if sandboxErr := validateExecutorSkillSandboxAvailability(sandboxContext, selected.Location.Path); sandboxErr != nil {
			warnings = append(warnings, "executor skills unavailable: "+sandboxErr.Error())
			continue
		}
		cacheKey := string(raw) + "\x00" + executorSkillSandboxContextKey(sandboxContext)
		if environmentRecord, ok := r.selectedCapabilityEnvironmentRecord(&selected.Location); ok {
			cached := r.selectedCapabilitySkillCatalog(threadID, cacheKey)
			cached.once.Do(func() {
				cached.entries, cached.warnings, cached.err = discoverRemoteEnvironmentSkillsWithSandbox(ctx, environmentRecord, selected.Location.Path, sandboxContext)
				if cached.err != nil {
					cached.warnings = append(cached.warnings, "executor skills unavailable: "+cached.err.Error())
					cached.err = nil
				}
				applyExecutorSkillDisplayPaths(cached.entries, selected.ID)
			})
			entries = append(entries, cloneSkills(cached.entries)...)
			warnings = append(warnings, cached.warnings...)
			continue
		}
		if r.selectedCapabilityEnvironmentRequired(&selected.Location) {
			continue
		}
		path := capabilityRootLocalPath(selected.Location.Path)
		if strings.TrimSpace(path) == "" {
			continue
		}
		cached := r.selectedCapabilitySkillCatalog(threadID, cacheKey)
		cached.once.Do(func() {
			var discoveredErrors []SkillErrorInfo
			if sandboxContext != nil {
				cached.entries, cached.warnings, cached.err = discoverLocalEnvironmentSkillsWithSandbox(ctx, selected.Location.Path, sandboxContext)
			} else {
				cached.entries, discoveredErrors, cached.err = discover(SkillsRoot{Path: path, Scope: "environment"})
			}
			if cached.err != nil {
				cached.warnings = append(cached.warnings, "executor skills unavailable: "+cached.err.Error())
				cached.err = nil
			}
			applyExecutorSkillDisplayPaths(cached.entries, selected.ID)
			for _, skillErr := range discoveredErrors {
				cached.warnings = append(cached.warnings, fmt.Sprintf("Failed to load environment skill at %s: %s", skillErr.Path, skillErr.Message))
			}
		})
		entries = append(entries, cloneSkills(cached.entries)...)
		warnings = append(warnings, cached.warnings...)
	}
	return entries, warnings, nil
}

func (r *RuntimeRouter) selectedCapabilitySkillCatalog(threadID string, rootKey string) *runtimeSelectedSkillCatalog {
	r.selectedSkillMu.Lock()
	defer r.selectedSkillMu.Unlock()
	byRoot := r.selectedSkills[threadID]
	if byRoot == nil {
		byRoot = map[string]*runtimeSelectedSkillCatalog{}
		r.selectedSkills[threadID] = byRoot
	}
	if cached := byRoot[rootKey]; cached != nil {
		return cached
	}
	cached := &runtimeSelectedSkillCatalog{}
	byRoot[rootKey] = cached
	return cached
}

func (r *RuntimeRouter) selectedCapabilityEnvironmentRecord(location *CapabilityRootLocation) (*EnvironmentRecord, bool) {
	if r == nil || location == nil || r.services.Environment == nil {
		return nil, false
	}
	return r.services.Environment.Record(location.EnvironmentID)
}

func (r *RuntimeRouter) selectedCapabilityEnvironmentRequired(location *CapabilityRootLocation) bool {
	if r == nil || location == nil || r.services.Environment == nil {
		return false
	}
	return strings.TrimSpace(location.EnvironmentID) != "" && strings.TrimSpace(location.EnvironmentID) != "local"
}

func capabilityRootLocalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parsed, err := url.Parse(path)
	if err == nil && parsed.Scheme == "file" {
		value := parsed.Path
		if parsed.Host != "" {
			value = "//" + parsed.Host + value
		}
		if unescaped, unescapeErr := url.PathUnescape(value); unescapeErr == nil {
			value = unescaped
		}
		if len(value) >= 3 && value[0] == '/' && value[2] == ':' {
			value = value[1:]
		}
		return filepath.FromSlash(value)
	}
	return path
}

func applyExecutorSkillDisplayPaths(entries []SkillsListEntry, selectedRootID string) {
	for i := range entries {
		if strings.TrimSpace(entries[i].DisplayPath) == "" {
			entries[i].DisplayPath = executorSkillDisplayPath(selectedRootID, entries[i].Path)
		}
		if entries[i].SourcePath == "" {
			entries[i].SourcePath = entries[i].Path
		}
		entries[i].AuthorityKind = string(skillprovider.SourceExecutor)
		entries[i].AuthorityID = strings.TrimSpace(selectedRootID)
		entries[i].ResourceID = strings.TrimSpace(entries[i].DisplayPath)
		entries[i].PackageID = executorSkillPackageID(entries[i].ResourceID)
	}
}

func executorSkillPackageID(resource string) string {
	resource = strings.TrimSpace(resource)
	if !strings.HasPrefix(resource, "skill://") {
		return ""
	}
	separator := strings.LastIndex(resource, "/")
	if separator <= len("skill://") {
		return ""
	}
	return strings.TrimRight(resource[:separator], "/")
}

func executorSkillDisplayPath(selectedRootID string, sourcePath string) string {
	selectedRootID = strings.TrimSpace(selectedRootID)
	sourcePath = strings.TrimSpace(sourcePath)
	if selectedRootID == "" || sourcePath == "" {
		return ""
	}
	path := sourcePath
	if strings.Contains(sourcePath, "://") {
		parsed, err := url.Parse(sourcePath)
		if err != nil || parsed.Scheme == "" {
			return ""
		}
		path = parsed.Path
		if path == "" {
			path = "/" + strings.TrimPrefix(parsed.Opaque, "/")
		}
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = pathpkg.Clean(path)
	if path == "." || path == "/" {
		return ""
	}
	return "skill://" + selectedRootID + "/" + strings.TrimLeft(path, "/")
}

func prefixPluginSkillNames(entry *SkillsListEntry, pluginDisplayName string) {
	if entry == nil {
		return
	}
	prefix := strings.TrimSpace(pluginDisplayName)
	if prefix == "" {
		return
	}
	if strings.TrimSpace(entry.Name) != "" && !strings.HasPrefix(entry.Name, prefix+":") {
		entry.Name = prefix + ":" + entry.Name
	}
	entry.Scope = "plugin"
	for i := range entry.Skills {
		prefixPluginSkillNames(&entry.Skills[i], prefix)
	}
}

func promptSkillMetadataFromEntries(entries []SkillsListEntry) []promptctx.InstructionsSkillMetadata {
	return promptSkillMetadataFromEntriesWithPolicy(entries, true)
}

func promptHostSkillMetadataFromEntries(entries []SkillsListEntry) []promptctx.InstructionsSkillMetadata {
	return promptSkillMetadataFromEntriesWithPolicy(entries, false)
}

func promptSkillMetadataFromEntriesWithPolicy(entries []SkillsListEntry, preferShortDescription bool) []promptctx.InstructionsSkillMetadata {
	out := make([]promptctx.InstructionsSkillMetadata, 0, len(entries))
	var walk func([]SkillsListEntry)
	walk = func(values []SkillsListEntry) {
		for _, entry := range values {
			if len(entry.Skills) > 0 {
				walk(entry.Skills)
				continue
			}
			if !entry.Enabled || strings.TrimSpace(entry.Name) == "" {
				continue
			}
			description := entry.Description
			if preferShortDescription {
				description = firstNonEmpty(entry.ShortDescription, entry.Description)
			}
			var allowImplicit *bool
			if entry.Policy != nil && entry.Policy.AllowImplicitInvocation != nil {
				value := *entry.Policy.AllowImplicitInvocation
				allowImplicit = &value
			}
			displayPath := entry.DisplayPath
			// Rust 72d937ed4d (#37144): a symlinked skill's discovery path is
			// advertised in the catalog so structured selections and linked
			// mentions that use the configured-root path still resolve.
			if strings.TrimSpace(displayPath) == "" {
				displayPath = entry.DiscoveryPath
			}
			if strings.TrimSpace(displayPath) == "" && !strings.EqualFold(strings.TrimSpace(entry.Scope), "environment") {
				displayPath = strings.ReplaceAll(entry.Path, "\\", "/")
			}
			out = append(out, promptctx.InstructionsSkillMetadata{
				Name:                    entry.Name,
				Scope:                   entry.Scope,
				Path:                    entry.Path,
				LocatorPath:             displayPath,
				LocatorKind:             promptSkillLocatorKind(entry),
				Root:                    entry.Root,
				RootOrder:               entry.RootOrder,
				HasRootOrder:            entry.HasRootOrder,
				Description:             description,
				ShortDescription:        entry.ShortDescription,
				RoutingMetadata:         promptSkillRoutingMetadata(entry.Interface),
				PluginID:                entry.PluginID,
				RemotePluginID:          entry.RemotePluginID,
				Contents:                entry.Contents,
				AllowImplicitInvocation: allowImplicit,
				AuthorityKind:           entry.AuthorityKind,
				AuthorityID:             entry.AuthorityID,
				PackageID:               entry.PackageID,
				ResourceID:              entry.ResourceID,
				Dependencies:            promptDependenciesFromSkillEntry(entry.Dependencies),
			})
		}
	}
	walk(entries)
	return out
}

func promptSkillLocatorKind(entry SkillsListEntry) string {
	switch strings.ToLower(strings.TrimSpace(entry.Scope)) {
	case "environment":
		return "executor package"
	default:
		return "file"
	}
}

func (r *RuntimeRouter) orchestratorSkillMetadataForRuntime(threadID string) ([]promptctx.InstructionsSkillMetadata, []string) {
	if r == nil {
		return nil, nil
	}
	service := r.mcpServiceForThread(threadID, nil)
	if service == nil {
		return nil, nil
	}
	cacheKey, cache := r.orchestratorSkillCacheForRuntime(threadID, service)
	cache.once.Do(func() {
		cache.catalog, cache.err = turn.LoadOrchestratorSkillCatalog(service, strings.TrimSpace(threadID))
	})
	metadata := make([]promptctx.InstructionsSkillMetadata, 0, len(cache.catalog.Skills))
	for _, skill := range cache.catalog.Skills {
		metadata = append(metadata, promptctx.InstructionsSkillMetadata{
			Name:          skill.Name,
			Scope:         "orchestrator",
			Path:          skill.MainResource,
			LocatorPath:   skill.Package,
			LocatorKind:   "orchestrator package",
			Description:   skill.Description,
			AuthorityKind: "orchestrator",
			AuthorityID:   mcp.RuntimeCodexAppsMCPServerName,
			PackageID:     skill.Package,
			ResourceID:    skill.MainResource,
		})
	}
	warnings := append([]string(nil), cache.catalog.Warnings...)
	if cache.err != nil {
		warnings = append(warnings, "orchestrator skills unavailable: "+cache.err.Error())
	}
	if len(warnings) == 0 {
		return metadata, nil
	}
	r.orchestratorSkillMu.Lock()
	if r.orchestratorWarned[cacheKey] {
		warnings = nil
	} else {
		r.orchestratorWarned[cacheKey] = true
	}
	r.orchestratorSkillMu.Unlock()
	return metadata, warnings
}

func (r *RuntimeRouter) orchestratorSkillCacheForRuntime(threadID string, service *mcp.MCPService) (string, *runtimeOrchestratorSkillCatalog) {
	bindingRevision := uint64(0)
	if r != nil && r.mcpRuntimes != nil {
		bindingRevision = r.mcpRuntimes.bindingRevision(threadID, service)
	}
	generation := uint64(0)
	if service != nil {
		generation = service.Generation()
	}
	cacheKey := fmt.Sprintf("%s\x00%d\x00%d", strings.TrimSpace(threadID), bindingRevision, generation)
	r.orchestratorSkillMu.Lock()
	defer r.orchestratorSkillMu.Unlock()
	if r.orchestratorSkills == nil {
		r.orchestratorSkills = map[string]*runtimeOrchestratorSkillCatalog{}
	}
	if r.orchestratorWarned == nil {
		r.orchestratorWarned = map[string]bool{}
	}
	cache := r.orchestratorSkills[cacheKey]
	if cache == nil {
		cache = &runtimeOrchestratorSkillCatalog{resources: map[string]string{}}
		r.orchestratorSkills[cacheKey] = cache
	}
	return cacheKey, cache
}

func (r *RuntimeRouter) readOrchestratorSkillResourceForRuntime(threadID string, skill promptctx.InstructionsSkillMetadata) (string, error) {
	if r == nil {
		return "", errors.New("session MCP resource client is not configured")
	}
	service := r.mcpServiceForThread(threadID, nil)
	if service == nil {
		return "", errors.New("session MCP resource client is not configured")
	}
	_, cache := r.orchestratorSkillCacheForRuntime(threadID, service)
	key := skill.PackageID + "\x00" + skill.ResourceID
	cache.resourceMu.Lock()
	if contents, ok := cache.resources[key]; ok {
		cache.resourceMu.Unlock()
		return contents, nil
	}
	cache.resourceMu.Unlock()
	contents, err := turn.ReadOrchestratorSkillResource(service, threadID, skill.PackageID, skill.ResourceID)
	if err != nil {
		return "", err
	}
	cache.resourceMu.Lock()
	defer cache.resourceMu.Unlock()
	if cached, ok := cache.resources[key]; ok {
		return cached, nil
	}
	if len(cache.resources) < 100 && cache.resourceBytes+len(contents) <= 8*1024*1024 {
		cache.resources[key] = contents
		cache.resourceBytes += len(contents)
	}
	return contents, nil
}

func (r *RuntimeRouter) customSkillMetadataForRuntime(ctx context.Context, turnID string) ([]promptctx.InstructionsSkillMetadata, []string) {
	if r == nil || r.services.CustomSkills == nil {
		return nil, nil
	}
	catalog := r.services.CustomSkills.ListCustom(ctx, skillprovider.ListQuery{TurnID: turnID})
	metadata := make([]promptctx.InstructionsSkillMetadata, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		if !entry.Enabled || strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.MainResource) == "" {
			continue
		}
		var allowImplicit *bool
		if !entry.PromptVisible {
			value := false
			allowImplicit = &value
		}
		metadata = append(metadata, promptctx.InstructionsSkillMetadata{
			Name:                    entry.Name,
			Scope:                   "custom",
			Path:                    entry.MainResource,
			LocatorPath:             firstNonEmpty(entry.DisplayPath, entry.MainResource),
			LocatorKind:             "custom resource",
			Description:             firstNonEmpty(entry.ShortDescription, entry.Description),
			AllowImplicitInvocation: allowImplicit,
			AuthorityKind:           string(entry.Authority.Kind),
			AuthorityID:             entry.Authority.ID,
			PackageID:               entry.PackageID,
			ResourceID:              entry.MainResource,
			Dependencies:            promptDependenciesFromCustomSkill(entry.Dependencies),
		})
	}
	return metadata, append([]string(nil), catalog.Warnings...)
}

func promptDependenciesFromCustomSkill(dependencies []skillprovider.ToolDependency) []promptctx.InstructionsSkillDependency {
	out := make([]promptctx.InstructionsSkillDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		out = append(out, promptctx.InstructionsSkillDependency{
			Type:      dependency.Type,
			Value:     dependency.Value,
			Transport: dependency.Transport,
			Command:   dependency.Command,
			URL:       dependency.URL,
		})
	}
	return out
}

func promptSkillRoutingMetadata(value *SkillInterface) string {
	if value == nil {
		return ""
	}
	defaultPrompt := ""
	if value.DefaultPrompt != nil {
		defaultPrompt = *value.DefaultPrompt
	}
	return strings.Join(nonEmptyStrings(value.DisplayName, value.ShortDescription, defaultPrompt), " ")
}

func promptDependenciesFromSkillEntry(dependencies *SkillDependencies) []promptctx.InstructionsSkillDependency {
	if dependencies == nil {
		return nil
	}
	limit := min(len(dependencies.Tools), 32)
	out := make([]promptctx.InstructionsSkillDependency, 0, limit)
	for _, dependency := range dependencies.Tools[:limit] {
		command, url := "", ""
		if dependency.Command != nil {
			command = *dependency.Command
		}
		if dependency.URL != nil {
			url = *dependency.URL
		}
		out = append(out, promptctx.InstructionsSkillDependency{
			Type: dependency.Type, Value: dependency.Value, Description: dependency.Description,
			Transport: dependency.Transport, Command: command, URL: url,
		})
	}
	return out
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

const skillMCPDependencyPromptID = "skill_mcp_dependency_install"

func (r *RuntimeRouter) maybePromptAndInstallSkillMCPDependencies(ctx context.Context, threadID string, turnID string, cfg *config.Config, params *turn.TurnStartParams, entries []SkillsListEntry, skills []promptctx.InstructionsSkillMetadata) {
	if r == nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "skill_mcp_dependency_install") || !skillMCPFirstPartyOriginator(turnOriginator(params)) {
		return
	}
	selected := promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: skillMentionInputsFromTurn(params),
		Skills: skills,
	})
	selectedRuntimeSkills := runtimeSkillMetadataFromSelectedEntries(entries, selected)
	if len(selectedRuntimeSkills) == 0 {
		return
	}
	missing := mcp.CollectMissingRuntimeDependencies(selectedRuntimeSkills, r.runtimeMCPServerConfigsForSkills(cfg))
	missing = r.unpromptedSkillMCPDependencies(threadID, missing)
	if len(missing) == 0 {
		return
	}
	install := skillMCPDependencyPromptAutoApproved(cfg, params)
	if !install && turnApprovalPolicyForTurn(cfg, params) == sandbox.ApprovalNever {
		// Rust 95aada11c4 (#38205): when the approval policy is `never`,
		// skip the prompt for missing skill MCP dependencies entirely (do not
		// prompt, do not install). Full-access auto-approval above still
		// installs without a prompt.
		return
	}
	if !install {
		question := ToolRequestUserInputQuestion{
			ID:       skillMCPDependencyPromptID,
			Header:   "Install MCP servers?",
			Question: "The following MCP servers are required by the selected skills but are not installed yet: " + mcp.FormatMissingRuntimeDependencies(missing) + ". Install them now?",
			Options: []ToolRequestUserInputOption{
				{Label: "Install", Description: "Install and enable the missing MCP servers in your global config."},
				{Label: "Continue anyway", Description: "Skip installation for now and do not show again for these MCP servers in this session."},
			},
		}
		var response ToolRequestUserInputResponse
		err := r.requireServerRequests().Request(ctx, ServerRequestToolUserInput, &ToolRequestUserInputParams{
			ThreadID:  threadID,
			TurnID:    turnID,
			ItemID:    "mcp-deps-" + turnID,
			Questions: []ToolRequestUserInputQuestion{question},
		}, &response)
		if err == nil {
			for _, answer := range response.Answers[skillMCPDependencyPromptID].Answers {
				if answer == "Install" {
					install = true
					break
				}
			}
		}
	}
	r.recordSkillMCPDependenciesPrompted(threadID, missing)
	if install {
		r.installSkillMCPDependencies(ctx, cfg, missing)
	}
}

func runtimeSkillMetadataFromSelectedEntries(entries []SkillsListEntry, selected []promptctx.InstructionsSkillMetadata) []mcp.RuntimeSkillMetadata {
	selectedPaths := make(map[string]bool, len(selected))
	for _, skill := range selected {
		selectedPaths[skill.Path] = true
	}
	out := make([]mcp.RuntimeSkillMetadata, 0, len(entries))
	var walk func([]SkillsListEntry)
	walk = func(values []SkillsListEntry) {
		for _, entry := range values {
			if len(entry.Skills) > 0 {
				walk(entry.Skills)
				continue
			}
			if !entry.Enabled || !selectedPaths[entry.Path] || strings.TrimSpace(entry.Name) == "" || entry.Dependencies == nil {
				continue
			}
			dependencies := runtimeDependenciesFromSkillDependencies(entry.Dependencies)
			if len(dependencies) == 0 {
				continue
			}
			out = append(out, mcp.RuntimeSkillMetadata{
				Name:         entry.Name,
				Dependencies: dependencies,
			})
		}
	}
	walk(entries)
	for _, skill := range selected {
		if len(skill.Dependencies) == 0 {
			continue
		}
		dependencies := make([]mcp.RuntimeDependency, 0, len(skill.Dependencies))
		for _, dependency := range skill.Dependencies {
			dependencies = append(dependencies, mcp.RuntimeDependency{
				Type:      dependency.Type,
				Value:     dependency.Value,
				Transport: dependency.Transport,
				Command:   dependency.Command,
				URL:       dependency.URL,
			})
		}
		out = append(out, mcp.RuntimeSkillMetadata{Name: skill.Name, Dependencies: dependencies})
	}
	return out
}

func turnOriginator(params *turn.TurnStartParams) string {
	if params == nil {
		return ""
	}
	return params.Originator
}

func skillMCPFirstPartyOriginator(originator string) bool {
	return originator == "codex_cli_rs" || originator == "codex-tui" || originator == "codex_vscode" || strings.HasPrefix(originator, "Codex ")
}

func skillMCPDependencyPromptAutoApproved(cfg *config.Config, params *turn.TurnStartParams) bool {
	if turnApprovalPolicyForTurn(cfg, params) != sandbox.ApprovalNever {
		return false
	}
	cwd := turnCWD(params)
	profile, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil || profile == nil || profile.Profile == nil {
		return false
	}
	return profile.Profile.Disabled || profile.Profile.SandboxPolicy != nil && profile.Profile.SandboxPolicy.HasFullDiskWriteAccess()
}

func (r *RuntimeRouter) unpromptedSkillMCPDependencies(threadID string, missing map[string]mcp.RuntimeServerConfig) map[string]mcp.RuntimeServerConfig {
	if len(missing) == 0 {
		return nil
	}
	r.skillMCPPromptMu.Lock()
	defer r.skillMCPPromptMu.Unlock()
	out := make(map[string]mcp.RuntimeServerConfig, len(missing))
	for name, server := range missing {
		key := threadID + "\x00" + mcp.CanonicalRuntimeServerKey(name, server)
		if _, prompted := r.skillMCPPrompted[key]; !prompted {
			out[name] = server
		}
	}
	return out
}

func (r *RuntimeRouter) recordSkillMCPDependenciesPrompted(threadID string, missing map[string]mcp.RuntimeServerConfig) {
	r.skillMCPPromptMu.Lock()
	defer r.skillMCPPromptMu.Unlock()
	if r.skillMCPPrompted == nil {
		r.skillMCPPrompted = map[string]struct{}{}
	}
	for name, server := range missing {
		key := threadID + "\x00" + mcp.CanonicalRuntimeServerKey(name, server)
		r.skillMCPPrompted[key] = struct{}{}
	}
}

func (r *RuntimeRouter) installSkillMCPDependencies(ctx context.Context, cfg *config.Config, missing map[string]mcp.RuntimeServerConfig) {
	if r == nil || r.services.Config == nil || len(missing) == 0 {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil {
		return
	}
	servers := map[string]any{}
	if existing, ok := read.Config["mcp_servers"].(map[string]any); ok {
		servers = cloneAnyMapAppserver(existing)
	}
	added := make(map[string]mcp.RuntimeServerConfig, len(missing))
	for name, server := range missing {
		if _, exists := servers[name]; exists {
			continue
		}
		value := map[string]any{"enabled": true}
		if server.Transport == "stdio" {
			value["command"] = server.Command
		} else {
			value["url"] = server.URL
		}
		servers[name] = value
		added[name] = server
	}
	if len(added) == 0 {
		return
	}
	if _, err := r.services.Config.WriteValue(&config.ConfigValueWriteParams{KeyPath: "mcp_servers", Value: servers, MergeStrategy: config.MergeReplace}); err != nil {
		return
	}
	if cfg.Values == nil {
		cfg.Values = map[string]any{}
	}
	cfg.Values["mcp_servers"] = servers
	if r.services.MCP != nil {
		codexHome := r.services.Config.CodexHome()
		for name, server := range added {
			r.services.MCP.SetServerConfig(name, &mcp.ServerConfig{Command: server.Command, URL: server.URL, Enabled: true, Required: false, CodexHome: codexHome})
		}
		openBrowser := r.services.BrowserOpen
		if openBrowser == nil {
			openBrowser = auth.OpenBrowser
		}
		for name := range added {
			supported, err := r.services.MCP.LoginOAuthDependency(ctx, &mcp.OAuthDependencyLoginOptions{
				Name: name,
				OpenBrowser: func(target string) error {
					err := openBrowser(target)
					if err != nil {
						slog.Warn("failed to open browser for MCP skill dependency OAuth login", "server", name, "url", target, "error", err)
					}
					return err
				},
			})
			if err != nil {
				slog.Warn("failed to login to MCP dependency", "server", name, "error", err)
			} else if supported {
				slog.Debug("completed OAuth login for MCP skill dependency", "server", name)
			}
		}
		r.services.MCP.Refresh()
	}
}

func runtimeDependenciesFromSkillDependencies(dependencies *SkillDependencies) []mcp.RuntimeDependency {
	if dependencies == nil {
		return nil
	}
	out := make([]mcp.RuntimeDependency, 0, len(dependencies.Tools))
	for _, tool := range dependencies.Tools {
		out = append(out, mcp.RuntimeDependency{
			Type:      tool.Type,
			Value:     tool.Value,
			Transport: tool.Transport,
			Command:   stringPtrValue(tool.Command),
			URL:       stringPtrValue(tool.URL),
		})
	}
	return out
}

func (r *RuntimeRouter) runtimeMCPServerConfigsForSkills(cfg *config.Config) map[string]mcp.RuntimeServerConfig {
	values := map[string]any{}
	if cfg != nil && cfg.Values != nil {
		values = cfg.Values
	}
	codexHome := ""
	if r != nil && r.services.Config != nil {
		codexHome = r.services.Config.CodexHome()
	}
	var runtimeAuth *mcp.RuntimeAuth
	if r != nil {
		runtimeAuth = mcp.RuntimeAuthFromSnapshot(r.requireAccount().AuthSnapshot())
	}
	runtimeConfig := mcp.RuntimeConfigFromValuesWithAuthAndRequirements(values, codexHome, runtimeAuth, cfg.Requirements)
	out := make(map[string]mcp.RuntimeServerConfig, len(runtimeConfig.Servers))
	for name, registration := range runtimeConfig.Servers {
		config := registration.Config
		if !config.Enabled {
			continue
		}
		transport := "stdio"
		if strings.TrimSpace(config.URL) != "" {
			transport = "streamable_http"
		}
		out[name] = mcp.RuntimeServerConfig{
			Name:      name,
			Transport: transport,
			URL:       strings.TrimSpace(config.URL),
			Command:   strings.TrimSpace(config.Command),
			Args:      append([]string(nil), config.Args...),
			Enabled:   config.Enabled,
			Required:  config.Required,
		}
	}
	return out
}

type implicitShellSkillArgs struct {
	Cmd     string `json:"cmd"`
	Command string `json:"command"`
	CWD     string `json:"cwd,omitempty"`
	Workdir string `json:"workdir,omitempty"`
}

func (r *RuntimeRouter) implicitSkillInvocationEventProvider(threadID string, turnID string, modelID string, params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata) turn.ToolPostExecutionInputItems {
	if len(skills) == 0 {
		return nil
	}
	baseCWD := ""
	if params != nil {
		baseCWD = strings.TrimSpace(params.CWD)
	}
	if baseCWD == "" && r != nil {
		baseCWD = strings.TrimSpace(r.services.DefaultCWD)
	}
	productClientID := ""
	if params != nil {
		productClientID = params.Originator
	}
	seen := map[string]bool{}
	for _, skill := range promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: skillMentionInputsFromTurn(params),
		Skills: skills,
	}) {
		if key := implicitSkillInvocationSeenKey(skill); key != "" {
			seen[key] = true
		}
	}
	var mu sync.Mutex
	return func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any {
		skill := implicitSkillForToolInvocation(skills, invocation, baseCWD)
		if skill == nil {
			return nil
		}
		if implicitSkillResourceReadInvocation(invocation) && (output == nil || !output.Success) {
			// Resource-backed implicit invocations are recorded only when the
			// main resource is successfully read (Rust #38066).
			return nil
		}
		key := implicitSkillInvocationSeenKey(*skill)
		if key == "" {
			return nil
		}
		mu.Lock()
		if seen[key] {
			mu.Unlock()
			return nil
		}
		seen[key] = true
		mu.Unlock()
		r.trackSkillInvocationEvent(ctx, threadID, turnID, modelID, productClientID, *skill, telemetry.SkillInvocationTypeImplicit)
		return nil
	}
}

func implicitSkillForToolInvocation(skills []promptctx.InstructionsSkillMetadata, invocation *tool.Invocation, baseCWD string) *promptctx.InstructionsSkillMetadata {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil
	}
	name := invocation.ToolName.Key()
	if name == "skills.read" {
		// Resource-backed skill reads (Rust #38066): match the requested
		// package to an executor/orchestrator skill catalog entry.
		var args struct {
			Package string `json:"package"`
		}
		if strings.TrimSpace(invocation.Payload.Arguments) == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &args); err != nil {
			return nil
		}
		pkg := strings.TrimSpace(args.Package)
		if pkg == "" {
			return nil
		}
		for i := range skills {
			skill := skills[i]
			if !isResourceBackedSkill(skill) {
				continue
			}
			if skill.PackageID == pkg || skill.ResourceID == pkg || skill.LocatorPath == pkg {
				return &skill
			}
		}
		return nil
	}
	if !tool.IsShellCommandToolName(invocation.ToolName) && name != "shell" {
		return nil
	}
	var args implicitShellSkillArgs
	if strings.TrimSpace(invocation.Payload.Arguments) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &args); err != nil {
		return nil
	}
	command := strings.TrimSpace(firstNonEmpty(args.Cmd, args.Command))
	if command == "" {
		return nil
	}
	workdir := implicitSkillWorkdir(baseCWD, firstNonEmpty(args.CWD, args.Workdir))
	if strings.TrimSpace(workdir) == "" {
		return nil
	}
	return promptctx.DetectImplicitSkillInvocationForCommand(skills, command, workdir)
}

func implicitSkillResourceReadInvocation(invocation *tool.Invocation) bool {
	return invocation != nil && invocation.ToolName.Key() == "skills.read"
}

func implicitSkillWorkdir(base string, override string) string {
	base = strings.TrimSpace(base)
	raw := strings.TrimSpace(override)
	if raw == "" {
		raw = base
	}
	if raw == "" {
		if cwd, err := os.Getwd(); err == nil {
			raw = cwd
		}
	}
	if raw == "" {
		return ""
	}
	if !filepath.IsAbs(raw) && base != "" {
		raw = filepath.Join(base, raw)
	}
	if absolute, err := filepath.Abs(raw); err == nil {
		raw = absolute
	}
	return filepath.Clean(raw)
}

func implicitSkillInvocationSeenKey(skill promptctx.InstructionsSkillMetadata) string {
	if isResourceBackedSkill(skill) {
		if id := strings.TrimSpace(firstNonEmpty(skill.ResourceID, skill.PackageID, skill.LocatorPath)); id != "" {
			return "resource:" + id
		}
		return ""
	}
	if skill.Path == "" || skill.Name == "" {
		return ""
	}
	return skillInvocationScope(skill.Scope) + ":" + skill.Path + ":" + skill.Name
}

func (r *RuntimeRouter) trackSkillInvocationEvent(ctx context.Context, threadID string, turnID string, modelID string, productClientID string, skill promptctx.InstructionsSkillMetadata, invokeType string) {
	if invokeType == telemetry.SkillInvocationTypeImplicit {
		r.recordSkillShadowInvocation(threadID, turnID, skill)
	}
	if r == nil || r.services.Analytics == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.SkillInvocationEventSink)
	if !ok {
		return
	}
	repoRoot, repoURL := skillInvocationRepo(skill.Path)
	scope := skillInvocationScope(skill.Scope)
	if isResourceBackedSkill(skill) {
		// Resource-backed skills (Rust #38066): stable fallback ID derived from
		// the main resource, provider-scoped, no repository context.
		repoRoot = ""
		repoURL = ""
	}
	sink.TrackSkillInvocationEvent(ctx, telemetry.SkillInvocationEventRequest{
		EventType: telemetry.SkillInvocationEventType,
		SkillID:   resourceOrHostSkillInvocationID(skill, repoURL, repoRoot),
		SkillName: skill.Name,
		EventParams: telemetry.SkillInvocationEventParams{
			ProductClientID: stringPtrIfNotEmpty(productClientID),
			SkillScope:      stringPtrIfNotEmpty(scope),
			PluginID:        stringPtrIfNotEmpty(skill.PluginID),
			RemotePluginID:  stringPtrIfNotEmpty(skill.RemotePluginID),
			RepoURL:         stringPtrIfNotEmpty(repoURL),
			ThreadID:        stringPtrIfNotEmpty(threadID),
			TurnID:          stringPtrIfNotEmpty(turnID),
			InvokeType:      stringPtrIfNotEmpty(invokeType),
			ModelSlug:       stringPtrIfNotEmpty(modelID),
		},
	})
}

func skillInvocationScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "repo":
		return "repo"
	case "system":
		return "system"
	case "admin":
		return "admin"
	default:
		return "user"
	}
}

func skillInvocationRepo(skillPath string) (string, string) {
	current := filepath.Dir(skillPath)
	for current != "" {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			if info, ok := utils.CollectGitInfoFromDir(current); ok {
				return current, info.RepositoryURL
			}
			return current, ""
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", ""
}

func skillInvocationID(repoURL string, repoRoot string, skillPath string, skillName string) string {
	resolvedPath := skillPath
	if canonical, err := filepath.EvalSymlinks(skillPath); err == nil {
		resolvedPath = canonical
	}
	prefix := "personal"
	if repoURL != "" {
		prefix = "repo_" + repoURL
		if canonicalRoot, err := filepath.EvalSymlinks(repoRoot); err == nil {
			repoRoot = canonicalRoot
		}
		if relative, err := filepath.Rel(repoRoot, resolvedPath); err == nil {
			resolvedPath = relative
		}
	}
	rawID := prefix + "_" + strings.ReplaceAll(resolvedPath, "\\", "/") + "_" + skillName
	digest := sha1.Sum([]byte(rawID))
	return fmt.Sprintf("%x", digest)
}

func (r *RuntimeRouter) explicitSkillInputItems(threadID string, params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata) []any {
	return r.explicitSkillInputItemsForTurn(threadID, "", "", params, skills)
}

func (r *RuntimeRouter) explicitSkillInputItemsForTurn(threadID string, turnID string, modelID string, params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata) []any {
	return r.explicitSkillInputItemsForTurnWithProvider(threadID, turnID, modelID, params, skills, nil)
}

func (r *RuntimeRouter) explicitSkillInputItemsForTurnWithProvider(threadID string, turnID string, modelID string, params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata, executorProviders *skillprovider.Registry) []any {
	selected := promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: skillMentionInputsFromTurn(params),
		Skills: skills,
	})
	if len(selected) == 0 {
		return nil
	}
	items := make([]any, 0, len(selected))
	for _, skill := range selected {
		item, truncated, err := r.skillInstructionsInputItemWithProvider(threadID, skill, executorProviders)
		if err != nil {
			r.emitExplicitSkillInjectionMetric(skill.Name, "error")
			if r != nil {
				r.notify(NotificationWarning, &WarningNotification{
					ThreadID: stringPtrIfNotEmpty(threadID),
					Message:  fmt.Sprintf("Failed to load skill `%s`: %s", skill.Name, err),
				})
			}
			continue
		}
		r.emitExplicitSkillInjectionMetric(skill.Name, "success")
		if item != nil {
			items = append(items, item)
			if strings.EqualFold(strings.TrimSpace(skill.LocatorKind), "file") || strings.TrimSpace(skill.LocatorKind) == "" {
				productClientID := ""
				if params != nil {
					productClientID = params.Originator
				}
				r.trackSkillInvocationEvent(context.Background(), threadID, turnID, modelID, productClientID, skill, telemetry.SkillInvocationTypeExplicit)
			}
			if truncated && r != nil {
				r.notify(NotificationWarning, &WarningNotification{
					ThreadID: stringPtrIfNotEmpty(threadID),
					Message:  promptctx.SkillMainPromptTruncatedWarning(skill.Name),
				})
			}
		}
	}
	return items
}

func (r *RuntimeRouter) emitExplicitSkillInjectionMetric(skillName string, status string) {
	if r == nil || r.services.SkillInjectionMetrics == nil {
		return
	}
	r.services.SkillInjectionMetrics.Counter("codex.skill.injected", 1, map[string]string{
		"status":      status,
		"skill":       sanitizeMetricTagValue(skillName),
		"invoke_type": telemetry.SkillInvocationTypeExplicit,
	})
}

func sanitizeMetricTagValue(value string) string {
	const maxLength = 256
	var builder strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("._-/", ch) {
			builder.WriteRune(ch)
		} else {
			builder.WriteByte('_')
		}
	}
	trimmed := strings.Trim(builder.String(), "_")
	hasASCIIAlphanumeric := false
	for _, ch := range trimmed {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			hasASCIIAlphanumeric = true
			break
		}
	}
	if trimmed == "" || !hasASCIIAlphanumeric {
		return "unspecified"
	}
	if len(trimmed) > maxLength {
		return trimmed[:maxLength]
	}
	return trimmed
}

func (r *RuntimeRouter) skillInstructionsInputItem(threadID string, skill promptctx.InstructionsSkillMetadata) (any, bool, error) {
	return r.skillInstructionsInputItemWithProvider(threadID, skill, nil)
}

func (r *RuntimeRouter) skillInstructionsInputItemWithProvider(threadID string, skill promptctx.InstructionsSkillMetadata, executorProviders *skillprovider.Registry) (any, bool, error) {
	contents := skill.Contents
	if strings.TrimSpace(contents) == "" {
		if strings.EqualFold(strings.TrimSpace(skill.AuthorityKind), "orchestrator") {
			if r == nil || r.services.MCP == nil {
				return nil, false, errors.New("session MCP resource client is not configured")
			}
			read, err := r.readOrchestratorSkillResourceForRuntime(threadID, skill)
			if err != nil {
				return nil, false, err
			}
			contents = read
		} else if strings.EqualFold(strings.TrimSpace(skill.AuthorityKind), string(skillprovider.SourceExecutor)) {
			providers := executorProviders
			if providers == nil {
				providers = r.executorSkillProviderForThread(threadID)
			}
			if providers == nil {
				return nil, false, errors.New("executor skill provider is not configured")
			}
			result, err := providers.Read(context.Background(), skillprovider.ReadRequest{
				Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: skill.AuthorityID},
				PackageID: skill.PackageID,
				Resource:  skill.ResourceID,
			})
			if err != nil {
				return nil, false, err
			}
			contents = result.Contents
		} else if strings.TrimSpace(skill.AuthorityKind) != "" {
			if r == nil || r.services.CustomSkills == nil {
				return nil, false, fmt.Errorf("%s skill provider is not configured", skill.AuthorityKind)
			}
			result, err := r.services.CustomSkills.Read(context.Background(), skillprovider.ReadRequest{
				Authority: skillprovider.Authority{Kind: skillprovider.SourceKind(skill.AuthorityKind), ID: skill.AuthorityID},
				PackageID: skill.PackageID,
				Resource:  skill.ResourceID,
			})
			if err != nil {
				return nil, false, err
			}
			contents = result.Contents
		} else {
			data, err := os.ReadFile(skill.Path)
			if err != nil {
				return nil, false, fmt.Errorf("failed to read host skill resource %s: %w", skill.Path, err)
			}
			contents = string(data)
		}
		if strings.TrimSpace(contents) == "" {
			return nil, false, nil
		}
	}
	renderPath := firstNonEmpty(skill.LocatorPath, skill.Path)
	name, renderPath, contents, truncated := promptctx.TruncateSkillInstructionFields(skill.Name, renderPath, contents)
	var fragment *contextfrag.SkillInstructions
	if strings.EqualFold(strings.TrimSpace(skill.AuthorityKind), string(skillprovider.SourceExecutor)) && !skill.AllowsImplicitInvocation() {
		fragment = contextfrag.NewSkillInstructionsWithExecutorResourceAccess(name, renderPath, contents, &contextfrag.ExecutorSkillResourceAccess{
			AuthorityID:  skill.AuthorityID,
			Package:      skill.PackageID,
			MainResource: skill.ResourceID,
		})
	} else {
		fragment = contextfrag.NewSkillInstructions(name, renderPath, contents)
	}
	rendered := contextfrag.Render(fragment)
	return renderedFragmentInputItem(rendered), truncated, nil
}

func skillMentionInputsFromTurn(params *turn.TurnStartParams) []promptctx.SkillMentionInput {
	if params == nil {
		return nil
	}
	inputs := make([]promptctx.SkillMentionInput, 0, len(params.Input)+1)
	if strings.TrimSpace(params.Prompt) != "" {
		inputs = append(inputs, promptctx.SkillMentionInput{Type: "text", Text: params.Prompt})
	}
	for _, input := range params.Input {
		inputs = append(inputs, promptctx.SkillMentionInput{
			Type: input.Type,
			Text: input.Text,
			Name: input.Name,
			Path: input.Path,
		})
	}
	return inputs
}

func renderedFragmentInputItem(rendered *contextfrag.RenderedFragment) any {
	if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
		return nil
	}
	role := strings.TrimSpace(rendered.Role)
	if role == "" {
		role = contextfrag.RoleUser
	}
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{{
			"type": "input_text",
			"text": rendered.Content,
		}},
	}
}

func (r *RuntimeRouter) skillContextWindowForTurn(cfg *config.Config, params *turn.TurnStartParams) int64 {
	modelID := firstNonEmpty(turnParamModel(params), stringConfigValue(cfg, "model"), defaultModelForAppTurn())
	if strings.TrimSpace(modelID) == "" {
		return 0
	}
	contextWindow := r.modelContextWindowForModel(modelID)
	if contextWindow == nil {
		return 0
	}
	return *contextWindow
}

func (r *RuntimeRouter) includeSkillsUsageInstructionsForTurn(cfg *config.Config, params *turn.TurnStartParams) bool {
	modelID := firstNonEmpty(turnParamModel(params), stringConfigValue(cfg, "model"), defaultModelForAppTurn())
	info := r.modelInfoForRuntimeWithConfig(modelID, cfg)
	return info != nil && info.IncludeSkillsUsageInstructions
}

func contextPluginSummaries(capabilities []plugin.CapabilitySummary) []contextfrag.PluginSummary {
	out := make([]contextfrag.PluginSummary, 0, len(capabilities))
	for _, capability := range capabilities {
		name := firstNonEmpty(capability.ConfigName, capability.Name)
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, contextfrag.PluginSummary{
			ConfigName:  name,
			DisplayName: firstNonEmpty(capability.DisplayName, capability.Name, capability.ConfigName),
			HasSkills:   capability.HasSkills,
		})
	}
	return out
}

func pluginUserInputFromTurn(params *turn.TurnStartParams) []plugin.UserInput {
	if params == nil {
		return nil
	}
	input := make([]plugin.UserInput, 0, len(params.Input)+1)
	if strings.TrimSpace(params.Prompt) != "" {
		input = append(input, plugin.UserInput{Type: "text", Text: params.Prompt})
	}
	for _, item := range params.Input {
		input = append(input, plugin.UserInput{
			Type: item.Type,
			Text: item.Text,
			Path: item.Path,
		})
	}
	return input
}

func appBaseInstructionsForConfig(cfg *config.Config) (string, error) {
	if path := stringConfigValue(cfg, "model_instructions_file"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read model instructions file %s: %w", path, err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			return "", fmt.Errorf("model instructions file is empty: %s", path)
		}
		return text, nil
	}
	return stringConfigValue(cfg, "instructions"), nil
}

func modelConfigForAppTurn(cfg *config.Config) *model.ModelsManagerConfig {
	settings := map[string]bool{}
	if cfg != nil {
		settings = cfg.FeatureSettings()
	}
	out := &model.ModelsManagerConfig{
		PersonalityEnabled: features.Enabled(settings, "personality"),
	}
	if cfg != nil {
		out.ModelContextWindow = int64(intFromAny(cfg.Values["model_context_window"]))
		out.ModelAutoCompactTokenLimit = int64(intFromAny(cfg.Values["model_auto_compact_token_limit"]))
	}
	return out
}

func appPersonalityForTurn(cfg *config.Config, params *turn.TurnStartParams) string {
	if params != nil && params.Personality != nil {
		return strings.TrimSpace(*params.Personality)
	}
	return stringConfigValue(cfg, "personality")
}

func turnParamModel(params *turn.TurnStartParams) string {
	if params == nil {
		return ""
	}
	return strings.TrimSpace(params.Model)
}

func turnBaseInstructions(params *turn.TurnStartParams) string {
	if params == nil || params.BaseInstructions == nil {
		return ""
	}
	return strings.TrimSpace(*params.BaseInstructions)
}

func turnCWD(params *turn.TurnStartParams) string {
	if params == nil {
		return ""
	}
	return strings.TrimSpace(params.CWD)
}

func stringConfigValue(cfg *config.Config, key string) string {
	if cfg == nil || cfg.Values == nil {
		return ""
	}
	value, ok := cfg.Values[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func appIncludeTimingMetrics(cfg *config.Config) bool {
	return boolConfigValue(cfg, "include_timing_metrics") ||
		boolConfigValue(cfg, "includeTimingMetrics") ||
		boolConfigValue(cfg, "responsesapi_include_timing_metrics") ||
		boolConfigValue(cfg, "responsesapiIncludeTimingMetrics")
}

func (r *RuntimeRouter) appServiceTierForTurn(cfg *config.Config, params *turn.TurnStartParams, modelID string) string {
	settings := map[string]bool{}
	if cfg != nil {
		settings = cfg.FeatureSettings()
	}
	if !features.Enabled(settings, "fast_mode") {
		return ""
	}
	requested := ""
	if params != nil {
		if params.ServiceTierSet && params.ServiceTier == nil {
			requested = model.ServiceTierDefaultRequestValue
		} else if params.ServiceTier != nil {
			requested = stringPtrValue(params.ServiceTier)
		}
		if params.ServiceTierSet || params.ServiceTier != nil {
			info := r.modelInfoForRuntime(modelID)
			return model.ServiceTierForRequest(info, requested)
		}
	}
	value := firstNonEmpty(
		stringConfigValue(cfg, "service_tier"),
		stringConfigValue(cfg, "serviceTier"),
	)
	if value == "" {
		return ""
	}
	info := r.modelInfoForRuntime(modelID)
	return model.ServiceTierForRequest(info, value)
}

func boolConfigValue(cfg *config.Config, key string) bool {
	if cfg == nil || cfg.Values == nil {
		return false
	}
	switch value := cfg.Values[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func defaultModelForAppTurn() string {
	manager := model.NewStaticModelsManager(model.BundledModelsResponse())
	return manager.GetDefaultModel("", true, model.RefreshOffline)
}

func (r *RuntimeRouter) modelSupportsParallelToolCalls(modelID string) bool {
	if strings.TrimSpace(modelID) == "" {
		return false
	}
	info := r.requireModels().Info(&model.ModelInfoReadParams{Model: modelID})
	return info != nil && info.SupportsParallelToolCalls && !info.UseResponsesLite
}

func (r *RuntimeRouter) modelInfoForRuntime(modelID string) *model.ModelInfo {
	return r.modelInfoForRuntimeWithConfig(modelID, nil)
}

func (r *RuntimeRouter) modelInfoForRuntimeWithConfig(modelID string, cfg *config.Config) *model.ModelInfo {
	if strings.TrimSpace(modelID) == "" {
		return nil
	}
	modelConfig := modelConfigForAppTurn(cfg)
	var values map[string]any
	if cfg != nil {
		values = cfg.Values
	}
	if catalog := model.ModelsCatalogFromConfigValues(values); catalog != nil {
		info := model.NewStaticModelsManager(*catalog).GetModelInfo(modelID, modelConfig)
		return &info
	}
	return r.requireModels().Info(&model.ModelInfoReadParams{Model: modelID, Config: modelConfig})
}

func (r *RuntimeRouter) modelUsesResponsesLite(modelID string) bool {
	info := r.modelInfoForRuntime(modelID)
	return info != nil && info.UseResponsesLite
}

func (r *RuntimeRouter) modelContextWindowForModel(modelID string) *int64 {
	if strings.TrimSpace(modelID) == "" {
		return nil
	}
	info := r.requireModels().Info(&model.ModelInfoReadParams{Model: modelID})
	if info == nil || info.ContextWindow <= 0 {
		return nil
	}
	value := info.ContextWindow
	return &value
}

func appTurnMetadata(turnID string, extra map[string]any) map[string]any {
	metadata := map[string]any{"turnId": turnID}
	for key, value := range extra {
		if value != nil && value != "" {
			metadata[key] = value
		}
	}
	return metadata
}

func sessionItemFromTurnSteer(params *turn.TurnSteerParams) (session.Item, bool) {
	if params == nil {
		return session.Item{}, false
	}
	text := firstNonEmpty(strings.TrimSpace(params.Prompt), turnUserInputsText(params.Input))
	content := sessionContentFromTurnUserInputs(params.Input)
	if strings.TrimSpace(text) == "" && len(content) == 0 {
		return session.Item{}, false
	}
	now := time.Now().UTC()
	itemID := strings.TrimSpace(params.ClientUserMessageID)
	if itemID == "" {
		itemID = "steer-" + safeIdentifier(params.ExpectedTurnID) + "-" + safeIdentifier(fmt.Sprintf("%d", now.UnixNano()))
	}
	metadata := appTurnMetadata(params.ExpectedTurnID, map[string]any{
		"clientId":            params.ClientUserMessageID,
		"client_user_message": params.ClientUserMessageID,
		"steered":             true,
	})
	if len(params.AdditionalContext) > 0 {
		metadata["additionalContext"] = additionalContextMetadata(params.AdditionalContext)
	}
	return session.Item{
		ID:        itemID,
		Type:      "message",
		Role:      "user",
		Text:      text,
		Content:   content,
		CreatedAt: now,
		Data:      runtimeUserInputThreadItemData(params.Prompt, params.Input),
		Metadata:  metadata,
	}, true
}

func inputItemsFromTurnSteer(params *turn.TurnSteerParams) []any {
	if params == nil {
		return nil
	}
	items := []any{}
	if item := userMessageInputItemFromTurnUserInputs(params.Prompt, params.Input); item != nil {
		items = append(items, item)
	}
	instructions, additionalItems := instructionsAndInputItemsWithAdditionalContext("", params.AdditionalContext)
	if strings.TrimSpace(instructions) != "" {
		items = append(items, map[string]any{
			"type": "message",
			"role": "developer",
			"content": []map[string]any{{
				"type": "input_text",
				"text": strings.TrimSpace(instructions),
			}},
		})
	}
	items = append(items, additionalItems...)
	return items
}

func userMessageInputItemFromTurnUserInputs(prompt string, inputs []turn.TurnUserInput) any {
	content := inputContentFromTurnUserInputs(prompt, inputs)
	if len(content) == 0 {
		return nil
	}
	return map[string]any{
		"type":    "message",
		"role":    "user",
		"content": content,
	}
}

func inputContentFromTurnUserInputs(prompt string, inputs []turn.TurnUserInput) []map[string]any {
	content := []map[string]any{}
	if text := strings.TrimSpace(prompt); text != "" {
		content = append(content, map[string]any{"type": "input_text", "text": text})
	}
	imageIndex := 0
	for i := range inputs {
		input := inputs[i]
		inputType := strings.TrimSpace(input.Type)
		if text := strings.TrimSpace(input.Text); text != "" {
			content = append(content, map[string]any{"type": "input_text", "text": text})
			continue
		}
		if imageURL := strings.TrimSpace(input.URL); imageURL != "" && (inputType == "" || strings.EqualFold(inputType, "image")) {
			imageIndex++
			content = append(content, inputImageContentBlock(imageURL, inputDetail(input)))
			continue
		}
		if path := strings.TrimSpace(input.Path); path != "" && (inputType == "" || strings.EqualFold(inputType, "localImage")) {
			imageIndex++
			content = append(content, localImageInputContentBlocks(path, inputDetail(input), imageIndex)...)
			continue
		}
		if audioURL := strings.TrimSpace(input.URL); audioURL != "" && strings.EqualFold(inputType, "audio") {
			content = append(content, map[string]any{"type": "input_audio", "audio_url": audioURL})
			continue
		}
		if path := strings.TrimSpace(input.Path); path != "" && strings.EqualFold(inputType, "localAudio") {
			content = append(content, localAudioInputContentBlocks(path)...)
		}
	}
	return content
}

func inputImageContentBlock(imageURL string, detail string) map[string]any {
	if strings.TrimSpace(detail) == "" {
		detail = "high"
	}
	return map[string]any{"type": "input_image", "image_url": imageURL, "detail": detail}
}

func inputDetail(input turn.TurnUserInput) string {
	if input.Detail != nil && strings.TrimSpace(*input.Detail) != "" {
		return strings.TrimSpace(*input.Detail)
	}
	return "high"
}

func localImageInputContentBlocks(path string, detail string, imageIndex int) []map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return []map[string]any{{
			"type": "input_text",
			"text": fmt.Sprintf("Codex could not read the local image at `%s`: %v", path, err),
		}}
	}
	return []map[string]any{
		{"type": "input_text", "text": fmt.Sprintf("<image name=[Image #%d] path=\"%s\">", imageIndex, path)},
		inputImageContentBlock(dataURLFromBytes(data), detail),
		{"type": "input_text", "text": "</image>"},
	}
}

func localAudioInputContentBlocks(path string) []map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return []map[string]any{{"type": "input_text", "text": fmt.Sprintf("Codex could not read the local audio at `%s`: %v", path, err)}}
	}
	return []map[string]any{{"type": "input_audio", "audio_url": dataURLFromBytes(data)}}
}

func dataURLFromBytes(data []byte) string {
	mimeType := http.DetectContentType(data)
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func turnUserInputsText(inputs []turn.TurnUserInput) string {
	parts := []string{}
	for i := range inputs {
		if text := strings.TrimSpace(inputs[i].Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func sessionContentFromTurnStart(params *turn.TurnStartParams) []session.ContentPart {
	if params == nil {
		return nil
	}
	content := []session.ContentPart{}
	if text := strings.TrimSpace(params.Prompt); text != "" {
		content = append(content, session.ContentPart{Type: "input_text", Text: text})
	}
	content = append(content, sessionContentFromTurnUserInputs(params.Input)...)
	if len(content) == 0 {
		return nil
	}
	return content
}

func sessionContentFromTurnUserInputs(inputs []turn.TurnUserInput) []session.ContentPart {
	content := make([]session.ContentPart, 0, len(inputs))
	for i := range inputs {
		input := inputs[i]
		inputType := strings.TrimSpace(input.Type)
		switch {
		case strings.TrimSpace(input.Text) != "":
			content = append(content, session.ContentPart{Type: "text", Text: strings.TrimSpace(input.Text)})
		case strings.TrimSpace(input.URL) != "" && (inputType == "" || strings.EqualFold(inputType, "image")):
			if inputType == "" {
				inputType = "image"
			}
			content = append(content, session.ContentPart{Type: inputType, ImageURL: strings.TrimSpace(input.URL), Detail: cloneString(input.Detail)})
		case strings.TrimSpace(input.Path) != "" && (inputType == "" || strings.EqualFold(inputType, "localImage")):
			if inputType == "" {
				inputType = "localImage"
			}
			content = append(content, session.ContentPart{Type: inputType, ImageURL: strings.TrimSpace(input.Path), Detail: cloneString(input.Detail)})
		case strings.TrimSpace(input.URL) != "" && strings.EqualFold(inputType, "audio"):
			content = append(content, session.ContentPart{Type: "audio", AudioURL: strings.TrimSpace(input.URL)})
		case strings.TrimSpace(input.Path) != "" && strings.EqualFold(inputType, "localAudio"):
			content = append(content, session.ContentPart{Type: "localAudio", AudioURL: strings.TrimSpace(input.Path)})
		}
	}
	return content
}

func additionalContextMetadata(values map[string]turn.AdditionalContextEntry) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = map[string]any{
			"value": value.Value,
			"kind":  string(value.Kind),
		}
	}
	return out
}

func appTimingMetadata(metadata map[string]any, started time.Time, completed time.Time) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if !started.IsZero() {
		value := started.UTC().UnixMilli()
		metadata["startedAtMs"] = value
		metadata["started_at_ms"] = value
	}
	if !completed.IsZero() {
		value := completed.UTC().UnixMilli()
		metadata["completedAtMs"] = value
		metadata["completed_at_ms"] = value
	}
	if !started.IsZero() && !completed.IsZero() && !completed.Before(started) {
		value := completed.Sub(started).Milliseconds()
		metadata["durationMs"] = value
		metadata["duration_ms"] = value
	}
	return metadata
}

func threadItemPayload(item ThreadItem) ThreadItemPayload {
	data, err := json.Marshal(&item)
	if err == nil {
		payload := ThreadItemPayload{}
		if err := json.Unmarshal(data, &payload); err == nil {
			return payload
		}
	}
	payload := ThreadItemPayload{"id": item.ID, "type": item.Type}
	return payload
}

func threadItemFromStreamAgentItem(item *model.AgentItem, turnID string, responseID string, createdAt time.Time) ThreadItem {
	if item == nil {
		return ThreadItem{}
	}
	threadItem := ThreadItem{
		ID:         firstNonEmpty(item.ID, item.CallID),
		Type:       item.Type,
		Text:       firstNonEmpty(item.Text, item.Arguments, item.Input),
		TurnID:     turnID,
		CreatedAt:  createdAt.UnixMilli(),
		Data:       cloneAnyMap(item.Data),
		ResponseID: strings.TrimSpace(responseID),
	}
	if threadItem.Type == "" || threadItem.Type == "message" {
		threadItem.Type = "agent_message"
	}
	if threadItem.ID == "" {
		threadItem.ID = threadItem.Type + "-" + safeIdentifier(turnID)
	}
	if item.Type == "agent_message" {
		threadItem.Role = "assistant"
	}
	if item.Type == "image_generation_call" {
		threadItem.Type = "imageGeneration"
		if threadItem.Data == nil {
			threadItem.Data = map[string]any{}
		}
		result := firstNonEmpty(stringFromMap(threadItem.Data, "result"), item.Text)
		threadItem.Status = model.NormalizeImageGenerationStatus(firstNonEmpty(item.Status, stringFromMap(threadItem.Data, "status")), result)
		if item.Text != "" {
			threadItem.Data["result"] = item.Text
		}
		threadItem.Data["status"] = threadItem.Status
		if revised := firstNonEmpty(stringFromMap(item.Data, "revisedPrompt"), stringFromMap(item.Data, "revised_prompt")); revised != "" {
			threadItem.Data["revisedPrompt"] = revised
			threadItem.Data["revised_prompt"] = revised
		}
	}
	if item.Type == "web_search_call" {
		action, query := appWebSearchActionFromAgentItem(item)
		threadItem.Type = "webSearch"
		threadItem.Text = query
		threadItem.Data = map[string]any{"query": query, "action": action}
	}
	if isAppToolItem(item) {
		if threadItem.Data == nil {
			threadItem.Data = map[string]any{}
		}
		threadItem.Data["toolName"] = item.Name
		threadItem.Data["callId"] = item.CallID
		if item.Namespace != "" {
			threadItem.Data["namespace"] = item.Namespace
		}
	}
	return threadItem
}

func appWebSearchActionFromAgentItem(item *model.AgentItem) (any, string) {
	if item == nil {
		return map[string]any{"type": "other"}, ""
	}
	action := threadItemWebSearchActionFromAny(item.Search)
	if action == nil {
		action = map[string]any{"type": "other"}
	}
	return action, webSearchQueryFromAction(action)
}

func streamAgentItemLooksLikeMCP(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(item.Namespace), mcp.LegacyMCPToolNamePrefix) ||
		strings.HasPrefix(strings.TrimSpace(item.Name), mcp.LegacyMCPToolNamePrefix)
}

func streamAgentItemIsToolSearch(item *model.AgentItem) bool {
	return item != nil && strings.TrimSpace(item.Type) == "tool_search_call"
}

func mcpToolCallStartedThreadItem(invocation *tool.Invocation, turnID string, startedAt time.Time) (ThreadItem, bool) {
	if invocation == nil || !strings.HasPrefix(strings.TrimSpace(invocation.ToolName.Namespace), mcp.LegacyMCPToolNamePrefix) {
		return ThreadItem{}, false
	}
	data := appToolInvocationData(invocation)
	markMCPToolData(data, invocation.ToolName)
	data["status"] = "inProgress"
	data = appTimingMetadata(appTurnMetadata(turnID, data), startedAt, time.Time{})
	return ThreadItem{
		ID:        firstNonEmpty(strings.TrimSpace(invocation.CallID), "mcp-tool-call-"+safeIdentifier(turnID)),
		Type:      "mcpToolCall",
		Name:      invocation.ToolName.Key(),
		CallID:    strings.TrimSpace(invocation.CallID),
		TurnID:    turnID,
		CreatedAt: startedAt.UTC().UnixMilli(),
		Data:      data,
	}, true
}

func cloneTurnStartParams(params *turn.TurnStartParams) *turn.TurnStartParams {
	if params == nil {
		return nil
	}
	clone := *params
	clone.Input = append([]turn.TurnUserInput(nil), params.Input...)
	clone.ResponsesAPIMetadata = cloneStringMap(params.ResponsesAPIMetadata)
	clone.RuntimeWorkspaceRoots = append([]string(nil), params.RuntimeWorkspaceRoots...)
	clone.Environments = cloneMapSlice(params.Environments)
	clone.Config = cloneAnyMap(params.Config)
	clone.AdditionalContext = cloneAdditionalContext(params.AdditionalContext)
	clone.DynamicTools = append([]turn.DynamicToolSpec(nil), params.DynamicTools...)
	clone.AdditionalInputItems = append([]any(nil), params.AdditionalInputItems...)
	return &clone
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneMapSlice(values []map[string]any) []map[string]any {
	if values == nil {
		return nil
	}
	out := make([]map[string]any, len(values))
	for i := range values {
		out[i] = cloneAnyMap(values[i])
	}
	return out
}

func cloneAdditionalContext(values map[string]turn.AdditionalContextEntry) map[string]turn.AdditionalContextEntry {
	if values == nil {
		return nil
	}
	out := make(map[string]turn.AdditionalContextEntry, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func isAppToolItem(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case "function_call", "custom_tool_call", "tool_search_call":
		return true
	default:
		return false
	}
}

func appToolSessionItemType(kind tool.PayloadKind) string {
	switch kind {
	case tool.PayloadCustom:
		return "custom_tool_call"
	case tool.PayloadToolSearch:
		return "tool_search_call"
	default:
		return "function_call"
	}
}

func appToolInvocationText(invocation *tool.Invocation) string {
	if invocation == nil {
		return ""
	}
	switch invocation.Payload.Kind {
	case tool.PayloadCustom:
		return invocation.Payload.Input
	case tool.PayloadToolSearch:
		data, err := json.Marshal(invocation.Payload.Search)
		if err != nil {
			return ""
		}
		return string(data)
	default:
		return invocation.Payload.Arguments
	}
}

func appToolInvocationData(invocation *tool.Invocation) map[string]any {
	if invocation == nil {
		return nil
	}
	data := map[string]any{
		"name":         invocation.ToolName.Key(),
		"call_id":      invocation.CallID,
		"payload_kind": string(invocation.Payload.Kind),
	}
	if invocation.Payload.Arguments != "" {
		data["arguments"] = invocation.Payload.Arguments
		addShellCommandData(data, invocation.Payload.Arguments)
	}
	addNamespacedToolData(data, invocation.ToolName)
	if invocation.Payload.Input != "" {
		data["input"] = invocation.Payload.Input
	}
	if invocation.Payload.Search != nil {
		data["search"] = invocation.Payload.Search
		data["arguments_map"] = invocation.Payload.Search
	}
	addInvocationReadOnlyHint(data, invocation)
	return data
}

func appToolOutputData(invocation *tool.Invocation, output *tool.Output) map[string]any {
	if output == nil {
		return nil
	}
	data := map[string]any{
		"call_id": output.CallID,
		"success": output.Success,
	}
	if output.Body != "" {
		data["output"] = output.Body
	}
	if output.Error != "" {
		data["error"] = output.Error
	}
	for key, value := range output.Data {
		data[key] = value
	}
	if invocation != nil {
		data["name"] = invocation.ToolName.Key()
		if invocation.Payload.Arguments != "" {
			data["arguments"] = invocation.Payload.Arguments
			addShellCommandData(data, invocation.Payload.Arguments)
		}
		addInvocationReadOnlyHint(data, invocation)
		if appToolOutputIsMCP(output) {
			markMCPToolData(data, invocation.ToolName)
		} else if appToolOutputIsDynamic(output) {
			markDynamicToolData(data, invocation.ToolName)
		} else if appToolOutputIsFileChange(output) {
			markFileChangeToolData(data, output, false)
		} else {
			addNamespacedToolData(data, invocation.ToolName)
		}
	}
	return data
}

func addInvocationReadOnlyHint(data map[string]any, invocation *tool.Invocation) {
	if data == nil || invocation == nil || invocation.Context == nil {
		return
	}
	if hint, ok := invocation.Context["read_only_hint"].(bool); ok {
		data["read_only_hint"] = hint
	}
}

func addShellCommandData(data map[string]any, arguments string) {
	if data == nil || strings.TrimSpace(arguments) == "" {
		return
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return
	}
	if command, ok := args["cmd"].(string); ok && strings.TrimSpace(command) != "" {
		data["command"] = command
	}
	if cwd, ok := args["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
		data["cwd"] = cwd
	}
	if workdir, ok := args["workdir"].(string); ok && strings.TrimSpace(workdir) != "" {
		data["cwd"] = workdir
	}
}

func addNamespacedToolData(data map[string]any, name tool.ToolName) {
	if data == nil {
		return
	}
	if strings.TrimSpace(name.Namespace) == "" {
		return
	}
	data["namespace"] = name.Namespace
	data["tool"] = name.Name
}

func markMCPToolData(data map[string]any, name tool.ToolName) {
	if data == nil {
		return
	}
	if strings.TrimSpace(stringFromMap(data, "server")) == "" && strings.TrimSpace(name.Namespace) != "" {
		data["server"] = strings.TrimPrefix(name.Namespace, mcp.LegacyMCPToolNamePrefix)
	}
	if strings.TrimSpace(stringFromMap(data, "tool")) == "" && strings.TrimSpace(name.Name) != "" {
		data["tool"] = name.Name
	}
	data["mcpToolCall"] = true
}

func markDynamicToolData(data map[string]any, name tool.ToolName) {
	if data == nil {
		return
	}
	addNamespacedToolData(data, name)
	data["dynamicToolCall"] = true
}

func markFileChangeToolData(data map[string]any, output *tool.Output, inProgress bool) {
	if data == nil {
		return
	}
	data["fileChange"] = true
	if output != nil && output.Data != nil {
		if changes, ok := output.Data["changes"]; ok {
			data["changes"] = changes
		}
	}
	if inProgress {
		data["status"] = "inProgress"
		return
	}
	if status, ok := data["status"].(string); ok && strings.TrimSpace(status) != "" {
		return
	}
	if output != nil && !output.Success {
		data["status"] = "failed"
		return
	}
	data["status"] = "completed"
}

func appToolOutputIsMCP(output *tool.Output) bool {
	if output == nil || output.Data == nil {
		return false
	}
	if marker, ok := output.Data["mcpToolCall"].(bool); ok && marker {
		return true
	}
	if marker, ok := output.Data["mcp_tool_call"].(bool); ok && marker {
		return true
	}
	return false
}

func appToolOutputIsFileChange(output *tool.Output) bool {
	if output == nil || output.Data == nil {
		return false
	}
	if marker, ok := output.Data["fileChange"].(bool); ok && marker {
		return true
	}
	if marker, ok := output.Data["file_change"].(bool); ok && marker {
		return true
	}
	return false
}

func appToolOutputIsDynamic(output *tool.Output) bool {
	if output == nil || output.Data == nil {
		return false
	}
	if marker, ok := output.Data["dynamicToolCall"].(bool); ok && marker {
		return true
	}
	if marker, ok := output.Data["dynamic_tool_call"].(bool); ok && marker {
		return true
	}
	return false
}

const explicitRequestOnlyMultiAgentModeText = "Any earlier instruction enabling proactive multi-agent delegation no longer applies. Do not spawn sub-agents unless the user or applicable AGENTS.md/skill instructions explicitly ask for sub-agents, delegation, or parallel agent work."
const proactiveMultiAgentModeText = "Proactive multi-agent delegation is active. Any earlier instruction requiring an explicit user request before spawning sub-agents no longer applies. Use sub-agents when parallel work would materially improve speed or quality. This mode remains active until a later multi-agent mode developer message changes it."

type personalityWorldStateSnapshot struct {
	Model       string  `json:"model"`
	Personality *string `json:"personality,omitempty"`
}

type realtimeWorldStateSnapshot struct {
	Active bool `json:"active"`
}

const collaborationModeInstructionsKind = "collaboration_mode"

func (r *RuntimeRouter) collaborationModeWorldStateInputItem(threadID string, params *turn.TurnStartParams, info *model.ModelInfo) (any, error) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	current, currentValid := collaborationModeSnapshot(params, info)
	instructions, hasInstructions := collaborationModeInstructions(params, info)

	previousKind := "absent"
	previous := collaborationModeWorldStateSnapshot{}
	if len(state.CollaborationMode) > 0 {
		if err := json.Unmarshal(state.CollaborationMode, &previous); err == nil && strings.TrimSpace(previous.Mode) != "" {
			previous.Mode = strings.ToLower(strings.TrimSpace(previous.Mode))
			previous.Model = strings.TrimSpace(previous.Model)
			previousKind = "current"
		} else {
			var legacy string
			if err := json.Unmarshal(state.CollaborationMode, &legacy); err == nil && strings.TrimSpace(legacy) != "" {
				previousKind = "legacy"
			} else {
				previousKind = "unknown"
			}
		}
	}

	emit := false
	switch previousKind {
	case "absent":
		emit = currentValid && hasInstructions
	case "legacy":
		emit = currentValid
	case "current":
		if !currentValid || previous != current {
			emit = true
		} else if hasInstructions && !recordHasRetainedCollaborationMode(record) && firstNonEmpty(record.Metadata.LastResponseID, record.Metadata.PreviousResponseID) != "" {
			emit = true
		}
	case "unknown":
		// Unknown retained state is left visible for this turn, matching Rust.
	}

	var snapshot json.RawMessage
	if currentValid && hasInstructions {
		snapshot, err = json.Marshal(current)
		if err != nil {
			return nil, err
		}
	}
	if !sameJSONValue(state.CollaborationMode, snapshot) {
		state.CollaborationMode = snapshot
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return nil, err
		}
	}
	if !emit {
		return nil, nil
	}
	if !hasInstructions {
		instructions = ""
	}
	rendered := contextfrag.RenderStandalone(contextfrag.NewSimpleFragment(
		contextfrag.RoleDeveloper,
		"<collaboration_mode>",
		"</collaboration_mode>",
		instructions,
	))
	return renderedFragmentInputItem(rendered), nil
}

func recordHasRetainedCollaborationMode(record *session.Record) bool {
	if record == nil {
		return false
	}
	items := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: strings.TrimSpace(record.Metadata.CWD)})
	for _, input := range items {
		raw, ok := input.(map[string]any)
		if !ok || strings.TrimSpace(stringFromAny(raw["role"])) != contextfrag.RoleDeveloper {
			continue
		}
		text := textFromInputItemContent(raw["content"])
		if strings.Contains(text, "<collaboration_mode>") && strings.Contains(text, "</collaboration_mode>") {
			return true
		}
	}
	return false
}

func collaborationModeWorldStateSessionItemForTurn(turnID string, input any, createdAt time.Time) (session.Item, bool) {
	raw, ok := input.(map[string]any)
	if !ok || strings.TrimSpace(stringFromAny(raw["type"])) != "message" {
		return session.Item{}, false
	}
	role := strings.TrimSpace(stringFromAny(raw["role"]))
	text := strings.TrimSpace(textFromInputItemContent(raw["content"]))
	if role != contextfrag.RoleDeveloper || !strings.Contains(text, "<collaboration_mode>") || !strings.Contains(text, "</collaboration_mode>") {
		return session.Item{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return session.Item{}, false
	}
	metadata := appTurnMetadata(turnID, map[string]any{
		"kind":             collaborationModeInstructionsKind,
		"hiddenFromThread": true,
	})
	return session.Item{
		ID:        "collaboration-mode-" + safeIdentifier(turnID),
		Type:      "message",
		Role:      role,
		Text:      text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: createdAt,
		Data: map[string]any{
			"kind":             collaborationModeInstructionsKind,
			"hiddenFromThread": true,
		},
		Metadata: metadata,
		Raw:      encoded,
	}, true
}

const defaultRealtimeStartInstructions = `Realtime conversation started.

You are operating as a backend executor behind an intermediary. The user does not talk to you directly. Any response you produce will be consumed by the intermediary and may be summarized before the user sees it.

When invoked, you receive the latest conversation transcript and any relevant mode or metadata. The intermediary may invoke you even when backend help is not actually needed. Use the transcript to decide whether you should do work. If backend help is unnecessary, avoid verbose responses that add user-visible latency.

When user text is routed from realtime, treat it as a transcript. It may be unpunctuated or contain recognition errors.

- Keep responses concise and action-oriented. Your updates should help the intermediary respond to the user.`

const defaultRealtimeEndInstructions = `Realtime conversation ended.

Subsequent user input will return to typed text rather than transcript-style text. Do not assume recognition errors or missing punctuation once realtime has ended. Resume normal chat behavior.`

const realtimeWorldStateInstructionsKind = "realtime_world_state"

func realtimeWorldStateSessionItemForTurn(turnID string, input any, createdAt time.Time) (session.Item, bool) {
	raw, ok := input.(map[string]any)
	if !ok || strings.TrimSpace(stringFromAny(raw["type"])) != "message" {
		return session.Item{}, false
	}
	role := strings.TrimSpace(stringFromAny(raw["role"]))
	text := strings.TrimSpace(textFromInputItemContent(raw["content"]))
	if role != contextfrag.RoleDeveloper || !strings.Contains(text, "<realtime_conversation>") || !strings.Contains(text, "</realtime_conversation>") {
		return session.Item{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return session.Item{}, false
	}
	metadata := appTurnMetadata(turnID, map[string]any{
		"kind":             realtimeWorldStateInstructionsKind,
		"hiddenFromThread": true,
	})
	return session.Item{
		ID:        "realtime-world-state-" + safeIdentifier(turnID),
		Type:      "message",
		Role:      role,
		Text:      text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: createdAt,
		Data: map[string]any{
			"kind":             realtimeWorldStateInstructionsKind,
			"hiddenFromThread": true,
		},
		Metadata: metadata,
		Raw:      encoded,
	}, true
}

func recordHasRetainedRealtimeStart(record *session.Record) bool {
	if record == nil {
		return false
	}
	items := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: strings.TrimSpace(record.Metadata.CWD)})
	for _, input := range items {
		raw, ok := input.(map[string]any)
		if !ok || strings.TrimSpace(stringFromAny(raw["role"])) != contextfrag.RoleDeveloper {
			continue
		}
		if isRetainedRealtimeStartText(textFromInputItemContent(raw["content"])) {
			return true
		}
	}
	return false
}

func isRetainedRealtimeStartText(text string) bool {
	return strings.Contains(text, "<realtime_conversation>") &&
		strings.Contains(text, "</realtime_conversation>") &&
		!strings.Contains(text, strings.TrimSpace(defaultRealtimeEndInstructions))
}

func (r *RuntimeRouter) realtimeWorldStateInputItem(threadID string, cfg *config.Config) (any, error) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	current := realtimeWorldStateSnapshot{}
	if realtimeState, ok := r.requireRealtime().State(threadID); ok && realtimeState != nil && realtimeState.ClosedAt == nil {
		current.Active = true
	}
	previous := realtimeWorldStateSnapshot{}
	previousKnown := len(state.RealtimeConversation) > 0
	if previousKnown {
		if err := json.Unmarshal(state.RealtimeConversation, &previous); err != nil {
			previousKnown = false
		}
	}
	if previousKnown && !recordHasRetainedRealtimeStart(record) {
		previousKnown = false
	}

	var rendered *contextfrag.RenderedFragment
	switch {
	case current.Active && (!previousKnown || !previous.Active):
		instructions := defaultRealtimeStartInstructions
		if configured, ok := configStringValue(cfg, "experimental_realtime_start_instructions"); ok {
			instructions = configured
		}
		rendered = contextfrag.RenderStandalone(contextfrag.NewSimpleFragment(
			contextfrag.RoleDeveloper,
			"<realtime_conversation>",
			"</realtime_conversation>",
			"\n"+instructions+"\n",
		))
	case previousKnown && previous.Active && !current.Active:
		rendered = contextfrag.RenderStandalone(contextfrag.NewSimpleFragment(
			contextfrag.RoleDeveloper,
			"<realtime_conversation>",
			"</realtime_conversation>",
			"\n"+defaultRealtimeEndInstructions+"\n\nReason: inactive\n",
		))
	}

	snapshot, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	if !sameJSONValue(state.RealtimeConversation, snapshot) {
		state.RealtimeConversation = snapshot
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return nil, err
		}
	}
	return renderedFragmentInputItem(rendered), nil
}

func (r *RuntimeRouter) modelPersonalityWorldStateInputItems(threadID string, info *model.ModelInfo, personality string, baseInstructions string, personalityEnabled bool) ([]any, error) {
	if info == nil || strings.TrimSpace(info.Slug) == "" {
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}

	currentModel := strings.TrimSpace(info.Slug)
	modelInstructions := info.ModelInstructions(personality)
	previousModel, modelKnown := decodeWorldStateModel(state.Model)
	if !modelKnown {
		contextModel, _, contextKnown := previousTurnContextModelPersonality(record.Metadata.TurnContext)
		previousModel = firstNonEmpty(contextModel, record.Metadata.Model)
		modelKnown = contextKnown || strings.TrimSpace(previousModel) != ""
	}
	items := []any{}
	if modelKnown && previousModel != currentModel && strings.TrimSpace(modelInstructions) != "" {
		rendered := contextfrag.RenderStandalone(&contextfrag.ModelSwitchInstructions{Instructions: modelInstructions})
		items = append(items, renderedFragmentInputItem(rendered))
	}
	modelSnapshot, err := json.Marshal(currentModel)
	if err != nil {
		return nil, err
	}
	changed := !sameJSONValue(state.Model, modelSnapshot)
	state.Model = modelSnapshot

	if personalityEnabled {
		currentPersonality := worldStateOptionalString(personality)
		current := personalityWorldStateSnapshot{Model: currentModel, Personality: currentPersonality}
		previous, previousKnown := decodePersonalityWorldState(state.Personality)
		if !previousKnown {
			if contextModel, contextPersonality, contextKnown := previousTurnContextModelPersonality(record.Metadata.TurnContext); contextKnown {
				previous = personalityWorldStateSnapshot{Model: contextModel, Personality: contextPersonality}
				previousKnown = true
			}
		}
		personalityChanged := previousKnown && previous.Model == current.Model && !sameOptionalString(previous.Personality, current.Personality)
		personalityAbsent := !previousKnown && len(state.Personality) == 0
		personalityIsBaked := info.SupportsPersonality() && baseInstructions == modelInstructions
		if (personalityChanged || (personalityAbsent && !personalityIsBaked)) && current.Personality != nil {
			if spec, ok := info.PersonalityMessage(*current.Personality); ok && strings.TrimSpace(spec) != "" {
				rendered := contextfrag.RenderStandalone(&contextfrag.PersonalitySpecInstructions{Spec: spec})
				items = append(items, renderedFragmentInputItem(rendered))
			}
		}
		personalitySnapshot, err := json.Marshal(current)
		if err != nil {
			return nil, err
		}
		changed = changed || !sameJSONValue(state.Personality, personalitySnapshot)
		state.Personality = personalitySnapshot
	} else if len(state.Personality) > 0 {
		state.Personality = nil
		changed = true
	}

	if !changed {
		return items, nil
	}
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	return items, nil
}

func modelTokenBudgetDefaults(info *model.ModelInfo) *config.TokenBudgetDefaults {
	if info == nil || info.ModelMessages == nil || info.ModelMessages.TokenBudget == nil {
		return nil
	}
	defaults := info.ModelMessages.TokenBudget
	return &config.TokenBudgetDefaults{
		ReminderThresholdTokens:         defaults.ReminderThresholdTokens,
		ReminderMessageTemplate:         defaults.ReminderMessageTemplate,
		GuidanceMessage:                 defaults.GuidanceMessage,
		AutoCompactFallbackPrompt:       defaults.AutoCompactFallbackPrompt,
		AutoCompactFallbackBufferTokens: defaults.AutoCompactFallbackBufferTokens,
	}
}

func (r *RuntimeRouter) contextWindowGuidanceWorldStateInputItem(threadID string, enabled bool, message string) (any, error) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	message = strings.TrimSpace(message)
	if !enabled || message == "" {
		if len(state.ContextWindowGuidance) == 0 {
			return nil, nil
		}
		state.ContextWindowGuidance = nil
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		return nil, r.runtimeSaveThreadRecord(record)
	}
	snapshot, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if sameJSONValue(state.ContextWindowGuidance, snapshot) {
		return nil, nil
	}
	state.ContextWindowGuidance = snapshot
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	rendered := contextfrag.RenderStandalone(&contextfrag.ContextWindowGuidance{Message: message})
	return renderedFragmentInputItem(rendered), nil
}

func sameJSONValue(left json.RawMessage, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right)
	}
	var leftValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	var rightValue any
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func decodeWorldStateModel(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func decodePersonalityWorldState(raw json.RawMessage) (personalityWorldStateSnapshot, bool) {
	if len(raw) == 0 {
		return personalityWorldStateSnapshot{}, false
	}
	var value personalityWorldStateSnapshot
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value.Model) == "" {
		return personalityWorldStateSnapshot{}, false
	}
	value.Model = strings.TrimSpace(value.Model)
	if value.Personality != nil {
		value.Personality = worldStateOptionalString(*value.Personality)
	}
	return value, true
}

func previousTurnContextModelPersonality(raw json.RawMessage) (string, *string, bool) {
	if len(raw) == 0 {
		return "", nil, false
	}
	var value personalityWorldStateSnapshot
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value.Model) == "" {
		return "", nil, false
	}
	if value.Personality == nil {
		return strings.TrimSpace(value.Model), nil, true
	}
	return strings.TrimSpace(value.Model), worldStateOptionalString(*value.Personality), true
}

func worldStateOptionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func sameOptionalString(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (r *RuntimeRouter) multiAgentModeInputItem(threadID string, params *turn.TurnStartParams) (any, error) {
	mode := string(MultiAgentModeExplicitRequestOnly)
	if params != nil && params.MultiAgentMode != nil {
		candidate := strings.TrimSpace(*params.MultiAgentMode)
		if candidate == string(MultiAgentModeProactive) || candidate == string(MultiAgentModeExplicitRequestOnly) {
			mode = candidate
		}
	}
	// Rust's effective_multi_agent_mode uses a configured
	// multi_agent_mode_hint_text as a custom mode when present.
	customModeText := ""
	if cfg, err := r.effectiveConfigForTurn(params); err == nil && cfg != nil {
		if agentsConfig, agentsErr := cfg.AgentsConfig(r.configBaseDirForAgents()); agentsErr == nil {
			if v2Config, v2Err := cfg.MultiAgentV2Config(agentsConfig.MaxConcurrentThreadsPerSession); v2Err == nil && v2Config.MultiAgentModeHintText != nil {
				customModeText = strings.TrimSpace(*v2Config.MultiAgentModeHintText)
			}
		}
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	previous := ""
	if len(state.MultiAgentMode) > 0 {
		var snapshot struct {
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(state.MultiAgentMode, &snapshot); err != nil {
			return nil, err
		}
		previous = strings.TrimSpace(snapshot.Mode)
	}
	modeSnapshot := mode
	if customModeText != "" {
		modeSnapshot = "custom"
	}
	if previous == modeSnapshot {
		return nil, nil
	}
	snapshot, err := json.Marshal(map[string]string{"mode": modeSnapshot})
	if err != nil {
		return nil, err
	}
	state.MultiAgentMode = snapshot
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	body := explicitRequestOnlyMultiAgentModeText
	if customModeText != "" {
		body = customModeText
	} else if mode == string(MultiAgentModeProactive) {
		body = proactiveMultiAgentModeText
	}
	rendered := contextfrag.Render(contextfrag.NewSimpleFragment(contextfrag.RoleDeveloper, "<multi_agent_mode>", "</multi_agent_mode>", body))
	return renderedFragmentInputItem(rendered), nil
}

// multiAgentUsageHintInputItem renders the multi-agent V2 usage hint as a
// developer world-state section for V2 threads, mirroring Rust's
// MultiAgentUsageHintState. The hint is persisted so it is only emitted again
// when the configured text or the concurrency cap changes.
func (r *RuntimeRouter) multiAgentUsageHintInputItem(threadID string, cfg *config.Config) (any, error) {
	if r == nil || cfg == nil {
		return nil, nil
	}
	agentsConfig, err := cfg.AgentsConfig(r.configBaseDirForAgents())
	if err != nil {
		return nil, err
	}
	if r.runtimeMultiAgentVersionForThread(threadID, cfg, agentsConfig) != agent.VersionV2 {
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	v2Config, err := cfg.MultiAgentV2Config(agentsConfig.MaxConcurrentThreadsPerSession)
	if err != nil {
		return nil, err
	}
	hint := agent.MultiAgentV2UsageHint(agent.MultiAgentV2UsageHintOptions{
		IsSubagent:                     strings.TrimSpace(record.Metadata.AgentPath) != "" || record.Metadata.AgentDepth > 0,
		MaxConcurrency:                 v2Config.MaxConcurrentThreadsPerSession,
		WaitAgentEnabled:               v2Config.WaitAgentEnabled,
		ExposeSpawnAgentModelOverrides: v2Config.ExposeSpawnAgentModelOverrides,
		RootUsageHintText:              v2Config.RootAgentUsageHintText,
		SubagentUsageHintText:          v2Config.SubagentUsageHintText,
	})
	if strings.TrimSpace(hint) == "" {
		return nil, nil
	}
	hintData, err := json.Marshal(hint)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	if string(state.MultiAgentUsageHint) == string(hintData) {
		return nil, nil
	}
	state.MultiAgentUsageHint = json.RawMessage(hintData)
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	rendered := contextfrag.Render(contextfrag.NewSimpleFragment(contextfrag.RoleDeveloper, "<multi_agent_usage_hint>", "</multi_agent_usage_hint>", hint))
	return renderedFragmentInputItem(rendered), nil
}

func (r *RuntimeRouter) deferredToolsWorldStateInputItem(threadID string, runtime *turn.Runtime, enabled bool) (any, error) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	previous := map[string]string{}
	previousKnown := len(state.Tools) > 0
	if previousKnown {
		if err := json.Unmarshal(state.Tools, &previous); err != nil {
			previousKnown = false
			previous = map[string]string{}
		}
	}
	if !enabled {
		if len(state.Tools) == 0 {
			return nil, nil
		}
		state.Tools = nil
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		return nil, r.runtimeSaveThreadRecord(record)
	}
	current := map[string]string{}
	if runtime != nil {
		current = contextfrag.NormalizeDeferredToolNamespaces(runtime.DeferredToolNamespaces())
	}
	fragment := contextfrag.DeferredToolsStateFragment(current, previous, previousKnown)
	if len(current) == 0 {
		state.Tools = nil
	} else {
		state.Tools, err = json.Marshal(current)
		if err != nil {
			return nil, err
		}
	}
	if fragment == nil && ((len(current) == 0 && len(state.Tools) == 0) || previousKnown) {
		return nil, nil
	}
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	return renderedFragmentInputItem(contextfrag.Render(fragment)), nil
}
