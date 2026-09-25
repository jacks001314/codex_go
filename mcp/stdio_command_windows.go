package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"codex_go/envutil"
	"golang.org/x/sys/windows"
)

func newMCPStdioCommand(command string, args ...string) *exec.Cmd {
	switch strings.ToLower(filepath.Ext(command)) {
	case ".cmd", ".bat":
		comspec := strings.TrimSpace(os.Getenv("ComSpec"))
		if comspec == "" {
			comspec = "cmd.exe"
		}
		cmd := exec.Command(comspec)
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: windowsBatchCommandLine(command, args)}
		suppressMCPStdioConsoleWindow(cmd)
		envutil.ScrubCommandEnv(cmd)
		return cmd
	default:
		cmd := exec.Command(command, args...)
		suppressMCPStdioConsoleWindow(cmd)
		envutil.ScrubCommandEnv(cmd)
		return cmd
	}
}

// suppressMCPStdioConsoleWindow mirrors Rust #48238: the launcher sets
// CREATE_NO_WINDOW on the shared command wrapper, so both a direct program and a
// batch shim launched through cmd.exe run without a console window. A server is
// a stdio-only helper, so a window would only flash over the user's terminal.
func suppressMCPStdioConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}

func windowsBatchCommandLine(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteWindowsBatchArg(command))
	for _, arg := range args {
		parts = append(parts, quoteWindowsBatchArg(arg))
	}
	return `/d /s /c "` + strings.Join(parts, " ") + `"`
}

func quoteWindowsBatchArg(value string) string {
	// Batch shims such as npx.cmd are interpreted by cmd.exe rather than by
	// CreateProcess. Quoting every token protects spaces and command operators;
	// doubled quotes preserve literal quote characters inside a token.
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
