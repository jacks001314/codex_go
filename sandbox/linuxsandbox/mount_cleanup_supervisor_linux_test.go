//go:build linux

package linuxsandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func mountCleanupWorkload(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd
}

func init() {
	if os.Getenv("CODEX_MOUNT_CLEANUP_TEST_CHILD") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			childArgs := os.Args[i+1:]
			if len(childArgs) > 0 && childArgs[0] == mountCleanupSupervisorArg {
				childArgs = childArgs[1:]
			}
			err := runMountCleanupSupervisor(childArgs)
			if err != nil {
				_, _ = os.Stderr.WriteString(err.Error())
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func waitCleanupSupervisor(t *testing.T, supervisor *mountCleanupSupervisor) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- supervisor.wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("mount cleanup supervisor did not exit")
		return nil
	}
}

func TestMountCleanupSupervisorNormalExit(t *testing.T) {
	t.Setenv("CODEX_MOUNT_CLEANUP_TEST", "1")
	workload := mountCleanupWorkload(t)
	path := filepath.Join(t.TempDir(), "synthetic")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	supervisor, err := startMountCleanupSupervisor(workload.Process.Pid, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := workload.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = workload.Wait()
	if err := waitCleanupSupervisor(t, supervisor); err != nil {
		t.Fatalf("%v (stderr=%q)", err, supervisor.stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("synthetic mount remains: %v", err)
	}
}

func TestMountCleanupSupervisorWorkloadKilled(t *testing.T) {
	t.Setenv("CODEX_MOUNT_CLEANUP_TEST", "1")
	workload := mountCleanupWorkload(t)
	path := filepath.Join(t.TempDir(), "synthetic")
	if err := os.WriteFile(path, []byte("mount"), 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor, err := startMountCleanupSupervisor(workload.Process.Pid, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := workload.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = workload.Wait()
	if err := waitCleanupSupervisor(t, supervisor); err != nil {
		t.Fatalf("%v (stderr=%q)", err, supervisor.stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("killed workload synthetic mount remains: %v", err)
	}
}

func TestMountCleanupSupervisorLauncherKilled(t *testing.T) {
	t.Setenv("CODEX_MOUNT_CLEANUP_TEST", "1")
	workload := mountCleanupWorkload(t)
	path := filepath.Join(t.TempDir(), "synthetic")
	if err := os.WriteFile(path, []byte("mount"), 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor, err := startMountCleanupSupervisor(workload.Process.Pid, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate launcher death: the workload is terminated independently while
	// the supervisor remains alive and owns cleanup.
	if err := workload.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = workload.Wait()
	if err := waitCleanupSupervisor(t, supervisor); err != nil {
		t.Fatalf("%v (stderr=%q)", err, supervisor.stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launcher-killed synthetic mount remains: %v", err)
	}
}

func TestMountCleanupSupervisorReportsCleanupFailure(t *testing.T) {
	t.Setenv("CODEX_MOUNT_CLEANUP_TEST", "1")
	workload := mountCleanupWorkload(t)
	parent := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "synthetic")
	if err := os.WriteFile(path, []byte("mount"), 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor, err := startMountCleanupSupervisor(workload.Process.Pid, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	_ = workload.Process.Kill()
	_ = workload.Wait()
	if err := waitCleanupSupervisor(t, supervisor); err == nil {
		t.Fatal("cleanup failure was not reported")
	}
}

func TestMountCleanupSupervisorRejectsUnmarkedPath(t *testing.T) {
	workload := mountCleanupWorkload(t)
	path := filepath.Join(t.TempDir(), "unmarked")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	start, err := linuxProcessStartTime(workload.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := workload.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = workload.Wait()
	err = runMountCleanupSupervisor([]string{"--owner-pid", strconv.Itoa(workload.Process.Pid), "--owner-start", strconv.FormatUint(start, 10), "--path", path})
	if err == nil {
		t.Fatal("unmarked path was accepted")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("unmarked path changed: %v", statErr)
	}
}

func TestMountCleanupSupervisorDisabledByDefault(t *testing.T) {
	t.Setenv(mountCleanupEnableEnv, "")
	if mountCleanupSupervisorEnabled() {
		t.Fatal("mount cleanup supervisor unexpectedly enabled")
	}
}
