package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// Mirrors Rust #44691: unrecognized startup settings produce one bounded
// configWarning, and repeated delivery is suppressed.
func TestRuntimeRouterEmitsIgnoredConfigWarnings(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, "config.toml"),
		[]byte("network_proxy = { nested = \"private_value\" }\n"),
		0o600,
	); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(home, "requirements.toml"),
		[]byte("allowed_permissions = [\":read-only\"]\n"),
		0o600,
	); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	configService := config.NewConfigService(home)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Config: configService})
	router.SetNotificationSink(sink)

	response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "1.0.0"},
	}))
	if response.Error != nil {
		t.Fatalf("initialize = %+v", response)
	}
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationConfigWarning {
		t.Fatalf("config warning notifications = %+v", notifications)
	}
	warning, ok := notifications[0].Params.(*config.ConfigWarningNotification)
	if !ok {
		t.Fatalf("config warning payload = %#v", notifications[0].Params)
	}
	if !strings.Contains(warning.Summary, "`network_proxy` is ignored.") ||
		!strings.Contains(warning.Summary, "`allowed_permissions` is ignored.") {
		t.Fatalf("summary = %q", warning.Summary)
	}
	if strings.Contains(warning.Summary, "private_value") {
		t.Fatalf("summary leaked a value: %q", warning.Summary)
	}

	// Repeating the same delivery must be suppressed.
	router.emitThreadConfigWarnings("")
	if got := len(sink.List()); got != 1 {
		t.Fatalf("repeated warning emitted %d notifications, want 1", got)
	}
}
