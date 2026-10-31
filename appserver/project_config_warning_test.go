package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// Mirrors Rust app-server startup (codex-rs/app-server/src/lib.rs
// project_config_warning): project layers the trust decision disabled are
// reported once, in the condensed form, as a configWarning.
func TestRuntimeRouterInitializeEmitsProjectConfigWarning(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write .git/HEAD: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".gcode"), 0o755); err != nil {
		t.Fatalf("mkdir .gcode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), nil, 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	configService := config.NewConfigService(home)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Config: configService, DefaultCWD: project})
	router.SetNotificationSink(sink)

	response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "1.0.0"},
	}))
	if response.Error != nil {
		t.Fatalf("initialize = %+v", response)
	}

	folder := filepath.Join(project, ".gcode")
	projectWarnings := 0
	for _, notification := range sink.List() {
		if notification.Method != NotificationConfigWarning {
			continue
		}
		warning, ok := notification.Params.(*config.ConfigWarningNotification)
		if !ok || !strings.Contains(warning.Summary, "Project-local config, hooks, and exec policies are disabled") {
			continue
		}
		projectWarnings++
		if !strings.Contains(warning.Summary, folder) {
			t.Fatalf("summary = %q, want it to list %s", warning.Summary, folder)
		}
	}
	if projectWarnings != 1 {
		t.Fatalf("project config warnings = %d, want 1: %+v", projectWarnings, sink.List())
	}

	// The thread-scoped delivery must not repeat the startup warning.
	router.emitThreadConfigWarnings(project)
	total := 0
	for _, notification := range sink.List() {
		if notification.Method != NotificationConfigWarning {
			continue
		}
		warning, ok := notification.Params.(*config.ConfigWarningNotification)
		if ok && strings.Contains(warning.Summary, "Project-local config, hooks, and exec policies are disabled") {
			total++
		}
	}
	if total != 1 {
		t.Fatalf("project config warnings after thread start = %d, want 1", total)
	}
}
