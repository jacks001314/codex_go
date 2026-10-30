package rollout

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/compact"
	"codex_go/eventmap"
	"codex_go/session"
)

// ContextBaseline is the resume/fork context baseline recovered from a rollout.
//
// TurnContext mirrors Rust's `PreviousTurnSettings`/`reference_context_item`
// restoration: it is the newest surviving turn-context record that belongs to a
// segment which actually established a model-context baseline. WorldState is
// the replayed world-state snapshot of the surviving segments, with full
// snapshots resetting and patches merging onto the current baseline.
//
// Rust source: `core/src/session/rollout_reconstruction.rs`
// (`finalize_active_segment` and the world-state replay at the end of
// `reconstruct_rollout`).
type ContextBaseline struct {
	TurnContext json.RawMessage
	WorldState  json.RawMessage
}

// ReconstructContextBaseline rebuilds the context baseline a cold resume or a
// fork inherits from persisted rollout lines.
//
// A surviving full world-state snapshot is itself a context baseline even when
// a child fork removed the associated user message. Partial snapshots and full
// snapshots superseded by a later compaction are ignored, and segments dropped
// by `thread_rolled_back` contribute nothing.
func ReconstructContextBaseline(lines []Line) ContextBaseline {
	segments := replayContextBaselineSegments(lines)
	baseline := ContextBaseline{WorldState: replayContextWorldState(segments)}
	for i := len(segments) - 1; i >= 0; i-- {
		segment := &segments[i]
		if !segment.hasContextBaseline() {
			continue
		}
		baseline.TurnContext = segment.newestTurnContext()
		break
	}
	return baseline
}

// contextBaselineSegment mirrors Rust's `ActiveReplaySegment` for the state
// needed by baseline reconstruction. Segments are delimited by `turn_started`
// events; a segment owns the `turn_started` event that opens it.
type contextBaselineSegment struct {
	// startTurnID is the id carried by the segment's own `turn_started` event.
	startTurnID string
	// newestTurnID is the id of the newest turn-scoped item in the segment
	// (a completion, abort, or context record), matching Rust's
	// `active_segment.turn_id` discovery order.
	newestTurnID string
	// countsAsUserTurn mirrors `ActiveReplaySegment::counts_as_user_turn`.
	countsAsUserTurn bool
	turnContexts     []contextBaselineTurnContext
	timeline         []contextBaselineTimelineEntry
}

type contextBaselineTurnContext struct {
	turnID  string
	payload json.RawMessage
}

// contextBaselineTimelineEntry preserves the chronological order of the
// compaction and world-state records inside a segment so patches keep their
// meaning relative to the compaction that reset the baseline.
type contextBaselineTimelineEntry struct {
	compacted  bool
	worldState json.RawMessage
	full       bool
}

// turnID resolves the segment's turn identity, preferring the newest
// turn-scoped record over its `turn_started` boundary.
func (s *contextBaselineSegment) turnID() string {
	if s == nil {
		return ""
	}
	return firstNonEmptyString(s.newestTurnID, s.startTurnID)
}

// hasContextBaseline reports whether this segment established a model-context
// baseline: a real user turn, or a full world-state snapshot that a later
// compaction in the same segment did not supersede.
func (s *contextBaselineSegment) hasContextBaseline() bool {
	if s == nil {
		return false
	}
	if s.countsAsUserTurn {
		return true
	}
	for i := len(s.timeline) - 1; i >= 0; i-- {
		switch {
		case s.timeline[i].compacted:
			return false
		case s.timeline[i].full:
			return true
		}
	}
	return false
}

// newestTurnContext returns the newest turn-context record compatible with the
// segment's turn identity, or nil when the segment has none.
func (s *contextBaselineSegment) newestTurnContext() json.RawMessage {
	if s == nil {
		return nil
	}
	turnID := s.turnID()
	for i := len(s.turnContexts) - 1; i >= 0; i-- {
		if !attributionTurnIDsCompatible(turnID, s.turnContexts[i].turnID) {
			continue
		}
		return append(json.RawMessage(nil), s.turnContexts[i].payload...)
	}
	return nil
}

// replayContextBaselineSegments projects rollout lines into the surviving
// replay segments Rust scans backward. Rollback drops the newest segments until
// its user-turn budget is exhausted, and everything in a dropped segment is
// discarded.
func replayContextBaselineSegments(lines []Line) []contextBaselineSegment {
	segments := make([]contextBaselineSegment, 0, len(lines))
	current := -1
	ensure := func() *contextBaselineSegment {
		if current < 0 {
			segments = append(segments, contextBaselineSegment{})
			current = len(segments) - 1
		}
		return &segments[current]
	}
	for i := range lines {
		line := &lines[i]
		if line.ThreadRolledBack != nil {
			segments = rollbackContextBaselineSegments(segments, int(line.ThreadRolledBack.NumTurns))
			current = -1
			continue
		}
		switch line.Type {
		case "compacted":
			segment := ensure()
			segment.timeline = append(segment.timeline, contextBaselineTimelineEntry{compacted: true})
		case "world_state":
			segment := ensure()
			segment.timeline = append(segment.timeline, contextBaselineTimelineEntry{
				worldState: append(json.RawMessage(nil), line.WorldState...),
				full:       rolloutWorldStateIsFull(line.WorldState),
			})
		case "turn_context":
			segment := ensure()
			turnID := turnIDFromTurnContext(line.TurnContext)
			if turnID != "" && segment.newestTurnID == "" && segment.startTurnID == "" {
				segment.newestTurnID = turnID
			}
			segment.turnContexts = append(segment.turnContexts, contextBaselineTurnContext{
				turnID:  turnID,
				payload: append(json.RawMessage(nil), line.TurnContext...),
			})
		case "item":
			if rolloutLineItemCountsAsUserTurn(line) {
				ensure().countsAsUserTurn = true
			}
		case "inter_agent_communication":
			ensure().countsAsUserTurn = true
		case "event_msg":
			payload, ok := rolloutEventPayloadFromLine(line)
			if !ok {
				continue
			}
			switch normalizeRolloutEventType(payload.Type) {
			case "turn_started":
				segments = append(segments, contextBaselineSegment{
					startTurnID: firstNonEmptyString(payload.TurnID, payload.TurnIDCamel),
				})
				current = len(segments) - 1
			case "turn_complete", "turn_aborted":
				segment := ensure()
				turnID := firstNonEmptyString(payload.TurnID, payload.TurnIDCamel)
				if turnID != "" && attributionTurnIDsCompatible(segment.startTurnID, turnID) {
					segment.newestTurnID = turnID
				}
			case "user_message", "agent_message":
				ensure().countsAsUserTurn = true
			case "item_completed":
				if rolloutEventItemCountsAsUserTurn(payload.Item) {
					ensure().countsAsUserTurn = true
				}
			}
		}
	}
	return segments
}

// rollbackContextBaselineSegments drops the newest segments the way Rust's
// reverse replay does: every segment above the rollback boundary is discarded,
// and the budget decrements only for segments that count as user turns.
func rollbackContextBaselineSegments(segments []contextBaselineSegment, numTurns int) []contextBaselineSegment {
	for numTurns > 0 && len(segments) > 0 {
		last := segments[len(segments)-1]
		segments = segments[:len(segments)-1]
		if last.countsAsUserTurn {
			numTurns--
		}
	}
	return segments
}

// replayContextWorldState folds the surviving segments' chronologically ordered
// compaction and world-state records into one snapshot. A compaction resets the
// baseline, a full record replaces it, and a patch without a live baseline is
// ignored.
func replayContextWorldState(segments []contextBaselineSegment) json.RawMessage {
	var baseline map[string]any
	for i := range segments {
		for _, entry := range segments[i].timeline {
			if entry.compacted {
				baseline = nil
				continue
			}
			state, ok := decodeRolloutWorldState(entry.worldState)
			if !ok {
				continue
			}
			switch {
			case entry.full:
				baseline = state
			case baseline == nil:
				continue
			default:
				applyRolloutWorldStatePatch(baseline, state)
			}
		}
	}
	if baseline == nil {
		return nil
	}
	encoded, err := json.Marshal(baseline)
	if err != nil {
		return nil
	}
	return encoded
}

// rolloutWorldStateIsFull reports whether a persisted world-state record is a
// full snapshot. Legacy Go rollouts wrote the bare state object and are treated
// as full snapshots, matching the previous reader.
func rolloutWorldStateIsFull(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var wrapper struct {
		Full *bool `json:"full"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil || wrapper.Full == nil {
		return true
	}
	return *wrapper.Full
}

func decodeRolloutWorldState(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	state := raw
	var wrapper struct {
		State json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.State) > 0 && string(wrapper.State) != "null" {
		state = wrapper.State
	}
	var decoded map[string]any
	if err := json.Unmarshal(state, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

// applyRolloutWorldStatePatch applies an RFC 7386 merge patch to the replaying
// baseline, mirroring Rust's `WorldStateSnapshot::apply_merge_patch`.
func applyRolloutWorldStatePatch(target map[string]any, patch map[string]any) {
	for key, value := range patch {
		target[key] = mergeRolloutWorldStateValue(target[key], value)
		if target[key] == nil {
			delete(target, key)
		}
	}
}

func mergeRolloutWorldStateValue(target any, patch any) any {
	patchObject, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	targetObject, ok := target.(map[string]any)
	if !ok {
		targetObject = map[string]any{}
	}
	for key, value := range patchObject {
		merged := mergeRolloutWorldStateValue(targetObject[key], value)
		if merged == nil {
			delete(targetObject, key)
			continue
		}
		targetObject[key] = merged
	}
	return targetObject
}

func rolloutEventPayloadFromLine(line *Line) (rolloutEventPayload, bool) {
	if line == nil || len(line.Payload) == 0 {
		return rolloutEventPayload{}, false
	}
	var payload rolloutEventPayload
	if err := json.Unmarshal(line.Payload, &payload); err != nil {
		return rolloutEventPayload{}, false
	}
	return payload, true
}

func rolloutLineItemCountsAsUserTurn(line *Line) bool {
	item, ok := ItemFromLine(line)
	if !ok {
		return false
	}
	sessionItem := SessionItemFromRolloutItem(item, time.Time{}, 0)
	return sessionItemCountsAsUserTurn(&sessionItem)
}

func rolloutEventItemCountsAsUserTurn(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var item session.Item
	if err := json.Unmarshal(raw, &item); err != nil {
		return false
	}
	return sessionItemCountsAsUserTurn(&item)
}

// sessionItemCountsAsUserTurn mirrors Rust's `is_user_turn_boundary`: an agent
// message or a non-contextual user message (excluding compaction summaries)
// closes a user turn.
func sessionItemCountsAsUserTurn(item *session.Item) bool {
	if item == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "agent_message":
		return true
	case "message":
		role := strings.ToLower(strings.TrimSpace(item.Role))
		if role == "assistant" {
			return true
		}
		if role != "user" {
			return false
		}
		if sessionItemIsCompactionSummary(item) {
			return false
		}
		return !sessionItemHasContextualUserContent(item)
	default:
		return false
	}
}

func sessionItemIsCompactionSummary(item *session.Item) bool {
	prefix := compact.SummaryPrefix + "\n"
	if strings.HasPrefix(item.Text, prefix) {
		return true
	}
	for _, part := range item.Content {
		if strings.HasPrefix(part.Text, prefix) {
			return true
		}
	}
	return false
}

func sessionItemHasContextualUserContent(item *session.Item) bool {
	content := make([]eventmap.ContentItem, 0, len(item.Content))
	for _, part := range item.Content {
		content = append(content, eventmap.ContentItem{Kind: eventmap.ContentKind(part.Type), Text: part.Text})
	}
	if len(content) == 0 && strings.TrimSpace(item.Text) != "" {
		content = append(content, eventmap.ContentItem{Kind: eventmap.ContentInputText, Text: item.Text})
	}
	return eventmap.IsContextualUserMessageContent(content)
}
