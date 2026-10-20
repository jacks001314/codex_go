package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverExternalSessionsSkipsBackgroundedClaudeOriginal(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".claude")
	project := filepath.Join(root, "repo")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(home, "projects", "repo")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	user := map[string]any{
		"type": "user", "cwd": project, "timestamp": timestamp,
		"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "plan a vacation"}}},
	}
	assistant := map[string]any{
		"type": "assistant", "cwd": project, "timestamp": timestamp,
		"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Here is your itinerary"}}},
	}
	writeJSONLines := func(name string, records ...any) string {
		t.Helper()
		path := filepath.Join(sessionDir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeJSONLines("original.jsonl", user, map[string]any{
		"type": "continued-in", "sessionId": "original", "continuedInSessionId": "background",
	})
	continuation := writeJSONLines("background.jsonl", user, assistant)

	sessions := discoverExternalSessions(home)
	if len(sessions) != 1 || filepath.Clean(sessions[0].Path) != filepath.Clean(continuation) {
		t.Fatalf("sessions = %#v, want only %s", sessions, continuation)
	}
	if sessions[0].Title == nil || *sessions[0].Title != "plan a vacation" {
		t.Fatalf("title = %#v", sessions[0].Title)
	}
}
