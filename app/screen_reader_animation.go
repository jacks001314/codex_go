package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/config"
	codextea "codex_go/tui/tea"
)

// Rust parity: tui/src/screen_reader.rs (#46040). Detection runs once, is
// bounded, and seeds a persisted animation default only when the user has not
// configured `tui.animations`. The completion marker skips future probes.

var screenReaderDetectionTimeout = 450 * time.Millisecond
var screenReaderProbeFunc = screenReaderProbe

func detectScreenReaderWithTimeout() bool {
	result := make(chan bool, 1)
	go func() {
		result <- screenReaderProbeFunc()
	}()
	select {
	case detected := <-result:
		return detected
	case <-time.After(screenReaderDetectionTimeout):
		return false
	}
}

// interactiveScreenReaderAnimationSettings applies the one-time host probe to
// the launch settings and persists the completion marker. A persistence failure
// keeps the detected session default and is intentionally non-fatal.
func interactiveScreenReaderAnimationSettings(settings codextea.SettingsWriteResult) codextea.SettingsWriteResult {
	home := auth.DefaultCodexHome()
	loaded, err := config.LoadEffective(home, nil, nil, nil)
	if err != nil || loaded == nil {
		return settings
	}
	return applyScreenReaderAnimationSettings(settings, loaded.Values, config.ConfigPath(home))
}

func applyScreenReaderAnimationSettings(settings codextea.SettingsWriteResult, values map[string]any, path string) codextea.SettingsWriteResult {
	tui, _ := values["tui"].(map[string]any)
	if _, done := tui["screen_reader_detection_done"]; done {
		return settings
	}
	animationsConfigured := false
	if raw, ok := tui["animations"]; ok {
		animationsConfigured = true
		if enabled, ok := raw.(bool); ok {
			settings.AnimationsEnabled = &enabled
		}
	}
	detected := detectScreenReaderWithTimeout()
	if detected && !animationsConfigured {
		disabled := false
		settings.AnimationsEnabled = &disabled
	}
	_ = persistScreenReaderDetection(path, detected, detected && !animationsConfigured)
	return settings
}

// persistScreenReaderDetection edits only the user config's `[tui]` table,
// preserving comments, ordering and unrelated content. It returns an error for
// inline `tui = { ... }` tables rather than risking a duplicate definition.
func persistScreenReaderDetection(path string, detected bool, writeAnimations bool) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	contents := ""
	if data, err := os.ReadFile(path); err == nil {
		contents = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(contents, "\r\n", "\n"), "\n")
	sectionStart := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "tui =") || strings.HasPrefix(trimmed, "tui=") {
			return fmt.Errorf("inline tui table cannot be edited safely")
		}
		if trimmed == "[tui]" {
			sectionStart = index
			break
		}
	}
	hasAnimations := false
	hasMarker := false
	if sectionStart >= 0 {
		for index := sectionStart + 1; index < len(lines); index++ {
			trimmed := strings.TrimSpace(lines[index])
			if strings.HasPrefix(trimmed, "[") {
				break
			}
			key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
			switch key {
			case "animations":
				hasAnimations = true
			case "screen_reader_detection_done":
				hasMarker = true
			}
		}
	}
	additions := []string{}
	if writeAnimations && !hasAnimations {
		additions = append(additions, "animations = false")
	}
	if !hasMarker {
		additions = append(additions, "screen_reader_detection_done = true")
	}
	if len(additions) == 0 {
		return nil
	}
	if sectionStart < 0 {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "[tui]")
		sectionStart = len(lines) - 1
	}
	insert := append([]string{}, additions...)
	updated := make([]string, 0, len(lines)+len(insert))
	updated = append(updated, lines[:sectionStart+1]...)
	updated = append(updated, insert...)
	updated = append(updated, lines[sectionStart+1:]...)
	output := strings.Join(updated, "\n")
	if !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(output); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		// Windows may refuse a rename over an existing file; fall back to a
		// direct write after the safe temp-file construction.
		return os.WriteFile(path, []byte(output), 0o600)
	}
	return nil
}
