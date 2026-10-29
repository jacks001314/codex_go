package onboarding

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func daemonRecoveryKey(code rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{code}}
}

func daemonRecoverySpecial(code bubbletea.KeyType) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: code}
}

// TestDaemonRecoveryDefaultsToCancelLikeRust covers Rust #55442's recovery
// menu: Cancel is preselected, and restarting needs an explicit confirmation.
func TestDaemonRecoveryDefaultsToCancelLikeRust(t *testing.T) {
	options := DaemonRecoveryOptions{
		Reason:          "This session requires api_key_model_discovery to be enabled",
		RestartFeatures: map[string]bool{"api_key_model_discovery": true},
		Managed:         true,
	}
	model := newDaemonRecoveryModel(options)
	model.Update(daemonRecoverySpecial(bubbletea.KeyEnter))
	if !model.done || model.result.Cancelled || model.result.Choice != DaemonRecoveryCancel {
		t.Fatalf("default enter = %#v (done=%v)", model.result, model.done)
	}

	model = newDaemonRecoveryModel(options)
	model.Update(daemonRecoveryKey('2'))
	if model.done {
		t.Fatal("selecting restart must not run it without confirmation")
	}
	model.Update(daemonRecoverySpecial(bubbletea.KeyEnter))
	if !model.done || model.result.Choice != DaemonRecoveryRestart {
		t.Fatalf("confirmed restart = %#v (done=%v)", model.result, model.done)
	}

	model = newDaemonRecoveryModel(options)
	model.Update(daemonRecoveryKey('1'))
	if !model.done || model.result.Choice != DaemonRecoveryRunWithoutDaemon {
		t.Fatalf("run without daemon = %#v (done=%v)", model.result, model.done)
	}

	model = newDaemonRecoveryModel(options)
	model.Update(daemonRecoverySpecial(bubbletea.KeyEsc))
	if !model.done || !model.result.Cancelled {
		t.Fatalf("esc = %#v (done=%v)", model.result, model.done)
	}
}

// TestDaemonRecoveryShortcutsLikeRust covers Rust's ListSelectionView
// shortcuts: a digit accepts the run/cancel rows immediately, while the restart
// row only moves the highlight so Enter has to confirm it.
func TestDaemonRecoveryShortcutsLikeRust(t *testing.T) {
	options := DaemonRecoveryOptions{
		Reason:          "mismatch",
		RestartFeatures: map[string]bool{"code_mode_host": true},
		Managed:         true,
	}
	for digit, want := range map[rune]DaemonRecoveryChoice{
		'1': DaemonRecoveryRunWithoutDaemon,
		'3': DaemonRecoveryCancel,
	} {
		model := newDaemonRecoveryModel(options)
		model.Update(daemonRecoveryKey(digit))
		if !model.done || model.result.Choice != want {
			t.Fatalf("shortcut %q = %#v (done=%v), want %v", digit, model.result, model.done, want)
		}
	}
}

// TestDaemonRecoveryUnmanagedRestartDisabledLikeRust covers a daemon Codex does
// not manage: the restart row is disabled and cannot be selected.
func TestDaemonRecoveryUnmanagedRestartDisabledLikeRust(t *testing.T) {
	for _, options := range []DaemonRecoveryOptions{
		{Reason: "mismatch", RestartFeatures: map[string]bool{"code_mode_host": true}},
		{Reason: "mismatch", Managed: true},
	} {
		model := newDaemonRecoveryModel(options)
		model.Update(daemonRecoveryKey('2'))
		model.Update(daemonRecoverySpecial(bubbletea.KeyEnter))
		if !model.done || model.result.Choice != DaemonRecoveryCancel {
			t.Fatalf("disabled restart = %#v (done=%v)", model.result, model.done)
		}
	}
}

// TestDaemonRecoveryViewLikeRust covers the header, the three-row feature
// preview with its overflow line, and the disabled reason.
func TestDaemonRecoveryViewLikeRust(t *testing.T) {
	model := newDaemonRecoveryModel(DaemonRecoveryOptions{
		Reason: "This session requires api_key_model_discovery to be enabled",
		RestartFeatures: map[string]bool{
			"api_key_model_discovery":        true,
			"code_mode_host":                 true,
			"instant_interrupt":              true,
			"mcp_oauth_refresh_coordination": false,
		},
		Managed: true,
	})
	view := model.View()
	for _, want := range []string{
		"Background server has incompatible feature settings",
		"This session requires api_key_model_discovery to be enabled",
		"These settings persist and can disable functionality for other clients. Restart may interrupt active or queued work.",
		"Restart will use these shared feature settings:",
		"  api_key_model_discovery = true",
		"  code_mode_host = true",
		"  instant_interrupt = true",
		"  and 1 more",
		"> Cancel",
		"Restart with these settings (recommended)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "mcp_oauth_refresh_coordination = false") {
		t.Fatalf("view should cap the preview at three rows:\n%s", view)
	}

	unmanaged := newDaemonRecoveryModel(DaemonRecoveryOptions{Reason: "mismatch", Managed: true})
	view = unmanaged.View()
	if !strings.Contains(view, "Cannot use the background server") ||
		!strings.Contains(view, "Restart cannot resolve this compatibility check.") {
		t.Fatalf("non-restartable view:\n%s", view)
	}
}
