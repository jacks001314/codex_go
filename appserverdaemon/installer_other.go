//go:build !windows

package appserverdaemon

import (
	"bytes"
	"context"
	"fmt"
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
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	environment := os.Environ()
	for name, value := range env {
		environment = append(environment, name+"="+value)
	}
	command.Env = environment
	if err := command.Run(); err != nil {
		return fmt.Errorf("standalone Codex updater exited with error: %w", err)
	}
	return nil
}
