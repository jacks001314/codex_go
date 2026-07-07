package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"codex_go/internal/appserver"
	"codex_go/internal/appserverdaemon"
	"codex_go/internal/auth"
	"codex_go/internal/cli"
	"codex_go/internal/remotecontrol"
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

func runRemoteControl(ctx context.Context, opts cli.RemoteControlOptions, stdout io.Writer) error {
	switch opts.Subcommand {
	case "":
		return runRemoteControlForeground(ctx, opts, stdout)
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

func runRemoteControlForeground(ctx context.Context, opts cli.RemoteControlOptions, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
	defer stopSignal()
	if err := appserverdaemon.EnsureSupportedPlatform(); err != nil {
		return err
	}
	if !opts.JSON {
		fmt.Fprintln(stdout, "Starting app-server with remote control enabled...")
	}
	socketDir, err := os.MkdirTemp("", "codex-rc-")
	if err != nil {
		return fmt.Errorf("failed to create private app-server socket directory: %w", err)
	}
	defer os.RemoveAll(socketDir)
	socketPath := filepath.Join(socketDir, "rc.sock")

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
