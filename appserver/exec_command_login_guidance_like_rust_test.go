package appserver

import (
	"testing"

	"codex_go/tool"
	"codex_go/turn"
)

// TestTurnEnvironmentHasPowerShellLikeRust mirrors Rust #61744: the login
// guidance follows any ready turn environment that reports PowerShell,
// including the implicit local environment.
func TestTurnEnvironmentHasPowerShellLikeRust(t *testing.T) {
	// The implicit local environment launches under the session shell.
	wantLocal := tool.NewDefaultShell().Type == tool.ShellPowerShell
	localRouter := NewRuntimeRouter(RuntimeServices{Environment: NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, "/workspace")})
	defer localRouter.Close()
	if got := localRouter.turnEnvironmentHasPowerShell(&turn.TurnStartParams{}); got != wantLocal {
		t.Fatalf("implicit local has_powershell = %t, want %t", got, wantLocal)
	}
	if got := localRouter.turnEnvironmentHasPowerShell(&turn.TurnStartParams{
		Environments: []map[string]any{{"environmentId": "local"}},
	}); got != wantLocal {
		t.Fatalf("explicit local has_powershell = %t, want %t", got, wantLocal)
	}

	remoteManager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, "/workspace")
	if _, err := remoteManager.Add(&EnvironmentAddParams{EnvironmentID: "windows", ExecServerURL: "ws://127.0.0.1:1234"}); err != nil {
		t.Fatal(err)
	}
	if _, err := remoteManager.Add(&EnvironmentAddParams{EnvironmentID: "linux", ExecServerURL: "ws://127.0.0.1:1235"}); err != nil {
		t.Fatal(err)
	}
	if err := remoteManager.SetInfo("windows", EnvironmentShellInfo{Name: "pwsh", Path: "pwsh.exe"}, "/workspace"); err != nil {
		t.Fatal(err)
	}
	if err := remoteManager.SetInfo("linux", EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, "/workspace"); err != nil {
		t.Fatal(err)
	}
	remoteRouter := NewRuntimeRouter(RuntimeServices{Environment: remoteManager})
	defer remoteRouter.Close()
	if !remoteRouter.turnEnvironmentHasPowerShell(&turn.TurnStartParams{
		Environments: []map[string]any{{"environmentId": "windows"}},
	}) {
		t.Fatal("remote PowerShell environment was not detected")
	}
	if remoteRouter.turnEnvironmentHasPowerShell(&turn.TurnStartParams{
		Environments: []map[string]any{{"environmentId": "linux"}},
	}) {
		t.Fatal("remote bash environment reported PowerShell")
	}
	// Any ready environment counts, even when the local one is not the primary.
	if !remoteRouter.turnEnvironmentHasPowerShell(&turn.TurnStartParams{
		Environments: []map[string]any{{"environmentId": "linux"}, {"environmentId": "windows"}},
	}) {
		t.Fatal("multi-environment turn missed the PowerShell environment")
	}
	// Pending and failed attachments are not ready turn environments.
	if remoteRouter.turnEnvironmentHasPowerShell(&turn.TurnStartParams{
		Environments: []map[string]any{{
			"environmentId": "windows",
			"config":        map[string]any{"state": "pending"},
		}},
	}) {
		t.Fatal("pending environment counted as ready")
	}
}
