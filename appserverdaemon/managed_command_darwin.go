//go:build darwin

package appserverdaemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

var taskpolicyProbeTimeout = 5 * time.Second

var runTaskpolicyProbe = func(ctx context.Context) error {
	command := exec.CommandContext(ctx, "/usr/sbin/taskpolicy", "-b", "/usr/bin/true")
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	return command.Run()
}

// managedPIDCommand launches managed processes with background CPU and I/O
// priority when taskpolicy is available. taskpolicy uses POSIX_SPAWN_SETEXEC,
// so the managed process keeps the PID recorded by the daemon backend.
func managedPIDCommand(program string, args []string) (*exec.Cmd, error) {
	info, err := os.Stat(program)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("managed Codex path is not executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), taskpolicyProbeTimeout)
	defer cancel()
	if err := runTaskpolicyProbe(ctx); err == nil {
		wrapped := append([]string{"-b", program}, args...)
		return exec.Command("/usr/sbin/taskpolicy", wrapped...), nil
	}
	// A restricted environment can deny taskpolicy or leave the probe stuck.
	// Preserve daemon availability by falling back to a direct launch.
	return exec.Command(program, args...), nil
}
