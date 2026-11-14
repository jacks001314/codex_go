package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayTraceBundleEnforcesSameBinarySchemaLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		line     string
		want     string
	}{
		{
			name:     "manifest mismatch",
			manifest: `{"schema_version":1,"trace_id":"trace"}`,
			line:     `{"schema_version":2,"seq":1,"type":"thread_started","thread_id":"thread"}`,
			want:     "unsupported manifest schema 1",
		},
		{
			name:     "event mismatch",
			manifest: `{"schema_version":2,"trace_id":"trace"}`,
			line:     `{"schema_version":1,"seq":1,"type":"thread_started","thread_id":"thread"}`,
			want:     "unsupported trace event schema 1",
		},
		{
			name:     "missing event schema",
			manifest: `{"schema_version":2,"trace_id":"trace"}`,
			line:     `{"seq":1,"type":"thread_started","thread_id":"thread"}`,
			want:     "unsupported trace event schema",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(tc.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(tc.line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ReplayTraceBundle(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReplayTraceBundle() error = %v, want %q", err, tc.want)
			}
		})
	}
}
