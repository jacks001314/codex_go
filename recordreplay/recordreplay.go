// Package recordreplay implements the djalign L2 record-replay layer
// (recording-driven contract verification, no tokens).
//
// The layer turns Rust-recorded session traces (for example
// tui/tests/fixtures/oss-story.jsonl in the Rust checkout) into a reusable
// dynamic facility:
//
//   - Parse reads the recording into a normalized contract view that drops
//     unstable fields (timestamps, machine-specific paths, model text) while
//     keeping the observable surface: event direction/kind, app-event
//     variants, codex message types, task (turn) ids and their lifecycle.
//   - Summarize renders a deterministic structural digest of the normalized
//     trace; the digest is frozen as a Go-side golden so upstream recording
//     drift is flagged on every sync (junction point 1).
//   - Replay drives Go's durable-session machinery (rollout.Recorder in
//     paginated mode) with the recording's task lifecycle and returns the
//     persisted structure so tests can compare event sequence, item states
//     and recoverability against the recording (junction point 2: any
//     recording or recorder drift becomes a failing contract).
//
// Model text (deltas/messages) is never compared; only its presence and the
// surrounding structure are.
package recordreplay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"codex_go/rollout"
)

// Event is the normalized contract view of one recorded line.
//
// Normalization rules (documented per djalign dynamic-layer requirements):
//   - ts is dropped: wall-clock timestamps are not observable contract.
//   - session_start cwd / model_provider_id / model_provider_name are dropped:
//     machine-specific absolute paths and provider naming details.
//   - codex_event delta / message / last_agent_message content is replaced by
//     the HasText presence flag: model text is explicitly out of contract.
//   - key_event's Rust Debug string and log_line content are dropped
//     (presence only): they are not part of the app-server/core surface.
//   - insert_history.lines is kept: a stable numeric surface.
type Event struct {
	Dir       string `json:"dir"`                  // meta / to_tui / from_tui
	Kind      string `json:"kind"`                 // session_start / app_event / codex_event / ...
	Variant   string `json:"variant,omitempty"`    // app_event variant
	MsgType   string `json:"msg_type,omitempty"`   // codex_event msg.type
	ID        string `json:"id,omitempty"`         // codex_event payload.id (task/turn id)
	SessionID string `json:"session_id,omitempty"` // session_configured session id
	Model     string `json:"model,omitempty"`      // session_start / session_configured model
	HasText   bool   `json:"has_text,omitempty"`   // presence of model text (delta/message)
	Lines     int    `json:"lines,omitempty"`      // insert_history line count
}

// rawLine mirrors the stable envelope fields shared by every recording line.
type rawLine struct {
	TS      string          `json:"ts"`
	Dir     string          `json:"dir"`
	Kind    string          `json:"kind"`
	Variant string          `json:"variant"`
	Event   string          `json:"event"`
	Line    string          `json:"line"`
	Lines   int             `json:"lines"`
	CWD     string          `json:"cwd"`
	Model   string          `json:"model"`
	Payload json.RawMessage `json:"payload"`
}

// rawCodexMsg is the codex_event msg payload surface we keep.
type rawCodexMsg struct {
	Type             string `json:"type"`
	SessionID        string `json:"session_id"`
	Model            string `json:"model"`
	Delta            string `json:"delta"`
	Message          string `json:"message"`
	LastAgentMessage string `json:"last_agent_message"`
}

// Parse reads a Rust-recorded JSONL trace and returns the normalized
// contract view. Lines are read byte-wise (no Scanner token limit), so long
// embedded outputs survive, mirroring rollout.Load's long-line handling.
func Parse(path string) ([]Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 16<<20)
	var events []Event
	for {
		raw, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 {
			ev, parseErr := normalize(trimmed)
			if parseErr != nil {
				return nil, fmt.Errorf("%s line %d: %w", path, len(events)+1, parseErr)
			}
			events = append(events, ev)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, readErr
		}
	}
	return events, nil
}

func normalize(raw []byte) (Event, error) {
	var line rawLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return Event{}, err
	}
	ev := Event{Dir: line.Dir, Kind: line.Kind, Variant: line.Variant, Lines: line.Lines}
	switch line.Kind {
	case "session_start":
		ev.Model = line.Model
	case "session_end":
		// presence only
	case "app_event":
		// variant kept above
	case "codex_event":
		var payload struct {
			ID  string      `json:"id"`
			Msg rawCodexMsg `json:"msg"`
		}
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			return Event{}, fmt.Errorf("codex_event payload: %w", err)
		}
		ev.ID = payload.ID
		ev.MsgType = payload.Msg.Type
		ev.SessionID = payload.Msg.SessionID
		ev.Model = payload.Msg.Model
		ev.HasText = payload.Msg.Delta != "" || payload.Msg.Message != "" || payload.Msg.LastAgentMessage != ""
	case "key_event", "log_line", "insert_history":
		// content dropped by design; presence and order are kept.
	default:
		// unknown kinds are preserved as presence events so recording drift is
		// not silently swallowed.
	}
	return ev, nil
}

// TaskSummary is the ordered msg-type lifecycle of one codex task id.
type TaskSummary struct {
	ID       string   `json:"id"`
	MsgTypes []string `json:"msg_types"`
}

// Digest is a deterministic structural digest of a normalized trace. Map
// fields marshal in sorted key order, so the JSON rendering is stable.
type Digest struct {
	TotalEvents   int            `json:"total_events"`
	SessionModel  string         `json:"session_model,omitempty"`
	SessionEnd    bool           `json:"session_end"`
	AppVariants   map[string]int `json:"app_event_variants"`
	MsgTypes      map[string]int `json:"codex_msg_types"`
	KeyEvents     int            `json:"key_events"`
	InsertHistory []int          `json:"insert_history_lines"`
	LogLines      int            `json:"log_lines"`
	Tasks         []TaskSummary  `json:"tasks"`
	OtherKinds    map[string]int `json:"other_kinds"`
}

// Summarize renders the structural digest of the normalized events.
func Summarize(events []Event) Digest {
	d := Digest{
		AppVariants: map[string]int{},
		MsgTypes:    map[string]int{},
		OtherKinds:  map[string]int{},
	}
	taskOrder := []string{}
	taskSeen := map[string]bool{}
	for _, ev := range events {
		d.TotalEvents++
		switch ev.Kind {
		case "session_start":
			d.SessionModel = ev.Model
		case "session_end":
			d.SessionEnd = true
		case "app_event":
			d.AppVariants[ev.Variant]++
		case "codex_event":
			d.MsgTypes[ev.MsgType]++
			if ev.ID != "" && !taskSeen[ev.ID] {
				taskSeen[ev.ID] = true
				taskOrder = append(taskOrder, ev.ID)
			}
		case "key_event":
			d.KeyEvents++
		case "insert_history":
			d.InsertHistory = append(d.InsertHistory, ev.Lines)
		case "log_line":
			d.LogLines++
		default:
			d.OtherKinds[ev.Kind]++
		}
	}
	for _, id := range taskOrder {
		summary := TaskSummary{ID: id}
		for _, ev := range events {
			if ev.Kind == "codex_event" && ev.ID == id {
				summary.MsgTypes = append(summary.MsgTypes, ev.MsgType)
			}
		}
		d.Tasks = append(d.Tasks, summary)
	}
	return d
}

// TaskLifecycle is the recorded lifecycle of one task (turn) id.
type TaskLifecycle struct {
	ID          string
	MsgTypes    []string
	HasComplete bool
}

// TaskLifecycles returns the ordered per-id codex_event lifecycles that have
// a task_started; ids that only carry session/shutdown messages are excluded
// from the replay (they have no turn lifecycle).
func TaskLifecycles(events []Event) []TaskLifecycle {
	order := []string{}
	started := map[string]bool{}
	byID := map[string][]string{}
	for _, ev := range events {
		if ev.Kind != "codex_event" || ev.ID == "" {
			continue
		}
		if ev.MsgType == "task_started" && !started[ev.ID] {
			started[ev.ID] = true
			order = append(order, ev.ID)
		}
		if _, ok := byID[ev.ID]; !ok {
			byID[ev.ID] = []string{}
		}
		byID[ev.ID] = append(byID[ev.ID], ev.MsgType)
	}
	var out []TaskLifecycle
	for _, id := range order {
		lc := TaskLifecycle{ID: id, MsgTypes: byID[id]}
		for _, msgType := range byID[id] {
			if msgType == "task_complete" {
				lc.HasComplete = true
			}
		}
		out = append(out, lc)
	}
	return out
}

// ReplayResult reports the persisted rollout structure produced by replaying
// a recording's task lifecycle through a Go rollout.Recorder (paginated mode).
type ReplayResult struct {
	Path          string
	TaskStarts    []string // turn ids of task_started lines, persisted order
	TaskCompletes []string // turn ids of task_complete lines, persisted order
	ItemIDs       []string // item ids of item_started lines, persisted order
}

// Replay drives a Go rollout.Recorder with the recording's task lifecycle:
// one turn per recorded task id, with a canonical message item started and
// completed inside the turn. The returned result exposes the persisted
// structure so tests can compare it with the recording's task order and
// lifecycle. It never calls a model provider.
func Replay(events []Event, codexHome string) (*ReplayResult, error) {
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:   codexHome,
		SessionID:   "replay-session",
		ThreadID:    "replay-thread",
		HistoryMode: "paginated",
		Source:      "recordreplay",
	})
	if err != nil {
		return nil, err
	}
	now := time.Unix(1700000000, 0).UTC() // fixed deterministic clock
	for _, lc := range TaskLifecycles(events) {
		if err := recorder.AppendTurnStarted(lc.ID, now); err != nil {
			recorder.Close()
			return nil, err
		}
		item, err := json.Marshal(rollout.Item{
			ID:   lc.ID,
			Type: "message",
			Role: "assistant",
			Text: "<model text omitted from contract>",
		})
		if err != nil {
			recorder.Close()
			return nil, err
		}
		if err := recorder.AppendItemStarted(item, lc.ID, now); err != nil {
			recorder.Close()
			return nil, err
		}
		if err := recorder.AppendItemCompleted(item, lc.ID, now, now); err != nil {
			recorder.Close()
			return nil, err
		}
		if err := recorder.AppendTurnComplete(lc.ID, now, 0); err != nil {
			recorder.Close()
			return nil, err
		}
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		return nil, err
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return nil, err
	}
	result := &ReplayResult{Path: path}
	for _, line := range lines {
		if line.Type != "event_msg" {
			continue
		}
		var payload struct {
			Type   string          `json:"type"`
			TurnID string          `json:"turn_id"`
			Item   json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			return nil, fmt.Errorf("event_msg payload: %w", err)
		}
		switch payload.Type {
		case "task_started":
			result.TaskStarts = append(result.TaskStarts, payload.TurnID)
		case "task_complete":
			result.TaskCompletes = append(result.TaskCompletes, payload.TurnID)
		case "item_started":
			var item rollout.Item
			if err := json.Unmarshal(payload.Item, &item); err != nil {
				return nil, fmt.Errorf("item_started item: %w", err)
			}
			result.ItemIDs = append(result.ItemIDs, item.ID)
		}
	}
	return result, nil
}

// RustRoot resolves the Rust checkout root, honoring CODEX_RUST_ROOT and the
// same sibling-checkout candidates used by the parity verifier tests.
func RustRoot() (string, error) {
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_ROOT"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs"),
		filepath.Join("..", "codex-main", "codex-rs"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(abs, "Cargo.toml")); err == nil && !info.IsDir() {
			return abs, nil
		}
	}
	return "", errors.New("Rust checkout not found; set CODEX_RUST_ROOT to the codex-rs root")
}

// DefaultStoryRecordingPath returns the Rust oss-story.jsonl recording path
// under the resolved Rust root.
func DefaultStoryRecordingPath() (string, error) {
	root, err := RustRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "tui", "tests", "fixtures", "oss-story.jsonl")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("Rust recording %s: %w", path, err)
	}
	return path, nil
}

// equalStrings reports whether two slices hold the same strings in order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ValidateRecordingReplayCrossCheck checks that the replay structure and the
// recording agree: every recorded task started/completed exactly once in the
// same order, and the message items were persisted for the same ids. It is
// kept exported so the parity manifest verifier and ad-hoc diagnostics can
// share the same assertion logic.
func ValidateRecordingReplayCrossCheck(events []Event, result *ReplayResult) error {
	var wantStarts, wantCompletes []string
	for _, lc := range TaskLifecycles(events) {
		wantStarts = append(wantStarts, lc.ID)
		if lc.HasComplete {
			wantCompletes = append(wantCompletes, lc.ID)
		}
	}
	if !equalStrings(result.TaskStarts, wantStarts) {
		return fmt.Errorf("task_started order mismatch: got %v want %v", result.TaskStarts, wantStarts)
	}
	if !equalStrings(result.TaskCompletes, wantCompletes) {
		return fmt.Errorf("task_complete order mismatch: got %v want %v", result.TaskCompletes, wantCompletes)
	}
	if !equalStrings(result.ItemIDs, wantStarts) {
		return fmt.Errorf("item id order mismatch: got %v want %v", result.ItemIDs, wantStarts)
	}
	return nil
}
