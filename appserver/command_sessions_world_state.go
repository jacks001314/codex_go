package appserver

import (
	"encoding/json"
	"fmt"
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
	commandSessionStatusRunning = "running"
	commandSessionStatusExited  = "exited"
)

// commandSessionSnapshot is one harness-visible retained session. Only the
// harness-assigned id and fixed lifecycle labels are rendered.
type commandSessionSnapshot struct {
	SessionID int    `json:"sessionId"`
	Status    string `json:"status"`
	InUse     bool   `json:"inUse,omitempty"`
}

// commandSessionsSnapshot mirrors Rust CommandSessionsState: at most 16
// sessions ordered by id, the number of sessions that did not fit, and whether
// write_stdin polling is unavailable for this turn's tool set.
type commandSessionsSnapshot struct {
	Sessions           []commandSessionSnapshot `json:"sessions,omitempty"`
	Omitted            int                      `json:"omitted,omitempty"`
	PollingUnavailable bool                     `json:"pollingUnavailable,omitempty"`
}

func decodeCommandSessionsSnapshot(raw json.RawMessage) (commandSessionsSnapshot, bool) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return commandSessionsSnapshot{}, false
	}
	var snapshot commandSessionsSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return commandSessionsSnapshot{}, false
	}
	return snapshot, true
}

func commandSessionsSnapshotsEqual(a commandSessionsSnapshot, b commandSessionsSnapshot) bool {
	if a.Omitted != b.Omitted || a.PollingUnavailable != b.PollingUnavailable || len(a.Sessions) != len(b.Sessions) {
		return false
	}
	for i := range a.Sessions {
		if a.Sessions[i] != b.Sessions[i] {
			return false
		}
	}
	return true
}

// boundedCommandSessions keeps the lowest 16 session ids like Rust's
// BTreeMap-ordered CommandSessionsState and counts the rest as omitted.
func boundedCommandSessions(sessions []commandSessionSnapshot) ([]commandSessionSnapshot, int) {
	if len(sessions) <= commandSessionsMaxEntries {
		return sessions, 0
	}
	return sessions[:commandSessionsMaxEntries], len(sessions) - commandSessionsMaxEntries
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
	for _, entry := range snapshot.Sessions {
		status := "running"
		if entry.Status == commandSessionStatusExited {
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
	current := commandSessionsSnapshot{}
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
			current.Sessions, current.Omitted = boundedCommandSessions(sessions)
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
