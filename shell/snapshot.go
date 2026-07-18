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

func CleanupStaleSnapshots(codexHome string, sessionID string, now time.Time) ([]string, error) {
	dir := filepath.Join(codexHome, SnapshotDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	removed := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), sessionID+".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return removed, err
		}
		if now.Sub(info.ModTime()) <= SnapshotRetention {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	return removed, nil
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
