package appserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/features"
	"codex_go/session"
)

// Retained command sessions in incremental world state (Rust #59633,
// codex-rs/core/src/context/world_state/command_sessions.rs). A Code Mode
// script can start a command without returning its session handle; publishing
// the live snapshots keeps the session discoverable so the model can collect
// an exited result with write_stdin. The persisted snapshot is only a
// duplicate-suppression baseline: the live process table stays authoritative.
const (
	commandSessionsSectionID        = "command_sessions"
	commandSessionsContentKind      = "exec.command_sessions"
	commandSessionsMaxEntries       = 16
	commandSessionsInstructionsKind = "command_sessions_world_state"
	commandSessionsOpenMarker       = "<command_sessions>"
	commandSessionsCloseMarker      = "</command_sessions>"
	commandSessionsHeader           = "Current command sessions for this thread (supersedes earlier snapshots). Exited sessions retain results for write_stdin. In-use sessions are temporarily busy with a tool interaction or result finalization.\n"
	commandSessionsPollingNotice    = "write_stdin is unavailable with the current tool set.\n"
	commandSessionsEmptyBody        = "None."
)

const (
	// Rust serializes `CommandSessionStatus` with its variant names
	// (`Running` / `Exited`), while the rendered body uses lowercase prose.
	commandSessionStatusRunning = "Running"
	commandSessionStatusExited  = "Exited"
)

func normalizeCommandSessionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return commandSessionStatusRunning
	case "exited":
		return commandSessionStatusExited
	default:
		return strings.TrimSpace(status)
	}
}

// commandSessionSnapshot is one harness-visible retained session. Only the
// harness-assigned id and fixed lifecycle labels are rendered. The field names
// and the capitalized lifecycle values are Rust's serde shape for
// `CommandSession` / `CommandSessionStatus`
// (codex-rs/core/src/context/world_state/command_sessions.rs).
type commandSessionSnapshot struct {
	SessionID int    `json:"session_id"`
	Status    string `json:"status"`
	InUse     bool   `json:"in_use"`
}

// commandSessionsSnapshot mirrors Rust CommandSessionsState: a BTreeMap of at
// most 16 sessions keyed by id, the number of sessions that did not fit, and
// whether write_stdin polling is unavailable for this turn's tool set. Rust
// always serializes all three fields, so the Go snapshot does too.
type commandSessionsSnapshot struct {
	Sessions           map[string]commandSessionSnapshot `json:"sessions"`
	Omitted            int                               `json:"omitted"`
	PollingUnavailable bool                              `json:"polling_unavailable"`
}

func newCommandSessionsSnapshot(sessions []commandSessionSnapshot) commandSessionsSnapshot {
	snapshot := commandSessionsSnapshot{Sessions: map[string]commandSessionSnapshot{}}
	for _, session := range sessions {
		snapshot.Sessions[fmt.Sprintf("%d", session.SessionID)] = session
	}
	return snapshot
}

// decodeCommandSessionsSnapshot reads Rust's nested-map snapshot. The retired
// Go array shape (`sessionId`/`inUse`/`pollingUnavailable`) stays readable so
// threads written before the alignment keep their duplicate-suppression
// baseline.
func decodeCommandSessionsSnapshot(raw json.RawMessage) (commandSessionsSnapshot, bool) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return commandSessionsSnapshot{}, false
	}
	var current struct {
		Sessions           map[string]commandSessionSnapshot `json:"sessions"`
		Omitted            int                               `json:"omitted"`
		PollingUnavailable bool                              `json:"polling_unavailable"`
	}
	if err := json.Unmarshal(raw, &current); err == nil && current.Sessions != nil {
		for key, session := range current.Sessions {
			session.Status = normalizeCommandSessionStatus(session.Status)
			current.Sessions[key] = session
		}
		return commandSessionsSnapshot(current), true
	}
	var legacy struct {
		Sessions []struct {
			SessionID int    `json:"sessionId"`
			Status    string `json:"status"`
			InUse     bool   `json:"inUse"`
		} `json:"sessions"`
		Omitted            int  `json:"omitted"`
		PollingUnavailable bool `json:"pollingUnavailable"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return commandSessionsSnapshot{}, false
	}
	sessions := make([]commandSessionSnapshot, 0, len(legacy.Sessions))
	for _, session := range legacy.Sessions {
		sessions = append(sessions, commandSessionSnapshot{
			SessionID: session.SessionID,
			Status:    normalizeCommandSessionStatus(session.Status),
			InUse:     session.InUse,
		})
	}
	snapshot := newCommandSessionsSnapshot(sessions)
	snapshot.Omitted = legacy.Omitted
	snapshot.PollingUnavailable = legacy.PollingUnavailable
	return snapshot, true
}

func commandSessionsSnapshotsEqual(a commandSessionsSnapshot, b commandSessionsSnapshot) bool {
	if a.Omitted != b.Omitted || a.PollingUnavailable != b.PollingUnavailable || len(a.Sessions) != len(b.Sessions) {
		return false
	}
	for id, session := range a.Sessions {
		other, ok := b.Sessions[id]
		if !ok || other != session {
			return false
		}
	}
	return true
}

// sortedCommandSessions returns the persisted sessions in ascending id order,
// matching Rust's BTreeMap iteration.
func sortedCommandSessions(snapshot commandSessionsSnapshot) []commandSessionSnapshot {
	ids := make([]int, 0, len(snapshot.Sessions))
	for key := range snapshot.Sessions {
		id := 0
		if _, err := fmt.Sscanf(key, "%d", &id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	sessions := make([]commandSessionSnapshot, 0, len(ids))
	for _, id := range ids {
		if session, ok := snapshot.Sessions[fmt.Sprintf("%d", id)]; ok {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

// boundedCommandSessions keeps the lowest 16 session ids like Rust's
// BTreeMap-ordered CommandSessionsState and counts the rest as omitted.
func boundedCommandSessions(sessions []commandSessionSnapshot) ([]commandSessionSnapshot, int) {
	sorted := append([]commandSessionSnapshot(nil), sessions...)
	sort.Slice(sorted, func(i int, j int) bool { return sorted[i].SessionID < sorted[j].SessionID })
	if len(sorted) <= commandSessionsMaxEntries {
		return sorted, 0
	}
	return sorted[:commandSessionsMaxEntries], len(sorted) - commandSessionsMaxEntries
}

// renderCommandSessionsBody mirrors CommandSessionsState::body byte for byte,
// minus the surrounding fragment markers.
func renderCommandSessionsBody(snapshot commandSessionsSnapshot) string {
	var body strings.Builder
	body.WriteString(commandSessionsHeader)
	if snapshot.PollingUnavailable {
		body.WriteString(commandSessionsPollingNotice)
	}
	if len(snapshot.Sessions) == 0 && snapshot.Omitted == 0 {
		body.WriteString(commandSessionsEmptyBody)
	}
	for _, entry := range sortedCommandSessions(snapshot) {
		status := "running"
		if normalizeCommandSessionStatus(entry.Status) == commandSessionStatusExited {
			status = "exited; poll to collect"
		}
		fmt.Fprintf(&body, "%d: %s", entry.SessionID, status)
		if entry.InUse {
			body.WriteString("; in use")
		}
		body.WriteString("\n")
	}
	if snapshot.Omitted > 0 {
		fmt.Fprintf(&body, "%d additional sessions omitted; this snapshot is incomplete.", snapshot.Omitted)
	}
	return strings.TrimRight(body.String(), "\n")
}

// commandSessionsSectionResult is the section contribution for one request:
// Item is the developer fragment the request must carry (nil when unchanged),
// and SessionItem is its persisted history form.
type commandSessionsSectionResult struct {
	Item        any
	SessionItem session.Item
}

// commandSessionsWorldStateSection computes the command_sessions world-state
// section for one sampling step. force re-renders an unchanged snapshot, which
// compaction needs because the replacement window starts without the earlier
// fragment. When the feature is disabled the section still publishes an empty
// snapshot if the model saw sessions before, which clears stale handles after a
// restart or after the flag is turned off (Rust DisabledCommandSessionsState).
func (r *RuntimeRouter) commandSessionsWorldStateSection(threadID string, turnID string, cfg *config.Config, writeStdinAvailable bool, createdAt time.Time, force bool) (commandSessionsSectionResult, error) {
	if r == nil {
		return commandSessionsSectionResult{}, nil
	}
	threadID = strings.TrimSpace(threadID)
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return commandSessionsSectionResult{}, err
	}
	if record == nil {
		return commandSessionsSectionResult{}, nil
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return commandSessionsSectionResult{}, err
	}
	previous, known := decodeCommandSessionsSnapshot(state.CommandSessions)
	current := newCommandSessionsSnapshot(nil)
	if cfg != nil && features.Enabled(cfg.FeatureSettings(), "command_sessions_world_state") {
		if r.services.UnifiedExec != nil {
			retained := r.services.UnifiedExec.CommandSessions(threadID)
			sessions := make([]commandSessionSnapshot, 0, len(retained))
			for _, entry := range retained {
				status := commandSessionStatusRunning
				if entry.Exited {
					status = commandSessionStatusExited
				}
				sessions = append(sessions, commandSessionSnapshot{
					SessionID: entry.SessionID,
					Status:    status,
					InUse:     entry.InUse,
				})
			}
			kept, omitted := boundedCommandSessions(sessions)
			current = newCommandSessionsSnapshot(kept)
			current.Omitted = omitted
		}
		current.PollingUnavailable = !writeStdinAvailable
	}
	emit := force
	if !known {
		emit = emit || len(current.Sessions) > 0 || current.Omitted > 0 || current.PollingUnavailable
	} else if !commandSessionsSnapshotsEqual(previous, current) {
		emit = true
	}
	if known && commandSessionsSnapshotsEqual(previous, current) && !emit {
		return commandSessionsSectionResult{}, nil
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return commandSessionsSectionResult{}, err
	}
	if !sameJSONValue(state.CommandSessions, encoded) {
		state.CommandSessions = json.RawMessage(encoded)
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return commandSessionsSectionResult{}, err
		}
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return commandSessionsSectionResult{}, err
		}
	}
	if !emit {
		return commandSessionsSectionResult{}, nil
	}
	rendered := contextfrag.RenderStandalone(contextfrag.NewSimpleFragmentWithKind(
		contextfrag.RoleDeveloper,
		commandSessionsOpenMarker,
		commandSessionsCloseMarker,
		"\n"+renderCommandSessionsBody(current)+"\n",
		commandSessionsContentKind,
	))
	item := buildRenderedMessage(rendered)
	sessionItem, ok := commandSessionsWorldStateSessionItem(turnID, item, createdAt)
	if !ok {
		return commandSessionsSectionResult{Item: item}, nil
	}
	return commandSessionsSectionResult{Item: item, SessionItem: sessionItem}, nil
}

func commandSessionsWorldStateSessionItem(turnID string, input any, createdAt time.Time) (session.Item, bool) {
	raw, ok := input.(map[string]any)
	if !ok || strings.TrimSpace(stringFromAny(raw["type"])) != "message" {
		return session.Item{}, false
	}
	role := strings.TrimSpace(stringFromAny(raw["role"]))
	text := strings.TrimSpace(textFromInputItemContent(raw["content"]))
	if role != contextfrag.RoleDeveloper || !strings.Contains(text, commandSessionsOpenMarker) || !strings.Contains(text, commandSessionsCloseMarker) {
		return session.Item{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return session.Item{}, false
	}
	metadata := appTurnMetadata(turnID, map[string]any{
		"kind":             commandSessionsInstructionsKind,
		"hiddenFromThread": true,
	})
	return session.Item{
		ID:        commandSessionsSectionID + "-" + safeIdentifier(turnID),
		Type:      "message",
		Role:      role,
		Text:      text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: createdAt,
		Data: map[string]any{
			"kind":             commandSessionsInstructionsKind,
			"hiddenFromThread": true,
		},
		Metadata: metadata,
		Raw:      encoded,
	}, true
}
