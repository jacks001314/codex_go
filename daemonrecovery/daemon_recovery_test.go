package daemonrecovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFilePathLikeRust pins the shared snapshot location.
func TestFilePathLikeRust(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "app-server-daemon", "loaded-threads.json")
	if got := FilePath(home); got != want {
		t.Fatalf("FilePath() = %q, want %q", got, want)
	}
}

// TestReadSnapshotTreatsMissingFileAsEmptyLikeRust pins the default snapshot.
func TestReadSnapshotTreatsMissingFileAsEmptyLikeRust(t *testing.T) {
	snapshot, err := ReadSnapshot(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("ReadSnapshot(missing) error = %v", err)
	}
	if len(snapshot.Loaded) != 0 || len(snapshot.Interrupted) != 0 {
		t.Fatalf("snapshot = %#v, want empty", snapshot)
	}
}

// TestReadSnapshotRejectsMalformedJSONLikeRust pins that a corrupt file is an
// error rather than an empty snapshot.
func TestReadSnapshotRejectsMalformedJSONLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if _, err := ReadSnapshot(path); err == nil {
		t.Fatal("ReadSnapshot(malformed) returned nil error")
	}
}

// TestWriteCandidatesWritesASortedUniqueArrayLikeRust pins the candidate file
// shape: a JSON array, deduplicated and ordered like a BTreeSet.
func TestWriteCandidatesWritesASortedUniqueArrayLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", FileName)
	if err := WriteCandidates(path, []string{"b", "a", "b"}); err != nil {
		t.Fatalf("WriteCandidates error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if string(raw) != `["a","b"]` {
		t.Fatalf("candidates = %s, want a sorted unique array", raw)
	}
	candidates, err := ReadCandidates(path)
	if err != nil {
		t.Fatalf("ReadCandidates error = %v", err)
	}
	if len(candidates) != 2 || candidates[0] != "a" || candidates[1] != "b" {
		t.Fatalf("candidates = %#v", candidates)
	}
}

// TestWriteCandidatesWithNoCandidatesWritesAnEmptyArrayLikeRust pins `[]`
// rather than `null`.
func TestWriteCandidatesWithNoCandidatesWritesAnEmptyArrayLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := WriteCandidates(path, nil); err != nil {
		t.Fatalf("WriteCandidates(nil) error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if string(raw) != `[]` {
		t.Fatalf("candidates = %s, want []", raw)
	}
}

// TestSnapshotRoundTripCarriesInterruptedTurnsLikeRust pins the metadata entry:
// the interrupt detail travels in the same atomic file, older readers skip it,
// and turns for threads that are no longer loaded are dropped.
func TestSnapshotRoundTripCarriesInterruptedTurnsLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	tier := "priority"
	schema := json.RawMessage(`{"type":"object"}`)
	snapshot := Snapshot{
		Loaded: []string{"thread-1", "thread-2"},
		Interrupted: map[string]InterruptedTurn{
			"thread-1": {
				TurnID:             "turn-1",
				OutputSchema:       schema,
				ServiceTier:        &tier,
				CyberAccessProgram: json.RawMessage(`"daybreak_blue"`),
				LocalEnvironment:   json.RawMessage(`{"environmentId":"local","cwd":"/w","runtimeWorkspaceRoots":["/w"]}`),
			},
			"thread-dropped": {TurnID: "turn-dropped"},
		},
	}
	if err := WriteSnapshot(path, snapshot); err != nil {
		t.Fatalf("WriteSnapshot error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("candidates are not a JSON array: %v (%s)", err, raw)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %#v, want two candidates plus the metadata entry", entries)
	}
	if entries[0] != "thread-1" || entries[1] != "thread-2" {
		t.Fatalf("candidate entries = %#v", entries[:2])
	}
	if _, ok := cutPrefix(entries[2], interruptionPrefix); !ok {
		t.Fatalf("metadata entry = %q, want the interruption prefix", entries[2])
	}

	// An old reader sees only the thread IDs it understands.
	candidates, err := ReadCandidates(path)
	if err != nil {
		t.Fatalf("ReadCandidates error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v", candidates)
	}

	read, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot error = %v", err)
	}
	if len(read.Loaded) != 2 || read.Loaded[0] != "thread-1" || read.Loaded[1] != "thread-2" {
		t.Fatalf("loaded = %#v", read.Loaded)
	}
	if len(read.Interrupted) != 1 {
		t.Fatalf("interrupted = %#v, want only the loaded thread's turn", read.Interrupted)
	}
	turn, ok := read.Interrupted["thread-1"]
	if !ok || turn.TurnID != "turn-1" || turn.ServiceTier == nil || *turn.ServiceTier != "priority" {
		t.Fatalf("interrupted turn = %#v", turn)
	}
	if string(turn.CyberAccessProgram) != `"daybreak_blue"` {
		t.Fatalf("cyber access program = %s, want the Rust snake_case value", turn.CyberAccessProgram)
	}
	if string(turn.OutputSchema) != `{"type":"object"}` {
		t.Fatalf("output schema = %s", turn.OutputSchema)
	}
}

// TestReadSnapshotIgnoresUnparsableMetadataEntryLikeRust pins that a broken
// metadata blob leaves the candidates intact.
func TestReadSnapshotIgnoresUnparsableMetadataEntryLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	contents := `["thread-1","` + interruptionPrefix + `{"]`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot error = %v", err)
	}
	if len(snapshot.Loaded) != 1 || snapshot.Loaded[0] != "thread-1" || len(snapshot.Interrupted) != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func cutPrefix(value string, prefix string) (string, bool) {
	if len(value) < len(prefix) || value[:len(prefix)] != prefix {
		return value, false
	}
	return value[len(prefix):], true
}

// TestWriteSnapshotKeepsAbsentOptionalsAsNullLikeRust pins the on-disk shape of
// an interrupted turn whose optionals are absent: Rust's InterruptedTurn has no
// skip_serializing_if, so it writes `null` rather than dropping the field
// (app-server-transport/src/daemon_recovery.rs).
func TestWriteSnapshotKeepsAbsentOptionalsAsNullLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := WriteSnapshot(path, Snapshot{
		Loaded:      []string{"thread-1"},
		Interrupted: map[string]InterruptedTurn{"thread-1": {TurnID: "turn-1"}},
	}); err != nil {
		t.Fatalf("WriteSnapshot error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("entries are not a JSON array: %v (%s)", err, raw)
	}
	metadata := ""
	for _, entry := range entries {
		if rest, ok := cutPrefix(entry, interruptionPrefix); ok {
			metadata = rest
		}
	}
	if metadata == "" {
		t.Fatalf("entries = %#v, want a metadata entry", entries)
	}
	for _, want := range []string{
		`"turn_id":"turn-1"`,
		`"output_schema":null`,
		`"service_tier":null`,
		`"cyber_access_program":null`,
		`"local_environment":null`,
	} {
		if !strings.Contains(metadata, want) {
			t.Fatalf("metadata entry = %s, want %s", metadata, want)
		}
	}

	// The shape round-trips: absent optionals read back as absent, not as a
	// literal null value.
	read, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot error = %v", err)
	}
	got := read.Interrupted["thread-1"]
	if got.TurnID != "turn-1" || got.ServiceTier != nil || got.OutputSchema != nil || got.LocalEnvironment != nil {
		t.Fatalf("interrupted turn = %#v", got)
	}
}
