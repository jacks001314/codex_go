package rollout

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const ReducedStateFileName = "state.json"

type TraceReduceOptions struct {
	BundleDir string
	Output    string
}

type ReducedTrace struct {
	Manifest map[string]any `json:"manifest"`
	// RecordingComplete reports that the writer's complete recording marker was
	// replayed. Rust #58172 only accepts a marker whose line was fully written,
	// so a short write or copy of the final record leaves this false.
	RecordingComplete bool                `json:"recording_complete"`
	EventCount        int                 `json:"event_count"`
	ParseErrors       int                 `json:"parse_errors"`
	Threads           []string            `json:"threads"`
	EventTypes        map[string]int      `json:"event_types"`
	PayloadRefs       []string            `json:"payload_refs"`
	FirstEventSeq     any                 `json:"first_event_seq,omitempty"`
	LastEventSeq      any                 `json:"last_event_seq,omitempty"`
	Events            []ReducedTraceEvent `json:"events"`
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
	payloadPaths := map[string]bool{}
	reader := bufio.NewReaderSize(file, 64*1024)
	for {
		line, readErr := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if readErr != nil {
				break
			}
			continue
		}
		raw := map[string]any{}
		if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
			reduced.ParseErrors++
			if readErr != nil {
				break
			}
			continue
		}
		// Rust #58172: a short write or copy can leave the entire marker except
		// its final newline. Only the writer's complete record establishes clean
		// closure, so an unterminated completed marker ends replay without
		// applying the event.
		if traceRecordedCompletion(raw) && !strings.HasSuffix(line, "\n") {
			break
		}
		for _, path := range tracePayloadPaths(raw) {
			payloadPaths[path] = true
		}
		if traceRecordedCompletion(raw) {
			reduced.RecordingComplete = true
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
		if readErr != nil {
			break
		}
	}
	// Rust #58172 validates every payload the events reference: a truncated or
	// non-UTF-8 body is an incomplete export, not an opaque payload.
	for _, path := range sortedTracePayloadPaths(payloadPaths) {
		if err := validateTracePayload(filepath.Join(bundleDir, path)); err != nil {
			return nil, err
		}
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

// traceRecordedCompletion reports whether a raw event is the writer's final
// recording-completed marker.
func traceRecordedCompletion(raw map[string]any) bool {
	payload, ok := raw["payload"].(map[string]any)
	if !ok {
		return false
	}
	return firstTraceString(payload, "type") == "recording_completed"
}

// tracePayloadPaths collects the bundle-local payload files one raw event
// references.
func tracePayloadPaths(raw map[string]any) []string {
	var paths []string
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			id := firstTraceString(typed, "raw_payload_id", "rawPayloadId")
			if id != "" {
				if path := firstTraceString(typed, "path"); path != "" {
					paths = append(paths, path)
				}
			}
			for _, nested := range typed {
				walk(nested)
			}
		case []any:
			for _, nested := range typed {
				walk(nested)
			}
		}
	}
	walk(raw)
	return paths
}

func sortedTracePayloadPaths(paths map[string]bool) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// validateTracePayload mirrors Rust #58172: the body must be UTF-8 text holding
// a complete JSON document. A nonempty file alone is not evidence that a copy
// finished writing JSON.
func validateTracePayload(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read payload %s: %w", path, err)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("read payload %s: invalid UTF-8", path)
	}
	var body any
	if err := json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("invalid payload %s: %w", path, err)
	}
	return nil
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
