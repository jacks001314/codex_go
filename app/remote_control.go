package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/features"
	"codex_go/remotecontrol"
)

const (
	foregroundSocketConnectTimeout    = 10 * time.Second
	foregroundSocketConnectRetryDelay = 50 * time.Millisecond
	foregroundAppServerAbortTimeout   = time.Second
)

type remoteControlStartJSON struct {
	Mode          string                                    `json:"mode"`
	Status        remotecontrol.ConnectionStatus            `json:"status"`
	ServerName    string                                    `json:"serverName"`
	EnvironmentID *string                                   `json:"environmentId,omitempty"`
	TimedOut      bool                                      `json:"timedOut"`
	Daemon        *appserverdaemon.RemoteControlStartOutput `json:"daemon,omitempty"`
}

// remoteControlNoDaemonConflict mirrors Rust remote_control_cmd::run's
// rejection of `--no-daemon` together with an explicit subcommand.
const remoteControlNoDaemonConflict = "`--no-daemon` cannot be used with a remote-control subcommand"

func runRemoteControl(ctx context.Context, opts cli.RemoteControlOptions, root *cli.RootOptions, stdout io.Writer) error {
	if opts.NoDaemon && opts.Subcommand != "" {
		return errors.New(remoteControlNoDaemonConflict)
	}
	if opts.Subcommand == "" {
		return runBareRemoteControl(ctx, opts, root, stdout)
	}
	switch opts.Subcommand {
	case "start":
		return runRemoteControlStart(opts, stdout)
	case "stop":
		return runRemoteControlStop(opts, stdout)
	case "pair":
		return runRemoteControlPair(opts, stdout)
	default:
		return fmt.Errorf("unknown remote-control subcommand %s", opts.Subcommand)
	}
}

// remoteControlDaemonStart starts (or reuses) the managed daemon with no extra
// feature overrides (Rust `codex_app_server_daemon::start_with_features(&Default::default())`).
// It is a variable so tests can exercise the launch policy without a packaged
// CLI.
var remoteControlDaemonStart = func() (*appserverdaemon.LifecycleOutput, error) {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	return runner.StartWithFeatures(nil)
}

// remoteControlEnableOnSocket enables remote control through the daemon's
// control socket with the command's foreground timeouts (Rust
// `codex_app_server_daemon::enable_remote_control_on_socket`). It is a variable
// so tests can observe the socket the launch selected.
var remoteControlEnableOnSocket = func(socketPath string) (appserverdaemon.RemoteControlReadyStatus, error) {
	return appserverdaemon.EnableRemoteControlOnSocket(socketPath, foregroundSocketConnectTimeout, foregroundSocketConnectRetryDelay)
}

// remoteControlDaemonEligibleFor reports whether a bare `remote-control` launch
// may use the managed daemon. It is a variable so tests can drive both branches.
var remoteControlDaemonEligibleFor = remoteControlDaemonEligible

// remoteControlRunForeground is the foreground-server seam (tests replace it so
// a policy test does not bind a real control socket).
var remoteControlRunForeground = runRemoteControlForeground

// runBareRemoteControl mirrors Rust remote_control_cmd::run's bare-command
// branch (#50803): the managed daemon serves `codex remote-control` when daemon
// auto-start is enabled and the environment is eligible; `--no-daemon`, `--json`
// and ineligible launches keep the foreground server. A daemon that reports no
// backend, or a Windows launcher that forbids detaching one, also falls back to
// the foreground server.
func runBareRemoteControl(ctx context.Context, opts cli.RemoteControlOptions, root *cli.RootOptions, stdout io.Writer) error {
	if opts.NoDaemon || opts.JSON || !remoteControlDaemonEligibleFor(root) {
		return remoteControlRunForeground(ctx, opts, stdout)
	}
	printRemoteControlProgress(stdout, opts.JSON, "Starting app-server daemon with remote control enabled...")
	if err := appserverdaemon.EnsureSupportedPlatform(); err != nil {
		return err
	}
	start, err := remoteControlDaemonStart()
	if err != nil {
		// Rust #48491: a Windows launcher that forbids detaching a background
		// process falls back to the foreground server; every other launch
		// failure is fatal.
		if appserverdaemon.IsDetachedLaunchRestricted(err) {
			return remoteControlRunForeground(ctx, opts, stdout)
		}
		return err
	}
	if start == nil || start.Backend == nil {
		return remoteControlRunForeground(ctx, opts, stdout)
	}
	status, err := remoteControlEnableOnSocket(start.SocketPath)
	if err != nil {
		return err
	}
	output := &appserverdaemon.RemoteControlReadyOutput{
		Daemon:        &appserverdaemon.RemoteControlStartOutput{Start: start},
		RemoteControl: status,
	}
	if err := ensureRemoteControlStartable(&output.RemoteControl); err != nil {
		return err
	}
	if opts.JSON {
		return writeRemoteControlStartJSON(stdout, "daemon", &output.RemoteControl, output.Daemon)
	}
	fmt.Fprintf(stdout, "%s\n", remoteControlStartMessage(&output.RemoteControl, false))
	return nil
}

// remoteControlDaemonEligible mirrors Rust remote_control_cmd::daemon_eligible
// (#50803): the managed daemon serves a bare launch only when the launch carries
// no configuration overrides, no workload identity and no executor selection,
// the launcher is not an elevated Windows process, the effective configuration
// enables `daemon_auto_start`, and CODEX_HOME is not on a Windows-mounted WSL
// filesystem. Rust's Result only surfaces its token query, which the Go probe
// cannot fail.
func remoteControlDaemonEligible(root *cli.RootOptions) bool {
	if rootHasRawConfigOverrides(root) {
		return false
	}
	if auth.IsWorkloadIdentitySelected() {
		return false
	}
	if _, selected := os.LookupEnv(appserver.CodexExecServerURLEnvVar); selected {
		return false
	}
	if appserverdaemon.IsElevated() {
		return false
	}
	cfg, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), nil)
	if err != nil || cfg == nil {
		return false
	}
	if !features.Enabled(cfg.FeatureSettings(), "daemon_auto_start") {
		return false
	}
	return !daemonWSLDrvfsDetector(auth.DefaultCodexHome())
}

// rootHasRawConfigOverrides reports whether the launch carries the raw
// configuration overrides Rust's CliConfigOverrides holds before dispatch: the
// top-level `-c` values plus the `--enable`/`--disable` feature toggles, which
// cli_main folds into them.
func rootHasRawConfigOverrides(root *cli.RootOptions) bool {
	if root == nil {
		return false
	}
	return len(root.ConfigOverrides) > 0 || len(root.EnableFeatures) > 0 || len(root.DisableFeatures) > 0
}

// printRemoteControlProgress mirrors Rust print_remote_control_progress: --json
// launches are silent because the JSON document is the output.
func printRemoteControlProgress(stdout io.Writer, jsonOutput bool, message string) {
	if jsonOutput {
		return
	}
	fmt.Fprintln(stdout, message)
}

func runRemoteControlForeground(ctx context.Context, opts cli.RemoteControlOptions, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
	defer stopSignal()
	if err := appserverdaemon.EnsureSupportedPlatform(); err != nil {
		return err
	}
	printRemoteControlProgress(stdout, opts.JSON, "Starting app-server with remote control enabled...")
	socketPath, cleanupSocket, err := foregroundRemoteControlSocketPath()
	if err != nil {
		return err
	}
	defer cleanupSocket()

	serverCtx, cancelServer := context.WithCancel(ctx)
	defer cancelServer()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- appserver.ServeUnixSocket(serverCtx, &appserver.UnixSocketOptions{
			CodexHome: auth.DefaultCodexHome(),
			Listen:    "unix://" + socketPath,
			RuntimeOptions: &appserver.RuntimeRouterOptions{
				RemoteControlStartupMode: appserver.RemoteControlStartupEnabledEphemeral,
			},
		})
	}()

	readyDone := make(chan remoteControlReadyResult, 1)
	go func() {
		status, err := appserverdaemon.EnableRemoteControlOnSocket(socketPath, foregroundSocketConnectTimeout, foregroundSocketConnectRetryDelay)
		readyDone <- remoteControlReadyResult{Status: status, Err: err}
	}()

	var status appserverdaemon.RemoteControlReadyStatus
	select {
	case <-ctx.Done():
		cancelServer()
		waitForForegroundAppServer(serverDone)
		return nil
	case err := <-serverDone:
		if err == nil {
			return errors.New("foreground app-server exited before remote control became ready")
		}
		return fmt.Errorf("foreground app-server exited before remote control became ready: %w", err)
	case ready := <-readyDone:
		if ready.Err != nil {
			cancelServer()
			waitForForegroundAppServer(serverDone)
			return ready.Err
		}
		status = ready.Status
	}

	if err := ensureRemoteControlStartable(&status); err != nil {
		cancelServer()
		waitForForegroundAppServer(serverDone)
		return err
	}
	if opts.JSON {
		if err := writeRemoteControlStartJSON(stdout, "foreground", &status, nil); err != nil {
			cancelServer()
			waitForForegroundAppServer(serverDone)
			return err
		}
	} else {
		fmt.Fprintf(stdout, "%s\n", remoteControlStartMessage(&status, true))
	}

	select {
	case <-ctx.Done():
		cancelServer()
		waitForForegroundAppServer(serverDone)
		return nil
	case err := <-serverDone:
		if err != nil {
			return fmt.Errorf("foreground app-server exited with an error: %w", err)
		}
		return nil
	}
}

type remoteControlReadyResult struct {
	Status appserverdaemon.RemoteControlReadyStatus
	Err    error
}

func runRemoteControlStart(opts cli.RemoteControlOptions, stdout io.Writer) error {
	if err := appserverdaemon.EnsureSupportedPlatform(); err != nil {
		return err
	}
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	start, err := runner.EnsureRemoteControlStarted()
	if err != nil {
		return err
	}
	status, err := appserverdaemon.EnableRemoteControlOnSocket(runner.Daemon.Paths.SocketPath, appserverdaemon.RemoteControlReadyTimeout, foregroundSocketConnectRetryDelay)
	if err != nil {
		return err
	}
	output := &appserverdaemon.RemoteControlReadyOutput{
		Daemon:        start,
		RemoteControl: status,
	}
	if err := ensureRemoteControlStartable(&output.RemoteControl); err != nil {
		return err
	}
	if opts.JSON {
		return writeRemoteControlStartJSON(stdout, "daemon", &output.RemoteControl, output.Daemon)
	}
	fmt.Fprintf(stdout, "%s\n", remoteControlStartMessage(&output.RemoteControl, false))
	return nil
}

func runRemoteControlStop(opts cli.RemoteControlOptions, stdout io.Writer) error {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	output, err := runner.Run(appserverdaemon.LifecycleStop)
	if err != nil {
		return err
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(output)
	}
	switch output.Status {
	case appserverdaemon.StatusStopped:
		fmt.Fprintln(stdout, "Remote control stopped.")
	case appserverdaemon.StatusNotRunning:
		fmt.Fprintln(stdout, "Remote control is not running.")
	default:
		fmt.Fprintf(stdout, "Remote control stop completed with status %s.\n", output.Status)
	}
	return nil
}

func runRemoteControlPair(opts cli.RemoteControlOptions, stdout io.Writer) error {
	manager := newRemoteControlManager()
	manager.Enable(&remotecontrol.EnableParams{Ephemeral: true})
	response, err := manager.StartPairing(&remotecontrol.PairingStartParams{ManualCode: true})
	if err != nil {
		return err
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(response)
	}
	if response.ManualPairingCode == nil {
		return errors.New("remote-control pairing response did not include a manual pairing code")
	}
	fmt.Fprintf(stdout, "Pairing code: %s\n", *response.ManualPairingCode)
	return nil
}

func writeRemoteControlStartJSON(stdout io.Writer, mode string, status *appserverdaemon.RemoteControlReadyStatus, daemon *appserverdaemon.RemoteControlStartOutput) error {
	if status == nil {
		status = &appserverdaemon.RemoteControlReadyStatus{}
	}
	payload := &remoteControlStartJSON{
		Mode:          mode,
		Status:        status.Status,
		ServerName:    status.ServerName,
		EnvironmentID: status.EnvironmentID,
		TimedOut:      status.TimedOut,
		Daemon:        daemon,
	}
	return json.NewEncoder(stdout).Encode(payload)
}

func ensureRemoteControlStartable(status *appserverdaemon.RemoteControlReadyStatus) error {
	if status == nil {
		return errors.New("Remote control is unavailable.")
	}
	switch status.Status {
	case remotecontrol.StatusConnected, remotecontrol.StatusConnecting:
		return nil
	case remotecontrol.StatusErrored:
		return fmt.Errorf("Remote control is enabled on %s but the connection is errored.", status.ServerName)
	case remotecontrol.StatusDisabled:
		return fmt.Errorf("Remote control is disabled on %s.", status.ServerName)
	default:
		return nil
	}
}

func remoteControlStartMessage(status *appserverdaemon.RemoteControlReadyStatus, foreground bool) string {
	if status == nil {
		return "Remote control is unavailable."
	}
	switch status.Status {
	case remotecontrol.StatusConnected:
		message := fmt.Sprintf("This machine is available for remote control as %s.", status.ServerName)
		if foreground {
			message += "\nPress Ctrl-C to stop."
		}
		return message
	case remotecontrol.StatusConnecting:
		return fmt.Sprintf("Remote control is enabled on %s and still connecting.", status.ServerName)
	case remotecontrol.StatusErrored:
		return fmt.Sprintf("Remote control is enabled on %s but the connection is errored.", status.ServerName)
	case remotecontrol.StatusDisabled:
		return fmt.Sprintf("Remote control is disabled on %s.", status.ServerName)
	default:
		return fmt.Sprintf("Remote control status is %s on %s.", status.Status, status.ServerName)
	}
}

func waitForForegroundAppServer(done <-chan error) {
	timer := time.NewTimer(foregroundAppServerAbortTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func newRemoteControlManager() *remotecontrol.Manager {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "codex"
	}
	return remotecontrol.NewManager(host, "local")
}
