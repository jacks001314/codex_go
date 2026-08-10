package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"codex_go/envutil"
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
		envutil.ScrubCommandEnv(cmd)
		return cmd
	default:
		cmd := exec.Command(command, args...)
		envutil.ScrubCommandEnv(cmd)
		return cmd
	}
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
