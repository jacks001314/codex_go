//go:build windows

package appserverdaemon

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const (
	daemonStdioTestRoleEnv = "CODEX_TEST_STDIO_ROLE"
	daemonStdioTestHomeEnv = "CODEX_TEST_STDIO_HOME"
)

// TestDaemonLaunchDoesNotRetainLauncherStdio mirrors Rust #48272's
// `captured_stdio_closes_while_child_is_alive`: a caller that captures the
// launcher's stdout/stderr must see EOF when the launcher exits even while the
// detached managed child is still alive. The launcher marks its own standard
// handles inheritable first, exactly as Rust's test does, so a child that
// inherited them would keep the capture open and the launcher wait would hang.
func TestDaemonLaunchDoesNotRetainLauncherStdio(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	switch os.Getenv(daemonStdioTestRoleEnv) {
	case "child":
		runDaemonStdioChildRole(t)
		return
	case "launcher":
		runDaemonStdioLauncherRole(t, executable)
		return
	}

	home := t.TempDir()
	launcher := exec.Command(executable, daemonStdioTestSelfArgs()...)
	launcher.Env = append(os.Environ(),
		daemonStdioTestRoleEnv+"=launcher",
		daemonStdioTestHomeEnv+"="+home,
	)
	var stdout, stderr bytes.Buffer
	launcher.Stdout = &stdout
	launcher.Stderr = &stderr
	if err := launcher.Start(); err != nil {
		t.Fatalf("launcher start error = %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- launcher.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("launcher wait error = %v (stderr: %s)", err, stderr.String())
		}
	case <-time.After(20 * time.Second):
		_ = launcher.Process.Kill()
		t.Fatal("the launcher's captured stdio stayed open: a detached child retained the launcher's handles")
	}
	if !strings.Contains(stdout.String(), "launcher stdout") {
		t.Fatalf("captured stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "launcher stderr") {
		t.Fatalf("captured stderr = %q", stderr.String())
	}

	pidText, err := os.ReadFile(filepath.Join(home, "pid"))
	if err != nil {
		t.Fatalf("read the detached child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err != nil {
		t.Fatalf("parse the detached child pid %q: %v", pidText, err)
	}
	defer func() {
		if process, findErr := os.FindProcess(pid); findErr == nil {
			_ = process.Kill()
		}
	}()

	// The child is still alive after the launcher's stdio closed, and it kept
	// writing to its configured log instead of the launcher's capture.
	readyDeadline := time.Now().Add(20 * time.Second)
	for {
		if _, statErr := os.Stat(filepath.Join(home, "ready")); statErr == nil {
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatal("the detached child never reported readiness")
		}
		time.Sleep(25 * time.Millisecond)
	}
	logDeadline := time.Now().Add(20 * time.Second)
	for {
		if data, readErr := os.ReadFile(filepath.Join(home, "child.log")); readErr == nil &&
			strings.Contains(string(data), "child stderr") {
			break
		}
		if time.Now().After(logDeadline) {
			t.Fatal("the detached child never wrote its configured log")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// daemonStdioTestSelfArgs re-runs only this test in the launcher and child roles.
func daemonStdioTestSelfArgs() []string {
	return []string{"-test.run=^TestDaemonLaunchDoesNotRetainLauncherStdio$", "-test.v"}
}

// runDaemonStdioChildRole is the detached child: it reports readiness, keeps
// running, and writes only to its configured log.
func runDaemonStdioChildRole(t *testing.T) {
	home := os.Getenv(daemonStdioTestHomeEnv)
	if home == "" {
		t.Fatal("the child role needs a test home")
	}
	fmt.Println("child stdout")
	fmt.Fprintln(os.Stderr, "child stderr")
	if err := os.WriteFile(filepath.Join(home, "ready"), nil, 0o600); err != nil {
		t.Fatalf("write the readiness marker: %v", err)
	}
	time.Sleep(30 * time.Second)
}

// runDaemonStdioLauncherRole is the launcher: it makes its own output handles
// inheritable, starts the detached child with the managed launch's stdio
// configuration, records the child's pid, prints its own output, and exits.
func runDaemonStdioLauncherRole(t *testing.T, executable string) {
	home := os.Getenv(daemonStdioTestHomeEnv)
	if home == "" {
		t.Fatal("the launcher role needs a test home")
	}
	for _, name := range []string{"stdout", "stderr"} {
		stdHandle := uint32(windows.STD_OUTPUT_HANDLE)
		if name == "stderr" {
			stdHandle = uint32(windows.STD_ERROR_HANDLE)
		}
		handle, err := windows.GetStdHandle(stdHandle)
		if err != nil {
			t.Fatalf("GetStdHandle(%s) error = %v", name, err)
		}
		if handle == 0 || handle == windows.InvalidHandle {
			continue
		}
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			t.Fatalf("SetHandleInformation(%s) error = %v", name, err)
		}
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open NUL: %v", err)
	}
	defer devNull.Close()
	stderrLog, err := os.OpenFile(filepath.Join(home, "child.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open the child log: %v", err)
	}
	defer stderrLog.Close()

	command := exec.Command(executable, daemonStdioTestSelfArgs()...)
	command.Dir = home
	command.Env = append(os.Environ(),
		daemonStdioTestRoleEnv+"=child",
		daemonStdioTestHomeEnv+"="+home,
	)
	// Rust's test skips CREATE_BREAKAWAY_FROM_JOB because a restricted job may
	// forbid it and it does not affect stdio inheritance.
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	command.Stdin = devNull
	command.Stdout = devNull
	command.Stderr = stderrLog
	if err := startDetachedCommand(command); err != nil {
		t.Fatalf("startDetachedCommand() error = %v", err)
	}
	pid := strconv.Itoa(command.Process.Pid)
	_ = command.Process.Release()
	if err := os.WriteFile(filepath.Join(home, "pid"), []byte(pid), 0o600); err != nil {
		t.Fatalf("write the child pid: %v", err)
	}
	fmt.Println("launcher stdout")
	fmt.Fprintln(os.Stderr, "launcher stderr")
}
