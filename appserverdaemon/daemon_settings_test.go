package appserverdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDaemonSettingsFeatureOverridesAndUpdaterLikeRust mirrors Rust
// settings.rs: the stored feature overrides round-trip, an empty set removes the
// key, and the updater block defaults when absent and validates its interval.
func TestDaemonSettingsFeatureOverridesAndUpdaterLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SettingsFileName)
	original := `{"remoteControlEnabled":false,"featureOverrides":{"code_mode_host":true,"auth_elicitation":false},"updater":{"autoUpdateEnabled":false,"updateIntervalMinutes":120}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, map[string]bool{"code_mode_host": true, "auth_elicitation": false}) {
		t.Fatalf("featureOverrides = %#v", settings.FeatureOverrides)
	}
	if settings.AutoUpdateEnabled() {
		t.Fatal("autoUpdateEnabled = true, want false")
	}
	if got := settings.UpdateInterval(); got != 120*time.Minute {
		t.Fatalf("update interval = %v, want 120m", got)
	}

	// Defaults apply when the updater block is absent.
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	settings, err = LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !settings.AutoUpdateEnabled() || settings.UpdateInterval() != time.Duration(DefaultUpdateIntervalMinutes)*time.Minute {
		t.Fatalf("updater defaults = %v/%v", settings.AutoUpdateEnabled(), settings.UpdateInterval())
	}

	// An out-of-range interval is rejected like Rust's UpdaterSettings::validate.
	if err := os.WriteFile(path, []byte(`{"updater":{"updateIntervalMinutes":0}}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if _, err := LoadSettings(path); err == nil || !strings.Contains(err.Error(), "update interval must be positive") {
		t.Fatalf("LoadSettings(interval 0) error = %v", err)
	}
}

// TestDaemonSettingsSaveClearsFeatureOverridesLikeRust pins that saving an empty
// override set removes the key instead of storing an empty map, keeps the
// updater block, and leaves no temporary file behind.
func TestDaemonSettingsSaveClearsFeatureOverridesLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SettingsFileName)
	if err := os.WriteFile(path, []byte(`{"featureOverrides":{"code_mode_host":true},"updater":{"updateIntervalMinutes":90}}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if err := SaveSettings(path, &DaemonSettings{RemoteControlEnabled: true}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if _, ok := values["featureOverrides"]; ok {
		t.Fatalf("featureOverrides = %#v, want the key removed", values["featureOverrides"])
	}
	if _, ok := values["updater"]; !ok {
		t.Fatalf("updater block dropped on save: %#v", values)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary settings file left behind: %v", err)
	}

	if err := SaveSettings(path, &DaemonSettings{FeatureOverrides: map[string]bool{"worktrees": true}}); err != nil {
		t.Fatalf("SaveSettings(overrides): %v", err)
	}
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, map[string]bool{"worktrees": true}) {
		t.Fatalf("saved featureOverrides = %#v", settings.FeatureOverrides)
	}
}

func graceIntPtr(value int) *int { return &value }

func TestDaemonSettingsShutdownGraceDefaultsAndBounds(t *testing.T) {
	if got := (&DaemonSettings{}).ShutdownGraceSecondsValue(); got != DefaultShutdownGraceSeconds {
		t.Fatalf("default grace = %d, want %d", got, DefaultShutdownGraceSeconds)
	}
	// Zero is valid: request graceful shutdown, then force immediately.
	if got := (&DaemonSettings{ShutdownGraceSeconds: graceIntPtr(0)}).ShutdownGraceSecondsValue(); got != 0 {
		t.Fatalf("zero grace = %d, want 0", got)
	}
	if got := (&DaemonSettings{ShutdownGraceSeconds: graceIntPtr(MaxShutdownGraceSeconds)}).ShutdownGraceSecondsValue(); got != MaxShutdownGraceSeconds {
		t.Fatalf("max grace = %d, want %d", got, MaxShutdownGraceSeconds)
	}
	for _, value := range []int{-1, MaxShutdownGraceSeconds + 1} {
		if got := (&DaemonSettings{ShutdownGraceSeconds: graceIntPtr(value)}).ShutdownGraceSecondsValue(); got != DefaultShutdownGraceSeconds {
			t.Fatalf("grace for %d = %d, want default", value, got)
		}
	}
}

func TestDaemonSettingsLoadValidatesShutdownGrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SettingsFileName)
	if err := os.WriteFile(path, []byte(`{"shutdownGraceSeconds":301}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if _, err := LoadSettings(path); err == nil {
		t.Fatal("LoadSettings accepted an out-of-range grace")
	}
	// The stop path tolerates invalid settings and uses the default.
	if got := LoadSettingsForStop(path).ShutdownGraceSecondsValue(); got != DefaultShutdownGraceSeconds {
		t.Fatalf("stop grace = %d, want default", got)
	}

	if err := os.WriteFile(path, []byte(`{"shutdownGraceSeconds":15}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got := settings.ShutdownGraceSecondsValue(); got != 15 {
		t.Fatalf("grace = %d, want 15", got)
	}

	// A missing field stays nil so the default applies.
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if settings, err = LoadSettings(path); err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.ShutdownGraceSeconds != nil {
		t.Fatalf("missing grace = %#v, want nil", settings.ShutdownGraceSeconds)
	}
}

func TestDaemonSettingsSavePreservesUnknownKeysLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SettingsFileName)
	original := `{"remoteControlEnabled":false,"shutdownGraceSeconds":15,"updater":{"autoUpdateEnabled":false}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if err := SaveSettings(path, &DaemonSettings{RemoteControlEnabled: true}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if values["remoteControlEnabled"] != true {
		t.Fatalf("remoteControlEnabled = %#v", values["remoteControlEnabled"])
	}
	if values["shutdownGraceSeconds"] != float64(15) {
		t.Fatalf("shutdownGraceSeconds = %#v, want 15 preserved", values["shutdownGraceSeconds"])
	}
	if _, ok := values["updater"]; !ok {
		t.Fatalf("updater key dropped on save: %#v", values)
	}
}

func TestLoadSettingsForStopToleratesUnreadableSettings(t *testing.T) {
	if got := LoadSettingsForStop(filepath.Join(t.TempDir(), "missing.json")).ShutdownGraceSecondsValue(); got != DefaultShutdownGraceSeconds {
		t.Fatalf("missing-settings grace = %d, want default", got)
	}
	broken := filepath.Join(t.TempDir(), SettingsFileName)
	if err := os.WriteFile(broken, []byte(`{`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if got := LoadSettingsForStop(broken).ShutdownGraceSecondsValue(); got != DefaultShutdownGraceSeconds {
		t.Fatalf("broken-settings grace = %d, want default", got)
	}
}

func TestPIDBackendStopWithGraceWithoutRecord(t *testing.T) {
	backend := NewPIDBackend(BackendPaths{PIDFile: filepath.Join(t.TempDir(), PIDFileName)})
	if err := backend.StopWithGrace(0); err != nil {
		t.Fatalf("StopWithGrace(0) = %v", err)
	}
}
