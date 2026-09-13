package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// Mirrors Rust thread_processor: the thread config's startup_warnings include
// project-scoped requirement conflicts that were absent at initialization.
func TestRuntimeRouterEmitsThreadScopedRequirementWarnings(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, "requirements.toml"),
		[]byte("allowed_approval_policies = [\"on-request\"]\n"),
		0o600,
	); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		config.ConfigPath(home),
		[]byte("[projects.\""+strings.ReplaceAll(repo, `\`, `\\`)+"\"]\ntrust_level = \"trusted\"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	dotCodex := filepath.Join(repo, ".gcode")
	if err := os.MkdirAll(dotCodex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte("approval_policy = \"never\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	configService := config.NewConfigService(home)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Config: configService, DefaultCWD: repo})
	router.SetNotificationSink(sink)

	router.emitThreadConfigWarnings(repo)
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationConfigWarning {
		t.Fatalf("config warning notifications = %+v", notifications)
	}
	warning, ok := notifications[0].Params.(*config.ConfigWarningNotification)
	if !ok {
		t.Fatalf("config warning payload = %#v", notifications[0].Params)
	}
	if !strings.Contains(warning.Summary, "Configured value for `approval_policy` is disallowed by requirements") {
		t.Fatalf("summary = %q", warning.Summary)
	}
}
