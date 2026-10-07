package rollout

import (
	"encoding/json"
	"strings"

	"codex_go/eventmap"
)

// This file models the attribution half of Rust #51402
// (`551bd409eb`) `Session::reconstruct_history_from_rollout`. Rust replays the
// rollout backward into `ActiveReplaySegment`s; Go keeps its forward projection
// for items and turn snapshots (`session_items.go`) and reconstructs only the
// attribution-relevant line-level subset here, on the same lines:
//
//   - reverse segments delimited by `turn_started`,
//   - the rollback gate that drops the newest surviving user turns,
//   - terminal events that belong to a preceding (already finished) turn,
//   - the compaction checkpoint's persisted attribution.
//
// Rust source: `core/src/session/rollout_reconstruction.rs`
// (`take_checkpoint_attribution`, `finalize_active_segment`,
// `turn_ids_are_compatible`).

// attributionSegment accumulates the attribution-relevant state Rust keeps on
// `ActiveReplaySegment` between a segment's newest item and its matching
// `TurnStarted`.
type attributionSegment struct {
	// turnID is the segment's turn, discovered from the newest turn-scoped item
	// it contains (a terminal event, a turn context, or the `TurnStarted` itself).
	turnID string
	// countsAsUserTurn mirrors `ActiveReplaySegment::counts_as_user_turn`: only a
	// real user message (or inter-agent communication) makes a segment a
	// rollback-droppable user turn.
	countsAsUserTurn bool
	// turnAttribution is the attribution the segment collected.
	turnAttribution *TurnAttribution
}

// takeCheckpointAttribution mirrors Rust
// `ActiveReplaySegment::take_checkpoint_attribution`: a checkpoint naming this
// segment (or an unidentified segment) is consumed here, so a segment that
// rollback discards still prevents the checkpoint from being restored a second
// time at the tail. A segment that already carries its own attribution leaves
// the checkpoint untouched.
func (s *attributionSegment) takeCheckpointAttribution(checkpoint **TurnAttribution) {
	if s == nil || *checkpoint == nil {
		return
	}
	if s.turnID != "" && s.turnID != (*checkpoint).TurnID {
		return
	}
	if s.turnAttribution != nil {
		return
	}
	s.turnAttribution = *checkpoint
	*checkpoint = nil
}

// attributionTurnIDsCompatible mirrors Rust `turn_ids_are_compatible`: an absent
// id on either side is compatible with anything.
func attributionTurnIDsCompatible(activeTurnID string, itemTurnID string) bool {
	return activeTurnID == "" || itemTurnID == "" || itemTurnID == activeTurnID
}

// finalizeAttributionSegment mirrors `finalize_active_segment`'s attribution
// return value: a segment consumed by thread rollback contributes nothing,
// otherwise it hands back the attribution it accumulated.
func finalizeAttributionSegment(segment *attributionSegment, pendingRollbackTurns *int) *TurnAttribution {
	if *pendingRollbackTurns > 0 {
		if segment.countsAsUserTurn {
			*pendingRollbackTurns--
		}
		return nil
	}
	return segment.turnAttribution
}

func orTurnAttribution(current *TurnAttribution, candidate *TurnAttribution) *TurnAttribution {
	if current != nil {
		return current
	}
	return candidate
}

// compactionResumeMetadata mirrors the subset of Rust
// `codex_history::CompactionResumeMetadata` (Rust #51402) that reconstruction
// reads: the continuation marker `last_started_turn_id` and the
// `turn_attribution` of the latest regular turn. Rust persists it on the
// `compacted` rollout item; Rust's own `#[serde(default,
// skip_serializing_if)]` makes both fields optional on the wire.
type compactionResumeMetadata struct {
	LastStartedTurnID      *string          `json:"last_started_turn_id"`
	LastStartedTurnIDCamel *string          `json:"lastStartedTurnId"`
	TurnAttribution        *TurnAttribution `json:"turn_attribution"`
	TurnAttributionCamel   *TurnAttribution `json:"turnAttribution"`
}

// lastStartedTurnID returns the recorded continuation marker, or "".
func (m *compactionResumeMetadata) lastStartedTurnID() string {
	if m == nil {
		return ""
	}
	if m.LastStartedTurnID != nil {
		return strings.TrimSpace(*m.LastStartedTurnID)
	}
	if m.LastStartedTurnIDCamel != nil {
		return strings.TrimSpace(*m.LastStartedTurnIDCamel)
	}
	return ""
}

// attribution returns the checkpoint's persisted regular-turn attribution.
func (m *compactionResumeMetadata) attribution() *TurnAttribution {
	if m == nil {
		return nil
	}
	if m.TurnAttribution != nil {
		return m.TurnAttribution
	}
	return m.TurnAttributionCamel
}

func compactionResumeMetadataFromLine(line *Line) *compactionResumeMetadata {
	if line == nil || line.Type != "compacted" || len(line.Payload) == 0 {
		return nil
	}
	var payload struct {
		ResumeMetadata      *compactionResumeMetadata `json:"resume_metadata"`
		ResumeMetadataCamel *compactionResumeMetadata `json:"resumeMetadata"`
	}
	if err := json.Unmarshal(line.Payload, &payload); err != nil {
		return nil
	}
	if payload.ResumeMetadata != nil {
		return payload.ResumeMetadata
	}
	return payload.ResumeMetadataCamel
}

// selectAttributionCheckpoint mirrors Rust `select_input_compaction`'s
// eligibility gate: only the newest compaction can bound replay, and only a
// complete checkpoint (replacement history and window number) that carries
// resume metadata contributes attribution. An incomplete newest compaction gets
// no older fallback.
func selectAttributionCheckpoint(lines []Line) *compactionResumeMetadata {
	for i := len(lines) - 1; i >= 0; i-- {
		line := &lines[i]
		if line.Type != "compacted" {
			continue
		}
		event := compactedEventFromPayload(line.Payload)
		if event == nil {
			continue
		}
		if len(event.ReplacementHistory) == 0 || event.WindowNumber == nil {
			return nil
		}
		return compactionResumeMetadataFromLine(line)
	}
	return nil
}

// attributionEvent is the decoded subset of a `turn_started`, `turn_complete`,
// `turn_aborted` or `user_message` event the reconstruction observes.
type attributionEvent struct {
	kind        string
	turnID      string
	attribution *TurnAttribution
}

func decodeAttributionEvent(payload json.RawMessage) (attributionEvent, bool) {
	if len(payload) == 0 {
		return attributionEvent{}, false
	}
	var decoded rolloutEventPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return attributionEvent{}, false
	}
	turnID := firstNonEmptyString(decoded.TurnID, decoded.TurnIDCamel)
	switch normalizeRolloutEventType(decoded.Type) {
	case "turn_started":
		attribution := decoded.TurnAttribution
		if attribution == nil {
			attribution = decoded.TurnAttributionCamel
		}
		return attributionEvent{kind: "turn_started", turnID: turnID, attribution: attribution}, true
	case "turn_complete":
		return attributionEvent{kind: "turn_complete", turnID: turnID}, true
	case "turn_aborted":
		return attributionEvent{kind: "turn_aborted", turnID: turnID}, true
	case "user_message":
		return attributionEvent{kind: "user_message"}, true
	}
	return attributionEvent{}, false
}

// rolloutLineCountsAsUserTurn mirrors Rust's
// `is_user_turn_boundary(&response_item.item)`: a real (non-contextual) user
// message is a user-turn boundary, and therefore a rollback-droppable segment.
// Contextual user messages (environment context, instructions, notifications)
// are hidden runtime context, not user turns.
func rolloutLineCountsAsUserTurn(line *Line) bool {
	if line == nil || line.Type != "item" || len(line.Item) == 0 {
		return false
	}
	var raw struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(line.Item, &raw); err != nil {
		return false
	}
	if raw.Type != string(eventmap.ResponseMessage) || raw.Role != "user" {
		return false
	}
	content := make([]eventmap.ContentItem, 0, len(raw.Content))
	for _, part := range raw.Content {
		content = append(content, eventmap.ContentItem{Kind: eventmap.ContentKind(part.Type), Text: part.Text})
	}
	return !eventmap.IsContextualUserMessageContent(content)
}

// ReconstructTurnAttribution mirrors Rust #51402
// `RolloutReconstruction::turn_attribution`: it replays the rollout backward and
// returns the provenance of the newest surviving regular turn, or nil when the
// rollout carries none.
func ReconstructTurnAttribution(lines []Line) *TurnAttribution {
	checkpoint := selectAttributionCheckpoint(lines)
	var checkpointTurnID string
	var checkpointAttribution *TurnAttribution
	if checkpoint != nil {
		checkpointAttribution = checkpoint.attribution()
		// Saved attribution still identifies the turn when continuation
		// eligibility was cleared.
		checkpointTurnID = checkpoint.lastStartedTurnID()
		if checkpointTurnID == "" && checkpointAttribution != nil {
			checkpointTurnID = strings.TrimSpace(checkpointAttribution.TurnID)
		}
	}

	// Newest-first list of `turn_started` ids; the cursor advances as the reverse
	// scan passes them (Rust's peekable `started_turn_ids`).
	startedTurnIDs := make([]string, 0, 4)
	for i := range lines {
		if turnID, ok := rolloutTurnStartedLineID(&lines[i]); ok {
			startedTurnIDs = append(startedTurnIDs, turnID)
		}
	}
	for i, j := 0, len(startedTurnIDs)-1; i < j; i, j = i+1, j-1 {
		startedTurnIDs[i], startedTurnIDs[j] = startedTurnIDs[j], startedTurnIDs[i]
	}
	started := 0
	peekStartedTurnID := func() string {
		if started < len(startedTurnIDs) {
			return startedTurnIDs[started]
		}
		return ""
	}

	var turnAttribution *TurnAttribution
	var segment *attributionSegment
	// Rollback is "drop the newest N user turns". While scanning in reverse that
	// becomes "skip the next N user-turn segments we finalize".
	pendingRollbackTurns := 0

	for i := len(lines) - 1; i >= 0; i-- {
		line := &lines[i]
		precedingTurnID := peekStartedTurnID()
		if precedingTurnID == "" {
			precedingTurnID = checkpointTurnID
		}

		if line.ThreadRolledBack != nil {
			pendingRollbackTurns += int(line.ThreadRolledBack.NumTurns)
			continue
		}
		if len(line.TurnContext) > 0 {
			// `TurnContextItem` can attach metadata to an existing segment, but
			// only a real user message makes the segment count as a user turn.
			segment = ensureAttributionSegment(segment)
			if segment.turnID == "" {
				segment.turnID = turnIDFromTurnContext(line.TurnContext)
			}
			continue
		}
		if line.Type == "compacted" || len(line.WorldState) > 0 {
			// Rust's replay opens a segment for compaction and world-state items.
			segment = ensureAttributionSegment(segment)
			continue
		}
		if line.Type == "event_msg" {
			event, ok := decodeAttributionEvent(line.Payload)
			if !ok {
				continue
			}
			switch event.kind {
			case "turn_complete":
				// Stop hooks can finish after a newer turn has started. A
				// terminal event for a preceding turn is not this segment's.
				if !attributionTurnIDsCompatible(precedingTurnID, event.turnID) {
					continue
				}
				segment = ensureAttributionSegment(segment)
				if segment.turnID == "" {
					segment.turnID = event.turnID
				}
			case "turn_aborted":
				if !attributionTurnIDsCompatible(precedingTurnID, event.turnID) {
					continue
				}
				if segment != nil {
					if segment.turnID == "" {
						segment.turnID = event.turnID
					}
				} else if event.turnID != "" {
					segment = &attributionSegment{turnID: event.turnID}
				}
			case "turn_started":
				started++
				// Startup can be suspended before any input or context is
				// written, so explicit start attribution is authoritative.
				if attribution := event.attribution; attribution.Valid() && attribution.TurnID == event.turnID {
					segment = ensureAttributionSegment(segment)
					if attributionTurnIDsCompatible(segment.turnID, event.turnID) {
						segment.turnAttribution = attribution
					}
				}
				// `TurnStarted` is the oldest boundary of the active segment.
				if segment != nil && attributionTurnIDsCompatible(segment.turnID, event.turnID) {
					if segment.turnID == "" {
						segment.turnID = event.turnID
					}
					segment.takeCheckpointAttribution(&checkpointAttribution)
					turnAttribution = orTurnAttribution(turnAttribution, finalizeAttributionSegment(segment, &pendingRollbackTurns))
					segment = nil
				}
			case "user_message":
				segment = ensureAttributionSegment(segment)
				segment.countsAsUserTurn = true
			}
			continue
		}
		if rolloutLineCountsAsUserTurn(line) {
			segment = ensureAttributionSegment(segment)
			segment.countsAsUserTurn = true
		}
	}

	if segment != nil {
		segment.takeCheckpointAttribution(&checkpointAttribution)
		turnAttribution = orTurnAttribution(turnAttribution, finalizeAttributionSegment(segment, &pendingRollbackTurns))
	}
	if pendingRollbackTurns == 0 {
		turnAttribution = orTurnAttribution(turnAttribution, checkpointAttribution)
	}
	return turnAttribution
}

func ensureAttributionSegment(segment *attributionSegment) *attributionSegment {
	if segment == nil {
		return &attributionSegment{}
	}
	return segment
}

func rolloutTurnStartedLineID(line *Line) (string, bool) {
	if line == nil || line.Type != "event_msg" || len(line.Payload) == 0 {
		return "", false
	}
	var payload rolloutEventPayload
	if err := json.Unmarshal(line.Payload, &payload); err != nil {
		return "", false
	}
	if normalizeRolloutEventType(payload.Type) != "turn_started" {
		return "", false
	}
	return firstNonEmptyString(payload.TurnID, payload.TurnIDCamel), true
}
