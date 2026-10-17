package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/session"
)

// TestGuardianSessionSourceRolloutLikeRust mirrors Rust #53816: Guardian
// review threads persist SessionSource::SubAgent(SubAgentSource::Other(
// "guardian")), which serializes externally tagged ({"subagent":{"other":
// "guardian"}}) instead of as a plain string. The rollout reader must keep the
// thread metadata instead of dropping the session_meta line.
func TestGuardianSessionSourceRolloutLikeRust(t *testing.T) {
	dir := t.TempDir()
	threadID := "00000000-0000-0000-0000-000000000001"
	parentID := "00000000-0000-0000-0000-0000000000aa"
	path := filepath.Join(dir, "rollout-2025-01-05T12-00-00-"+threadID+".jsonl")
	meta := `{"timestamp":"2025-01-05T12:00:00Z","type":"session_meta","payload":{"session_id":"` + threadID + `","id":"` + threadID + `","timestamp":"2025-01-05T12:00:00Z","cwd":"/repo","originator":"codex","cli_version":"0.0.0","source":{"subagent":{"other":"guardian"}},"parent_thread_id":"` + parentID + `","model_provider":"openai"}}`
	message := `{"timestamp":"2025-01-05T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Guardian review","kind":"plain"}}`
	if err := os.WriteFile(path, []byte(strings.Join([]string{meta, message, ""}, "\n")), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if record.ID != session.ThreadID(threadID) || record.ParentThreadID != session.ThreadID(parentID) {
		t.Fatalf("record lineage = id:%q parent:%q", record.ID, record.ParentThreadID)
	}
	if record.Metadata.Source != session.GuardianReviewSource {
		t.Fatalf("record source = %q, want %q", record.Metadata.Source, session.GuardianReviewSource)
	}
	if !session.IsGuardianReviewSource(record.Metadata.Source) {
		t.Fatalf("IsGuardianReviewSource(%q) = false", record.Metadata.Source)
	}
}

// TestGuardianSessionSourceRoundTripLikeRust keeps the Rust wire form when a
// Guardian session_meta is rewritten, and keeps plain string sources plain.
func TestGuardianSessionSourceRoundTripLikeRust(t *testing.T) {
	raw, err := json.Marshal(SessionMeta{
		ID:        "thread-guardian",
		Timestamp: "2025-01-05T12:00:00Z",
		Source:    session.GuardianReviewSource,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(raw), `"source":{"subagent":{"other":"guardian"}}`) {
		t.Fatalf("guardian source wire form = %s", raw)
	}
	var decoded SessionMeta
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Source != session.GuardianReviewSource {
		t.Fatalf("round-trip source = %q", decoded.Source)
	}

	plain, err := json.Marshal(SessionMeta{ID: "thread-cli", Timestamp: "2025-01-05T12:00:00Z", Source: "cli"})
	if err != nil {
		t.Fatalf("Marshal(cli) error = %v", err)
	}
	if !strings.Contains(string(plain), `"source":"cli"`) {
		t.Fatalf("cli source wire form = %s", plain)
	}
}
