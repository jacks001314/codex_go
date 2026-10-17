package rollout

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #47026's bounded MetadataReadError reasons used by rollout
// compression diagnostics.
func TestFirstSessionMetaClassifiesMetadataReadFailuresLikeRust(t *testing.T) {
	dir := t.TempDir()
	sessionMetaLine := func(id string, historyMode string) string {
		t.Helper()
		payload := map[string]any{
			"id":           id,
			"session_id":   id,
			"timestamp":    "2025-01-01T00:00:00Z",
			"cwd":          "/workspace",
			"originator":   "test",
			"cli_version":  "0.0.0-test",
			"history_mode": historyMode,
		}
		return string(mustJSON(t, map[string]any{"type": "session_meta", "payload": payload})) + "\n"
	}
	cases := []struct {
		name       string
		contents   string
		wantReason string
	}{
		{
			name:       "invalid history mode",
			contents:   sessionMetaLine("invalid-mode", "future"),
			wantReason: "invalid_history_mode",
		},
		{
			name:       "untrimmed history mode",
			contents:   sessionMetaLine("untrimmed-mode", " legacy "),
			wantReason: "invalid_history_mode",
		},
		{
			name:       "response before metadata",
			contents:   string(mustJSON(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{}}})) + "\n",
			wantReason: "response_before_metadata",
		},
		{
			name: "response after unrelated line",
			contents: string(mustJSON(t, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started"}})) + "\n" +
				string(mustJSON(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{}}})) + "\n",
			wantReason: "response_before_metadata",
		},
		{
			name:       "missing metadata",
			contents:   string(mustJSON(t, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started"}})) + "\n",
			wantReason: "missing_metadata",
		},
		{
			name:       "empty rollout",
			contents:   "",
			wantReason: "missing_metadata",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".jsonl")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			_, err := FirstSessionMeta(path)
			var metadataErr *metadataReadError
			if !errors.As(err, &metadataErr) {
				t.Fatalf("FirstSessionMeta() error = %#v, want *metadataReadError", err)
			}
			if metadataErr.Reason != tc.wantReason {
				t.Fatalf("metadata reason = %q, want %q", metadataErr.Reason, tc.wantReason)
			}
			if reason := metadataReadReason(err); reason != tc.wantReason {
				t.Fatalf("metadataReadReason() = %q, want %q", reason, tc.wantReason)
			}
		})
	}

	for _, mode := range []string{"", "legacy", "paginated"} {
		path := filepath.Join(dir, "valid-"+mode+".jsonl")
		contents := sessionMetaLine("valid-"+mode, mode)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile(valid %q) error = %v", mode, err)
		}
		meta, err := FirstSessionMeta(path)
		if err != nil {
			t.Fatalf("FirstSessionMeta(valid %q) error = %v", mode, err)
		}
		if meta.HistoryMode != mode {
			t.Fatalf("history mode = %q, want %q", meta.HistoryMode, mode)
		}
	}

	if reason := metadataReadReason(os.ErrNotExist); reason != "io" {
		t.Fatalf("metadataReadReason(io) = %q, want io", reason)
	}
}

// Mirrors Rust #47026's compression diagnostics: every metadata failure is
// counted, at most five warnings are emitted, and warnings contain only bounded
// labels plus the parsed rollout id.
func TestRolloutCompressionMetadataDiagnosticsLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("bad-%d", i)
		path := filepath.Join(root, "rollout-2025-01-01T00-00-00-"+id+".jsonl")
		writeTestRollout(t, path, id, "future")
	}

	recorder := installRolloutMetricsRecorder(t)
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	if err := RunRolloutCompression(context.Background(), home, RolloutCompressionTriggerRPC); err != nil {
		t.Fatalf("RunRolloutCompression() error = %v", err)
	}

	samples := recorder.samples(rolloutCompressionScanCounter)
	if !hasRolloutMetricSample(samples, map[string]string{
		"outcome":    "failed",
		"stage":      "read_metadata",
		"error_kind": "other",
		"trigger":    "rpc",
		"reason":     "invalid_history_mode",
	}) {
		t.Fatalf("scan counters = %#v, want a reasoned metadata failure", samples)
	}

	logText := logs.String()
	warnings := []string{}
	for _, line := range strings.Split(logText, "\n") {
		if strings.Contains(line, "skipping rollout compression because session metadata could not be read") {
			warnings = append(warnings, line)
		}
	}
	if len(warnings) != maxMetadataWarningsPerRun {
		t.Fatalf("metadata warnings = %d, want %d: %s", len(warnings), maxMetadataWarningsPerRun, logText)
	}
	for _, line := range warnings {
		for _, want := range []string{
			"rollout_id=bad-",
			"reason=invalid_history_mode",
			"error_kind=other",
			"file_size_bytes=",
			"mtime_age_seconds=",
		} {
			if !strings.Contains(line, want) {
				t.Fatalf("metadata warning missing %q: %s", want, line)
			}
		}
		if strings.Contains(line, "file_size_bytes=0x") || strings.Contains(line, "mtime_age_seconds=0x") {
			t.Fatalf("metadata warning logged a pointer instead of a value: %s", line)
		}
	}
	if strings.Contains(logText, root) {
		t.Fatalf("metadata diagnostics leaked the rollout path: %s", logText)
	}

	completionLogs := 0
	for _, line := range strings.Split(logText, "\n") {
		if strings.Contains(line, "rollout compression worker finished") {
			completionLogs++
			if !strings.Contains(line, "metadata_read_failures=7") {
				t.Fatalf("completion log missing metadata failure count: %s", line)
			}
		}
	}
	if completionLogs != 1 {
		t.Fatalf("completion logs = %d, want 1", completionLogs)
	}
}
