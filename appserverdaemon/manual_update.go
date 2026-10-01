package appserverdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"codex_go/install"
)

// Manual daemon package updates (Rust app-server-daemon/src/manual_update.rs).
//
// The CLI never installs directly: it forwards `update\n` to the running
// updater's private socket, or starts a one-shot worker updater when none is
// running (which is the normal state of a pinned installation). A launch under
// the operation lock may additionally authorize returning to production updates
// exactly once, which is how `daemon update` undoes a pin.

const (
	// manualUpdateRequest is the whole request the updater accepts.
	manualUpdateRequest = "update\n"
	// manualUpdateResponseLimit bounds the JSON response the CLI reads.
	manualUpdateResponseLimit = 16 * 1024
	// manualUpdateConnectRetry is how often the CLI retries the updater socket.
	manualUpdateConnectRetry = 50 * time.Millisecond
	// manualUpdateWorkerTimeout bounds waiting for a started worker updater.
	manualUpdateWorkerTimeout = 30 * time.Second
	// manualUpdateReadTimeout bounds reading the request on the updater side.
	manualUpdateRequestTimeout = 5 * time.Second
)

// ManualUpdateOptions injects the process and socket edges so the lane is
// testable without spawning installers.
type ManualUpdateOptions struct {
	// UpdateLoop are the options one update run uses (installer + identity edges).
	UpdateLoop *UpdateLoopOptions
	// Migration are the installer, probing, and lifecycle edges a legacy
	// standalone selection migrates with instead of a forwarded request.
	Migration *MigrationOptions
	// Connect opens the updater socket; defaults to a codexuds-validated dial.
	Connect func(socketPath string, timeout time.Duration) (net.Conn, error)
	// StartWorker starts the one-shot worker updater for a pinned selection.
	StartWorker func(daemon *Daemon, settings *DaemonSettings, restoreRelease string) error
	// ConnectTimeout bounds waiting for the worker's socket to accept.
	ConnectTimeout time.Duration
}

func normalizeManualUpdateOptions(options *ManualUpdateOptions) *ManualUpdateOptions {
	if options == nil {
		options = &ManualUpdateOptions{}
	}
	if options.UpdateLoop == nil {
		options.UpdateLoop = DefaultUpdateLoopOptions()
	}
	if options.Connect == nil {
		options.Connect = connectManualUpdaterSocket
	}
	if options.StartWorker == nil {
		options.StartWorker = startManualUpdateWorker
	}
	if options.ConnectTimeout <= 0 {
		options.ConnectTimeout = manualUpdateWorkerTimeout
	}
	return options
}

// RequestManualUpdate asks the managed updater to install the latest release
// (Rust update_loop::request_manual_update -> manual_update::request).
func RequestManualUpdate(ctx context.Context, codexHome string, options *ManualUpdateOptions) (*UpdateOutput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	options = normalizeManualUpdateOptions(options)
	daemon := NewDaemonForCodexHome(codexHome, "")
	daemon.refreshInstallation()
	// A daemon that still serves the pre-dedicated standalone package moves to
	// its dedicated package instead of forwarding a request to its updater
	// (Rust update_loop::request_manual_update).
	if filepath.Base(daemon.Paths.PIDFile) == LegacyPIDFileName {
		supported, err := manualUpdateSupported(daemon)
		if err != nil {
			return nil, err
		}
		if supported {
			return runDaemonMigration(ctx, daemon, options.Migration)
		}
	}
	socketPath := manualUpdateSocketPath(daemon)
	if socketPath == "" {
		return nil, ErrDaemonPathsRequired
	}
	if conn, err := options.Connect(socketPath, manualUpdateConnectRetry); err == nil {
		return exchangeManualUpdate(conn, socketPath)
	}

	// No updater is running. Take the operation lock and re-check before
	// deciding whether an installer run is even authorized.
	lock, err := acquireExclusiveFileLock(daemon.Paths.OperationLockFile, OperationLockTimeout, OperationLockRetry, "daemon operation lock")
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if conn, err := options.Connect(socketPath, manualUpdateConnectRetry); err == nil {
		return exchangeManualUpdate(conn, socketPath)
	}
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	supported, err := manualUpdateSupported(daemon)
	if err != nil {
		return nil, err
	}
	running, err := prepareRunningBackend(daemon, settings)
	if err != nil {
		return nil, err
	}
	if !supported || (!running && prepareSocketAnswers(daemon)) {
		return unsupportedUpdateOutput(daemon), nil
	}
	if err := stopDaemonUpdater(daemon, settings); err != nil {
		return nil, err
	}
	restoreRelease := ""
	if !isStableStandaloneRelease(daemonCodexHome(daemon), ManagedCodexBin(daemonCodexHome(daemon))) {
		if _, _, name, err := selectedRelease(daemon); err == nil {
			restoreRelease = name
		}
	}
	if err := options.StartWorker(daemon, settings, restoreRelease); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(options.ConnectTimeout)
	var lastErr error
	for {
		conn, err := options.Connect(socketPath, manualUpdateConnectRetry)
		if err == nil {
			return exchangeManualUpdate(conn, socketPath)
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for the daemon updater to accept a manual request: %w", lastErr)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		time.Sleep(manualUpdateConnectRetry)
	}
}

// exchangeManualUpdate writes the request and decodes the updater's answer,
// which is either an update result or the updater's error text
// (Rust's `Result<UpdateOutput, String>` wire shape).
func exchangeManualUpdate(conn net.Conn, socketPath string) (*UpdateOutput, error) {
	if conn == nil {
		return nil, fmt.Errorf("failed to connect to the daemon updater at %s", socketPath)
	}
	defer conn.Close()
	// A peer that is another user or an elevated token must never receive the
	// request; an unavailable identity is accepted on the private socket
	// directory's contract (see socket_peer_*).
	if err := ensureSocketPeerAllowed(conn); err != nil {
		return nil, fmt.Errorf("refusing the daemon updater connection on %s: %w", socketPath, err)
	}
	if _, err := conn.Write([]byte(manualUpdateRequest)); err != nil {
		return nil, fmt.Errorf("failed to send the daemon updater request: %w", err)
	}
	response, err := io.ReadAll(io.LimitReader(conn, manualUpdateResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to read the daemon updater response: %w", err)
	}
	if len(response) == 0 {
		return nil, errors.New("daemon updater disconnected before responding")
	}
	var outcome struct {
		OK  *UpdateOutput `json:"Ok"`
		Err *string       `json:"Err"`
	}
	if err := json.Unmarshal(response, &outcome); err != nil {
		return nil, fmt.Errorf("invalid response from daemon updater: %w", err)
	}
	if outcome.Err != nil {
		return nil, errors.New(*outcome.Err)
	}
	if outcome.OK == nil {
		return nil, fmt.Errorf("invalid response from daemon updater: %s", strings.TrimSpace(string(response)))
	}
	return outcome.OK, nil
}

// connectManualUpdaterSocket dials the updater socket without waiting.
func connectManualUpdaterSocket(socketPath string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = manualUpdateConnectRetry
	}
	return net.DialTimeout("unix", socketPath, timeout)
}

// startManualUpdateWorker starts the one-shot worker updater a manual request
// needs when none is running (Rust manual_update::request).
func startManualUpdateWorker(daemon *Daemon, settings *DaemonSettings, restoreRelease string) error {
	currentExe, err := currentExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to resolve the current updater executable: %w", err)
	}
	backend := NewPIDUpdateLoopBackend(BackendPaths{
		CodexBin:       currentExe,
		PIDFile:        daemon.Paths.PIDFile,
		UpdatePIDFile:  daemon.Paths.UpdatePIDFile,
		RestoreRelease: restoreRelease,
	})
	if _, err := startPIDBackend(backend); err != nil {
		return err
	}
	return nil
}

// manualUpdateDisposition mirrors Rust manual_update::RequestDisposition.
type manualUpdateDisposition int

const (
	manualUpdateContinue manualUpdateDisposition = iota
	manualUpdateUnchanged
	manualUpdateStop
)

// handleManualUpdateConnection serves one request on the updater's socket
// (Rust manual_update::handle_request).
func handleManualUpdateConnection(conn net.Conn, runner *LifecycleRunner, options *UpdateLoopOptions, runningUpdaterIdentity *install.ExecutableIdentity, trigger UpdateTrigger) (manualUpdateDisposition, error) {
	if conn == nil {
		return manualUpdateUnchanged, nil
	}
	defer conn.Close()
	// Rust refuses a peer that fails the Windows peer check; an unavailable
	// identity is accepted on the private socket directory's contract.
	if err := ensureSocketPeerAllowed(conn); err != nil {
		return manualUpdateUnchanged, nil
	}
	request := make([]byte, len(manualUpdateRequest))
	_ = conn.SetReadDeadline(time.Now().Add(manualUpdateRequestTimeout))
	if _, err := io.ReadFull(conn, request); err != nil || string(request) != manualUpdateRequest {
		return manualUpdateUnchanged, nil
	}
	output, reexecBin, err := runManualUpdate(context.Background(), runner, options, runningUpdaterIdentity, trigger)
	interrupted := err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, errUpdaterInterrupted))
	payload := map[string]any{}
	if err != nil {
		payload["Err"] = err.Error()
	} else {
		payload["Ok"] = output
	}
	encoded, encodeErr := json.Marshal(payload)
	if encodeErr != nil {
		return manualUpdateStop, encodeErr
	}
	_ = conn.SetWriteDeadline(time.Now().Add(manualUpdateRequestTimeout))
	_, writeErr := conn.Write(encoded)
	if interrupted {
		return manualUpdateStop, nil
	}
	if writeErr != nil {
		return manualUpdateStop, writeErr
	}
	if strings.TrimSpace(reexecBin) != "" {
		// The managed binary changed: answer first, then hand the socket to the
		// replacement updater (Rust adopt_managed_updater).
		if err := options.ReexecUpdater(reexecBin); err != nil {
			return manualUpdateStop, err
		}
		return manualUpdateStop, nil
	}
	if err == nil && output != nil && output.Status == UpdateUnsupported {
		return manualUpdateUnchanged, nil
	}
	return manualUpdateContinue, nil
}

// errUpdaterInterrupted reports that an update run was interrupted before it
// finished, which makes the updater exit (Rust's Interrupted io error).
var errUpdaterInterrupted = errors.New("daemon update interrupted")

// runManualUpdate performs one manual update and reports what happened
// (Rust manual_update::run).
func runManualUpdate(ctx context.Context, runner *LifecycleRunner, options *UpdateLoopOptions, runningUpdaterIdentity *install.ExecutableIdentity, trigger UpdateTrigger) (*UpdateOutput, string, error) {
	runner = normalizeLifecycleRunner(runner)
	options = normalizeUpdateLoopOptions(options)
	daemon := runner.Daemon
	home := daemonCodexHome(daemon)
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, "", err
	}
	running, err := prepareRunningBackend(daemon, settings)
	if err != nil {
		return nil, "", err
	}
	stable := isStableStandaloneRelease(home, ManagedCodexBin(home))
	supported := false
	if trigger.Kind == UpdateTriggerRestoreProduction {
		supported, err = manualUpdateSupported(daemon)
		if err != nil {
			return nil, "", err
		}
	}
	if !(stable || (trigger.Kind == UpdateTriggerRestoreProduction && supported)) ||
		(!running && prepareSocketAnswers(daemon)) {
		return unsupportedUpdateOutput(daemon), "", nil
	}
	managedCodexPath := ManagedCodexBin(home)
	_, previousRelease, _, err := selectedRelease(daemon)
	if err != nil {
		return nil, "", err
	}
	previousIdentity, err := install.ExecutableIdentityFromFile(managedCodexPath)
	if err != nil {
		return nil, "", err
	}
	control, outcome, reexecBin, err := updateOnce(ctx, runner, runningUpdaterIdentity, options, trigger)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, "", errUpdaterInterrupted
		}
		return nil, "", err
	}
	if control == UpdateLoopStop && outcome == nil {
		return nil, "", errUpdaterInterrupted
	}
	managedNow := ManagedCodexBin(home)
	installedVersion, err := managedCodexVersionStrict(managedNow)
	if err != nil {
		return nil, "", err
	}
	_, currentRelease, _, err := selectedRelease(daemon)
	if err != nil {
		return nil, "", err
	}
	currentIdentity, err := install.ExecutableIdentityFromFile(managedNow)
	if err != nil {
		return nil, "", err
	}
	updated := previousRelease != currentRelease || previousIdentity != currentIdentity
	message := manualUpdateMessage(outcome)
	status := UpdateNoUpdate
	if updated {
		status = UpdateUpdated
	}
	return &UpdateOutput{
		Status:           status,
		ManagedCodexPath: managedNow,
		InstalledVersion: &installedVersion,
		RunningVersion:   runningAppServerVersion(daemon),
		Message:          message,
	}, reexecBin, nil
}

// manualUpdateMessage maps the restart outcome to the operator-facing message
// (Rust manual_update::run's message match).
func manualUpdateMessage(outcome *RestartIfRunningOutcome) string {
	if outcome == nil {
		return ManualUpdateNotRunningMessage
	}
	switch *outcome {
	case RestartRestarted:
		return ManualUpdateRestartedMessage
	case RestartAlreadyCurrent:
		return ManualUpdateCurrentMessage
	default:
		return ManualUpdateNotRunningMessage
	}
}

// normalizeLifecycleRunner keeps the lane usable when a caller passes none.
func normalizeLifecycleRunner(runner *LifecycleRunner) *LifecycleRunner {
	if runner == nil {
		return &LifecycleRunner{}
	}
	return runner
}
