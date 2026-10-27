package rollout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const SessionIndexFilename = "session_index.jsonl"

// sessionIndexLockFilename serializes index mutations across processes that
// share a Codex home (Rust #57358). The file is persistent: replacing it would
// let another process lock a new file while an update still holds the old one.
const sessionIndexLockFilename = "session_index.jsonl.lock"

const sessionIndexLockRetry = 50 * time.Millisecond

// sessionIndexLockTimeout is a variable so tests can shorten the wait.
var sessionIndexLockTimeout = 30 * time.Second

// sessionIndexMu serializes mutations within this process before they contend
// on the cross-process file lock.
var sessionIndexMu sync.Mutex

type SessionIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

func AppendThreadName(codexHome string, threadID string, name string) error {
	return AppendSessionIndexEntry(codexHome, SessionIndexEntry{
		ID:         threadID,
		ThreadName: name,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func AppendSessionIndexEntry(codexHome string, entry SessionIndexEntry) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	return withSessionIndexLock(codexHome, func() error {
		file, err := os.OpenFile(sessionIndexPath(codexHome), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
		return nil
	})
}

// withSessionIndexLock mirrors Rust with_session_index_lock: mutations hold a
// cross-process file lock for as long as the write runs, and return a timeout
// error without touching the index when contention lasts past the deadline.
func withSessionIndexLock(codexHome string, update func() error) error {
	sessionIndexMu.Lock()
	defer sessionIndexMu.Unlock()
	lock := flock.New(filepath.Join(codexHome, sessionIndexLockFilename))
	deadline := time.Now().Add(sessionIndexLockTimeout)
	for {
		locked, err := lock.TryLock()
		if err != nil {
			return err
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: timed out waiting for the session index lock; another Codex process may be suspended on this or another host", os.ErrDeadlineExceeded)
		}
		time.Sleep(sessionIndexLockRetry)
	}
	defer func() { _ = lock.Unlock() }()
	return update()
}

func RemoveThreadNameEntries(codexHome string, threadID string) error {
	path := sessionIndexPath(codexHome)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return withSessionIndexLock(codexHome, func() error {
		return removeThreadNameEntriesLocked(path, threadID)
	})
}

func removeThreadNameEntriesLocked(path string, threadID string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removed := false
	var remaining bytes.Buffer
	// Rust parity: str::lines() yields no trailing empty segment, keeps interior
	// blank lines, and drops a trailing carriage return. bytes.Split adds a final
	// empty element when the data ends with '\n', so drop only that one.
	lines := bytes.Split(data, []byte{'\n'})
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		var entry SessionIndexEntry
		if json.Unmarshal(bytes.TrimSpace(line), &entry) == nil && entry.ID == threadID {
			removed = true
			continue
		}
		remaining.Write(line)
		remaining.WriteByte('\n')
	}
	if !removed {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "session-index-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		cleanup()
		return err
	}
	if _, err := temporary.Write(remaining.Bytes()); err != nil {
		_ = temporary.Close()
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func FindThreadNameByID(codexHome string, threadID string) (string, bool, error) {
	entries, err := readSessionIndex(codexHome)
	if err != nil {
		return "", false, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].ID == threadID {
			return entries[i].ThreadName, true, nil
		}
	}
	return "", false, nil
}

func FindThreadNamesByIDs(codexHome string, threadIDs map[string]struct{}) (map[string]string, error) {
	if len(threadIDs) == 0 {
		return map[string]string{}, nil
	}
	entries, err := readSessionIndex(codexHome)
	if err != nil {
		return nil, err
	}
	// Rust #49297: scan the append-only index backwards so the first nonempty,
	// trimmed name seen for an id is its latest usable name, and stop once every
	// requested id has been resolved. Whitespace-only names are skipped.
	remaining := make(map[string]struct{}, len(threadIDs))
	for id := range threadIDs {
		remaining[id] = struct{}{}
	}
	names := make(map[string]string, len(threadIDs))
	for i := len(entries) - 1; i >= 0 && len(remaining) > 0; i-- {
		entry := entries[i]
		if _, ok := remaining[entry.ID]; !ok {
			continue
		}
		name := strings.TrimSpace(entry.ThreadName)
		if name == "" {
			continue
		}
		names[entry.ID] = name
		delete(remaining, entry.ID)
	}
	return names, nil
}

// SessionMetaCandidate pairs a readable rollout path with its session header.
type SessionMetaCandidate struct {
	Path string
	Meta *SessionMeta
}

// FindThreadMetaByName returns the most recently modified readable active
// rollout whose session index records the exact name (Rust c38a60ded2).
func FindThreadMetaByName(codexHome string, name string) (string, *SessionMeta, bool, error) {
	return FindThreadMetaByNameInCollection(codexHome, name, false)
}

func FindThreadMetaByNameInCollection(codexHome string, name string, archived bool) (string, *SessionMeta, bool, error) {
	candidates, err := FindThreadMetaCandidatesByNameInCollection(codexHome, name, archived, nil, nil)
	if err != nil {
		return "", nil, false, err
	}
	if len(candidates) == 0 {
		return "", nil, false, nil
	}
	return candidates[0].Path, candidates[0].Meta, true, nil
}

// FindThreadMetaCandidatesByNameInCollection returns every readable rollout
// recorded under the exact name, newest modification time first, applying the
// optional source and model-provider filters before ranking so an ineligible
// newer duplicate cannot hide an older usable session (Rust c38a60ded2,
// #37157). Empty filter slices disable their respective filters; provider
// filtering rejects only explicit mismatches because older rollouts omitted
// provider metadata.
func FindThreadMetaCandidatesByNameInCollection(codexHome string, name string, archived bool, allowedSources []string, allowedProviders []string) ([]SessionMetaCandidate, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	entries, err := readSessionIndex(codexHome)
	if err != nil {
		return nil, err
	}
	var candidates []SessionMetaCandidate
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.ThreadName != name {
			continue
		}
		path, findErr := FindThreadPath(codexHome, entry.ID, archived)
		if findErr != nil {
			continue
		}
		lines, _, loadErr := Load(path)
		if loadErr != nil {
			continue
		}
		for lineIndex := range lines {
			if lines[lineIndex].Meta != nil {
				meta := lines[lineIndex].Meta
				if !sessionMetaAllowedByFilters(meta, allowedSources, allowedProviders) {
					break
				}
				candidates = append(candidates, SessionMetaCandidate{Path: path, Meta: meta})
				break
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return sessionMetaCandidateMtime(candidates[i]).After(sessionMetaCandidateMtime(candidates[j]))
	})
	return candidates, nil
}

func sessionMetaAllowedByFilters(meta *SessionMeta, allowedSources []string, allowedProviders []string) bool {
	if meta == nil {
		return false
	}
	if len(allowedSources) > 0 && !stringInSlice(meta.Source, allowedSources) {
		return false
	}
	if len(allowedProviders) > 0 && meta.ModelProvider != "" && !stringInSlice(meta.ModelProvider, allowedProviders) {
		return false
	}
	return true
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sessionMetaCandidateMtime(candidate SessionMetaCandidate) time.Time {
	if info, err := os.Stat(candidate.Path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

func readSessionIndex(codexHome string) ([]SessionIndexEntry, error) {
	data, err := os.ReadFile(sessionIndexPath(codexHome))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	physicalLines := bytes.Split(data, []byte{'\n'})
	entries := make([]SessionIndexEntry, 0, len(physicalLines))
	for _, line := range physicalLines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry SessionIndexEntry
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func sessionIndexPath(codexHome string) string {
	return filepath.Join(codexHome, SessionIndexFilename)
}
