package appserverdaemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/install"
)

// TestRunPIDUpdateLoopServesManualRequestsLikeRust runs the real updater loop
// against a real socket and drives it with the CLI's own request path.
func TestRunPIDUpdateLoopServesManualRequestsLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.appRunning = true
	stub.socketReady = true
	stubStrictManagedVersion(t, "1.0.0")
	// AF_UNIX sun_path is 108 bytes, so the home stays short.
	home, err := os.MkdirTemp("", "updloop")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	publishStableSelection(t, home, "1.0.0")
	daemon := NewDaemonForCodexHome(home, "")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	// On Windows the updater waits for its launcher to publish the pid record
	// before acknowledging readiness, so this test publishes it like the
	// launcher would (Rust PidBackend::wait_for_ownership).
	startTime, err := readPIDProcessStartTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("readPIDProcessStartTime error = %v", err)
	}
	if err := WritePIDRecord(daemon.Paths.UpdatePIDFile, &PIDRecord{PID: uint32(os.Getpid()), ProcessStartTime: startTime}); err != nil {
		t.Fatalf("WritePIDRecord(updater) error = %v", err)
	}
	installRuns := 0
	options := &UpdateLoopOptions{
		InitialDelay: time.Hour,
		Install: func(context.Context, installerMode, string) error {
			installRuns++
			return nil
		},
		CurrentExe:    func() (string, error) { return daemon.Paths.ManagedCodexBin, nil },
		ReadFile:      os.ReadFile,
		ReexecUpdater: func(string) error { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loopDone := make(chan error, 1)
	go func() { loopDone <- RunPIDUpdateLoop(ctx, runner, options) }()

	socketPath := manualUpdateSocketPath(daemon)
	deadline := time.Now().Add(15 * time.Second)
	var conn net.Conn
	for {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		select {
		case loopErr := <-loopDone:
			t.Fatalf("RunPIDUpdateLoop stopped before serving: %v", loopErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("updater socket %s never accepted a connection: %v", socketPath, err)
		}
		time.Sleep(manualUpdateConnectRetry)
	}
	output, err := exchangeManualUpdate(conn, socketPath)
	if err != nil {
		t.Fatalf("exchangeManualUpdate error = %v", err)
	}
	if output == nil || output.Status != UpdateNoUpdate {
		t.Fatalf("manual update output = %#v, want noUpdate", output)
	}
	if output.Message != ManualUpdateRestartedMessage {
		t.Fatalf("message = %q", output.Message)
	}
	if installRuns != 1 {
		t.Fatalf("install runs = %d, want 1", installRuns)
	}
	cancel()
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("RunPIDUpdateLoop error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunPIDUpdateLoop did not stop after cancellation")
	}
}

// unixSocketPair returns the two ends of a real unix socket connection, which is
// the transport the updater request lane uses.
func unixSocketPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	dir, err := os.MkdirTemp("", "upd")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "upd.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	server := <-accepted
	if server == nil {
		t.Fatal("listener did not accept the connection")
	}
	return server, client
}

// publishStableSelection writes the installer-shaped selection the update lane
// requires and returns the package root and the selected release name.
func publishStableSelection(t *testing.T, home string, version string) (string, string) {
	t.Helper()
	root := filepath.Join(home, "packages", daemonPackagesDirname)
	name := version + "-" + platformTarget()
	release := filepath.Join(root, releasesDirName, name)
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o700); err != nil {
		t.Fatalf("MkdirAll release error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(release, "bin", managedCodexFileName()), []byte(version), 0o700); err != nil {
		t.Fatalf("WriteFile entrypoint error = %v", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll package root error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, autoUpdateVersionFileName), []byte(name), 0o600); err != nil {
		t.Fatalf("WriteFile marker error = %v", err)
	}
	if err := selectDaemonRelease(root, release); err != nil {
		t.Fatalf("selectDaemonRelease error = %v", err)
	}
	return root, name
}

// TestInstallerGuardsLikeRust pins the guard environment each installer mode
// receives and the script markers the updater refuses to run without.
func TestInstallerGuardsLikeRust(t *testing.T) {
	update := installerMode{Kind: installerUpdate, Release: "1.0.0-x"}
	restore := installerMode{Kind: installerRestoreProduction, Release: "local-abc-x"}
	if got := update.env()[installerGuardLatest]; got != "1" {
		t.Fatalf("%s = %q, want 1", installerGuardLatest, got)
	}
	if got := update.env()[installerGuardCurrent]; got != "0" {
		t.Fatalf("%s = %q, want 0", installerGuardCurrent, got)
	}
	if got := restore.env()[installerGuardCurrent]; got != "1" {
		t.Fatalf("restore %s = %q, want 1", installerGuardCurrent, got)
	}
	if got := restore.env()[installerRelease]; got != "latest" {
		t.Fatalf("%s = %q, want latest", installerRelease, got)
	}
	if got := restore.env()[installerNonInteractive]; got != "1" {
		t.Fatalf("%s = %q, want 1", installerNonInteractive, got)
	}

	script := []byte("#!/bin/sh\n# " + installerGuardLatest + " " + installerGuardCurrent + " " + installerDaemonOnly + "\n")
	if err := validateInstallerScript(script, update, "packages/app-server-daemon"); err != nil {
		t.Fatalf("validateInstallerScript(update) error = %v", err)
	}
	if err := validateInstallerScript(script, restore, "packages/app-server-daemon"); err != nil {
		t.Fatalf("validateInstallerScript(restore) error = %v", err)
	}
	// A daemon-owned package root requires daemon-only support.
	if err := validateInstallerScript([]byte(installerGuardLatest), update, "packages/app-server-daemon"); err == nil ||
		!strings.Contains(err.Error(), "daemon-owned packages") {
		t.Fatalf("daemon-owned package error = %v", err)
	}
	// A script without the guard is refused.
	if err := validateInstallerScript([]byte("#!/bin/sh\n"), update, "packages/standalone"); err == nil ||
		!strings.Contains(err.Error(), "does not support "+installerGuardLatest) {
		t.Fatalf("missing guard error = %v", err)
	}
}

// TestManualUpdateSocketPathLikeRust pins where the updater serves requests.
func TestManualUpdateSocketPathLikeRust(t *testing.T) {
	daemon := NewDaemonForCodexHome(filepath.Join(t.TempDir(), "home"), "")
	want := pidPathWithExtension(daemon.Paths.UpdatePIDFile, "sock")
	if got := manualUpdateSocketPath(daemon); got != want || !strings.HasSuffix(got, ".sock") {
		t.Fatalf("manualUpdateSocketPath() = %q, want %q", got, want)
	}
}

// TestManualUpdateSupportedLikeRust pins the two shapes a manual update accepts:
// an installer-published release and a pinned package inside releases/.
func TestManualUpdateSupportedLikeRust(t *testing.T) {
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "")
	if supported, err := manualUpdateSupported(daemon); err != nil || supported {
		t.Fatalf("supported without a selection = %v, %v", supported, err)
	}
	root, _ := publishStableSelection(t, home, "1.0.0")
	daemon.refreshInstallation()
	if supported, err := manualUpdateSupported(daemon); err != nil || !supported {
		t.Fatalf("supported for a stable selection = %v, %v", supported, err)
	}
	// A pinned release (no latest marker, local name) is still supported, which
	// is what lets `daemon update` undo the pin.
	pinned := filepath.Join(root, releasesDirName, "local-deadbeef-"+platformTarget())
	if err := os.MkdirAll(filepath.Join(pinned, "bin"), 0o700); err != nil {
		t.Fatalf("MkdirAll pinned error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "bin", managedCodexFileName()), []byte("pinned"), 0o700); err != nil {
		t.Fatalf("WriteFile pinned entrypoint error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, autoUpdateVersionFileName)); err != nil {
		t.Fatalf("Remove marker error = %v", err)
	}
	if err := selectDaemonRelease(root, pinned); err != nil {
		t.Fatalf("selectDaemonRelease(pinned) error = %v", err)
	}
	daemon.refreshInstallation()
	if supported, err := manualUpdateSupported(daemon); err != nil || !supported {
		t.Fatalf("supported for a pinned selection = %v, %v", supported, err)
	}
}

// TestHandleManualUpdateConnectionRoundTrip serves a real request over a unix
// socket and pins the wire shape the CLI decodes.
func TestHandleManualUpdateConnectionRoundTrip(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.appRunning = true
	stub.socketReady = true
	stubStrictManagedVersion(t, "1.0.0")
	home := t.TempDir()
	publishStableSelection(t, home, "1.0.0")
	daemon := NewDaemonForCodexHome(home, "")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	reinstalled := false
	options := &UpdateLoopOptions{
		Install: func(context.Context, installerMode, string) error {
			reinstalled = true
			return nil
		},
		ReadFile:      os.ReadFile,
		ReexecUpdater: func(string) error { return nil },
	}
	identity, err := ManagedExecutableIdentity(daemon.Paths.ManagedCodexBin, options)
	if err != nil {
		t.Fatalf("ManagedExecutableIdentity error = %v", err)
	}

	server, client := unixSocketPair(t)
	done := make(chan manualUpdateDisposition, 1)
	go func() {
		disposition, _ := handleManualUpdateConnection(server, runner, options, identity, UpdateTrigger{Kind: UpdateTriggerManual})
		done <- disposition
	}()
	if _, err := client.Write([]byte(manualUpdateRequest)); err != nil {
		t.Fatalf("write request error = %v", err)
	}
	response := make([]byte, manualUpdateResponseLimit)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	count, err := client.Read(response)
	if err != nil {
		t.Fatalf("read response error = %v", err)
	}
	if !strings.Contains(string(response[:count]), `"Ok"`) {
		t.Fatalf("response = %q, want an Ok payload", string(response[:count]))
	}
	if !reinstalled {
		t.Fatal("the manual request did not run the installer")
	}
	_ = client.Close()
	if disposition := <-done; disposition != manualUpdateContinue {
		t.Fatalf("disposition = %v, want continue", disposition)
	}
}

// TestHandleManualUpdateConnectionRejectsOtherRequests pins that only the
// documented request is served.
func TestHandleManualUpdateConnectionRejectsOtherRequests(t *testing.T) {
	server, client := unixSocketPair(t)
	done := make(chan manualUpdateDisposition, 1)
	go func() {
		disposition, _ := handleManualUpdateConnection(server, NewLifecycleRunner(nil), nil, nil, UpdateTrigger{Kind: UpdateTriggerManual})
		done <- disposition
	}()
	if _, err := client.Write([]byte("shutdown\n")); err != nil {
		t.Fatalf("write request error = %v", err)
	}
	_ = client.Close()
	if disposition := <-done; disposition != manualUpdateUnchanged {
		t.Fatalf("disposition = %v, want unchanged", disposition)
	}
}

// TestRunManualUpdateReportsLikeRust pins the status and message a manual update
// reports for an unchanged selection.
func TestRunManualUpdateReportsLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.appRunning = true
	stub.socketReady = true
	stubStrictManagedVersion(t, "1.0.0")
	home := t.TempDir()
	publishStableSelection(t, home, "1.0.0")
	daemon := NewDaemonForCodexHome(home, "")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	options := &UpdateLoopOptions{
		Install:       func(context.Context, installerMode, string) error { return nil },
		ReadFile:      os.ReadFile,
		ReexecUpdater: func(string) error { return nil },
	}
	identity, err := ManagedExecutableIdentity(daemon.Paths.ManagedCodexBin, options)
	if err != nil {
		t.Fatalf("ManagedExecutableIdentity error = %v", err)
	}
	output, reexecBin, err := runManualUpdate(context.Background(), runner, options, identity, UpdateTrigger{Kind: UpdateTriggerManual})
	if err != nil {
		t.Fatalf("runManualUpdate error = %v", err)
	}
	if output == nil || output.Status != UpdateNoUpdate {
		t.Fatalf("output = %#v, want noUpdate", output)
	}
	// The stub daemon has no confirmed launch identity, so the manual update
	// replaces the process, which is what Rust reports in that case too.
	if output.Message != ManualUpdateRestartedMessage {
		t.Fatalf("message = %q", output.Message)
	}
	if strings.TrimSpace(reexecBin) != "" {
		t.Fatalf("reexecBin = %q, want none for an unchanged selection", reexecBin)
	}
}

// TestManualUpdateMessagesLikeRust pins the three outcomes an operator sees.
func TestManualUpdateMessagesLikeRust(t *testing.T) {
	restarted, alreadyCurrent, notRunning := RestartRestarted, RestartAlreadyCurrent, RestartNotRunning
	for _, testCase := range []struct {
		outcome *RestartIfRunningOutcome
		want    string
	}{
		{&restarted, ManualUpdateRestartedMessage},
		{&alreadyCurrent, ManualUpdateCurrentMessage},
		{&notRunning, ManualUpdateNotRunningMessage},
		{nil, ManualUpdateNotRunningMessage},
	} {
		if got := manualUpdateMessage(testCase.outcome); got != testCase.want {
			t.Errorf("manualUpdateMessage(%v) = %q, want %q", testCase.outcome, got, testCase.want)
		}
	}
}

// TestRequestManualUpdateUnsupportedLikeRust pins the CLI-facing refusal when
// CODEX_HOME has no managed package selection.
func TestRequestManualUpdateUnsupportedLikeRust(t *testing.T) {
	home := t.TempDir()
	output, err := RequestManualUpdate(context.Background(), home, nil)
	if err != nil {
		t.Fatalf("RequestManualUpdate error = %v", err)
	}
	if output == nil || output.Status != UpdateUnsupported {
		t.Fatalf("output = %#v, want unsupported", output)
	}
	if output.Message != ManualUpdateUnsupportedMessage {
		t.Fatalf("message = %q", output.Message)
	}
}

// TestRequestManualUpdatePinnedPackageStartsAWorkerLikeRust pins that a pinned
// selection starts a one-shot worker authorized to restore production updates.
func TestRequestManualUpdatePinnedPackageStartsAWorkerLikeRust(t *testing.T) {
	home := t.TempDir()
	root, _ := publishStableSelection(t, home, "1.0.0")
	pinned := filepath.Join(root, releasesDirName, "local-deadbeef-"+platformTarget())
	if err := os.MkdirAll(filepath.Join(pinned, "bin"), 0o700); err != nil {
		t.Fatalf("MkdirAll pinned error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "bin", managedCodexFileName()), []byte("pinned"), 0o700); err != nil {
		t.Fatalf("WriteFile pinned entrypoint error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, autoUpdateVersionFileName)); err != nil {
		t.Fatalf("Remove marker error = %v", err)
	}
	if err := selectDaemonRelease(root, pinned); err != nil {
		t.Fatalf("selectDaemonRelease(pinned) error = %v", err)
	}
	var restoreRelease string
	workerStarted := false
	options := &ManualUpdateOptions{
		Connect: connectManualUpdaterSocket,
		StartWorker: func(_ *Daemon, _ *DaemonSettings, restore string) error {
			workerStarted = true
			restoreRelease = restore
			return nil
		},
		ConnectTimeout: 200 * time.Millisecond,
	}
	if _, err := RequestManualUpdate(context.Background(), home, options); err == nil {
		t.Fatal("RequestManualUpdate unexpectedly succeeded without a worker socket")
	}
	if !workerStarted {
		t.Fatal("a pinned selection must start a one-shot worker updater")
	}
	if restoreRelease != "local-deadbeef-"+platformTarget() {
		t.Fatalf("restore release = %q", restoreRelease)
	}
}

// TestExchangeManualUpdateDecodesUpdaterErrorsLikeRust pins the error half of the
// updater's wire shape.
func TestExchangeManualUpdateDecodesUpdaterErrorsLikeRust(t *testing.T) {
	server, client := unixSocketPair(t)
	go func() {
		buffer := make([]byte, len(manualUpdateRequest))
		_, _ = server.Read(buffer)
		_, _ = server.Write([]byte(`{"Err":"installer did not select a stable latest release"}`))
		_ = server.Close()
	}()
	_, err := exchangeManualUpdate(client, "test.sock")
	if err == nil || !strings.Contains(err.Error(), "installer did not select a stable latest release") {
		t.Fatalf("exchangeManualUpdate error = %v", err)
	}
}

var _ = install.ExecutableIdentity{}
