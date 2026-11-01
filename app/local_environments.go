package app

import (
	"context"
	"fmt"
	"io"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	codexexec "codex_go/exec"
	"codex_go/execserver"
	"codex_go/tool"
)

// Rust parity: `codex exec`, the TUI and the worktree paths all build their
// executor environments from CODEX_HOME, exactly like `codex app-server`
// (codex-rs/app-server/src/lib.rs `EnvironmentManager::from_codex_home`, which
// the app-server-client's in-process hosts share).
//
// configureLocalRunnerEnvironments installs the configured executors on a runner
// that executes turns in-process, so an `environments.toml` entry (or the
// CODEX_EXEC_SERVER_URL fallback) selects the same transport it does for the
// app-server. A malformed file fails the run before any turn starts, and a
// provider without a selected default leaves tools on the implicit local
// environment. `ignoreUserConfig` (Rust's `ignore_user_config`) skips the user's
// file and keeps only the CODEX_EXEC_SERVER_URL fallback, matching Rust's
// `EnvironmentManager::from_env` branch. Both branches prefer the
// CODEX_EXEC_SERVER_NOISE_* registry configuration, exactly like Rust's
// `PreparedEnvironmentManager` bootstrap.
func configureLocalRunnerEnvironments(runner *codexexec.Runner, codexHome string, ignoreUserConfig bool) error {
	if runner == nil {
		return nil
	}
	var snapshot execserver.EnvironmentProviderSnapshot
	if ignoreUserConfig {
		loaded, err := execserver.PrepareEnvironmentProviderFromEnv()
		if err != nil {
			return err
		}
		snapshot = loaded
	} else {
		loaded, err := execserver.PrepareEnvironmentProviderFromCodexHome(codexHome)
		if err != nil {
			return err
		}
		snapshot = loaded
	}
	runner.UnifiedExecEnvironments = tool.UnifiedExecEnvironmentsForProviderDefault(snapshot)
	return nil
}

// newLocalRunnerWithEnvironments builds the in-process runner the non-app-server
// hosts use with the configured executor environments installed.
func newLocalRunnerWithEnvironments(codexHome string, ignoreUserConfig bool) (*codexexec.Runner, error) {
	runner := newCodexExecRunner(codexHome)
	if err := configureLocalRunnerEnvironments(runner, codexHome, ignoreUserConfig); err != nil {
		return nil, err
	}
	return runner, nil
}

// runLocalRunnerRequest runs one `codex exec` / `codex review` request on the
// standalone host with the configured executor environments installed.
func runLocalRunnerRequest(ctx context.Context, parsed *cli.Parsed, stdin io.Reader, stdout, stderr io.Writer) error {
	runner, err := newLocalRunnerWithEnvironments(auth.DefaultCodexHome(), parsed.Exec.IgnoreUserConfig)
	if err != nil {
		return err
	}
	_, err = runner.RunContext(ctx, &codexexec.Request{
		Root: parsed.Root,
		Exec: parsed.Exec,
	}, stdin, stdout, stderr)
	// Rust's exec bootstrap prints the quoted config.toml line and exits without
	// an `Error:` prefix when the document does not parse.
	if snippet, ok := config.TOMLSyntaxSnippetOf(err); ok {
		if stderr != nil {
			fmt.Fprintf(stderr, "Error loading config.toml:\n%s\n", snippet)
		}
		return &ExitError{Code: 1, Silent: true}
	}
	return err
}
