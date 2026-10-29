package app

// Rust parity: codex-rs/tui/src/daemon_recovery.rs (#55442). The compatibility
// check runs before the TUI opens; a restart is offered only for a managed
// daemon and reported feature mismatch, needs an explicit confirmation, and is
// followed by a single compatibility re-check.

import (
	"context"
	"fmt"
	"io"

	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/tui/onboarding"
)

// daemonCompatibilityWarningText is the startup warning the pre-TUI
// compatibility check reported. daemonStartupWarning reports it ahead of the
// auto-start exclusion warning, matching Rust's ordering.
var daemonCompatibilityWarningText string

// daemonRecoveryRun shows the blocking recovery screen.
var daemonRecoveryRun = onboarding.RunDaemonRecovery

// daemonRecoveryInteractive reports whether recovery has a terminal to prompt
// on; the check is a seam so tests do not need a real TTY.
var daemonRecoveryInteractive = shouldRunInteractiveTUI

// daemonRecoveryManaged reports whether Codex manages the running background
// server (Rust `codex_app_server_daemon::run(Version)` reporting a backend).
var daemonRecoveryManaged = func() bool {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	output, err := runner.Run(appserverdaemon.LifecycleVersion)
	return err == nil && output != nil && output.Backend != nil
}

// daemonRecoveryRestartWithFeatures applies the confirmed shared feature
// settings to the managed daemon.
var daemonRecoveryRestartWithFeatures = func(features map[string]bool) error {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	if _, err := runner.RestartWithFeatures(features); err != nil {
		return fmt.Errorf("%w\n%s", err, daemonFailureHint)
	}
	return nil
}

// resolveDaemonCompatibility runs the pre-TUI compatibility check and lets the
// user recover from an incompatible shared server. It returns the endpoint the
// TUI should use (nil for embedded mode) and the startup warning to display, or
// a fatal error.
func resolveDaemonCompatibility(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, allowEmbeddedFallback bool, stdin io.Reader, stdout io.Writer) (*appserverdaemon.RemoteAppServerEndpoint, string, error) {
	daemonCompatibilityWarningText = ""
	warning, issue := daemonCompatibilityCheck(ctx, root, endpoint, allowEmbeddedFallback)
	if issue == nil {
		if warning != "" {
			// Rust reports a fallback warning and moves the target to embedded.
			return nil, recordDaemonCompatibilityWarning(warning), nil
		}
		return endpoint, warning, nil
	}
	// Rust requires a terminal for recovery; without one the incompatibility is
	// fatal.
	if !daemonRecoveryInteractive(stdin, stdout) {
		return nil, "", issue
	}
	managed := false
	if issue.RestartFeatures != nil {
		managed = daemonRecoveryManaged()
	}
	result, err := daemonRecoveryRun(ctx, onboarding.DaemonRecoveryOptions{
		Reason:          issue.Reason,
		RestartFeatures: issue.RestartFeatures,
		Managed:         managed,
		NoAltScreen:     root != nil && root.Shared.NoAltScreen,
	}, stdin, stdout)
	if err != nil {
		return nil, "", err
	}
	if result.Cancelled {
		return nil, "", issue
	}
	switch result.Choice {
	case onboarding.DaemonRecoveryRunWithoutDaemon:
		return nil, recordDaemonCompatibilityWarning(fmt.Sprintf("Running without the shared background server: %s.", issue.Reason)), nil
	case onboarding.DaemonRecoveryRestart:
		if issue.RestartFeatures == nil || !managed {
			return nil, "", issue
		}
		if err := daemonRecoveryRestartWithFeatures(issue.RestartFeatures); err != nil {
			return nil, "", err
		}
		// One re-check after the restart, never a loop.
		retryWarning, retryIssue := daemonCompatibilityCheck(ctx, root, endpoint, allowEmbeddedFallback)
		if retryIssue != nil {
			return nil, "", retryIssue
		}
		if retryWarning != "" {
			return nil, recordDaemonCompatibilityWarning(retryWarning), nil
		}
		return endpoint, retryWarning, nil
	default:
		return nil, "", issue
	}
}

// recordDaemonCompatibilityWarning publishes the warning daemonStartupWarning
// reports once the TUI opens.
func recordDaemonCompatibilityWarning(warning string) string {
	daemonCompatibilityWarningText = warning
	return warning
}
