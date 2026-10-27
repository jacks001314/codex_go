package appserver

import (
	"encoding/json"
	"sort"
	"strings"

	"codex_go/compact"
	codexctx "codex_go/context"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
	"codex_go/utils"
)

// Guardian root request preparation mirrors Rust's
// `codex-guardian-context::prepare_root_context` (#57425): one bounded view of
// the root conversation that keeps user authorization, assistant context and
// host notices separate, and marks every omission instead of hiding it.

const (
	// guardianRootContextWindow is Rust's ROOT_CONTEXT_WINDOW: the number of
	// preceding messages each handoff boundary contributes.
	guardianRootContextWindow = 3
	// guardianMaxRootMessages is Rust's MAX_ROOT_MESSAGES (#47947), shared by
	// user records, verified answers and assistant context.
	guardianMaxRootMessages = 16
)

// guardianRootCandidate is one root message plus its comparable position.
// hasOrder is false for evidence whose acceptance order is unknown.
type guardianRootCandidate struct {
	message   state.RootMessage
	order     uint64
	hasOrder  bool
	messageID string
}

// guardianRootRetainedAssistant is retained assistant evidence that can stand
// in for a message the live history no longer carries.
type guardianRootRetainedAssistant struct {
	order              uint64
	message            retainedctx.RetainedUserMessage
	standaloneEligible bool
}

// guardianRootPreparationInput is the unselected root evidence snapshot from
// the same history as the root authorization metadata.
type guardianRootPreparationInput struct {
	userMessages            []guardianRootCandidate
	assistantMessages       []guardianRootCandidate
	retainedAssistants      []guardianRootRetainedAssistant
	retainedUserSources     []retainedctx.RetainedUserMessage
	handoffBoundaries       []uint64
	missingRootInstructions bool
	missingAssistantContext bool
	missingVerifiedAnswers  bool
}

// guardianRootPreparedContext is the bounded root view plus the selection
// metadata hosts use for cache revisioning.
type guardianRootPreparedContext struct {
	messages                []state.RootMessage
	missingRootInstructions bool
	handoffSelection        map[int]struct{}
	latestLegacyTurnID      string
}

// prepareGuardianRootContext applies the fixed root request policy without
// discarding the captured sources.
func prepareGuardianRootContext(input guardianRootPreparationInput, legacy []retainedctx.RetainedUserMessage) guardianRootPreparedContext {
	missingRootInstructions := input.missingRootInstructions
	missingAssistantContext := input.missingAssistantContext
	missingVerifiedAnswers := input.missingVerifiedAnswers

	// Verified answers stay whole; ordinary root user text keeps its budget.
	messages := make([]guardianRootCandidate, 0, len(input.userMessages))
	for _, candidate := range input.userMessages {
		if candidate.message.Kind == state.RootMessageUserInput &&
			!guardianRootAnswerFits(len(candidate.message.Text)) {
			continue
		}
		if candidate.message.Kind == state.RootMessageUser {
			candidate.message.Text, _ = codexctx.GuardianTruncateText(candidate.message.Text, guardianMaxRootMessageTokens)
		}
		messages = append(messages, candidate)
	}
	missingVerifiedAnswers = missingVerifiedAnswers || len(messages) != len(input.userMessages)

	legacyCapacity := guardianMaxRootMessages - len(messages)
	if legacyCapacity < 0 {
		legacyCapacity = 0
	}
	var legacyMessages []state.RootMessage
	latestLegacyTurnID := ""
	if missingRootInstructions && legacyCapacity > 0 {
		for _, candidate := range legacy {
			text, _ := codexctx.GuardianTruncateText(candidate.Text, guardianMaxRootMessageTokens)
			// Legacy records without IDs historically match after the
			// request-size cap, so workers receive one copy.
			if guardianRootLegacySourceMatched(input.retainedUserSources, candidate, text) {
				continue
			}
			if turnID := strings.TrimSpace(candidate.TurnID); turnID != "" {
				latestLegacyTurnID = turnID
			}
			legacyMessages = append(legacyMessages, state.RootMessage{Kind: state.RootMessageUser, Text: text})
			if len(legacyMessages) > legacyCapacity {
				legacyMessages = legacyMessages[1:]
			}
		}
	}

	assistants := guardianPrepareRootAssistantMessages(input.assistantMessages, input.retainedAssistants, missingAssistantContext)
	missingAssistantContext = assistants.missingContext
	messages = append(messages, assistants.messages...)

	sort.SliceStable(messages, func(i, j int) bool {
		return guardianRootCandidateSortLess(messages[i], messages[j])
	})
	selection := guardianSelectRootHandoffIndices(messages, input.handoffBoundaries)
	if selection != nil {
		kept := make([]guardianRootCandidate, 0, len(messages))
		for index, message := range messages {
			if _, ok := selection[index]; ok {
				kept = append(kept, message)
				continue
			}
			if guardianRootCandidateIsAssistant(message) {
				missingAssistantContext = true
			}
		}
		messages = kept
	}

	// Apply one shared cap so user records and confirmed questions compete
	// while keeping omissions explicit.
	removed := len(messages) - guardianMaxRootMessages
	if removed < 0 {
		removed = 0
	}
	for _, message := range messages[:removed] {
		switch message.message.Kind {
		case state.RootMessageUser, state.RootMessageUserInput:
			missingRootInstructions = true
		default:
			if guardianRootCandidateIsAssistant(message) {
				missingAssistantContext = true
			}
		}
	}
	messages = messages[removed:]
	// Keep the existing presentation of unordered evidence after ordered records.
	sort.SliceStable(messages, func(i, j int) bool {
		return guardianRootCandidateFinalLess(messages[i], messages[j])
	})
	if remaining := guardianMaxRootMessages - len(messages); len(legacyMessages) > remaining {
		if remaining < 0 {
			remaining = 0
		}
		legacyMessages = legacyMessages[len(legacyMessages)-remaining:]
	}

	out := make([]state.RootMessage, 0, len(messages)+len(legacyMessages)+4)
	out = append(out, state.RootMessage{Kind: state.RootMessageRetainedContextScope})
	if missingRootInstructions {
		out = append(out, state.RootMessage{Kind: state.RootMessageIncompleteRootInstructions})
	}
	if missingVerifiedAnswers {
		out = append(out, state.RootMessage{Kind: state.RootMessageIncompleteVerifiedAnswers})
	}
	if missingAssistantContext {
		out = append(out, state.RootMessage{Kind: state.RootMessageIncompleteAssistantContext})
	}
	for _, message := range messages {
		out = append(out, message.message)
	}
	if len(legacyMessages) > 0 {
		withLegacy := make([]state.RootMessage, 0, len(legacyMessages)+1+len(out))
		withLegacy = append(withLegacy, state.RootMessage{Kind: state.RootMessageLegacyContextScope})
		withLegacy = append(withLegacy, legacyMessages...)
		withLegacy = append(withLegacy, out...)
		out = withLegacy
	}
	return guardianRootPreparedContext{
		messages:                out,
		missingRootInstructions: missingRootInstructions,
		handoffSelection:        selection,
		latestLegacyTurnID:      latestLegacyTurnID,
	}
}

// guardianRootAnswerFits mirrors Rust's `answer_fits`: a verified answer is
// whole or absent, never truncated.
func guardianRootAnswerFits(bytes int) bool {
	return bytes != 0 && bytes <= utils.ApproxBytesForTokens(guardianMaxRootMessageTokens)
}

// guardianSelectRootHandoffIndices preserves the handoff windows, latest
// context and all user restrictions. Later user inputs can restrict an earlier
// handoff even when they are not recent; unordered legacy evidence is kept.
func guardianSelectRootHandoffIndices(messages []guardianRootCandidate, boundaries []uint64) map[int]struct{} {
	selected := map[int]struct{}{}
	for _, boundary := range boundaries {
		added := 0
		for index := len(messages) - 1; index >= 0 && added < guardianRootContextWindow; index-- {
			message := messages[index]
			if message.message.Kind == state.RootMessageUserInput ||
				!message.hasOrder || message.order > boundary {
				continue
			}
			selected[index] = struct{}{}
			added++
		}
	}
	if len(selected) == 0 {
		return nil
	}
	added := 0
	for index := len(messages) - 1; index >= 0 && added < guardianRootContextWindow; index-- {
		message := messages[index]
		if message.hasOrder && message.message.Kind != state.RootMessageUserInput {
			selected[index] = struct{}{}
			added++
		}
	}
	for index, message := range messages {
		if !message.hasOrder ||
			message.message.Kind == state.RootMessageUser ||
			message.message.Kind == state.RootMessageUserInput {
			selected[index] = struct{}{}
		}
	}
	return selected
}

// guardianPrepareRootAssistants prepares assistant evidence without changing
// which sources were captured: retained messages stand in for live ones the
// history no longer carries, and live text keeps its bounded fallback.
func guardianPrepareRootAssistantMessages(
	assistantMessages []guardianRootCandidate,
	retainedAssistants []guardianRootRetainedAssistant,
	missingContext bool,
) guardianRootPreparedAssistants {
	liveIDs := map[string]struct{}{}
	for _, candidate := range assistantMessages {
		if id := strings.TrimSpace(candidate.messageID); id != "" {
			liveIDs[id] = struct{}{}
		}
	}
	retainedByID := map[string]*guardianRootRetainedAssistant{}
	for index := range retainedAssistants {
		retained := &retainedAssistants[index]
		if id := guardianRootRetainedMessageID(retained.message.MessageID); id != "" {
			retainedByID[id] = retained
		}
	}
	messages := make([]guardianRootCandidate, 0, len(retainedAssistants)+len(assistantMessages))
	for index := range retainedAssistants {
		retained := &retainedAssistants[index]
		if !retained.standaloneEligible {
			continue
		}
		if id := guardianRootRetainedMessageID(retained.message.MessageID); id != "" {
			if _, ok := liveIDs[id]; ok {
				continue
			}
		}
		message, ok := state.RetainedAssistantMessage(&retained.message)
		if !ok {
			missingContext = true
			continue
		}
		messages = append(messages, guardianRootCandidate{
			message:  message,
			order:    retained.order,
			hasOrder: true,
		})
	}
	for _, candidate := range assistantMessages {
		retained := (*guardianRootRetainedAssistant)(nil)
		if id := strings.TrimSpace(candidate.messageID); id != "" {
			retained = retainedByID[id]
		}
		order, hasOrder := candidate.order, candidate.hasOrder
		text := candidate.message.Text
		if retained != nil {
			order, hasOrder = retained.order, true
			if message, ok := state.RetainedAssistantMessage(&retained.message); ok {
				text = message.Text
			}
		}
		text, _ = codexctx.GuardianTruncateText(text, guardianMaxRootMessageTokens)
		if !hasOrder {
			messages = append(messages, guardianRootCandidate{
				message: state.RootMessage{Kind: state.RootMessageUnorderedAssistant, Text: text},
			})
			continue
		}
		messages = append(messages, guardianRootCandidate{
			message:  state.RootMessage{Kind: state.RootMessageAssistant, Text: text},
			order:    order,
			hasOrder: true,
		})
	}
	return guardianRootPreparedAssistants{messages: messages, missingContext: missingContext}
}

type guardianRootPreparedAssistants struct {
	messages       []guardianRootCandidate
	missingContext bool
}

func guardianRootCandidateIsAssistant(candidate guardianRootCandidate) bool {
	return candidate.message.Kind == state.RootMessageAssistant ||
		candidate.message.Kind == state.RootMessageUnorderedAssistant
}

// guardianRootCandidateSortLess mirrors Rust's `sort_by_key(|message| order)`:
// unordered evidence sorts before ordered records.
func guardianRootCandidateSortLess(left, right guardianRootCandidate) bool {
	if left.hasOrder != right.hasOrder {
		return !left.hasOrder
	}
	if !left.hasOrder {
		return false
	}
	return left.order < right.order
}

// guardianRootCandidateFinalLess mirrors Rust's
// `sort_by_key(|message| (order.is_none(), order))`: ordered records keep their
// recorded order and unordered evidence follows.
func guardianRootCandidateFinalLess(left, right guardianRootCandidate) bool {
	if left.hasOrder != right.hasOrder {
		return left.hasOrder
	}
	if !left.hasOrder {
		return false
	}
	return left.order < right.order
}

func guardianRootLegacySourceMatched(sources []retainedctx.RetainedUserMessage, candidate retainedctx.RetainedUserMessage, text string) bool {
	for _, source := range sources {
		if source.MessageID != nil {
			if candidate.MessageID != nil && *candidate.MessageID == *source.MessageID {
				return true
			}
			continue
		}
		if candidate.TurnID == source.TurnID && text == source.Text {
			return true
		}
	}
	return false
}

func guardianRootRetainedMessageID(messageID *string) string {
	if messageID == nil {
		return ""
	}
	return strings.TrimSpace(*messageID)
}

// guardianRootRetainedUserCandidates selects the retained user instructions and
// verified answers that can authorize an action. It also reports evidence the
// retained snapshot could not recover.
func guardianRootRetainedUserCandidates(
	retained *retainedctx.RetainedContext,
	missingRootInstructions bool,
) ([]guardianRootCandidate, []retainedctx.RetainedUserMessage, bool) {
	if retained == nil {
		return nil, nil, missingRootInstructions
	}
	var candidates []guardianRootCandidate
	var sources []retainedctx.RetainedUserMessage
	for _, ordered := range retained.OrderedEntries() {
		switch {
		case ordered.Entry.UserMessage != nil:
			message := *ordered.Entry.UserMessage
			sources = append(sources, message)
			text := message.Text
			if strings.TrimSpace(text) == "" {
				missingRootInstructions = true
				continue
			}
			isGoalUpdate := codexctx.UserGoalUpdateMatchesText(text)
			if isGoalUpdate && !message.Complete {
				// A retained placeholder cannot recover an omitted goal restriction.
				missingRootInstructions = true
			}
			if guardianRootTextIsSummary(text) ||
				strings.HasPrefix(strings.TrimSpace(text), "<user_action>") ||
				(!isGoalUpdate && guardianRootContextualUserText(text)) {
				continue
			}
			candidates = append(candidates, guardianRootCandidate{
				message:  state.RootMessage{Kind: state.RootMessageUser, Text: text},
				order:    ordered.Order.Order,
				hasOrder: true,
			})
		case ordered.Entry.VerifiedAnswer != nil:
			candidates = append(candidates, guardianRootCandidate{
				message:  state.RootMessage{Kind: state.RootMessageUserInput, Text: state.RenderOriginalVerifiedAnswer(ordered.Entry.VerifiedAnswer)},
				order:    ordered.Order.Order,
				hasOrder: true,
			})
		}
	}
	return candidates, sources, missingRootInstructions
}

// guardianRootItemUserCandidates is the fallback snapshot when no retained
// evidence exists yet.
func guardianRootItemUserCandidates(items []session.Item) []guardianRootCandidate {
	var candidates []guardianRootCandidate
	for index := range items {
		item := &items[index]
		if guardianRootItemIsSummary(item) || !guardianRootItemIsUserMessage(item) {
			continue
		}
		text := guardianRootMessageText(item)
		if text == "" ||
			guardianRootTextIsSummary(text) ||
			strings.HasPrefix(strings.TrimSpace(text), "<user_action>") ||
			guardianRootContextualUserText(text) {
			continue
		}
		order, hasOrder := guardianRootItemOrder(item)
		candidates = append(candidates, guardianRootCandidate{
			message:  state.RootMessage{Kind: state.RootMessageUser, Text: text},
			order:    order,
			hasOrder: hasOrder,
		})
	}
	return candidates
}

// guardianRootRetainedAssistantCandidates exposes retained assistant evidence,
// including whether it may stand alone without a live source. Commentary is
// ineligible whether the retained record or its surviving source carries it.
func guardianRootRetainedAssistantCandidates(retained *retainedctx.RetainedContext, items []session.Item) []guardianRootRetainedAssistant {
	if retained == nil {
		return nil
	}
	commentaryIDs := map[string]struct{}{}
	for index := range items {
		item := &items[index]
		if !guardianRootItemIsAgentMessage(item) || !guardianRootPhaseIsCommentary(guardianRootItemPhase(item)) {
			continue
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			commentaryIDs[id] = struct{}{}
		}
	}
	var assistants []guardianRootRetainedAssistant
	for _, ordered := range retained.OrderedEntries() {
		message := ordered.Entry.AssistantMessage
		if message == nil {
			continue
		}
		standaloneEligible := true
		if message.Phase != nil && guardianRootPhaseIsCommentary(*message.Phase) {
			standaloneEligible = false
		}
		if id := guardianRootRetainedMessageID(message.MessageID); id != "" {
			if _, ok := commentaryIDs[id]; ok {
				standaloneEligible = false
			}
		}
		assistants = append(assistants, guardianRootRetainedAssistant{
			order:              ordered.Order.Order,
			message:            *message,
			standaloneEligible: standaloneEligible,
		})
	}
	return assistants
}

// guardianRootItemPhase resolves a message phase from the response payload or
// the decoded item data, matching the source Rust inspects.
func guardianRootItemPhase(item *session.Item) string {
	if item == nil {
		return ""
	}
	if phase := strings.TrimSpace(stringValueFromMap(item.Data, "phase")); phase != "" {
		return phase
	}
	var payload struct {
		Phase *string `json:"phase"`
	}
	if len(item.Raw) > 0 && json.Unmarshal(item.Raw, &payload) == nil && payload.Phase != nil {
		return strings.TrimSpace(*payload.Phase)
	}
	return ""
}

func guardianRootPhaseIsCommentary(phase string) bool {
	return strings.EqualFold(strings.TrimSpace(phase), "commentary")
}

// guardianRootLiveAssistantCandidates selects assistant output and delivered
// call bodies from the live root history.
func guardianRootLiveAssistantCandidates(items []session.Item, delivered map[string]string) []guardianRootCandidate {
	var candidates []guardianRootCandidate
	for index := range items {
		item := &items[index]
		if sessionItemIsCompactionOutput(item) {
			continue
		}
		candidate := guardianRootCandidate{messageID: strings.TrimSpace(item.ID)}
		switch {
		case strings.EqualFold(strings.TrimSpace(item.Type), "function_call"):
			callID := guardianRootItemCallID(item)
			text, ok := delivered[callID]
			if !ok {
				continue
			}
			candidate.message = state.RootMessage{Kind: state.RootMessageAssistant, Text: text}
		case guardianRootItemIsAgentMessage(item):
			phase := strings.ToLower(strings.TrimSpace(guardianRootItemPhase(item)))
			if phase != "" && phase != "final_answer" {
				continue
			}
			text := guardianRootMessageText(item)
			if text == "" {
				continue
			}
			candidate.message = state.RootMessage{Kind: state.RootMessageAssistant, Text: text}
		default:
			continue
		}
		candidate.order, candidate.hasOrder = guardianRootItemOrder(item)
		if !candidate.hasOrder {
			candidate.message.Kind = state.RootMessageUnorderedAssistant
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

// guardianRootDeliveredTexts joins messaging calls to their host-confirmed
// output text, newest first, as Rust's delivered-candidate lookup does.
func guardianRootDeliveredTexts(items []session.Item) map[string]string {
	delivered := map[string]string{}
	count := 0
	for index := len(items) - 1; index >= 0 && count < guardianMaxRootMessages; index-- {
		item := &items[index]
		if !strings.EqualFold(strings.TrimSpace(item.Type), "function_call_output") {
			continue
		}
		metadata := harnessMetadataFromSessionItem(item)
		if metadata == nil || metadata.DeliveredAssistantMessage == nil {
			continue
		}
		callID := guardianRootItemCallID(item)
		if callID == "" {
			continue
		}
		delivered[callID] = *metadata.DeliveredAssistantMessage
		count++
	}
	return delivered
}

func guardianRootItemCallID(item *session.Item) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(item.CallID), stringValueFromMap(item.Data, "call_id"))
}

// guardianRootItemRawText preserves the recorded body, including surrounding
// whitespace, for exact legacy-to-retained comparison.
func guardianRootItemRawText(item *session.Item) string {
	if item == nil {
		return ""
	}
	// Parsed rollout items trim `Text`; their typed content keeps the recorded
	// bytes the retained excerpt was truncated from.
	if len(item.Content) > 0 {
		var builder strings.Builder
		for _, part := range item.Content {
			builder.WriteString(part.Text)
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}
	if item.Text != "" {
		return item.Text
	}
	return stringValueFromMap(item.Data, "text")
}

// guardianRootItemTurnID mirrors Rust's `TurnItem::turn_id().unwrap_or_default`
// for legacy evidence: a missing turn id stays empty instead of becoming a
// synthetic value the retained source cannot match.
func guardianRootItemTurnID(item *session.Item) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(
		stringFromMap(item.Metadata, "turnId"),
		stringFromMap(item.Metadata, "turn_id"),
		stringFromMap(item.Data, "turnId"),
		stringFromMap(item.Data, "turn_id"),
	)
}

func guardianRootTextIsSummary(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), compact.SummaryPrefix+"\n")
}

// guardianRootLegacyUserMessages returns the original user instructions an old
// compaction backup carries. Their acceptance order is unknown; storage keeps
// its own independent bound.
func (r *RuntimeRouter) guardianRootLegacyUserMessages(record *session.Record) []retainedctx.RetainedUserMessage {
	if r == nil || record == nil || r.services.ThreadRouter == nil {
		return nil
	}
	path := r.services.ThreadRouter.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return nil
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return nil
	}
	items := rollout.CompactedGuardianHistory(lines, record.CreatedAt)
	if len(items) == 0 {
		return nil
	}
	legacy := make([]retainedctx.RetainedUserMessage, 0, len(items))
	for index := range items {
		item := &items[index]
		if !guardianRootItemIsUserMessage(item) || guardianRootItemIsSummary(item) {
			continue
		}
		// Legacy matching compares the recorded body exactly, so keep the
		// original spacing the retained excerpt was truncated from.
		text := guardianRootItemRawText(item)
		if strings.TrimSpace(text) == "" ||
			guardianRootTextIsSummary(text) ||
			strings.HasPrefix(strings.TrimSpace(text), "<user_action>") ||
			guardianRootContextualUserText(text) {
			continue
		}
		message := retainedctx.RetainedUserMessage{
			Text:   text,
			TurnID: guardianRootItemTurnID(item),
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			messageID := id
			message.MessageID = &messageID
		}
		legacy = append(legacy, message)
	}
	return legacy
}
