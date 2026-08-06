package rollout

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const SessionIndexFilename = "session_index.jsonl"

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
	sessionIndexMu.Lock()
	defer sessionIndexMu.Unlock()
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
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
}

func RemoveThreadNameEntries(codexHome string, threadID string) error {
	sessionIndexMu.Lock()
	defer sessionIndexMu.Unlock()
	path := sessionIndexPath(codexHome)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removed := false
	var remaining bytes.Buffer
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
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
	temporary := strings.TrimSuffix(path, ".jsonl") + ".jsonl.tmp"
	if err := os.WriteFile(temporary, remaining.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
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
	names := make(map[string]string, len(threadIDs))
	for _, entry := range entries {
		if _, ok := threadIDs[entry.ID]; ok && strings.TrimSpace(entry.ThreadName) != "" {
			names[entry.ID] = entry.ThreadName
		}
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
