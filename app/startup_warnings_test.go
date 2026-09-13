package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/config"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
	"codex_go/utils"
)

// Mirrors Rust's config.startup_warnings reaching the TUI: the local session's
// requirement-driven warnings are passed to the model, which coalesces them into
// the startup warnings entry.
func TestInteractiveStartupConfigWarningsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"never\"\nunknown_setting = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_approval_policies = [\"on-request\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := interactiveStartupConfigWarnings(home, "", "bogus-theme")
	var requirementWarning, ignoredWarning bool
	for _, warning := range warnings {
		if strings.Contains(warning, "Configured value for `approval_policy` is disallowed by requirements") {
			requirementWarning = true
		}
		if strings.Contains(warning, "unknown_setting") {
			ignoredWarning = true
		}
	}
	if !requirementWarning || !ignoredWarning {
		t.Fatalf("interactiveStartupConfigWarnings() = %#v", warnings)
	}
	var themeWarning bool
	for _, warning := range warnings {
		if strings.Contains(warning, "Theme \"bogus-theme\" not found.") {
			themeWarning = true
		}
	}
	if !themeWarning {
		t.Fatalf("theme warning missing: %#v", warnings)
	}

	model := codextea.NewModel(codextui.NewState(nil), codextea.Options{
		Width:                 100,
		Height:                24,
		ShowSessionHeader:     true,
		StartupConfigWarnings: warnings,
	})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "\u26a0 3 startup issues") {
		t.Fatalf("startup warnings not rendered:\n%s", view)
	}
}

// Mirrors Rust's chatwidget ServerNotification::ConfigWarning: the remote TUI
// forwards the notification into the model's startup warning path.
// Mirrors Rust's TUI-side theme validation for the remote app.
func TestRemoteTUIStartupConfigWarningsLikeRust(t *testing.T) {
	if got := remoteTUIStartupConfigWarnings(""); got != nil {
		t.Fatalf("remoteTUIStartupConfigWarnings(unset) = %#v", got)
	}
	if got := remoteTUIStartupConfigWarnings("ansi"); got != nil {
		t.Fatalf("remoteTUIStartupConfigWarnings(bundled) = %#v", got)
	}
	got := remoteTUIStartupConfigWarnings("bogus-theme")
	if len(got) != 1 || !strings.Contains(got[0], "Theme \"bogus-theme\" not found.") {
		t.Fatalf("remoteTUIStartupConfigWarnings(unknown) = %#v", got)
	}
}

func TestRemoteConfigWarningFeedsStartupWarningsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	messages := make(chan bubbletea.Msg, 1)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	details := "falling back to required value `managed`"
	payload, err := json.Marshal(config.ConfigWarningNotification{
		Summary: "Configured value for `permission_profile` is disallowed by requirements.",
		Details: &details,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationConfigWarning),
		Params: payload,
	}); err != nil {
		t.Fatal(err)
	}
	message, ok := (<-messages).(codextea.StartupConfigWarningMsg)
	if !ok {
		t.Fatalf("message = %#v", message)
	}
	if !strings.Contains(message.Message, "permission_profile") || !strings.Contains(message.Message, details) {
		t.Fatalf("config warning message = %q", message.Message)
	}
	model := codextea.NewModel(state, codextea.Options{Width: 100, Height: 24, ShowSessionHeader: true})
	model.Update(message)
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "\u26a0 1 startup issue") {
		t.Fatalf("config warning not rendered in the startup entry:\n%s", view)
	}
}
