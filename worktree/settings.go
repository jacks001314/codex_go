// Package worktree resolves managed worktree settings from the existing
// [desktop] configuration, mirroring the Rust codex-worktree crate (#40624).
package worktree

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const defaultWorktreeKeepCount = 15

const (
	worktreeRootKey      = "git-worktree-root"
	autoCleanupKey       = "worktree-auto-cleanup-enabled"
	worktreeKeepCountKey = "worktree-keep-count"
)

// WorktreeSettings mirrors Rust codex-worktree::WorktreeSettings.
type WorktreeSettings struct {
	Root               string
	AutoCleanupEnabled bool
	KeepCount          int
}

// FromDesktopConfig resolves [desktop] worktree settings without introducing a
// separate configuration format. Defaults mirror Rust: root is
// $CODEX_HOME/worktrees, automatic cleanup is enabled, and 15 worktrees are
// retained. Configured roots, cleanup flags, and retention counts are validated
// before being exposed.
func FromDesktopConfig(codexHome string, desktop map[string]any) (WorktreeSettings, error) {
	settings := WorktreeSettings{
		Root:               filepath.Clean(filepath.Join(codexHome, "worktrees")),
		AutoCleanupEnabled: true,
		KeepCount:          defaultWorktreeKeepCount,
	}

	if raw, ok := desktop[worktreeRootKey]; ok && raw != nil {
		configured, ok := raw.(string)
		if !ok {
			return settings, fmt.Errorf("desktop.git-worktree-root must be a string")
		}
		configured = strings.TrimSpace(configured)
		if configured != "" {
			if !filepath.IsAbs(configured) {
				return settings, fmt.Errorf("desktop.git-worktree-root must be an absolute path")
			}
			settings.Root = filepath.Clean(configured)
		}
	}

	if raw, ok := desktop[autoCleanupKey]; ok {
		enabled, ok := raw.(bool)
		if !ok {
			return settings, fmt.Errorf("desktop.worktree-auto-cleanup-enabled must be a boolean")
		}
		settings.AutoCleanupEnabled = enabled
	}

	if raw, ok := desktop[worktreeKeepCountKey]; ok {
		count, err := positiveInt(raw)
		if err != nil {
			return settings, err
		}
		settings.KeepCount = count
	}

	return settings, nil
}

func positiveInt(raw any) (int, error) {
	var n int64
	switch v := raw.(type) {
	case float64:
		n = int64(v)
	case int:
		n = int64(v)
	case int64:
		n = v
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("desktop.worktree-keep-count must be a positive integer")
		}
		n = parsed
	default:
		return 0, fmt.Errorf("desktop.worktree-keep-count must be a positive integer")
	}
	if n <= 0 {
		return 0, fmt.Errorf("desktop.worktree-keep-count must be a positive integer")
	}
	return int(n), nil
}
