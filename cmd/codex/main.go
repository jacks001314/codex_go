package main

import (
	"context"
	"fmt"
	"os"

	"codex_go/internal/app"
	"codex_go/internal/applypatch"
	"codex_go/internal/auth"
	"codex_go/internal/cli"
	"codex_go/internal/execserver"
	"codex_go/internal/sandbox"
	"codex_go/internal/sandbox/windowssandbox"
	commandrunner "codex_go/internal/sandbox/windowssandbox/bin/command_runner"
	setupmain "codex_go/internal/sandbox/windowssandbox/bin/setup_main"
)

func main() {
	argv1 := ""
	if len(os.Args) > 1 {
		argv1 = os.Args[1]
	}
	switch cli.DispatchKind(os.Args[0], argv1) {
	case "apply_patch":
		cwd, _ := os.Getwd()
		os.Exit(applypatch.RunCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, cwd))
	case "apply_patch_core":
		cwd, _ := os.Getwd()
		os.Exit(applypatch.RunCLI(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, cwd))
	case "linux_sandbox":
		os.Exit(sandbox.RunLinuxSandboxHelper(os.Args[1:], os.Stdout, os.Stderr))
	case "execve_wrapper":
		os.Exit(sandbox.RunExecveWrapperHelper(os.Args[1:], os.Stdout, os.Stderr))
	case "windows_sandbox":
		os.Exit(windowssandbox.RunWindowsSandboxWrapperMain(os.Args[1:]))
	case "windows_sandbox_setup":
		args := os.Args[1:]
		if argv1 == cli.DispatchWindowsSandboxSetupFlag {
			args = os.Args[2:]
		}
		os.Exit(setupmain.Run(args, os.Stdout, os.Stderr))
	case "windows_command_runner":
		args := os.Args[1:]
		if argv1 == cli.DispatchWindowsCommandRunnerFlag {
			args = os.Args[2:]
		}
		os.Exit(commandrunner.Run(args, os.Stdin, os.Stdout, os.Stderr))
	}
	if argv1 == execserver.FSHelperArg1 {
		os.Exit(execserver.RunFSHelper(os.Stdin, os.Stdout, os.Stderr))
	}
	exe, _ := os.Executable()
	var aliases *cli.DispatchAliasGuard
	if prepared, err := cli.PrepareArg0Aliases(auth.DefaultCodexHome(), exe, os.Getenv("PATH")); err == nil {
		aliases = prepared
		defer aliases.Cleanup()
		_ = os.Setenv("PATH", aliases.UpdatedPATH)
	} else {
		fmt.Fprintln(os.Stderr, "WARNING: proceeding, even though we could not create PATH aliases:", err)
	}
	dispatchPaths := cli.DispatchPathsForProcess(exe, aliases)
	if err := app.RunWithOptions(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, &app.RunOptions{DispatchPaths: &dispatchPaths}); err != nil {
		if app.ShouldPrintError(err) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(app.ExitCode(err))
	}
}
