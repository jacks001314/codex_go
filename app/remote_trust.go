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

// remoteProjectTrustRead is the config/read view the trust driver needs: the
// decision plus the effective config and layer list the #49160 projectless
// probe walks (Rust read_remote_project_trust requests include_layers).
type remoteProjectTrustRead struct {
	Status TrustStatus
	Values map[string]any
	Layers []config.Layer
}

// readRemoteProjectTrust reads the remote app server's effective config with
// layers and reports the trust decision for cwd / trustTarget (Rust #39082 /
// #49160: query remote project config layers before starting a thread).
func readRemoteProjectTrust(ctx context.Context, client *remoteAppServerTUIClient, cwd string, trustTarget string) (remoteProjectTrustRead, error) {
	var response config.ConfigReadResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, config.ConfigReadParams{IncludeLayers: true}, &response); err != nil {
		return remoteProjectTrustRead{Status: TrustStatusUndecided}, err
	}
	return remoteProjectTrustRead{
		Status: remoteTrustStatusFromValues(response.Config, cwd, trustTarget),
		Values: response.Config,
		Layers: response.Layers,
	}, nil
}

// remoteProjectTrustStatus reads the remote app server's effective config and
// reports the trust decision for cwd / trustTarget.
func remoteProjectTrustStatus(ctx context.Context, client *remoteAppServerTUIClient, cwd string, trustTarget string) (TrustStatus, error) {
	read, err := readRemoteProjectTrust(ctx, client, cwd, trustTarget)
	return read.Status, err
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
		// Rust config_update::write_trusted_project goes through
		// write_config_batch, which reloads the user config.
		ReloadUserConfig: true,
		Edits: []config.ConfigEdit{{
			KeyPath:       "projects." + strconv.Quote(path) + ".trust_level",
			Value:         level,
			MergeStrategy: config.MergeReplace,
		}},
	}, &response)
}

// remoteTrustRequest carries the live trust-driver inputs. Root supplies the
// launch overrides Rust reads from ConfigOverrides.
type remoteTrustRequest struct {
	CWD         string
	TrustTarget string
	Confirm     TrustConfirmFunc
	Interactive bool
	// Local reports that the connection runs on this host (Rust
	// ProjectTrustHost::Local, app/remote_tui.go's local endpoint).
	Local bool
	Root  *cli.RootOptions
}

// ensureRemoteProjectTrust queries the remote trust decision and, when the
// project has no existing decision, prompts (interactive only) and persists the
// accepted trust (Rust #39082). Non-interactive sessions decline without
// persisting so automation is never blocked by an interactive prompt.
//
// Rust #49160: a positively discovered local projectless folder with no saved
// decision needs no folder-trust decision, so the prompt is skipped and nothing
// is persisted (read_remote_project_trust returns Ok(None) and
// onboarding/directory_trust.rs never renders the trust widget).
func ensureRemoteProjectTrust(ctx context.Context, client *remoteAppServerTUIClient, req remoteTrustRequest) (TrustStatus, error) {
	read, err := readRemoteProjectTrust(ctx, client, req.CWD, req.TrustTarget)
	if err != nil || read.Status != TrustStatusUndecided {
		return read.Status, err
	}
	if projectlessRemoteTrustSkip(req, read) {
		return TrustStatusTrusted, nil
	}
	if !req.Interactive {
		return TrustStatusDeclined, nil
	}
	confirm := req.Confirm
	if confirm == nil {
		confirm = defaultTrustConfirm
	}
	trusted, err := confirm(req.CWD, trustTargetForDecision(req.CWD, req.TrustTarget))
	if err != nil {
		return TrustStatusUndecided, err
	}
	if !trusted {
		return TrustStatusDeclined, nil
	}
	if err := persistRemoteProjectTrust(ctx, client, trustTargetForDecision(req.CWD, req.TrustTarget), true); err != nil {
		return TrustStatusUndecided, err
	}
	return TrustStatusTrusted, nil
}

// projectlessRemoteTrustSkip reports whether the trust driver may skip the
// folder-trust prompt for this read. Only a local connection qualifies: Rust
// computes `projectless` inside the `host == ProjectTrustHost::Local` branch of
// read_remote_project_trust.
func projectlessRemoteTrustSkip(req remoteTrustRequest, read remoteProjectTrustRead) bool {
	if !req.Local {
		return false
	}
	return ProjectlessFolderTrustEligible(ProjectlessFolderTrustInputs{
		CWD:     req.CWD,
		Local:   true,
		Markers: (&config.Config{Values: read.Values}).ProjectRootMarkers(),
		// read.Status == Undecided means no explicit decision exists, so
		// SavedTrustDecision stays false (Rust trust_level.is_none()).
		ProjectLayers: projectConfigLayerCount(read.Layers),
	})
}

// projectConfigLayerCount counts the `project` layers the server reported;
// Rust's `project_layers.is_empty()` gate and `is_projectless()` both require
// none before a folder may count as projectless.
func projectConfigLayerCount(layers []config.Layer) int {
	count := 0
	for _, layer := range layers {
		if layer.Name.Type == config.LayerSourceProject {
			count++
		}
	}
	return count
}

// projectlessLaunchOverrides reports launch-time sandbox / permission overrides
// (Rust ConfigOverrides.sandbox_mode / permission_profile / default_permissions),
// which keep the configured permissions instead of the implicit defaults.
func projectlessLaunchOverrides(root *cli.RootOptions) bool {
	if root == nil {
		return false
	}
	if strings.TrimSpace(root.Shared.Sandbox) != "" {
		return true
	}
	for _, key := range remoteCLIConfigOverrideKeys(root) {
		switch strings.TrimSpace(key) {
		case "sandbox_mode", "sandbox", "sandbox_workspace_write", "default_permissions",
			"permissions", "permission_profile", "network":
			return true
		}
	}
	return false
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
	if status, err := ensureRemoteProjectTrust(reqCtx, client, remoteTrustRequest{
		CWD:         cwd,
		Interactive: interactive,
		Local:       interactiveRemoteEndpointIsLocal(endpoint),
		Root:        root,
	}); err != nil {
		slog.Warn("remote project trust check failed", "error", err)
	} else if status == TrustStatusDeclined {
		slog.Warn("remote project trust declined; project-local config, hooks, and exec policies will not load")
	}
}
