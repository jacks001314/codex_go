package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/internal/applypatch"
	"codex_go/internal/apps"
	"codex_go/internal/auth"
	"codex_go/internal/codexapi"
	"codex_go/internal/compact"
	"codex_go/internal/config"
	contextfrag "codex_go/internal/context"
	"codex_go/internal/eventmap"
	"codex_go/internal/features"
	"codex_go/internal/install"
	"codex_go/internal/mcp"
	"codex_go/internal/model"
	"codex_go/internal/plugin"
	promptctx "codex_go/internal/prompt"
	"codex_go/internal/rollout"
	"codex_go/internal/runtimeutil"
	"codex_go/internal/sandbox"
	"codex_go/internal/session"
	"codex_go/internal/telemetry"
	"codex_go/internal/tool"
	"codex_go/internal/turn"
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

func activeTurnDiffKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func (r *RuntimeRouter) responsesStreamHandler(threadID string, turnID string, params *turn.TurnStartParams) model.ResponsesStreamHandler {
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
	activeReasoningItemID     string
	planMode                  bool
	planItemID                string
	planParser                map[string]*proposedPlanStreamParser
	planStarted               bool
	startedAgentItems         map[string]bool
	experimentalRawEvents     bool
	applyPatchStreamingEvents bool
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
	if event.Item != nil && event.Item.Type == "reasoning" {
		s.activeReasoningItemID = itemID
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

func streamAgentItemID(item *model.AgentItem) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(item.ID, item.CallID)
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

func collaborationModeInstructionsInputItem(params *turn.TurnStartParams) any {
	instructions := collaborationModeDeveloperInstructions(params)
	if strings.TrimSpace(instructions) == "" {
		return nil
	}
	rendered := contextfrag.Render(contextfrag.NewSimpleFragment(contextfrag.RoleDeveloper, "<collaboration_mode>", "</collaboration_mode>", instructions))
	return renderedFragmentInputItem(rendered)
}

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
	case model.ResponsesStreamEventOutputAdded:
		state.rememberOutputItem(event)
		state.rememberTool(event)
		item := threadItemFromStreamAgentItem(event.Item, turnID, event.ResponseID, time.Now().UTC())
		if item.ID == "" {
			return
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
		itemID := firstNonEmpty(event.ItemID, "agent-message-"+safeIdentifier(turnID))
		if event.Delta == "" {
			return
		}
		if state.planMode {
			r.notifyPlanModeAgentDelta(threadID, turnID, itemID, event.Delta, state)
			return
		}
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
	}
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
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnStarted(threadID))
	r.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: appTurn})
	_ = r.appendRuntimeTurnStarted(threadID, turnID, startedAt)
	promptPersisted := false

	runConfig, err := r.appTurnConfig(ctx, threadID, turnID, params, startedAtMS)
	if err != nil {
		r.clearActiveRuntimeTurn(threadID, turnID)
		r.finishTurnWithError(threadID, turnID, startedAtMS, err)
		return
	}
	r.updateActiveRuntimeTurnAnalytics(threadID, turnID, connectionID, runConfig)
	promptPersisted = r.persistRuntimeTurnPrompt(threadID, turnID, params, startedAt)
	agentPrompt := promptFromTurnStart(params)
	inputItems := append([]any(nil), runConfig.InputItems...)
	if turnStartUsesStructuredUserInput(params) {
		if item := userMessageInputItemFromTurnUserInputs(params.Prompt, params.Input); item != nil {
			inputItems = append(inputItems, item)
			agentPrompt = ""
		}
	}
	result, err := runtime.Run(ctx, &turn.AgentLoopRequest{
		Prompt:               agentPrompt,
		Instructions:         runConfig.Instructions,
		InputItems:           inputItems,
		HostedTools:          append([]any(nil), runConfig.HostedTools...),
		SteerMailbox:         r.requireSteerMailbox(),
		Model:                runConfig.Model,
		ProviderID:           runConfig.ProviderID,
		TaskKind:             model.AgentTaskRegular,
		ThreadID:             threadID,
		TurnID:               turnID,
		Originator:           runConfig.Originator,
		Store:                runConfig.Store,
		PreviousResponseID:   runConfig.PreviousResponseID,
		ParallelToolCalls:    runConfig.ParallelToolCalls,
		ReasoningEffort:      runConfig.ReasoningEffort,
		ReasoningSummary:     runConfig.ReasoningSummary,
		ModelVerbosity:       runConfig.ModelVerbosity,
		IncludeTimingMetrics: runConfig.IncludeTimingMetrics,
		BetaFeaturesHeader:   runConfig.BetaFeaturesHeader,
		ItemIDsEnabled:       runConfig.ItemIDsEnabled,
		PromptCacheKey:       runConfig.PromptCacheKey,
		ServiceTier:          runConfig.ServiceTier,
		ClientMetadata:       cloneStringMap(runConfig.ClientMetadata),
		AttestationProvider:  runConfig.AttestationProvider,
		OutputSchema:         params.OutputSchema,
		PostToolInputItems:   runConfig.PostToolInputItems,
		OnToolStarted:        r.runtimeToolStartedNotifier(threadID, turnID, firstNonEmpty(params.CWD, r.services.DefaultCWD)),
	})
	if err != nil {
		steerCount := r.activeRuntimeTurnSteerCount(threadID, turnID)
		if ctx.Err() != nil {
			r.clearActiveRuntimeTurn(threadID, turnID)
			return
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
	if !r.consumeCompletedRuntimeTurn(threadID, turnID) {
		return
	}
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.notifyTurnPlanUpdates(threadID, turnID, result)
	_ = r.persistLastResponseID(threadID, result)
	items := append([]session.Item(nil), runConfig.SessionItems...)
	if runConfig.ExtraSessionItems != nil {
		items = append(items, runConfig.ExtraSessionItems()...)
	}
	items = append(items, r.sessionItemsForTurn(turnID, params, result, startedAt)...)
	if promptPersisted {
		items = withoutRuntimeUserPromptItem(items, turnID)
	}
	if len(items) > 0 {
		if _, err := r.runtimeAppendItems(session.ThreadID(threadID), items); err != nil {
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
	threadItems := make([]ThreadItem, 0, len(items))
	for _, item := range items {
		if sessionItemIsHiddenContextInstruction(&item) {
			continue
		}
		if item.Type == "tool_output" {
			r.notifyTurnDiffFromSessionItem(threadID, turnID, &item)
		}
		threadItem := BuildThreadItem(item)
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
		if threadItem.Type == "agent_message" && strings.TrimSpace(threadItem.Text) != "" {
			r.notify(NotificationAgentMessageDelta, &AgentMessageDeltaNotification{
				ThreadID: threadID,
				TurnID:   turnID,
				ItemID:   threadItem.ID,
				Delta:    threadItem.Text,
			})
		}
	}
	if usage := tokenUsageFromAgentLoopResult(result); usage != nil {
		usage.ModelContextWindow = r.modelContextWindowForModel(runConfig.Model)
		status, statusErr := r.persistCompactTokenStatus(threadID, result.Usage)
		r.notify(NotificationThreadTokenUsageUpdated, &ThreadTokenUsageUpdatedNotification{
			ThreadID:   threadID,
			TurnID:     turnID,
			TokenUsage: *usage,
		})
		if statusErr == nil && status != nil && status.ShouldCompact {
			if notification, err := r.autoCompactThreadAfterTurn(threadID, turnID, connectionID, status); err == nil && notification != nil {
				r.notify(NotificationContextCompacted, notification)
			}
		}
	}
	completedAt := time.Now().UTC()
	completedAtUnix := completedAt.Unix()
	durationMS := completedAt.UnixMilli() - startedAtMS
	_ = r.appendRuntimeTurnComplete(threadID, turnID, completedAt, durationMS)
	r.completeTurnRecord(threadID, turnID, TurnStatusCompleted)
	completedTurn := completedTurnNotificationTurn(turnID, TurnStatusCompleted, nil, &record.StartedAt, &completedAtUnix, &durationMS)
	r.notify(NotificationTurnCompleted, &TurnCompletedNotification{ThreadID: threadID, Turn: completedTurn})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnCompleted(threadID))
	r.emitCodexTurnAnalyticsEvent(ctx, connectionID, params, record, runConfig, result, TurnStatusCompleted, startedAt, completedAt, durationMS, steerCount, nil, nil, nil)
	r.emitAcceptedLineFingerprintsAnalyticsEvent(ctx, threadID, turnID, runConfig, completedAt)
	r.clearActiveDiffTracker(threadID, turnID)
}

func shouldNotifyRuntimeItemCompleted(item ThreadItem) bool {
	switch threadItemWireType(&item) {
	case "commandExecution":
		return threadItemCommandStatus(&item) != CommandExecutionInProgress
	case "fileChange":
		return threadItemFileChangeStatus(&item) != PatchApplyInProgress
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

func (r *RuntimeRouter) runtimeToolStartedNotifier(threadID string, turnID string, cwd string) turn.ToolStartedCallback {
	return func(ctx context.Context, invocation *tool.Invocation, startedAt time.Time) {
		if r == nil || invocation == nil {
			return
		}
		if item, ok := commandExecutionStartedThreadItem(invocation, turnID, cwd, startedAt); ok {
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

func commandExecutionStartedThreadItem(invocation *tool.Invocation, turnID string, cwd string, startedAt time.Time) (ThreadItem, bool) {
	if invocation == nil || invocation.ToolName.Key() != tool.DefaultExecCommandToolName || invocation.Payload.Kind != tool.PayloadFunction {
		return ThreadItem{}, false
	}
	var args tool.ExecCommandArgs
	if strings.TrimSpace(invocation.Payload.Arguments) != "" {
		if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &args); err != nil {
			return ThreadItem{}, false
		}
	}
	command := strings.TrimSpace(args.Cmd)
	if command == "" {
		return ThreadItem{}, false
	}
	itemCWD := firstNonEmpty(args.CWD, args.Workdir, cwd)
	if itemCWD != "" {
		if abs, err := filepath.Abs(itemCWD); err == nil {
			itemCWD = abs
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
			"commandActions": []map[string]any{{"type": "unknown", "command": command}},
		},
	}, true
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
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	if r.active == nil {
		r.active = map[string]*activeRuntimeTurn{}
	}
	if active := r.active[threadID]; active != nil {
		return fmt.Errorf("%w: thread %s already has active turn %s", session.ErrConflict, threadID, active.TurnID)
	}
	r.active[threadID] = &activeRuntimeTurn{ThreadID: threadID}
	return nil
}

func (r *RuntimeRouter) registerActiveRuntimeTurn(threadID string, turnID string, cancel context.CancelFunc, startedAtMS int64, params *turn.TurnStartParams) error {
	if r == nil {
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	if r.active == nil {
		r.active = map[string]*activeRuntimeTurn{}
	}
	if active := r.active[threadID]; active != nil {
		if active.TurnID == "" {
			active.TurnID = turnID
			active.Cancel = cancel
			active.StartedAtMS = startedAtMS
			active.Params = cloneTurnStartParams(params)
			r.ensureActiveDiffTrackerLocked(threadID, turnID)
			return nil
		}
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("%w: thread %s already has active turn %s", session.ErrConflict, threadID, active.TurnID)
	}
	r.active[threadID] = &activeRuntimeTurn{
		ThreadID:    threadID,
		TurnID:      turnID,
		Cancel:      cancel,
		StartedAtMS: startedAtMS,
		Params:      cloneTurnStartParams(params),
	}
	r.ensureActiveDiffTrackerLocked(threadID, turnID)
	return nil
}

func (r *RuntimeRouter) updateActiveRuntimeTurnAnalytics(threadID string, turnID string, connectionID string, runConfig *appTurnRunConfig) {
	if r == nil {
		return
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	active := r.active[threadID]
	if active == nil || active.TurnID != turnID {
		return
	}
	if strings.TrimSpace(connectionID) != "" {
		active.ConnectionID = normalizeConnectionID(connectionID)
	}
	active.RunConfig = runConfig
}

func (r *RuntimeRouter) noteAcceptedTurnSteer(threadID string, turnID string) {
	if r == nil {
		return
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	active := r.active[threadID]
	if active == nil || active.TurnID != turnID {
		return
	}
	active.SteerCount++
}

func (r *RuntimeRouter) activeRuntimeTurnSteerCount(threadID string, turnID string) int {
	if r == nil {
		return 0
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	active := r.active[threadID]
	if active == nil || active.TurnID != turnID {
		return 0
	}
	return active.SteerCount
}

func (r *RuntimeRouter) consumeCompletedRuntimeTurn(threadID string, turnID string) bool {
	if r == nil {
		return false
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	active := r.active[threadID]
	if active == nil || active.TurnID != turnID {
		return false
	}
	delete(r.active, threadID)
	return true
}

func (r *RuntimeRouter) cancelActiveRuntimeTurn(threadID string, turnID string) (*activeRuntimeTurn, bool) {
	if r == nil {
		return nil, false
	}
	r.turnsMu.Lock()
	active := r.active[threadID]
	if active == nil || active.TurnID != turnID {
		r.turnsMu.Unlock()
		return nil, false
	}
	delete(r.active, threadID)
	delete(r.diffs, activeTurnDiffKey(threadID, turnID))
	r.turnsMu.Unlock()
	if active.Cancel != nil {
		active.Cancel()
	}
	return active, true
}

func (r *RuntimeRouter) clearActiveRuntimeTurn(threadID string, turnID string) {
	if r == nil {
		return
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	active := r.active[threadID]
	if active == nil || active.TurnID != turnID {
		return
	}
	delete(r.active, threadID)
	delete(r.diffs, activeTurnDiffKey(threadID, turnID))
}

func (r *RuntimeRouter) ensureActiveDiffTrackerLocked(threadID string, turnID string) *runtimeutil.DiffTracker {
	if r == nil {
		return nil
	}
	if r.diffs == nil {
		r.diffs = map[string]*runtimeutil.DiffTracker{}
	}
	key := activeTurnDiffKey(threadID, turnID)
	tracker := r.diffs[key]
	if tracker == nil {
		tracker = runtimeutil.NewDiffTracker()
		r.diffs[key] = tracker
	}
	return tracker
}

func (r *RuntimeRouter) activeDiffTracker(threadID string, turnID string) *runtimeutil.DiffTracker {
	if r == nil {
		return nil
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	return r.ensureActiveDiffTrackerLocked(threadID, turnID)
}

func (r *RuntimeRouter) activeUnifiedDiffSnapshot(threadID string, turnID string) string {
	if r == nil {
		return ""
	}
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	tracker := r.diffs[activeTurnDiffKey(threadID, turnID)]
	if tracker == nil {
		return ""
	}
	diff := tracker.UnifiedDiff()
	if diff == nil {
		return ""
	}
	return *diff
}

func (r *RuntimeRouter) clearActiveDiffTracker(threadID string, turnID string) {
	if r == nil {
		return
	}
	r.turnsMu.Lock()
	delete(r.diffs, activeTurnDiffKey(threadID, turnID))
	r.turnsMu.Unlock()
	r.clearToolItemReviewSummaries(threadID, turnID)
}

func (r *RuntimeRouter) hasRuntimeThreadStore() bool {
	return r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil
}

func (r *RuntimeRouter) appendRuntimeRollout(threadID string, items []session.Item, now time.Time) error {
	recorder, err := r.resumeRuntimeRollout(threadID)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	return rollout.AppendSessionItems(recorder, items, now)
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
	return true
}

func (r *RuntimeRouter) appendRuntimeTurnStarted(threadID string, turnID string, startedAt time.Time) error {
	recorder, err := r.resumeRuntimeRollout(threadID)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	return recorder.AppendTurnStarted(turnID, startedAt)
}

func (r *RuntimeRouter) appendRuntimeTurnComplete(threadID string, turnID string, completedAt time.Time, durationMS int64) error {
	recorder, err := r.resumeRuntimeRollout(threadID)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	return recorder.AppendTurnComplete(turnID, completedAt, durationMS)
}

func (r *RuntimeRouter) appendRuntimeTurnError(threadID string, message string, now time.Time) error {
	recorder, err := r.resumeRuntimeRollout(threadID)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	return recorder.AppendTurnError(message, now)
}

func (r *RuntimeRouter) appendRuntimeTurnAborted(threadID string, turnID string, reason string, completedAt time.Time, durationMS int64) error {
	recorder, err := r.resumeRuntimeRollout(threadID)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	return recorder.AppendTurnAborted(turnID, reason, completedAt, durationMS)
}

func (r *RuntimeRouter) resumeRuntimeRollout(threadID string) (*rollout.Recorder, error) {
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false); ok {
		return nil, nil
	}
	codexHome := r.codexHomeForRollout()
	if codexHome == "" {
		return nil, nil
	}
	var record *session.Record
	var readErr error
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		record, readErr = r.services.ThreadRouter.store.Read(session.ThreadID(threadID), true, true)
	}
	path, err := rollout.FindThreadPath(codexHome, threadID, false)
	if err == nil {
		if recorder, replaced, replaceErr := r.replaceRuntimeSeedRollout(record, path); replaceErr != nil || replaced {
			return recorder, replaceErr
		}
		return rollout.Resume(path)
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
	return rollout.Resume(path)
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
	recorder, err := r.resumeRuntimeRollout(threadID)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	items := make([]rollout.Item, 0, len(replacement))
	for i := range replacement {
		item := rollout.ItemFromSessionItem(&replacement[i])
		if item == nil {
			continue
		}
		items = append(items, *item)
	}
	return recorder.AppendCompacted(message, items, now)
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
	ConnectionID             string
	Params                   *turn.TurnStartParams
	RunConfig                *appTurnRunConfig
	Result                   *turn.AgentLoopResult
	SteerCount               int
	TurnError                CodexErrorInfo
	CodexErrorKind           *string
	CodexErrorHTTPStatusCode *uint16
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
	_ = r.appendRuntimeTurnComplete(threadID, turnID, now, durationMS)
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.completeTurnRecord(threadID, turnID, TurnStatusFailed)
	r.notify(NotificationError, &ErrorNotification{
		Error:     *appErr,
		WillRetry: false,
		ThreadID:  threadID,
		TurnID:    turnID,
	})
	r.notify(NotificationTurnCompleted, &TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     completedTurnNotificationTurn(turnID, TurnStatusFailed, appErr, nil, &completedAt, &durationMS),
	})
	r.notifyThreadStatus(r.requireThreadStatus().NoteSystemError(threadID))
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
	now := time.Now().UTC()
	completedAt := now.Unix()
	durationMS := now.UnixMilli() - startedAtMS
	r.persistInterruptedTurnMarker(threadID, turnID, now)
	_ = r.appendRuntimeTurnAborted(threadID, turnID, "interrupted", now, durationMS)
	r.requireSteerMailbox().Clear(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	r.completeTurnRecord(threadID, turnID, TurnStatusInterrupted)
	r.notify(NotificationTurnCompleted, &TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     completedTurnNotificationTurn(turnID, TurnStatusInterrupted, nil, nil, &completedAt, &durationMS),
	})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnInterrupted(threadID))
	r.emitTurnCompletionAnalytics(context.Background(), analytics, turnID, TurnStatusInterrupted, startedAtMS, now, durationMS)
	r.clearActiveDiffTracker(threadID, turnID)
}

func (r *RuntimeRouter) emitTurnCompletionAnalytics(ctx context.Context, analytics *turnCompletionAnalyticsContext, turnID string, status TurnStatus, startedAtMS int64, completedAt time.Time, durationMS int64) {
	if r == nil || analytics == nil || analytics.Params == nil || analytics.RunConfig == nil {
		return
	}
	startedAt := time.UnixMilli(startedAtMS).UTC()
	record := &turn.TurnRecord{ID: turnID}
	r.emitCodexTurnAnalyticsEvent(ctx, analytics.ConnectionID, analytics.Params, record, analytics.RunConfig, analytics.Result, status, startedAt, completedAt, durationMS, analytics.SteerCount, analytics.TurnError, analytics.CodexErrorKind, analytics.CodexErrorHTTPStatusCode)
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
	status := uint16PtrFromHTTPStatus(err.Status)
	fields := func(info CodexErrorInfo, kind string) turnAnalyticsErrorFields {
		return turnAnalyticsErrorFields{TurnError: info, CodexErrorKind: stringPtrIfNotEmpty(kind), HTTPStatusCode: status}
	}
	switch err.Kind {
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
		return turnAnalyticsErrorFieldsFromAPIStatus(err.Status)
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
	if usage.InputTokens == 0 && usage.CachedInputTokens == 0 && usage.OutputTokens == 0 && usage.ReasoningOutputTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}
	return &TokenUsage{
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		TotalTokens:           model.AgentUsageTotalTokens(usage),
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
	_, err := r.services.ThreadRouter.store.UpdateMetadata(session.ThreadID(threadID), &session.MetadataPatch{
		LastResponseID: stringPointerIfNotEmpty(responseID),
	}, true)
	return err
}

func (r *RuntimeRouter) persistCompactTokenStatus(threadID string, usage model.AgentUsage) (*compact.TokenStatus, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || strings.TrimSpace(threadID) == "" {
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return nil, err
	}
	extra := cloneAnyMap(record.Metadata.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	status := compact.Evaluate(compact.Policy{
		Enabled:    true,
		TokenLimit: compactTokenLimitFromMetadata(extra),
	}, int(model.AgentUsageTotalTokens(usage)))
	extra["token_status"] = compactTokenStatusMap(status)
	extra["last_token_usage"] = tokenUsageMetadataMap(usage)
	if runtimeRecordEphemeral(record) {
		record.Metadata.Extra = extra
		r.saveEphemeralThreadRecord(record)
		return &status, nil
	}
	_, err = r.services.ThreadRouter.store.UpdateMetadata(session.ThreadID(threadID), &session.MetadataPatch{Extra: extra}, true)
	if err != nil {
		return nil, err
	}
	return &status, nil
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

type runtimeCompactRequest struct {
	ThreadID                  string
	TurnID                    string
	ConnectionID              string
	Trigger                   compact.Trigger
	Reason                    compact.Reason
	Phase                     compact.Phase
	Prompt                    string
	ActiveContextTokensBefore int64
}

func (r *RuntimeRouter) compactThread(ctx context.Context, params *runtimeCompactRequest) (*ContextCompactedNotification, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil || record == nil {
		return nil, err
	}
	startedAt := time.Now().UTC()
	request := &compact.Request{
		ThreadID:  strings.TrimSpace(params.ThreadID),
		TurnID:    strings.TrimSpace(params.TurnID),
		Trigger:   params.Trigger,
		Reason:    params.Reason,
		Phase:     params.Phase,
		Prompt:    strings.TrimSpace(params.Prompt),
		History:   compactItemsFromSessionItems(record.Items),
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
	hookCtx := r.compactHookContext(record, request)
	initialContext, err := r.runPreCompactHooks(ctx, hookCtx)
	if err != nil {
		r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, nil, err, startedAt, time.Now().UTC(), params.ActiveContextTokensBefore)
		return nil, err
	}
	compacted, err := compact.CompactRemotely(ctx, request, &compact.RemoteOptions{
		Runner:               r.compactRunnerForRecord(record),
		MaxSummaryChars:      4000,
		InitialContext:       initialContext,
		InjectBeforeLastUser: true,
		FallbackToLocal:      true,
	})
	if err != nil {
		r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, nil, err, startedAt, time.Now().UTC(), params.ActiveContextTokensBefore)
		return nil, err
	}
	if compacted == nil {
		return nil, nil
	}
	if err := r.runPostCompactHooks(ctx, hookCtx); err != nil {
		r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, compacted, err, startedAt, time.Now().UTC(), params.ActiveContextTokensBefore)
		return nil, err
	}
	now := time.Now().UTC()
	record.Items = sessionItemsFromCompactItems(compacted.NewHistory, now)
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
		return nil, err
	}
	_ = r.appendRuntimeCompacted(request.ThreadID, compacted.Summary, record.Items, now)
	r.emitCompactionAnalyticsEvent(ctx, params.ConnectionID, record, request, compacted, nil, startedAt, now, params.ActiveContextTokensBefore)
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
	}, nil
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
	if r.services.Agent == nil || record == nil {
		return nil
	}
	if _, ok := r.services.Agent.(*model.ResponsesAgentRunner); !ok {
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
		agent:      r.services.Agent,
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
	return status
}

func tokenUsageMetadataMap(usage model.AgentUsage) map[string]any {
	return map[string]any{
		"inputTokens":           usage.InputTokens,
		"cachedInputTokens":     usage.CachedInputTokens,
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
				"outputTokens":          tokenUsage.OutputTokens,
				"reasoningOutputTokens": tokenUsage.ReasoningOutputTokens,
				"totalTokens":           tokenUsage.TotalTokens,
			}
			metadata["usage"] = map[string]any{
				"input_tokens":            tokenUsage.InputTokens,
				"cached_input_tokens":     tokenUsage.CachedInputTokens,
				"output_tokens":           tokenUsage.OutputTokens,
				"reasoning_output_tokens": tokenUsage.ReasoningOutputTokens,
				"total_tokens":            tokenUsage.TotalTokens,
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
	return items
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
	if execution.Output != nil {
		metadata["success"] = execution.Output.Success
		if strings.TrimSpace(execution.Output.Error) != "" {
			metadata["error"] = execution.Output.Error
		}
	}
	return session.Item{
		ID:        callID,
		Type:      "webSearch",
		Text:      query,
		CreatedAt: itemCreatedAt,
		Data: map[string]any{
			"query":  query,
			"action": action,
		},
		Metadata: metadata,
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
	Model                string
	ProviderID           string
	Instructions         string
	Originator           string
	ClientMetadata       map[string]string
	SessionID            string
	ThreadSource         string
	SubagentSource       string
	ParentThreadID       string
	Ephemeral            bool
	WorkspaceKind        string
	NumInputImages       int
	IsFirstTurn          bool
	ApprovalPolicy       string
	ApprovalsReviewer    string
	SandboxPolicy        string
	SandboxNetworkAccess bool
	CollaborationMode    string
	Personality          string
	InputItems           []any
	HostedTools          []any
	SessionItems         []session.Item
	ExtraSessionItems    func() []session.Item
	PostToolInputItems   turn.ToolPostExecutionInputItems
	PreviousResponseID   string
	ParallelToolCalls    bool
	ReasoningEffort      string
	ReasoningSummary     string
	ModelVerbosity       string
	IncludeTimingMetrics bool
	BetaFeaturesHeader   string
	ItemIDsEnabled       bool
	PromptCacheKey       string
	ServiceTier          string
	Store                bool
	AttestationProvider  codexapi.AttestationProvider
}

type responsesMetadataLineage struct {
	SessionID          string
	ForkedFromThreadID string
	ParentThreadID     string
	SubagentHeader     string
	SubagentKind       string
	ThreadSource       string
}

func (r *RuntimeRouter) appTurnConfig(ctx context.Context, threadID string, turnID string, params *turn.TurnStartParams, startedAtMS int64) (*appTurnRunConfig, error) {
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
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
	if personalitySpec := explicitPersonalitySpecInstructions(modelInfo, params); personalitySpec != "" {
		instructions = strings.Join(nonEmpty([]string{personalitySpec, instructions}), "\n\n")
	}
	historyItems, previousResponseID := r.historyInputItemsForTurn(threadID)
	instructions, additionalInputItems := instructionsAndInputItemsWithAdditionalContext(instructions, params.AdditionalContext)
	inputItems := append([]any(nil), historyItems...)
	inputItems = append(inputItems, additionalInputItems...)
	if item := collaborationModeInstructionsInputItem(params); item != nil {
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
	instructions, skillInputItems, postToolInputItems, err = r.instructionsWithSkillsContext(threadID, cfg, params, instructions)
	if err != nil {
		return nil, err
	}
	sessionItems := append([]session.Item(nil), currentTimeSessionItems...)
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
	inputItems = append(inputItems, skillInputItems...)
	extraMetadata := turn.MergeClientMetadata(cfg.ResponsesAPIClientMetadata(), params.ResponsesAPIMetadata)
	serviceTier := r.appServiceTierForTurn(cfg, params, modelProviderConfig.Model)
	hostedTools, err := r.hostedToolsForTurn(params)
	if err != nil {
		return nil, err
	}
	cwd := firstNonEmpty(turnCWD(params), r.services.DefaultCWD)
	permissionProfile, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil {
		return nil, err
	}
	approvalPolicy := turnApprovalPolicyForTurn(cfg, params)
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
	return &appTurnRunConfig{
		Model:                modelProviderConfig.Model,
		ProviderID:           modelProviderConfig.ProviderID,
		Instructions:         instructions,
		Originator:           strings.TrimSpace(params.Originator),
		SessionID:            firstNonEmpty(lineage.SessionID, threadSnapshot.SessionID, threadID),
		ThreadSource:         lineage.ThreadSource,
		SubagentSource:       lineage.SubagentKind,
		ParentThreadID:       lineage.ParentThreadID,
		Ephemeral:            threadSnapshot.Ephemeral,
		WorkspaceKind:        strings.TrimSpace(extraMetadata["workspace_kind"]),
		NumInputImages:       countTurnStartInputImages(params),
		IsFirstTurn:          threadSnapshot.IsFirstTurn,
		ApprovalPolicy:       string(approvalPolicy),
		ApprovalsReviewer:    turnApprovalsReviewerForTurn(cfg, params),
		SandboxPolicy:        analyticsSandboxPolicy(permissionProfile, cwd),
		SandboxNetworkAccess: analyticsSandboxNetworkAccess(permissionProfile),
		CollaborationMode:    analyticsCollaborationMode(params),
		Personality:          analyticsOptionalModeString(personality),
		InputItems:           inputItems,
		HostedTools:          hostedTools,
		SessionItems:         sessionItems,
		ExtraSessionItems:    extraSessionItemsSnapshot,
		PostToolInputItems:   postToolInputItems,
		PreviousResponseID:   previousResponseID,
		ParallelToolCalls:    r.modelSupportsParallelToolCalls(modelProviderConfig.Model),
		ReasoningEffort:      firstNonEmpty(stringPtrValue(params.Effort), stringConfigValue(cfg, "model_reasoning_effort"), stringConfigValue(cfg, "modelReasoningEffort")),
		ReasoningSummary:     stringPtrValue(params.Summary),
		ModelVerbosity:       firstNonEmpty(stringConfigValue(cfg, "model_verbosity"), stringConfigValue(cfg, "modelVerbosity")),
		IncludeTimingMetrics: appIncludeTimingMetrics(cfg),
		BetaFeaturesHeader:   features.ModelClientBetaFeaturesHeader(cfg.FeatureSettings()),
		ItemIDsEnabled:       cfg.FeatureSettings()["item_ids"],
		PromptCacheKey:       threadID,
		ServiceTier:          serviceTier,
		Store:                modelProviderConfig.Store,
		AttestationProvider:  r.appServerAttestationProvider(),
		ClientMetadata: turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
			InstallationID:     installationID,
			SessionID:          firstNonEmpty(lineage.SessionID, threadID),
			ThreadID:           threadID,
			TurnID:             turnID,
			WindowID:           threadID + ":1",
			RequestKind:        codexapi.ClientRequestTurn,
			ForkedFromThreadID: lineage.ForkedFromThreadID,
			ParentThreadID:     lineage.ParentThreadID,
			SubagentHeader:     lineage.SubagentHeader,
			SubagentKind:       lineage.SubagentKind,
			ThreadSource:       lineage.ThreadSource,
			Extra:              extraMetadata,
			StartedAtMS:        startedAtMS,
			UseResponsesLite:   r.modelUsesResponsesLite(modelProviderConfig.Model),
		}),
	}, nil
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
			return value
		}
	}
	value := firstNonEmpty(
		stringConfigValue(cfg, "approvals_reviewer"),
		stringConfigValue(cfg, "approvalsReviewer"),
	)
	if value == "" {
		return "user"
	}
	return value
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

func (r *RuntimeRouter) emitCodexTurnAnalyticsEvent(ctx context.Context, connectionID string, params *turn.TurnStartParams, record *turn.TurnRecord, runConfig *appTurnRunConfig, result *turn.AgentLoopResult, status TurnStatus, startedAt time.Time, completedAt time.Time, durationMS int64, steerCount int, turnError CodexErrorInfo, codexErrorKind *string, codexErrorHTTPStatusCode *uint16) {
	if r == nil || r.services.Analytics == nil || params == nil || record == nil || runConfig == nil {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	event := telemetry.NewCodexTurnEvent(telemetry.CodexTurnEventInput{
		ThreadID:                 params.ThreadID,
		SessionID:                firstNonEmpty(runConfig.SessionID, params.ThreadID),
		TurnID:                   record.ID,
		AppServerClient:          client,
		ThreadOriginator:         runConfig.Originator,
		Runtime:                  telemetry.CurrentRuntimeMetadata(),
		Ephemeral:                runConfig.Ephemeral,
		ThreadSource:             stringPtrIfNotEmpty(runConfig.ThreadSource),
		InitializationMode:       "new",
		SubagentSource:           stringPtrIfNotEmpty(runConfig.SubagentSource),
		ParentThreadID:           stringPtrIfNotEmpty(runConfig.ParentThreadID),
		Model:                    stringPtrIfNotEmpty(runConfig.Model),
		ModelProvider:            runConfig.ProviderID,
		SandboxPolicy:            stringPtrIfNotEmpty(runConfig.SandboxPolicy),
		ReasoningEffort:          stringPtrIfNotEmpty(analyticsOptionalModeString(runConfig.ReasoningEffort)),
		ReasoningSummary:         stringPtrIfNotEmpty(analyticsOptionalModeString(runConfig.ReasoningSummary)),
		ServiceTier:              runConfig.ServiceTier,
		ApprovalPolicy:           firstNonEmpty(runConfig.ApprovalPolicy, string(sandbox.ApprovalOnRequest)),
		ApprovalsReviewer:        firstNonEmpty(runConfig.ApprovalsReviewer, "user"),
		SandboxNetworkAccess:     runConfig.SandboxNetworkAccess,
		CollaborationMode:        stringPtrIfNotEmpty(firstNonEmpty(runConfig.CollaborationMode, "default")),
		Personality:              stringPtrIfNotEmpty(runConfig.Personality),
		WorkspaceKind:            stringPtrIfNotEmpty(runConfig.WorkspaceKind),
		NumInputImages:           runConfig.NumInputImages,
		IsFirstTurn:              runConfig.IsFirstTurn,
		Status:                   stringPtrIfNotEmpty(string(status)),
		TurnError:                turnError,
		CodexErrorKind:           codexErrorKind,
		CodexErrorHTTPStatusCode: codexErrorHTTPStatusCode,
		SteerCount:               intPtrAppserver(steerCount),
		ToolCounts:               analyticsTurnToolCounts(result),
		TokenUsage:               analyticsTurnTokenUsage(result),
		TimingProfile:            analyticsTurnTimingProfile(result),
		DurationMS:               uint64PtrFromNonNegativeInt64(durationMS),
		StartedAt:                uint64PtrFromUnixSeconds(startedAt),
		CompletedAt:              uint64PtrFromUnixSeconds(completedAt),
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
		case key == tool.DefaultExecCommandToolName:
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
	lineage := r.responsesMetadataLineage(params.ThreadID)
	return turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
		InstallationID:     installationID,
		SessionID:          firstNonEmpty(lineage.SessionID, params.ThreadID),
		ThreadID:           params.ThreadID,
		TurnID:             params.ExpectedTurnID,
		WindowID:           params.ThreadID + ":1",
		RequestKind:        codexapi.ClientRequestTurn,
		ForkedFromThreadID: lineage.ForkedFromThreadID,
		ParentThreadID:     lineage.ParentThreadID,
		SubagentHeader:     lineage.SubagentHeader,
		SubagentKind:       lineage.SubagentKind,
		ThreadSource:       lineage.ThreadSource,
		Extra:              extraMetadata,
		StartedAtMS:        active.StartedAtMS,
		UseResponsesLite:   r.modelUsesResponsesLite(modelID),
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
	r.turnsMu.Lock()
	defer r.turnsMu.Unlock()
	active := r.active[threadID]
	if active == nil || active.TurnID != turnID {
		return nil
	}
	return &activeRuntimeTurn{
		ThreadID:     active.ThreadID,
		TurnID:       active.TurnID,
		StartedAtMS:  active.StartedAtMS,
		Params:       cloneTurnStartParams(active.Params),
		ConnectionID: active.ConnectionID,
	}
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
	items := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true})
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
	if r == nil || r.services.Plugins == nil {
		return nil
	}
	r.configureSuggestedPluginProviderForTurn(cfg)
	return plugin.ListDiscoverablePlugins(r.services.Plugins.DiscoverableInstallCandidates(), r.pluginDiscoverableConfigForTurn(cfg))
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
	return features.Enabled(settings, "plugins") &&
		features.Enabled(settings, "remote_plugin") &&
		features.Enabled(settings, "tool_suggest")
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
	now := time.Now().UTC()
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
	available := contextfrag.Render(contextfrag.NewAvailablePluginsInstructions(contextPluginSummaries(capabilities)))
	if available != nil && strings.TrimSpace(available.Content) != "" {
		sections = append(sections, available.Content)
	}
	mentioned := plugin.CollectExplicitPluginMentions(pluginUserInputFromTurn(params), capabilities)
	if len(mentioned) > 0 {
		mcpTools, _ := r.mcpRuntimeInputsForTurn(threadID, cfg)
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
	if r == nil || r.services.Skills == nil {
		return strings.TrimSpace(instructions), nil, nil, nil
	}
	listParams := &SkillsListParams{}
	if cwd := turnCWD(params); cwd != "" {
		listParams.CWDs = []string{cwd}
	}
	response, err := r.services.Skills.List(listParams)
	if err != nil {
		return "", nil, nil, err
	}
	skillEntries := cloneSkills(response.Skills)
	pluginSkillEntries, err := r.pluginSkillEntriesForRuntime()
	if err != nil {
		return "", nil, nil, err
	}
	skillEntries = append(skillEntries, pluginSkillEntries...)
	selectedCapabilitySkillEntries, err := r.selectedCapabilitySkillEntriesForRuntime(threadID)
	if err != nil {
		return "", nil, nil, err
	}
	skillEntries = append(skillEntries, selectedCapabilitySkillEntries...)
	r.notifyMissingSkillMCPDependencies(threadID, cfg, skillEntries)
	skillMetadata := promptSkillMetadataFromEntries(skillEntries)
	postToolInputItems := r.implicitSkillInputItemProvider(threadID, params, skillMetadata)
	available := promptctx.RenderAvailableSkills(
		skillMetadata,
		promptctx.DefaultSkillMetadataBudget(r.skillContextWindowForTurn(cfg, params)),
	)
	if available != nil && available.WarningMessage != nil && strings.TrimSpace(*available.WarningMessage) != "" {
		r.notify(NotificationWarning, &WarningNotification{
			ThreadID: stringPtrIfNotEmpty(threadID),
			Message:  strings.TrimSpace(*available.WarningMessage),
		})
	}
	skillInputItems := explicitSkillInputItems(params, skillMetadata)
	if available == nil || strings.TrimSpace(available.Body) == "" {
		return strings.TrimSpace(instructions), skillInputItems, postToolInputItems, nil
	}
	return strings.Join(nonEmpty([]string{strings.TrimSpace(available.Body), strings.TrimSpace(instructions)}), "\n\n"), skillInputItems, postToolInputItems, nil
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
	if r == nil || r.services.Plugins == nil {
		return nil, nil
	}
	roots := r.services.Plugins.EnabledSkillRoots()
	entries := make([]SkillsListEntry, 0)
	for _, root := range roots {
		discovered, err := discover(SkillsRoot{Path: root.Root, Scope: "plugin", PluginID: root.PluginID})
		if err != nil {
			return nil, err
		}
		for _, entry := range discovered {
			prefixed := cloneSkill(entry)
			prefixPluginSkillNames(&prefixed, root.PluginDisplayName)
			entries = append(entries, prefixed)
		}
	}
	return entries, nil
}

func (r *RuntimeRouter) selectedCapabilitySkillEntriesForRuntime(threadID string) ([]SkillsListEntry, error) {
	if r == nil || strings.TrimSpace(threadID) == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil, err
	}
	roots := make([]SkillsRoot, 0, len(record.Metadata.SelectedCapabilityRoots))
	entries := make([]SkillsListEntry, 0)
	for _, raw := range record.Metadata.SelectedCapabilityRoots {
		var selected SelectedCapabilityRoot
		if err := json.Unmarshal(raw, &selected); err != nil {
			continue
		}
		if selected.Location.Type != CapabilityRootLocationEnvironment {
			continue
		}
		if environmentRecord, ok := r.selectedCapabilityEnvironmentRecord(&selected.Location); ok {
			discovered, err := discoverRemoteEnvironmentSkills(context.Background(), environmentRecord, selected.Location.Path)
			if err != nil {
				return nil, err
			}
			entries = append(entries, discovered...)
			continue
		}
		if r.selectedCapabilityEnvironmentRequired(&selected.Location) {
			continue
		}
		path := capabilityRootLocalPath(selected.Location.Path)
		if strings.TrimSpace(path) == "" {
			continue
		}
		roots = append(roots, SkillsRoot{Path: path, Scope: "user"})
	}
	roots = dedupeSkillsRoots(roots)
	for _, root := range roots {
		discovered, err := discover(root)
		if err != nil {
			return nil, err
		}
		entries = append(entries, discovered...)
	}
	return entries, nil
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
			description := firstNonEmpty(entry.ShortDescription, entry.Description)
			var allowImplicit *bool
			if entry.Policy != nil && entry.Policy.AllowImplicitInvocation != nil {
				value := *entry.Policy.AllowImplicitInvocation
				allowImplicit = &value
			}
			out = append(out, promptctx.InstructionsSkillMetadata{
				Name:                    entry.Name,
				Scope:                   entry.Scope,
				Path:                    entry.Path,
				Description:             description,
				PluginID:                entry.PluginID,
				Contents:                entry.Contents,
				AllowImplicitInvocation: allowImplicit,
			})
		}
	}
	walk(entries)
	return out
}

func (r *RuntimeRouter) notifyMissingSkillMCPDependencies(threadID string, cfg *config.Config, entries []SkillsListEntry) {
	if r == nil {
		return
	}
	skills := runtimeSkillMetadataFromEntries(entries)
	if len(skills) == 0 {
		return
	}
	missing := mcp.CollectMissingRuntimeDependencies(skills, r.runtimeMCPServerConfigsForSkills(cfg))
	if len(missing) == 0 {
		return
	}
	r.notify(NotificationWarning, &WarningNotification{
		ThreadID: stringPtrIfNotEmpty(strings.TrimSpace(threadID)),
		Message:  "Some enabled skills require MCP servers that are not configured: " + mcp.FormatMissingRuntimeDependencies(missing) + ".",
	})
}

func runtimeSkillMetadataFromEntries(entries []SkillsListEntry) []mcp.RuntimeSkillMetadata {
	out := make([]mcp.RuntimeSkillMetadata, 0, len(entries))
	var walk func([]SkillsListEntry)
	walk = func(values []SkillsListEntry) {
		for _, entry := range values {
			if len(entry.Skills) > 0 {
				walk(entry.Skills)
				continue
			}
			if !entry.Enabled || strings.TrimSpace(entry.Name) == "" || entry.Dependencies == nil {
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
	return out
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
	runtimeConfig := mcp.RuntimeConfigFromValues(values, codexHome)
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
	CWD     string `json:"cwd,omitempty"`
	Workdir string `json:"workdir,omitempty"`
}

func (r *RuntimeRouter) implicitSkillInputItemProvider(threadID string, params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata) turn.ToolPostExecutionInputItems {
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
	seen := map[string]bool{}
	for _, skill := range promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: skillMentionInputsFromTurn(params),
		Skills: skills,
	}) {
		if key := implicitSkillSeenKey(skill); key != "" {
			seen[key] = true
		}
	}
	var mu sync.Mutex
	return func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any {
		skill := implicitSkillForToolInvocation(skills, invocation, baseCWD)
		if skill == nil {
			return nil
		}
		key := implicitSkillSeenKey(*skill)
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
		item := skillInstructionsInputItem(*skill)
		if item == nil {
			mu.Lock()
			delete(seen, key)
			mu.Unlock()
			return nil
		}
		if r != nil {
			r.notify(NotificationWarning, &WarningNotification{
				ThreadID: stringPtrIfNotEmpty(threadID),
				Message:  fmt.Sprintf("Implicitly invoked skill %q from shell command.", skill.Name),
			})
		}
		return []any{item}
	}
}

func implicitSkillForToolInvocation(skills []promptctx.InstructionsSkillMetadata, invocation *tool.Invocation, baseCWD string) *promptctx.InstructionsSkillMetadata {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil
	}
	name := invocation.ToolName.Key()
	if name != tool.DefaultExecCommandToolName && name != "shell" {
		return nil
	}
	var args implicitShellSkillArgs
	if strings.TrimSpace(invocation.Payload.Arguments) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &args); err != nil {
		return nil
	}
	command := strings.TrimSpace(args.Cmd)
	if command == "" {
		return nil
	}
	workdir := implicitSkillWorkdir(baseCWD, firstNonEmpty(args.CWD, args.Workdir))
	if strings.TrimSpace(workdir) == "" {
		return nil
	}
	return promptctx.DetectImplicitSkillInvocationForCommand(skills, command, workdir)
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

func implicitSkillSeenKey(skill promptctx.InstructionsSkillMetadata) string {
	if path := strings.TrimSpace(skill.Path); path != "" {
		return strings.ToLower(filepath.Clean(path))
	}
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(skill.Scope) + "\x00" + name)
}

func explicitSkillInputItems(params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata) []any {
	selected := promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: skillMentionInputsFromTurn(params),
		Skills: skills,
	})
	if len(selected) == 0 {
		return nil
	}
	items := make([]any, 0, len(selected))
	for _, skill := range selected {
		if item := skillInstructionsInputItem(skill); item != nil {
			items = append(items, item)
		}
	}
	return items
}

func skillInstructionsInputItem(skill promptctx.InstructionsSkillMetadata) any {
	contents := skill.Contents
	if strings.TrimSpace(contents) == "" {
		data, err := os.ReadFile(skill.Path)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			return nil
		}
		contents = string(data)
	}
	rendered := contextfrag.Render(contextfrag.NewSkillInstructions(skill.Name, skill.Path, contents))
	return renderedFragmentInputItem(rendered)
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
	return &model.ModelsManagerConfig{
		PersonalityEnabled: features.Enabled(settings, "personality"),
	}
}

func appPersonalityForTurn(cfg *config.Config, params *turn.TurnStartParams) string {
	if params != nil && params.Personality != nil {
		return strings.TrimSpace(*params.Personality)
	}
	return stringConfigValue(cfg, "personality")
}

func explicitPersonalitySpecInstructions(info *model.ModelInfo, params *turn.TurnStartParams) string {
	if params == nil || !params.PersonalitySet || params.Personality == nil || info == nil {
		return ""
	}
	message, ok := info.PersonalityMessage(*params.Personality)
	if !ok || strings.TrimSpace(message) == "" {
		return ""
	}
	return fmt.Sprintf(
		"<personality_spec> The user has requested a new communication style. Future messages should adhere to the following personality: \n%s </personality_spec>",
		message,
	)
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
	return r.requireModels().Info(&model.ModelInfoReadParams{Model: modelID, Config: modelConfigForAppTurn(cfg)})
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
	if strings.TrimSpace(name.Namespace) != "" {
		data["server"] = name.Namespace
	}
	if strings.TrimSpace(name.Name) != "" {
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
