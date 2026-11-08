//go:build windows

package mxc

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/sandbox"
)

// launcherProbeContent is the only string that counts as proof that console
// bytes reached the caller. A peek at the pipe is not enough: conhost writes a
// short initialization sequence before any workload output, so a non-empty read
// can succeed even when nothing the child wrote ever arrived (plan section 17).
const launcherProbeContent = "CONPTY_CONTENT_OK"

// launcherProbeShell returns the command interpreter used by the console probes.
func launcherProbeShell() string {
	if shell := strings.TrimSpace(os.Getenv("COMSPEC")); shell != "" {
		return shell
	}
	return "cmd.exe"
}

// drainLauncherSession reads the pseudoconsole until the helper exits and
// returns the collected console bytes plus the exit code. Console readers only
// see EOF once the session drops its creation handles, so the read runs
// concurrently with the wait instead of after it, and both the wait and the read
// are bounded: a workload that waits for input would otherwise hang the test
// binary instead of failing it.
func drainLauncherSession(t *testing.T, session *LiveSession, timeout time.Duration) (string, int) {
	t.Helper()
	output := make(chan string, 1)
	go func() {
		var builder strings.Builder
		for _, reader := range session.Readers {
			if reader == nil {
				continue
			}
			data, _ := io.ReadAll(reader)
			builder.Write(data)
		}
		output <- builder.String()
	}()
	type waited struct {
		code int
		err  error
	}
	finished := make(chan waited, 1)
	go func() {
		code, err := session.Wait()
		finished <- waited{code: code, err: err}
	}()
	var code int
	select {
	case result := <-finished:
		if result.err != nil {
			t.Fatalf("session.Wait() error = %v", result.err)
		}
		code = result.code
	case <-time.After(timeout):
		_ = session.Terminate()
		t.Fatalf("timed out after %s waiting for the MXC launcher console", timeout)
	}
	select {
	case text := <-output:
		return text, code
	case <-time.After(timeout):
		t.Fatalf("timed out after %s reading the MXC launcher console", timeout)
		return "", code
	}
}

// TestLauncherSessionDeliversConsoleContent is the stage one assertion the whole
// two-stage design rests on: a command started through SpawnLauncherSession runs
// under a real pseudoconsole and whatever it writes is readable from the session
// readers. It runs by default because it needs no process security environment;
// stage two is covered by TestLauncherSessionRunsSandboxedCommandThroughHelper.
func TestLauncherSessionDeliversConsoleContent(t *testing.T) {
	if !NativeTTYLaunchSupported() {
		t.Fatalf("NativeTTYLaunchSupported() = false: %s", TTYUnsupportedMessage)
	}
	cwd := t.TempDir()
	session, err := SpawnLauncherSession(
		launcherProbeShell(),
		[]string{"/d", "/c", "echo " + launcherProbeContent},
		map[string]string{"SystemRoot": os.Getenv("SystemRoot")},
		cwd,
	)
	if err != nil {
		t.Fatalf("SpawnLauncherSession() error = %v", err)
	}
	defer session.Close()
	text, code := drainLauncherSession(t, session, 30*time.Second)
	if code != 0 {
		t.Fatalf("launcher console exit code = %d, want 0 (output %q)", code, text)
	}
	if !strings.Contains(text, launcherProbeContent) {
		t.Fatalf("console output = %q, want it to contain %q", text, launcherProbeContent)
	}
}

// TestLauncherSessionNormalizesStdinForTheConsole covers the write half of a TTY
// session: a client sends "hello\n", and the workload running under the
// pseudoconsole reads it as one submitted line. Rust normalizes that byte
// stream (`WindowsTtyInputNormalizer`: LF becomes CR, backspace becomes DEL), so
// a raw LF would never submit the line to a console program.
func TestLauncherSessionNormalizesStdinForTheConsole(t *testing.T) {
	if !NativeTTYLaunchSupported() {
		t.Fatalf("NativeTTYLaunchSupported() = false: %s", TTYUnsupportedMessage)
	}
	// Delayed expansion keeps `!line!` from being expanded before `set /p` runs.
	script := "set /p line=& echo STDIN_ECHO:!line!"
	session, err := SpawnLauncherSession(
		launcherProbeShell(),
		[]string{"/d", "/v:on", "/c", script},
		map[string]string{"SystemRoot": os.Getenv("SystemRoot")},
		t.TempDir(),
	)
	if err != nil {
		t.Fatalf("SpawnLauncherSession() error = %v", err)
	}
	defer session.Close()
	if session.Stdin == nil {
		t.Fatal("launcher session has no stdin")
	}
	if _, err := session.Stdin.Write([]byte("hello\n")); err != nil {
		t.Fatalf("writing to the launcher console failed: %v", err)
	}
	text, code := drainLauncherSession(t, session, 30*time.Second)
	if code != 0 {
		t.Fatalf("launcher console exit code = %d, want 0 (output %q)", code, text)
	}
	if !strings.Contains(text, "STDIN_ECHO:hello") {
		t.Fatalf("console output = %q, want the workload to echo STDIN_ECHO:hello", text)
	}
}

// TestLauncherSessionRunsSandboxedCommandThroughHelper is the end-to-end probe
// for the production shape: os.Executable() is started as the stage one helper
// with LauncherArg1 plus an encoded request, the helper enters the PSEC sandbox
// and starts the workload, and the workload's console output comes back through
// the pseudoconsole. It is opt-in because it needs a host process security
// environment (and a helper build that includes the launcher dispatch):
//
//	go test ./sandbox/mxc -run TestLauncherSessionRunsSandboxedCommandThroughHelper -v
func TestLauncherSessionRunsSandboxedCommandThroughHelper(t *testing.T) {
	if os.Getenv("CODEX_MXC_LAUNCHER_PROBE") != "1" {
		t.Skip("set CODEX_MXC_LAUNCHER_PROBE=1 to launch the two-stage MXC helper end to end")
	}
	if !NativeTTYLaunchSupported() {
		t.Fatalf("NativeTTYLaunchSupported() = false: %s", TTYUnsupportedMessage)
	}
	support := ProbeNativeSupport()
	if !support.Available {
		t.Skipf("process security environment unavailable: %s", support.Reason)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	cwd := t.TempDir()
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = append(profile.SandboxPolicy.WritableRoots, cwd)
	// The workload writes inside the writable root *and* prints to the console:
	// the file proves the PSEC policy reached the workload, the console text
	// proves the pseudoconsole did.
	script := "echo " + launcherProbeContent + ">probe.txt & type probe.txt"
	command := []string{launcherProbeShell(), "/d", "/c", script}
	// An empty environment keeps `default_child_env` set, so the workload
	// inherits the helper's environment exactly like Rust's launcher transport;
	// CreateCommandArgs still stamps the host-owned launcher variables itself.
	env := map[string]string{}
	args, err := CreateCommandArgs(command, profile, cwd, nil, env)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if len(args) != 1 || args[0] != LauncherArg1 {
		t.Fatalf("CreateCommandArgs() = %v, want [%q]", args, LauncherArg1)
	}
	session, err := SpawnLauncherSession(executable, args, env, cwd)
	if err != nil {
		t.Fatalf("SpawnLauncherSession() error = %v", err)
	}
	defer session.Close()
	text, code := drainLauncherSession(t, session, 60*time.Second)
	if code != 0 {
		t.Fatalf("two-stage MXC exit code = %d, want 0 (console output %q)", code, text)
	}
	if !strings.Contains(text, launcherProbeContent) {
		t.Fatalf("console output = %q, want the sandboxed workload's %q", text, launcherProbeContent)
	}
	written, err := os.ReadFile(filepath.Join(cwd, "probe.txt"))
	if err != nil {
		t.Fatalf("sandboxed workload could not write inside the writable root: %v", err)
	}
	if !strings.Contains(string(written), launcherProbeContent) {
		t.Fatalf("probe.txt = %q, want %q", written, launcherProbeContent)
	}
}
