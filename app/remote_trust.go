package app

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
)

// TrustStatus mirrors Rust trust_directory.rs decisions (#39082).
type TrustStatus string

const (
	TrustStatusTrusted   TrustStatus = "trusted"
	TrustStatusUntrusted TrustStatus = "untrusted"
	TrustStatusUndecided TrustStatus = "undecided"
	TrustStatusDeclined  TrustStatus = "declined"
)

// TrustConfirmFunc asks the user whether to trust a directory; injected for
// tests and the TUI.
type TrustConfirmFunc func(cwd string, trustTarget string) (bool, error)

// remoteProjectTrustStatus reads the remote app server's effective config and
// reports the trust decision for cwd / trustTarget (Rust #39082: query remote
// project config layers before starting a thread).
func remoteProjectTrustStatus(ctx context.Context, client *remoteAppServerTUIClient, cwd string, trustTarget string) (TrustStatus, error) {
	var response config.ConfigReadResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, config.ConfigReadParams{}, &response); err != nil {
		return TrustStatusUndecided, err
	}
	return remoteTrustStatusFromValues(response.Config, cwd, trustTarget), nil
}

// remoteTrustStatusFromValues is the pure decision core (testable without a
// server): trusted when the effective projects table trusts cwd, untrusted
// when an explicit decision exists, undecided otherwise.
func remoteTrustStatusFromValues(values map[string]any, cwd string, trustTarget string) TrustStatus {
	if config.ProjectConfigEnabled(values, cwd) {
		return TrustStatusTrusted
	}
	level, explicit := config.ProjectTrustLevelForTarget(values, trustTargetForDecision(cwd, trustTarget))
	if explicit {
		if strings.EqualFold(level, "trusted") {
			return TrustStatusTrusted
		}
		return TrustStatusUntrusted
	}
	return TrustStatusUndecided
}

// persistRemoteProjectTrust persists an accepted trust decision through the
// remote server's config/batchWrite API (Rust #39082).
func persistRemoteProjectTrust(ctx context.Context, client *remoteAppServerTUIClient, path string, trusted bool) error {
	level := "untrusted"
	if trusted {
		level = "trusted"
	}
	var response config.ConfigWriteResponse
	return remoteSessionRequest(ctx, client, appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
		Edits: []config.ConfigEdit{{
			KeyPath:       "projects." + strconv.Quote(path) + ".trust_level",
			Value:         level,
			MergeStrategy: config.MergeReplace,
		}},
	}, &response)
}

// ensureRemoteProjectTrust queries the remote trust decision and, when the
// project has no existing decision, prompts (interactive only) and persists the
// accepted trust (Rust #39082). Non-interactive sessions decline without
// persisting so automation is never blocked by an interactive prompt.
func ensureRemoteProjectTrust(ctx context.Context, client *remoteAppServerTUIClient, cwd string, trustTarget string, confirm TrustConfirmFunc, interactive bool) (TrustStatus, error) {
	status, err := remoteProjectTrustStatus(ctx, client, cwd, trustTarget)
	if err != nil || status != TrustStatusUndecided {
		return status, err
	}
	if !interactive {
		return TrustStatusDeclined, nil
	}
	if confirm == nil {
		confirm = defaultTrustConfirm
	}
	trusted, err := confirm(cwd, trustTargetForDecision(cwd, trustTarget))
	if err != nil {
		return TrustStatusUndecided, err
	}
	if !trusted {
		return TrustStatusDeclined, nil
	}
	if err := persistRemoteProjectTrust(ctx, client, trustTargetForDecision(cwd, trustTarget), true); err != nil {
		return TrustStatusUndecided, err
	}
	return TrustStatusTrusted, nil
}

func trustTargetForDecision(cwd string, trustTarget string) string {
	target := strings.TrimSpace(trustTarget)
	if target == "" {
		target = strings.TrimSpace(cwd)
	}
	return target
}

// defaultTrustConfirm renders a simple terminal trust prompt. The TUI renders
// TrustDirectoryPrompt through its own flow; this covers non-TUI invocations.
func defaultTrustConfirm(cwd string, trustTarget string) (bool, error) {
	fmt.Fprintf(os.Stderr, "Do you trust the contents of this directory (%s)? Working with untrusted contents comes with higher risk of prompt injection. [y/N] ", trustTarget)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// interactiveRemoteTrustCheck is the startup hook for #39082: it queries the
// remote trust decision before the thread starts and persists accepted trust.
// Failures are non-fatal warnings so remote TUI startup stays robust.
func interactiveRemoteTrustCheck(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, root *cli.RootOptions, interactive bool) {
	if endpoint == nil || root == nil {
		return
	}
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return
	}
	defer client.close()
	cwd := strings.TrimSpace(root.Shared.CWD)
	if cwd == "" {
		return
	}
	if status, err := ensureRemoteProjectTrust(reqCtx, client, cwd, "", nil, interactive); err != nil {
		slog.Warn("remote project trust check failed", "error", err)
	} else if status == TrustStatusDeclined {
		slog.Warn("remote project trust declined; project-local config, hooks, and exec policies will not load")
	}
}
