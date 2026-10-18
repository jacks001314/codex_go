package rollout

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust #43848: runtime workspace roots live on SessionMeta itself, not
// only inside the Go legacy extra object, and reading a Rust rollout restores
// them into record metadata for resume.
func TestSessionMetaRuntimeWorkspaceRootsRoundTripLikeRust(t *testing.T) {
	home := t.TempDir()
	roots := []string{"/workspace", "/workspace/extra"}
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home,
		ThreadID:  "thread-roots",
		CWD:       "/workspace",
		Extra:     map[string]any{"runtime_workspace_roots": roots},
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var line struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &line); err != nil {
		t.Fatalf("Unmarshal(session_meta) error = %v", err)
	}
	if _, ok := line.Payload["runtime_workspace_roots"]; !ok {
		t.Fatalf("session_meta lacks top-level runtime_workspace_roots: %s", data)
	}
	var extra map[string]any
	if raw := line.Payload["extra"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &extra); err != nil {
			t.Fatalf("Unmarshal(extra) error = %v", err)
		}
	}
	if _, ok := extra["runtime_workspace_roots"]; ok {
		t.Fatalf("session_meta extra still contains runtime_workspace_roots: %s", data)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	got := stringSliceFromAnyForTest(record.Metadata.Extra["runtime_workspace_roots"])
	if len(got) != len(roots) || got[0] != roots[0] || got[1] != roots[1] {
		t.Fatalf("record runtime roots = %#v, want %#v", got, roots)
	}
}

// Mirrors Rust #43848: only snapshots owned by the resumed thread participate,
// and a snapshot without roots is authoritative instead of falling back to an
// older selection.
func TestLatestPersistedOwnedThreadSettingsLikeRust(t *testing.T) {
	lines := []Line{
		{Payload: json.RawMessage(`{"type":"thread_settings_applied","thread_id":"thread-1","thread_settings":{"cwd":"/a","runtime_workspace_roots":["/a","/b"]}}`)},
		{Payload: json.RawMessage(`{"type":"thread_settings_applied","thread_id":"thread-1","thread_settings":{"cwd":"/c","runtime_workspace_roots":[]}}`)},
		{Payload: json.RawMessage(`{"type":"thread_settings_applied","thread_id":"thread-2","thread_settings":{"cwd":"/foreign","runtime_workspace_roots":["/foreign"]}}`)},
	}
	snapshot, ok := LatestPersistedOwnedThreadSettings("thread-1", lines)
	if !ok {
		t.Fatal("LatestPersistedOwnedThreadSettings() = not found")
	}
	if snapshot.CWD != "/c" {
		t.Fatalf("snapshot cwd = %q, want /c", snapshot.CWD)
	}
	if snapshot.RuntimeWorkspaceRoots == nil || len(*snapshot.RuntimeWorkspaceRoots) != 0 {
		t.Fatalf("snapshot roots = %#v, want an explicit empty list", snapshot.RuntimeWorkspaceRoots)
	}

	withoutRoots := []Line{
		{Payload: json.RawMessage(`{"type":"thread_settings_applied","thread_id":"thread-1","thread_settings":{"cwd":"/a","runtime_workspace_roots":["/a","/b"]}}`)},
		{Payload: json.RawMessage(`{"type":"thread_settings_applied","thread_id":"thread-1","thread_settings":{"cwd":"/d"}}`)},
		{Payload: json.RawMessage(`{"type":"thread_settings_applied","thread_settings":{"cwd":"/ownerless","runtime_workspace_roots":["/ownerless"]}}`)},
	}
	snapshot, ok = LatestPersistedOwnedThreadSettings("thread-1", withoutRoots)
	if !ok {
		t.Fatal("LatestPersistedOwnedThreadSettings(withoutRoots) = not found")
	}
	if snapshot.CWD != "/d" || snapshot.RuntimeWorkspaceRoots != nil {
		t.Fatalf("snapshot = %#v, want cwd /d and unknown roots", snapshot)
	}
	if _, ok := LatestPersistedOwnedThreadSettings("thread-3", withoutRoots); ok {
		t.Fatal("LatestPersistedOwnedThreadSettings(thread-3) found an ownerless snapshot")
	}
}

func stringSliceFromAnyForTest(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
