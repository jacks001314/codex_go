//go:build !windows

package appserverdaemon

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// runInstallerProcess runs the fetched installer script with the guard
// environment (Rust run_installer_script's Unix branch): /bin/sh reads the
// script from stdin in its own process group so the updater can terminate the
// whole installer tree.
func runInstallerProcess(ctx context.Context, script []byte, env map[string]string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, "/bin/sh", "-s")
	command.Stdin = bytes.NewReader(script)
	command.Stdout = io.Discard
	// Keep the last installer diagnostics so a failed run reports them, and
	// bound the drain so a descendant holding the pipe open cannot stall the
	// updater (Rust update_loop's bounded stderr drain, #50499).
	tail := &installerStderrTail{}
	command.Stderr = tail
	command.WaitDelay = installerStderrDrainTimeout
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	environment := os.Environ()
	for name, value := range env {
		environment = append(environment, name+"="+value)
	}
	command.Env = environment
	if err := command.Run(); err != nil {
		return installerExitError(err, tail)
	}
	return nil
}
