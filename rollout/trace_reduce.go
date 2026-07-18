package rollout

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const ReducedStateFileName = "state.json"

type TraceReduceOptions struct {
	BundleDir string
	Output    string
}

type ReducedTrace struct {
	Manifest      map[string]any      `json:"manifest"`
	EventCount    int                 `json:"event_count"`
	ParseErrors   int                 `json:"parse_errors"`
	Threads       []string            `json:"threads"`
	EventTypes    map[string]int      `json:"event_types"`
	PayloadRefs   []string            `json:"payload_refs"`
	FirstEventSeq any                 `json:"first_event_seq,omitempty"`
	LastEventSeq  any                 `json:"last_event_seq,omitempty"`
	Events        []ReducedTraceEvent `json:"events"`
}

type ReducedTraceEvent struct {
	Seq       any            `json:"seq,omitempty"`
	Type      string         `json:"type,omitempty"`
	ThreadID  string         `json:"thread_id,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	PayloadID string         `json:"payload_id,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
}

func ReduceTraceBundle(options *TraceReduceOptions) (string, *ReducedTrace, error) {
	if options == nil {
		return "", nil, errors.New("trace reduce options are required")
	}
	bundleDir := strings.TrimSpace(options.BundleDir)
	if bundleDir == "" {
		return "", nil, errors.New("trace bundle is required")
	}
	output := strings.TrimSpace(options.Output)
	if output == "" {
		output = filepath.Join(bundleDir, ReducedStateFileName)
	}
	reduced, err := ReplayTraceBundle(bundleDir)
	if err != nil {
		return "", nil, err
	}
	data, err := json.MarshalIndent(reduced, "", "  ")
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(output, append(data, '\n'), 0o600); err != nil {
		return "", nil, err
	}
	return output, reduced, nil
}

func ReplayTraceBundle(bundleDir string) (*ReducedTrace, error) {
	manifest, err := readTraceManifest(filepath.Join(bundleDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(bundleDir, "trace.jsonl"))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reduced := &ReducedTrace{
		Manifest:   manifest,
		EventTypes: map[string]int{},
	}
	threadSet := map[string]bool{}
	payloadSet := map[string]bool{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		raw := map[string]any{}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			reduced.ParseErrors++
			continue
		}
		event := reduceTraceEvent(raw)
		if reduced.EventCount == 0 {
			reduced.FirstEventSeq = event.Seq
		}
		reduced.LastEventSeq = event.Seq
		reduced.EventCount++
		if event.Type != "" {
			reduced.EventTypes[event.Type]++
		}
		if event.ThreadID != "" && !threadSet[event.ThreadID] {
			threadSet[event.ThreadID] = true
			reduced.Threads = append(reduced.Threads, event.ThreadID)
		}
		if event.PayloadID != "" && !payloadSet[event.PayloadID] {
			payloadSet[event.PayloadID] = true
			reduced.PayloadRefs = append(reduced.PayloadRefs, event.PayloadID)
		}
		reduced.Events = append(reduced.Events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if reduced.Threads == nil {
		reduced.Threads = []string{}
	}
	if reduced.PayloadRefs == nil {
		reduced.PayloadRefs = []string{}
	}
	if reduced.Events == nil {
		reduced.Events = []ReducedTraceEvent{}
	}
	return reduced, nil
}

func readTraceManifest(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	manifest := map[string]any{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return manifest, nil
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func reduceTraceEvent(raw map[string]any) ReducedTraceEvent {
	event := ReducedTraceEvent{
		Seq:       firstTraceValue(raw, "seq", "sequence"),
		Type:      firstTraceString(raw, "type", "event", "kind"),
		ThreadID:  firstTraceString(raw, "thread_id", "threadId", "root_thread_id", "rootThreadId"),
		TurnID:    firstTraceString(raw, "turn_id", "turnId"),
		PayloadID: firstTraceString(raw, "payload_id", "payloadId", "payload_ref", "payloadRef"),
		Raw:       raw,
	}
	if event.PayloadID == "" {
		event.PayloadID = nestedTracePayloadID(raw)
	}
	return event
}

func firstTraceValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value
		}
	}
	return nil
}

func firstTraceString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok {
			return value
		}
	}
	return ""
}

func nestedTracePayloadID(raw map[string]any) string {
	payload, ok := raw["payload"].(map[string]any)
	if !ok {
		return ""
	}
	return firstTraceString(payload, "id", "payload_id", "payloadId", "path")
}
