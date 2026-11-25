//go:build linux

package linuxsandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const procPreflightStderrLimit = 64 * 1024

type bwrapPlan struct {
	args    []string
	dataFDs []int
}

func (p *bwrapPlan) close() {
	for _, fd := range p.dataFDs {
		_ = unix.Close(fd)
	}
}

// isProcMountFailure mirrors Rust #61900: the destination and errno must
// match exactly on the same diagnostic line, including Bubblewrap 0.12's /proc.
func isProcMountFailure(stderr string) bool {
	for _, line := range strings.Split(stderr, "\n") {
		_, diagnostic, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "Can't mount proc on ")
		if !ok {
			continue
		}
		destination, reason, ok := strings.Cut(diagnostic, ": ")
		if !ok || (destination != "/newroot/proc" && destination != "/proc") {
			continue
		}
		switch reason {
		case "Invalid argument", "Operation not permitted", "Permission denied":
			return true
		}
	}
	return false
}

// preflightProcMountSupport runs only a minimal read-only true command, using
// the launcher already selected under the real command's filesystem policy.
// A failed real user command is never retried.
func preflightProcMountSupport(cmd *linuxSandboxCommand, launcher bwrapLauncher) (bool, error) {
	probe := *cmd
	probe.SandboxPolicyCWD = "/"
	probe.CommandCWD = "/"
	probe.PermissionProfile = &linuxPermissionProfile{
		NetworkEnabled: cmd.PermissionProfile.NetworkEnabled,
		Filesystem: &linuxFilesystemPolicy{
			Kind:    "restricted",
			Entries: []linuxFilesystemEntry{{Access: "read", Special: linuxSpecialPath{Kind: "minimal"}}},
		},
	}
	probe.Command = []string{resolveProcPreflightTrue()}
	plan, err := buildBubblewrapArgs(&probe, probe.Command, true)
	if err != nil {
		return false, fmt.Errorf("build bubblewrap proc preflight: %w", err)
	}
	defer plan.close()
	stderr, err := runProcMountPreflight(launcher, plan.args)
	if err != nil {
		return false, err
	}
	return !isProcMountFailure(stderr), nil
}

func resolveProcPreflightTrue() string {
	for _, candidate := range []string{"/usr/bin/true", "/bin/true"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "true"
}

type procPreflightStderr struct{ bytes []byte }

func (b *procPreflightStderr) Write(p []byte) (int, error) {
	n := len(p)
	remaining := procPreflightStderrLimit - len(b.bytes)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.bytes = append(b.bytes, p...)
	}
	// Drain the full stream even after reaching the storage bound.
	return n, nil
}

func runProcMountPreflight(launcher bwrapLauncher, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, launcher.Program, args[1:]...)
	command.Args[0] = args[0]
	if launcher.Bundled {
		file, err := os.Open(launcher.Program)
		if err != nil {
			return "", fmt.Errorf("open bundled bubblewrap preflight: %w", err)
		}
		defer file.Close()
		// Verify the opened object and execute the same object in the child.
		path := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
		if err := verifyBundledBwrapDigest(path, bundledBwrapSHA256); err != nil {
			return "", err
		}
		command.Path = "/proc/self/fd/3"
		command.ExtraFiles = []*os.File{file}
	}
	var stderr procPreflightStderr
	command.Stderr = &stderr
	// A stray descendant retaining stderr must not keep startup stuck.
	command.WaitDelay = 100 * time.Millisecond
	err := command.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("bubblewrap proc preflight: %w", ctx.Err())
	}
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			return "", fmt.Errorf("run bubblewrap proc preflight: %w", err)
		}
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return "", fmt.Errorf("bubblewrap proc preflight interrupted: %w", err)
		}
		// Non-proc failures are left to the real invocation to diagnose.
	}
	return string(stderr.bytes), nil
}
