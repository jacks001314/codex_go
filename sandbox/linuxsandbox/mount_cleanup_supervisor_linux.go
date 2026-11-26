//go:build linux

package linuxsandbox

// The mount cleanup supervisor is an opt-in Linux slice for #61898.  It is
// deliberately independent from the proxy supervisor: a separate process
// owns the synthetic mount paths and waits for the workload identity before
// removing them.  The default launch path does not start this process.

import (
	"bytes"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	mountCleanupSupervisorArg = "--mount-cleanup-supervisor"
	mountCleanupEnableEnv     = "CODEX_MOUNT_CLEANUP_SUPERVISOR"
	syntheticMountMarkerName  = ".codex-synthetic-mount"
)

type mountCleanupSupervisor struct {
	cmd    *exec.Cmd
	stderr bytes.Buffer
}

type syntheticMountRegistration struct {
	path       string
	marker     string
	ownerPID   int
	ownerStart uint64
}

// mountCleanupSupervisorEnabled is intentionally environment-gated.  This
// keeps existing launches byte-for-byte compatible until the full mount handoff
// protocol (SCM_RIGHTS and attribution) is enabled by the caller.
func mountCleanupSupervisorEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(mountCleanupEnableEnv)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

// startMountCleanupSupervisor starts a detached process with an immutable
// workload identity.  The supervisor only removes paths after that exact
// pid/start-time pair has exited, preventing PID reuse from deleting a newer
// workload's synthetic mount.
func startMountCleanupSupervisor(ownerPID int, paths []string) (*mountCleanupSupervisor, error) {
	if ownerPID <= 1 || len(paths) == 0 {
		return nil, errors.New("invalid mount cleanup owner or paths")
	}
	start, err := linuxProcessStartTime(ownerPID)
	if err != nil {
		return nil, fmt.Errorf("capture mount cleanup owner identity: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{mountCleanupSupervisorArg, "--owner-pid", strconv.Itoa(ownerPID), "--owner-start", strconv.FormatUint(start, 10)}
	for _, path := range paths {
		if clean := filepath.Clean(path); clean != "." && clean != "/" {
			if err := writeSyntheticMountMarker(clean, ownerPID, start); err != nil {
				return nil, fmt.Errorf("register synthetic mount %s: %w", clean, err)
			}
			args = append(args, "--path", clean)
		}
	}
	if len(args) == 3 {
		return nil, errors.New("mount cleanup has no usable paths")
	}
	s := &mountCleanupSupervisor{}
	s.cmd = exec.Command(executable, args...)
	s.cmd.Args[0] = os.Args[0]
	if os.Getenv("CODEX_MOUNT_CLEANUP_TEST") == "1" {
		s.cmd.Args = append([]string{os.Args[0], "-test.run=^TestMountCleanupSupervisorProcess$", "--"}, args...)
		s.cmd.Env = append(os.Environ(), "CODEX_MOUNT_CLEANUP_TEST_CHILD=1")
	}
	s.cmd.Stdin = nil
	s.cmd.Stdout = nil
	s.cmd.Stderr = &s.stderr
	// Do not set Pdeathsig: this process must survive a launcher crash long
	// enough to observe and clean a workload that is being reaped.
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := s.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mount cleanup supervisor: %w", err)
	}
	return s, nil
}

func (s *mountCleanupSupervisor) wait() error {
	if s == nil || s.cmd == nil {
		return nil
	}
	err := s.cmd.Wait()
	if err != nil {
		diagnostic := strings.TrimSpace(s.stderr.String())
		if diagnostic != "" {
			return fmt.Errorf("%w: %s", err, diagnostic)
		}
		return err
	}
	return nil
}

// runMountCleanupSupervisor is the re-exec entry point.  It waits for the
// workload identity to disappear (or become a different process), then
// removes every registered synthetic mount path.  A cleanup error is reported
// on stderr and as a non-zero exit, so the launcher can surface it.
func runMountCleanupSupervisor(args []string) error {
	flags := map[string]string{}
	var paths []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--owner-pid", "--owner-start":
			if i+1 >= len(args) {
				return errors.New("mount cleanup supervisor missing value")
			}
			flags[args[i]] = args[i+1]
			i++
		case "--path":
			if i+1 >= len(args) {
				return errors.New("mount cleanup supervisor missing path")
			}
			path := filepath.Clean(args[i+1])
			if path == "." || path == "/" {
				return errors.New("mount cleanup supervisor refuses root path")
			}
			paths = append(paths, path)
			i++
		default:
			return fmt.Errorf("mount cleanup supervisor unknown argument %q", args[i])
		}
	}
	pid, err := strconv.Atoi(flags["--owner-pid"])
	if err != nil || pid <= 1 {
		return errors.New("mount cleanup supervisor invalid owner pid")
	}
	start, err := strconv.ParseUint(flags["--owner-start"], 10, 64)
	if err != nil || start == 0 {
		return errors.New("mount cleanup supervisor invalid owner start time")
	}
	if len(paths) == 0 {
		return errors.New("mount cleanup supervisor has no paths")
	}
	registrations := make(map[string]syntheticMountRegistration, len(paths))
	for _, path := range paths {
		marker, err := verifySyntheticMountMarker(path, pid, start)
		if err != nil {
			return fmt.Errorf("refuse unmarked synthetic mount %s: %w", path, err)
		}
		registrations[path] = syntheticMountRegistration{path: path, marker: marker, ownerPID: pid, ownerStart: start}
	}
	for {
		current, statErr := linuxProcessStartTime(pid)
		if errors.Is(statErr, os.ErrNotExist) || errors.Is(statErr, syscall.ESRCH) {
			break
		}
		if statErr != nil {
			// Permission or transient procfs errors are retried; an identity
			// mismatch is terminal and must never authorize cleanup.
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if current != start {
			return fmt.Errorf("mount cleanup owner identity changed for pid %d", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var cleanupErr error
	for _, registration := range registrations {
		if err := os.RemoveAll(registration.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove synthetic mount %s: %w", registration.path, err))
		}
		if registration.marker != filepath.Join(registration.path, syntheticMountMarkerName) {
			if err := os.Remove(registration.marker); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove synthetic mount marker %s: %w", registration.marker, err))
			}
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	return nil
}

func writeSyntheticMountMarker(path string, pid int, start uint64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return errors.New("target is not a real file or directory")
	}
	var id [16]byte
	if _, err := cryptorand.Read(id[:]); err != nil {
		return err
	}
	marker := syntheticMountMarkerPath(path, info.IsDir())
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "id=%x\npid=%d\nstart=%d\n", id, pid, start)
	return err
}

func verifySyntheticMountMarker(path string, pid int, start uint64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return "", errors.New("target is not a real file or directory")
	}
	marker := syntheticMountMarkerPath(filepath.Clean(path), info.IsDir())
	info, err = os.Lstat(marker)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("marker is not a regular file")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return "", err
	}
	fields := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "id=") || len(strings.TrimPrefix(fields[0], "id=")) != 32 {
		return "", errors.New("invalid marker id")
	}
	gotPID, errPID := strconv.Atoi(strings.TrimPrefix(fields[1], "pid="))
	gotStart, errStart := strconv.ParseUint(strings.TrimPrefix(fields[2], "start="), 10, 64)
	if !strings.HasPrefix(fields[1], "pid=") || !strings.HasPrefix(fields[2], "start=") || errPID != nil || errStart != nil || gotPID != pid || gotStart != start {
		return "", errors.New("marker owner identity mismatch")
	}
	return marker, nil
}

func syntheticMountMarkerPath(path string, directory bool) string {
	if directory {
		return filepath.Join(path, syntheticMountMarkerName)
	}
	return path + "." + syntheticMountMarkerName
}

// linuxProcessStartTime reads /proc/<pid>/stat field 22.  It is intentionally
// local to the supervisor so the identity check remains valid on old kernels
// without pidfd support.
func linuxProcessStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	close := strings.LastIndex(string(data), ") ")
	if close < 0 {
		return 0, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data)[close+2:])
	// The suffix begins at field 3; starttime is field 22, index 19.
	if len(fields) <= 19 {
		return 0, errors.New("short process stat")
	}
	value, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func mountCleanupSupervisorExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok {
			return linuxSandboxExitCode(status)
		}
	}
	return 1
}
