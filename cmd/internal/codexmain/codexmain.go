// Package codexmain holds the process entry point shared by the full Codex
// CLI and the standalone `codex-exec-server` binary (Rust #57589).
package codexmain

import (
	"context"
	"fmt"
	"io"
	"os"

	"codex_go/app"
	"codex_go/applypatch"
	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/execserver"
	"codex_go/processhardening"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
	commandrunner "codex_go/sandbox/windowssandbox/bin/command_runner"
	setupmain "codex_go/sandbox/windowssandbox/bin/setup_main"
)

// Options selects the entry point whose arguments are being dispatched.
type Options struct {
	// ExecServerStandalone mirrors Rust's `codex-exec-server`: the executor
	// arguments are parsed directly, without the `exec-server` subcommand.
	ExecServerStandalone bool
}

// Main dispatches internal helper modes and then runs the CLI. It returns the
// process exit code so callers can install their own `os.Exit` handling.
func Main(args []string, argv0 string, stdin io.Reader, stdout, stderr io.Writer, opts Options) int {
	// Executor compatibility checks (Rust #57192) compare the release version
	// reported in environmentInfo, so stamp it from the packaged build metadata.
	execserver.SetExecServerVersion(appserver.CodexVersion())
	// Rust process-hardening (pre_main_hardening): disable ptrace attach and
	// core dumps, clear LD_*/DYLD_* loader vars, fail-closed on Unix.
	processhardening.ApplyPreMain()

	if opts.ExecServerStandalone {
		if handled := runStandaloneMeta(args, stdout); handled {
			return 0
		}
	}

	argv1 := ""
	if len(args) > 0 {
		argv1 = args[0]
	}
	switch cli.DispatchKind(argv0, argv1) {
	case "apply_patch":
		cwd, _ := os.Getwd()
		return applypatch.RunCLI(args, stdin, stdout, stderr, cwd)
	case "apply_patch_core":
		cwd, _ := os.Getwd()
		return applypatch.RunCLI(args[1:], stdin, stdout, stderr, cwd)
	case "linux_sandbox":
		return sandbox.RunLinuxSandboxHelper(args, stdout, stderr)
	case "execve_wrapper":
		return sandbox.RunExecveWrapperHelper(args, stdout, stderr)
	case "windows_sandbox":
		return windowssandbox.RunWindowsSandboxWrapperMain(args)
	case "windows_sandbox_setup":
		if argv1 == cli.DispatchWindowsSandboxSetupFlag {
			args = args[1:]
		}
		return setupmain.Run(args, stdout, stderr)
	case "windows_command_runner":
		if argv1 == cli.DispatchWindowsCommandRunnerFlag {
			args = args[1:]
		}
		return commandrunner.Run(args, stdin, stdout, stderr)
	}
	if argv1 == execserver.FSHelperArg1 {
		return execserver.RunFSHelper(stdin, stdout, stderr)
	}

	exe, _ := os.Executable()
	var aliases *cli.DispatchAliasGuard
	if prepared, err := cli.PrepareArg0Aliases(auth.DefaultCodexHome(), exe, os.Getenv("PATH")); err == nil {
		aliases = prepared
		defer aliases.Cleanup()
		_ = os.Setenv("PATH", aliases.UpdatedPATH)
	} else {
		fmt.Fprintln(stderr, "WARNING: proceeding, even though we could not create PATH aliases:", err)
	}
	dispatchPaths := cli.DispatchPathsForProcess(exe, aliases)

	runArgs := args
	if opts.ExecServerStandalone {
		runArgs = append([]string{string(cli.CommandExecServer)}, args...)
	}
	if err := app.RunWithOptions(context.Background(), runArgs, stdin, stdout, stderr, &app.RunOptions{DispatchPaths: &dispatchPaths}); err != nil {
		if app.ShouldPrintError(err) {
			fmt.Fprintln(stderr, err)
		}
		return app.ExitCode(err)
	}
	return 0
}

// runStandaloneMeta handles the `--help` and `--version` flags clap provides to
// the standalone executor before any configuration is loaded.
func runStandaloneMeta(args []string, stdout io.Writer) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprint(stdout, ExecServerHelpText)
		return true
	case "-V", "--version":
		fmt.Fprintf(stdout, "codex-exec-server %s\n", appserver.CodexVersion())
		return true
	default:
		return false
	}
}

// ExecServerHelpText documents the standalone executor's options. Rust renders
// this from clap; Go keeps the same option list in a single string.
const ExecServerHelpText = `Standalone Codex executor

Usage: codex-exec-server [OPTIONS]

Options:
      --listen <URL>                     Listen for executor clients (use "stdio" for standard I/O)
      --remote <URL>                     Register this executor with a remote control plane
      --connect <URL>                    Forward an existing WebSocket executor as a remote environment
      --environment-id <ID>              Environment id for --remote registration
      --name <NAME>                      Environment name for --connect
      --strict-config                    Reject unknown configuration keys
      --use-agent-identity-auth          Authenticate remote registration with Agent Identity
      --exit-on-stdin-close              Exit when the parent process closes stdin
      --executor-credential-stdin        Read rotating scoped executor credentials from stdin
      --proxy-private-ips-via-upstream   Allow permitted private IPs to use the upstream proxy
      --ws-auth <MODE>                   WebSocket authentication mode
      --ws-token-file <PATH>             File containing the WebSocket bearer token
      --ws-token-sha256 <HEX>            Expected SHA-256 of the WebSocket bearer token
      --ws-shared-secret-file <PATH>     File containing the WebSocket shared secret
  -h, --help                             Print help
  -V, --version                          Print version
`
