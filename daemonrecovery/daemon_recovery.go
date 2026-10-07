// Package daemonrecovery holds the shared on-disk candidate set a managed
// daemon restart reads back (Rust
// app-server-transport/src/daemon_recovery.rs).
//
// The file is a JSON array of thread IDs. When interrupted turns need to
// continue automatically, one extra array entry carries the whole snapshot
// behind a version prefix, so older servers skip it while reading the
// candidates they understand.
package daemonrecovery

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/utils"
)

const (
	// FileName is the snapshot file a managed app-server writes before a
	// planned daemon replacement.
	FileName = "loaded-threads.json"
	// stateDirName is the daemon's private state directory under CODEX_HOME.
	stateDirName = "app-server-daemon"
	// interruptionPrefix marks the metadata entry that carries the interrupted
	// turns alongside the candidate array.
	interruptionPrefix = "codex-interrupted-v1:"
)

// FilePath mirrors Rust daemon_recovery_file_path.
func FilePath(codexHome string) string {
	return filepath.Join(codexHome, stateDirName, FileName)
}

// Snapshot is the candidate set: the thread IDs that were loaded, plus the
// interrupted turns that may continue automatically. Loaded is skipped in the
// metadata JSON, matching Rust's `#[serde(skip)]`.
type Snapshot struct {
	Loaded      []string                   `json:"-"`
	Interrupted map[string]InterruptedTurn `json:"interrupted"`
}

// InterruptedTurn mirrors Rust daemon_recovery::InterruptedTurn. The
// protocol-typed fields stay raw so the on-disk shape round-trips exactly as
// the Rust app-server writes it.
type InterruptedTurn struct {
	TurnID string `json:"turn_id"`
	// The four optional fields have no `omitempty`: Rust's InterruptedTurn has no
	// skip_serializing_if, so an absent option serializes as `null` and the on-disk
	// shape stays byte-identical across implementations.
	OutputSchema       json.RawMessage `json:"output_schema"`
	ServiceTier        *string         `json:"service_tier"`
	CyberAccessProgram json.RawMessage `json:"cyber_access_program"`
	LocalEnvironment   json.RawMessage `json:"local_environment"`
}

// ReadSnapshot reads path (Rust daemon_recovery::read_snapshot). A missing file
// is an empty snapshot, malformed JSON is an error, and the embedded metadata
// entry supplies the interrupted turns.
func ReadSnapshot(path string) (Snapshot, error) {
	entries, err := readEntries(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	loaded := make([]string, 0, len(entries))
	for _, entry := range entries {
		metadata, ok := strings.CutPrefix(entry, interruptionPrefix)
		if !ok {
			loaded = append(loaded, entry)
			continue
		}
		var saved Snapshot
		if err := json.Unmarshal([]byte(metadata), &saved); err == nil {
			snapshot = saved
		}
	}
	if len(snapshot.Interrupted) > 0 {
		retained := make(map[string]InterruptedTurn, len(snapshot.Interrupted))
		for id, turn := range snapshot.Interrupted {
			if containsString(loaded, id) {
				retained[id] = normalizeInterruptedTurn(turn)
			}
		}
		snapshot.Interrupted = retained
	}
	snapshot.Loaded = sortedUnique(loaded)
	return snapshot, nil
}

// ReadCandidates returns just the loaded thread IDs.
func ReadCandidates(path string) ([]string, error) {
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		return nil, err
	}
	return snapshot.Loaded, nil
}

// WriteCandidates writes the loaded thread IDs with no interrupted turns.
func WriteCandidates(path string, candidates []string) error {
	return WriteSnapshot(path, Snapshot{Loaded: candidates})
}

// WriteSnapshot writes the snapshot as the candidate array plus, when
// interrupted turns exist, one metadata entry carrying the whole snapshot.
func WriteSnapshot(path string, snapshot Snapshot) error {
	saved := sortedUnique(snapshot.Loaded)
	if len(snapshot.Interrupted) > 0 {
		metadata, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		saved = append(saved, interruptionPrefix+string(metadata))
	}
	contents, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return utils.WriteAtomically(path, string(contents))
}

// normalizeInterruptedTurn maps a stored JSON null back to an absent optional.
// Rust models these fields as Option<T>, which deserializes null as None, so a
// snapshot written by either implementation reads back the same way.
func normalizeInterruptedTurn(turn InterruptedTurn) InterruptedTurn {
	turn.OutputSchema = normalizeRawJSON(turn.OutputSchema)
	turn.CyberAccessProgram = normalizeRawJSON(turn.CyberAccessProgram)
	turn.LocalEnvironment = normalizeRawJSON(turn.LocalEnvironment)
	return turn
}

func normalizeRawJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return value
}

func readEntries(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// sortedUnique is Rust's BTreeSet shape: deduplicated and ordered, and always a
// JSON array (never null).
func sortedUnique(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
