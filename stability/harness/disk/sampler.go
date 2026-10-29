// Package disk measures storage usage under a Codex home or worktree root.
package disk

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot is one point-in-time disk observation.
type Snapshot struct {
	At         time.Time `json:"at"`
	Root       string    `json:"root"`
	TotalBytes int64     `json:"total_bytes"`
	Files      int       `json:"files"`
	WALBytes   int64     `json:"wal_bytes"`
	SHMBytes   int64     `json:"shm_bytes"`
	TempFiles  int       `json:"temp_files"`
	TempBytes  int64     `json:"temp_bytes"`
}

// Options customizes what counts as a temporary file.
type Options struct {
	TempPatterns []string
}

// DefaultTempPatterns matches the temporary names Codex writes while it is
// building a rollout or a state database.
var DefaultTempPatterns = []string{".tmp-*", "*.tmp", "tmp-*", "*.temp"}

// Take measures root with the default temporary patterns.
func Take(root string) (Snapshot, error) {
	return TakeWithOptions(root, Options{})
}

// TakeWithOptions measures root, counting regular files and WAL/SHM siblings.
func TakeWithOptions(root string, options Options) (Snapshot, error) {
	info, err := os.Stat(root)
	if err != nil {
		return Snapshot{}, err
	}
	if !info.IsDir() {
		return Snapshot{}, fmt.Errorf("disk: %s is not a directory", root)
	}
	patterns := options.TempPatterns
	if len(patterns) == 0 {
		patterns = DefaultTempPatterns
	}
	snapshot := Snapshot{At: time.Now(), Root: root}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		size := info.Size()
		snapshot.TotalBytes += size
		snapshot.Files++
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, "-wal"):
			snapshot.WALBytes += size
		case strings.HasSuffix(name, "-shm"):
			snapshot.SHMBytes += size
		}
		if matchesAny(name, patterns) {
			snapshot.TempFiles++
			snapshot.TempBytes += size
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Delta returns field-wise differences from previous. Time and Root are copied
// from the receiver.
func (s Snapshot) Delta(previous Snapshot) Snapshot {
	return Snapshot{
		At:         s.At,
		Root:       s.Root,
		TotalBytes: s.TotalBytes - previous.TotalBytes,
		Files:      s.Files - previous.Files,
		WALBytes:   s.WALBytes - previous.WALBytes,
		SHMBytes:   s.SHMBytes - previous.SHMBytes,
		TempFiles:  s.TempFiles - previous.TempFiles,
		TempBytes:  s.TempBytes - previous.TempBytes,
	}
}

func matchesAny(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return true
		}
	}
	return false
}
