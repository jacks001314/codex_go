package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/doctor"
	"codex_go/install"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// A daemon update the TUI recorded is executed by the CLI after the TUI
// restores the terminal (Rust cli/src/main.rs::run_update_action for
// UpdateAction::Daemon, and tui/src/update_action.rs).

// runPendingDaemonUpdate runs the update a TUI asked for, if any.
func runPendingDaemonUpdate(ctx context.Context, model *codextea.Model, stdout io.Writer, stderr io.Writer) error {
	if model == nil {
		return nil
	}
	return runDaemonUpdateAction(ctx, model.PendingUpdateAction(), stdout, stderr)
}

// daemonUpdateExecutable resolves the CLI that relaunches for a daemon update;
// injectable so tests can pin the launch without a real process.
var daemonUpdateExecutable = os.Executable

// daemonUpdateRunner runs the relaunched CLI; injectable for the same reason.
var daemonUpdateRunner = func(ctx context.Context, executable string, args []string, env []string, stdout io.Writer, stderr io.Writer) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = env
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

// runDaemonUpdateAction relaunches this CLI to perform the daemon update. The
// foreground handoff marker keeps the child from reporting its own daemon
// observation (Rust run_update_action's `env(HANDOFF_ENV, "1")`).
func runDaemonUpdateAction(ctx context.Context, action codextui.UpdateAction, stdout io.Writer, stderr io.Writer) error {
	source, ok := action.DaemonUpdateSourceOf()
	if !ok {
		return nil
	}
	executable, err := daemonUpdateExecutable()
	if err != nil || strings.TrimSpace(executable) == "" {
		return errors.New("Cannot locate the launching Codex CLI")
	}
	fmt.Fprintln(stdout, "Updating the local background server...")
	env := append(os.Environ(), appserverdaemon.TelemetryHandoffEnv+"=1")
	if err := daemonUpdateRunner(ctx, executable, source.CommandArgs(), env, stdout, stderr); err != nil {
		return fmt.Errorf("Daemon update failed with status %v", err)
	}
	fmt.Fprintln(stdout, "Relaunch Codex to reconnect.")
	return nil
}

// interactiveDaemonCLIIdentity describes this CLI for the /daemon menu: its
// executable, its version, and whether it has a complete local package to copy.
func interactiveDaemonCLIIdentity() (string, string, bool) {
	executable, err := os.Executable()
	if err != nil {
		return "", "", false
	}
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return "", "", false
	}
	return executable, doctor.Version(), install.PackageLayoutFromExe(executable) != nil
}

// interactiveDaemonVersionForEndpoint reports the running local daemon's
// app-server version for the /daemon menu, or "" when it is unknown.
func interactiveDaemonVersionForEndpoint(endpoint *appserverdaemon.RemoteAppServerEndpoint) string {
	if endpoint == nil || endpoint.Kind != appserverdaemon.RemoteEndpointUnixSocket {
		return ""
	}
	socketPath := strings.TrimSpace(endpoint.SocketPath)
	if socketPath == "" {
		return ""
	}
	version, err := appserverdaemon.ProbeAppServerVersionOnSocket(socketPath, appserverdaemon.ControlSocketProbeTimeout)
	if err != nil {
		return ""
	}
	return version
}

// interactiveRemoteEndpointIsExplicitRemote reports an explicit `--remote` app
// server, whose local daemon cannot be managed from this host (Rust
// AppServerTarget::Remote).
func interactiveRemoteEndpointIsExplicitRemote(root *cli.RootOptions) bool {
	return root != nil && strings.TrimSpace(root.Remote) != ""
}
