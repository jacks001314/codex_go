package appserverdaemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Bounded daemon observations for foreground callers (Rust
// app-server-daemon/src/telemetry.rs). This module never exports telemetry
// itself; in particular the detached updater must not create a telemetry
// provider.

// TelemetryHandoffEnv mirrors Rust telemetry::HANDOFF_ENV: when it is present,
// a child must not report its own daemon observation because the foreground
// front end already owns it.
const TelemetryHandoffEnv = "CODEX_DAEMON_TELEMETRY_HANDOFF"

// unknownSettingTag is the placeholder for a settings snapshot that cannot be
// read or validated (Rust telemetry::settings_tags' unwrap_or fallback).
const unknownSettingTag = "unknown"

// DaemonSettingsTags reports the bounded daemon-settings observations the CLI
// and TUI attach to their daemon counters (Rust telemetry::settings_tags): only
// presence and enabled state leave the settings reader, never values or
// contents. Unreadable or invalid settings report the "unknown" placeholders.
func DaemonSettingsTags(codexHome string) [4][2]string {
	tags, err := settingsTelemetryTags(daemonSettingsPath(codexHome))
	if err != nil {
		return [4][2]string{
			{"auto_update", unknownSettingTag},
			{"auto_update_setting", unknownSettingTag},
			{"update_interval_setting", unknownSettingTag},
			{"shutdown_grace_setting", unknownSettingTag},
		}
	}
	return tags
}

// DaemonSettingsTagMap is the tag map form of DaemonSettingsTags.
func DaemonSettingsTagMap(codexHome string) map[string]string {
	tags := DaemonSettingsTags(codexHome)
	out := make(map[string]string, len(tags))
	for _, tag := range tags {
		out[tag[0]] = tag[1]
	}
	return out
}

// daemonSettingsPath is where a daemon stores its settings
// (Rust Daemon::settings_file).
func daemonSettingsPath(codexHome string) string {
	return filepath.Join(codexHome, StateDirName, SettingsFileName)
}

// settingsTelemetryTags mirrors Rust settings::telemetry_tags: presence is read
// from the raw JSON keys, while the enabled state comes from the validated
// settings snapshot, so an invalid snapshot reports an error instead of tags.
func settingsTelemetryTags(path string) ([4][2]string, error) {
	settings, err := LoadSettings(path)
	if err != nil {
		return [4][2]string{}, err
	}
	raw, err := readRawDaemonSettings(path)
	if err != nil {
		return [4][2]string{}, err
	}
	updater, _ := raw["updater"].(map[string]any)
	autoUpdate := "disabled"
	if settings.AutoUpdateEnabled() {
		autoUpdate = "enabled"
	}
	return [4][2]string{
		{"auto_update", autoUpdate},
		{"auto_update_setting", configuredTag(mapHasKey(updater, "autoUpdateEnabled"))},
		{"update_interval_setting", configuredTag(mapHasKey(updater, "updateIntervalMinutes"))},
		{"shutdown_grace_setting", configuredTag(mapHasKey(raw, "shutdownGraceSeconds"))},
	}, nil
}

// configuredTag mirrors Rust's `presence` helper.
func configuredTag(configured bool) string {
	if configured {
		return "configured"
	}
	return "default"
}

func mapHasKey(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	_, ok := values[key]
	return ok
}

// readRawDaemonSettings reads the settings file as a JSON object, treating a
// missing file as an empty object (Rust settings::read_settings).
func readRawDaemonSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read settings %s: %w", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse settings %s: %w", path, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return raw, nil
}

// withoutEnvVar returns env with every NAME=... entry removed. A launcher uses
// it to keep an observation handoff marker away from a long-lived child
// (Rust pid_start's `env_remove(HANDOFF_ENV)`).
func withoutEnvVar(env []string, name string) []string {
	if len(env) == 0 {
		return env
	}
	prefix := name + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return out
}
