package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SnapshotDir       = "shell_snapshots"
	SnapshotRetention = 72 * time.Hour
)

var ExcludedExportVars = map[string]bool{
	"PWD":    true,
	"OLDPWD": true,
}

type SnapshotFile struct {
	path string
}

func NewSnapshotFile(path string) *SnapshotFile {
	return &SnapshotFile{path: path}
}

func (s *SnapshotFile) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *SnapshotFile) Close() error {
	if s == nil || s.path == "" {
		return nil
	}
	return os.Remove(s.path)
}

func SnapshotPath(codexHome string, sessionID string, shell ShellType, nonce int64) (string, string) {
	extension := "sh"
	if shell == ShellPowerShell {
		extension = "ps1"
	}
	name := fmt.Sprintf("%s.%d.%s", sessionID, nonce, extension)
	temp := fmt.Sprintf("%s.tmp-%d", sessionID, nonce)
	dir := filepath.Join(codexHome, SnapshotDir)
	return filepath.Join(dir, name), filepath.Join(dir, temp)
}

func StripSnapshotPreamble(snapshot string) (string, bool) {
	const marker = "# Snapshot file"
	index := strings.Index(snapshot, marker)
	if index < 0 {
		return "", false
	}
	return snapshot[index:], true
}

func BuildPOSIXSnapshot(env map[string]string, aliases map[string]string) string {
	var b strings.Builder
	b.WriteString("# Snapshot file\n")
	for _, key := range sortedKeys(env) {
		if ExcludedExportVars[key] {
			continue
		}
		b.WriteString("export ")
		b.WriteString(shellQuoteName(key))
		b.WriteString("=")
		b.WriteString(shellQuoteValue(env[key]))
		b.WriteByte('\n')
	}
	for _, name := range sortedKeys(aliases) {
		b.WriteString("alias ")
		b.WriteString(shellQuoteName(name))
		b.WriteString("=")
		b.WriteString(shellQuoteValue(aliases[name]))
		b.WriteByte('\n')
	}
	return b.String()
}

// SnapshotPruneLookup reports the rollout file's modification time for a
// session, and whether the session still has one (Rust's
// `find_thread_path_by_id_str` plus the rollout's metadata).
type SnapshotPruneLookup func(sessionID string) (time.Time, bool)

// CleanupSnapshots mirrors Rust's `cleanup_stale_snapshots`: it removes snapshots
// whose session no longer has a rollout, whose rollout has not been touched
// within the retention window, and files whose name carries no session id. The
// active session's snapshots are always kept.
func CleanupSnapshots(codexHome string, activeSessionID string, now time.Time, lookup SnapshotPruneLookup) ([]string, error) {
	dir := filepath.Join(codexHome, SnapshotDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	activeSessionID = strings.TrimSpace(activeSessionID)
	removed := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		sessionID, ok := SnapshotSessionIDFromFileName(entry.Name())
		if !ok {
			// A file Codex does not recognize is not a snapshot it may keep.
			if err := os.Remove(path); err != nil {
				return removed, err
			}
			removed = append(removed, path)
			continue
		}
		if sessionID == activeSessionID {
			continue
		}
		if lookup == nil {
			continue
		}
		rolloutModified, ok := lookup(sessionID)
		if !ok {
			if err := os.Remove(path); err != nil {
				return removed, err
			}
			removed = append(removed, path)
			continue
		}
		if now.Sub(rolloutModified) < SnapshotRetention {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	return removed, nil
}

// SnapshotSessionIDFromFileName decodes the session a snapshot file belongs to,
// mirroring Rust's `snapshot_session_id_from_file_name`: `<session>.<nonce>.sh`,
// `<session>.<nonce>.ps1` and `<session>.tmp-<nonce>` carry the session, and
// anything else does not.
func SnapshotSessionIDFromFileName(fileName string) (string, bool) {
	// Rust splits at the last dot: rsplit_once('.').
	index := strings.LastIndex(fileName, ".")
	if index <= 0 || index == len(fileName)-1 {
		return "", false
	}
	stem, extension := fileName[:index], fileName[index+1:]
	if strings.HasPrefix(extension, "tmp-") {
		return stem, true
	}
	if extension != "sh" && extension != "ps1" {
		return "", false
	}
	// The generation segment after the session id is ignored.
	sessionID, _, _ := strings.Cut(stem, ".")
	if sessionID == "" {
		return "", false
	}
	return sessionID, true
}

func shellQuoteName(name string) string {
	return strings.ReplaceAll(name, "'", "")
}

func shellQuoteValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
