package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// Mirrors Rust load_agent_roles: a malformed agent role definition is reported
// as a thread config warning instead of failing the load.
func TestRuntimeRouterEmitsAgentRoleWarnings(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, "config.toml"),
		[]byte("[agents.worker]\nnickname_candidates = [\"Scout\"]\n"),
		0o600,
	); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	configService := config.NewConfigService(home)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Config: configService})
	router.SetNotificationSink(sink)

	router.emitThreadConfigWarnings("")
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationConfigWarning {
		t.Fatalf("config warning notifications = %+v", notifications)
	}
	warning, ok := notifications[0].Params.(*config.ConfigWarningNotification)
	if !ok {
		t.Fatalf("config warning payload = %#v", notifications[0].Params)
	}
	if !strings.Contains(warning.Summary, "Ignoring malformed agent role definition: agent role `worker` must define a description") {
		t.Fatalf("summary = %q", warning.Summary)
	}

	// Repeating the delivery is suppressed.
	router.emitThreadConfigWarnings("")
	if got := len(sink.List()); got != 1 {
		t.Fatalf("repeated warning emitted %d notifications, want 1", got)
	}
}
