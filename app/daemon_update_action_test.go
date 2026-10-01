package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"codex_go/appserverdaemon"
	codextui "codex_go/tui"
)

// stubDaemonUpdateLaunch replaces the CLI relaunch edges for one test.
func stubDaemonUpdateLaunch(t *testing.T) (*string, *[]string, *[]string) {
	t.Helper()
	originalExecutable := daemonUpdateExecutable
	originalRunner := daemonUpdateRunner
	executable := ""
	args := []string{}
	env := []string{}
	daemonUpdateExecutable = func() (string, error) { return "/usr/local/bin/codex", nil }
	daemonUpdateRunner = func(_ context.Context, gotExecutable string, gotArgs []string, gotEnv []string, _ io.Writer, _ io.Writer) error {
		executable = gotExecutable
		args = append([]string(nil), gotArgs...)
		env = append([]string(nil), gotEnv...)
		return nil
	}
	t.Cleanup(func() {
		daemonUpdateExecutable = originalExecutable
		daemonUpdateRunner = originalRunner
	})
	return &executable, &args, &env
}

// TestRunDaemonUpdateActionLikeRust pins the relaunch Rust
// run_update_action(UpdateAction::Daemon) performs: this CLI with the source's
// arguments, the foreground handoff marker, and the two status lines.
func TestRunDaemonUpdateActionLikeRust(t *testing.T) {
	executable, args, env := stubDaemonUpdateLaunch(t)
	var stdout bytes.Buffer
	if err := runDaemonUpdateAction(context.Background(), codextui.UpdateActionDaemonThisCli, &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonUpdateAction error = %v", err)
	}
	if *executable != "/usr/local/bin/codex" {
		t.Fatalf("relaunched executable = %q", *executable)
	}
	want := []string{"app-server", "daemon", "update", "--from-cli", "--yes"}
	if len(*args) != len(want) {
		t.Fatalf("args = %#v, want %#v", *args, want)
	}
	for i := range want {
		if (*args)[i] != want[i] {
			t.Fatalf("args = %#v, want %#v", *args, want)
		}
	}
	found := false
	for _, entry := range *env {
		if entry == appserverdaemon.TelemetryHandoffEnv+"=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env is missing the handoff marker: %#v", *env)
	}
	out := stdout.String()
	if !strings.Contains(out, "Updating the local background server...") ||
		!strings.Contains(out, "Relaunch Codex to reconnect.") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestRunDaemonUpdateActionIgnoresOtherActionsLikeRust pins that an ordinary
// update action never relaunches the CLI here.
func TestRunDaemonUpdateActionIgnoresOtherActionsLikeRust(t *testing.T) {
	executable, _, _ := stubDaemonUpdateLaunch(t)
	if err := runDaemonUpdateAction(context.Background(), codextui.UpdateActionNPMGlobalLatest, io.Discard, io.Discard); err != nil {
		t.Fatalf("runDaemonUpdateAction error = %v", err)
	}
	if *executable != "" {
		t.Fatalf("relaunched %q for a non-daemon action", *executable)
	}
	if err := runPendingDaemonUpdate(context.Background(), nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("runPendingDaemonUpdate(nil) error = %v", err)
	}
}

// TestRunDaemonUpdateActionReportsFailuresLikeRust pins Rust's failure copy and
// the missing-CLI refusal.
func TestRunDaemonUpdateActionReportsFailuresLikeRust(t *testing.T) {
	stubDaemonUpdateLaunch(t)
	daemonUpdateRunner = func(context.Context, string, []string, []string, io.Writer, io.Writer) error {
		return errors.New("exit status 1")
	}
	err := runDaemonUpdateAction(context.Background(), codextui.UpdateActionDaemonPublicStable, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Daemon update failed with status") {
		t.Fatalf("error = %v, want the daemon update failure copy", err)
	}

	daemonUpdateExecutable = func() (string, error) { return "", nil }
	err = runDaemonUpdateAction(context.Background(), codextui.UpdateActionDaemonPublicStable, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Cannot locate the launching Codex CLI") {
		t.Fatalf("error = %v, want the missing-CLI refusal", err)
	}
}
