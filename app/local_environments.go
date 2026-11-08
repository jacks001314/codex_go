package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

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
//
// Rust #60041 keeps untrusted project configuration disabled in non-interactive
// runs, accepts an explicit `--trust` grant, and warns when the decision
// disabled a project layer. Both the gate and the warning run before the turn
// starts, so a refused grant never reaches the model.
func runLocalRunnerRequest(ctx context.Context, parsed *cli.Parsed, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := applyExecProjectTrust(parsed); err != nil {
		return err
	}
	if warning := execProjectTrustWarning(parsed); warning != "" && stderr != nil {
		fmt.Fprintln(stderr, warning)
	}
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

// applyExecProjectTrust resolves `codex exec --trust` (#60041). Without the flag
// it does nothing: no read, no write, and no refusal, so the interactive and
// existing non-interactive paths keep their behaviour.
func applyExecProjectTrust(parsed *cli.Parsed) error {
	if parsed == nil {
		return nil
	}
	_, err := config.ResolveExecProjectTrust(execProjectTrustOptions(parsed))
	return err
}

// execProjectTrustWarning is Rust's stderr warning for a project whose
// project-local configuration the trust decision disabled. A `--trust` grant in
// this same run removes the disabled layers, so the warning disappears.
func execProjectTrustWarning(parsed *cli.Parsed) string {
	if parsed == nil {
		return ""
	}
	// The warning has to describe the decision this run used: `--ignore-user-config`
	// leaves the user layer empty, so a trust_level saved there cannot suppress it.
	return config.NewConfigService(auth.DefaultCodexHome()).ExecProjectTrustWarningForOptions(
		execProjectTrustCWD(parsed),
		parsed.Exec.IgnoreUserConfig,
	)
}

func execProjectTrustOptions(parsed *cli.Parsed) config.ExecProjectTrustOptions {
	overrides := make([]string, 0, len(parsed.Root.ConfigOverrides)+len(parsed.Exec.ConfigOverrides))
	overrides = append(overrides, parsed.Root.ConfigOverrides...)
	overrides = append(overrides, parsed.Exec.ConfigOverrides...)
	return config.ExecProjectTrustOptions{
		CodexHome: auth.DefaultCodexHome(),
		CWD:       execProjectTrustCWD(parsed),
		// Rust declares `--trust` global on the exec Cli and separately on
		// ReviewCommand, so either copy grants.
		Trust:            parsed.Exec.Trust || parsed.Exec.Review.Trust,
		IgnoreUserConfig: parsed.Exec.IgnoreUserConfig,
		Ephemeral:        parsed.Exec.Ephemeral,
		RawOverrides:     overrides,
	}
}

// execProjectTrustCWD mirrors the exec runner's `requestCWD` precedence: the
// exec/root `--cd` value, then the process working directory. Rust resolves the
// same chain before its bootstrap config load, so the grant lands on the same
// project the turn will run in.
func execProjectTrustCWD(parsed *cli.Parsed) string {
	if parsed != nil {
		if cwd := strings.TrimSpace(parsed.Exec.Shared.CWD); cwd != "" {
			return cwd
		}
		if cwd := strings.TrimSpace(parsed.Root.Shared.CWD); cwd != "" {
			return cwd
		}
	}
	workingDir, err := os.Getwd()
	if err != nil || strings.TrimSpace(workingDir) == "" {
		return "."
	}
	return workingDir
}
