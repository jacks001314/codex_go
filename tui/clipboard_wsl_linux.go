//go:build linux

package tui

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"codex_go/envutil"
)

// defaultWSLClipboard resolves the Windows clipboard writers from trusted
// system locations only (Rust system_executable / linux_system_executable).
var defaultWSLClipboard = NewWSLClipboard(WSLClipboardOptions{
	SystemPath:        envutil.TrustedSystemPath,
	WindowsExecutable: envutil.TrustedExecutable,
	LinuxExecutable:   envutil.LinuxTrustedExecutable,
	Run:               runWSLClipboardWriter,
})

// WSLClipboardCopy returns the Windows clipboard writer for a WSL session, or
// nil when the host is not WSL. Callers then keep the native clipboard.
func WSLClipboardCopy() ClipboardCopyFunc {
	if !IsProbablyWSL() {
		return nil
	}
	return defaultWSLClipboard.Copy
}

// runWSLClipboardWriter spawns the writer through the native shell: directly
// spawning a Windows executable can stall under WSL (Rust shell_command). The
// child owns its process group so a stuck adapter and everything it spawned can
// be stopped together.
func runWSLClipboardWriter(ctx context.Context, executable string, args []string, path string, text string) error {
	argv := append([]string{"-c", `exec "$@"`, "codex-wsl-copy", executable}, args...)
	cmd := exec.CommandContext(ctx, "/bin/sh", argv...)
	cmd.Env = []string{"PATH=" + path}
	cmd.Stdin = strings.NewReader(text)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd.Run()
}