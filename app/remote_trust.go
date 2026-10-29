package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/tool"
	"codex_go/utils"
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

// remoteTrustHost mirrors Rust ProjectTrustHost
// (codex-rs/tui/src/config_update.rs). A local executor resolves trust with
// project markers, the Git root, and canonical path keys; a remote workspace
// only knows the literal cwd key (#44746).
type remoteTrustHost string

const (
	remoteTrustHostLocal  remoteTrustHost = "local"
	remoteTrustHostRemote remoteTrustHost = "remote"
)

// remoteProjectTrustRead is the config/read view the trust driver needs: the
// decision, the saved trust level, the folder's trust target, and the effective
// config and layer list the lookup walks (Rust read_remote_project_trust
// requests include_layers and cwd).
type remoteProjectTrustRead struct {
	Status TrustStatus
	// TrustLevel is the resolved saved decision ("trusted", "untrusted", or ""
	// when the folder has none), mirroring Rust RemoteProjectTrust.trust_level.
	TrustLevel  string
	CWD         string
	TrustTarget string
	Values      map[string]any
	Layers      []config.Layer
}

// remoteProjectTrustInputs is the pure resolution input, split from the
// config/read round trip so the lookup rules stay testable.
type remoteProjectTrustInputs struct {
	Host        remoteTrustHost
	CWD         string
	TrustTarget string
	Values      map[string]any
	Layers      []config.Layer
}

// errRemoteProjectInsideUntrusted mirrors the Rust error returned when a remote
// workspace cwd sits inside an explicitly untrusted project (#44746).
var errRemoteProjectInsideUntrusted = errors.New("remote project directory is inside an explicitly untrusted project; pass the repository root explicitly with --cd")

// readRemoteProjectTrust reads the connected app server's effective config with
// layers for cwd and resolves the folder-trust decision (Rust
// read_remote_project_trust).
func readRemoteProjectTrust(ctx context.Context, client *remoteAppServerTUIClient, cwd string, host remoteTrustHost) (remoteProjectTrustRead, error) {
	cwd = strings.TrimSpace(cwd)
	params := config.ConfigReadParams{IncludeLayers: true}
	if cwd != "" {
		params.CWD = &cwd
	}
	var response config.ConfigReadResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, params, &response); err != nil {
		return remoteProjectTrustRead{Status: TrustStatusUndecided, CWD: cwd}, err
	}
	return resolveRemoteProjectTrust(remoteProjectTrustInputs{
		Host:   host,
		CWD:    cwd,
		Values: response.Config,
		Layers: response.Layers,
	})
}

// remoteProjectTrustStatus reads the decision for one folder.
func remoteProjectTrustStatus(ctx context.Context, client *remoteAppServerTUIClient, cwd string, host remoteTrustHost) (TrustStatus, error) {
	read, err := readRemoteProjectTrust(ctx, client, cwd, host)
	return read.Status, err
}

// remoteTrustStatusFromValues is the decision core without a server: trusted
// when the effective projects table trusts cwd or an ancestor, untrusted when an
// explicit decision exists, undecided otherwise.
func remoteTrustStatusFromValues(values map[string]any, cwd string, trustTarget string) TrustStatus {
	read, _ := resolveRemoteProjectTrust(remoteProjectTrustInputs{
		Host:        remoteTrustHostRemote,
		CWD:         cwd,
		TrustTarget: trustTarget,
		Values:      values,
	})
	return read.Status
}

// resolveRemoteProjectTrust ports the resolution half of
// codex-rs/tui/src/config_update.rs (Rust #44746):
//
//   - cwd spellings (canonical, then original) take precedence over the
//     project-marker root, which takes precedence over the Git root;
//   - an explicit disabled project layer names its own trust target;
//   - a folder with no project configuration may skip consent entirely;
//   - a remote cwd inside an explicitly untrusted project is an error;
//   - otherwise the saved level selects trusted / restricted (untrusted) /
//     consent required.
func resolveRemoteProjectTrust(in remoteProjectTrustInputs) (remoteProjectTrustRead, error) {
	read := remoteProjectTrustRead{
		Status:      TrustStatusUndecided,
		CWD:         strings.TrimSpace(in.CWD),
		TrustTarget: strings.TrimSpace(in.TrustTarget),
		Values:      in.Values,
		Layers:      in.Layers,
	}
	projectLayers := projectConfigLayers(in.Layers)
	disabledProject := lastDisabledProjectLayer(projectLayers)
	disabledReason := ""
	if disabledProject != nil && disabledProject.DisabledReason != nil {
		disabledReason = strings.TrimSpace(*disabledProject.DisabledReason)
	}
	projects, _ := in.Values["projects"].(map[string]any)
	windowsPath := trustPathUsesWindowsConvention(read.CWD)
	cwdKey := read.CWD
	if windowsPath && cwdKey != "" {
		cwdKey = strings.ToLower(cwdKey)
	}
	projectEntry := func(key string) (string, string, bool) {
		return projectTrustEntryForCWDKey(projects, key, windowsPath)
	}

	local := in.Host == remoteTrustHostLocal
	var projectRootKeys, gitRootKeys []string
	projectless := false
	if local && len(projectLayers) == 0 && read.CWD != "" {
		markers := projectRootMarkersForTrust(in.Values)
		projectRootKeys = config.NormalizedProjectTrustKeys(config.FindProjectRoot(read.CWD, markers))
		if gitRoot, ok := config.GitRootForTrust(read.CWD); ok {
			gitRootKeys = config.NormalizedProjectTrustKeys(gitRoot)
		}
		// Rust #46328: a folder is projectless only when neither the configured
		// markers nor the defaults find a project root.
		projectless = config.DiscoveredProjectlessFolder(read.CWD, markers)
	}
	cwdKeys := []string{cwdKey}
	if local && read.CWD != "" {
		cwdKeys = config.NormalizedProjectTrustKeys(read.CWD)
	}
	savedTrustTarget := func(keys []string) (string, bool) {
		for _, key := range keys {
			if path, _, ok := projectEntry(key); ok {
				return path, true
			}
		}
		return "", false
	}
	cwdTrustTarget, hasCWDTrust := savedTrustTarget(cwdKeys)
	trustTarget := cwdTrustTarget
	switch {
	case hasCWDTrust:
	case disabledReason != "":
		if target, ok := trustTargetFromDisabledReason(disabledReason); ok {
			trustTarget = target
		} else if folder := disabledProjectDotCodexFolder(disabledProject); folder != "" {
			trustTarget = folder
		}
	case local && len(projectLayers) == 0:
		if path, ok := savedTrustTarget(projectRootKeys); ok {
			trustTarget = path
		} else if path, ok := savedTrustTarget(gitRootKeys); ok {
			trustTarget = path
		} else if len(gitRootKeys) > 0 {
			trustTarget = gitRootKeys[0]
		} else if len(projectRootKeys) > 0 {
			trustTarget = projectRootKeys[0]
		}
	}
	if strings.TrimSpace(trustTarget) == "" {
		if explicit := strings.TrimSpace(in.TrustTarget); explicit != "" {
			trustTarget = explicit
		} else {
			trustTarget = read.CWD
		}
	}

	trustLevel := ""
	if _, level, ok := projectEntry(trustTarget); ok {
		switch strings.ToLower(level) {
		case "trusted":
			trustLevel = "trusted"
		case "untrusted":
			trustLevel = "untrusted"
		}
	}
	explicitlyUntrusted := !hasCWDTrust && disabledReasonIsExplicitlyUntrusted(projects, disabledReason)
	if explicitlyUntrusted {
		trustLevel = "untrusted"
	}
	read.TrustTarget = trustTarget
	read.TrustLevel = trustLevel

	projectLayerEnabled := false
	for _, layer := range projectLayers {
		if layer.DisabledReason == nil {
			projectLayerEnabled = true
			break
		}
	}
	if !explicitlyUntrusted && (trustLevel == "trusted" ||
		(trustLevel == "" && projectless) ||
		(trustLevel == "" && disabledProject == nil && projectLayerEnabled)) {
		// Rust returns Ok(None): this folder needs no folder-consent decision.
		read.Status = TrustStatusTrusted
		return read, nil
	}
	if in.Host == remoteTrustHostRemote && trustLevel == "" && !explicitlyUntrusted && len(projectLayers) == 0 &&
		cwdInsideUntrustedProject(projects, read.CWD) {
		return read, errRemoteProjectInsideUntrusted
	}
	switch trustLevel {
	case "trusted":
		read.Status = TrustStatusTrusted
	case "untrusted":
		read.Status = TrustStatusUntrusted
	default:
		read.Status = TrustStatusUndecided
	}
	return read, nil
}

// projectTrustEntryForCWDKey selects a project entry the way Rust's project_entry
// closure does: the exact projects key first, then (Windows only) the
// deterministic case-insensitive alias with the smallest spelling. Only entries
// carrying a trust_level count as a saved decision.
func projectTrustEntryForCWDKey(projects map[string]any, key string, windowsPath bool) (string, string, bool) {
	if len(projects) == 0 || strings.TrimSpace(key) == "" {
		return "", "", false
	}
	if path, level, ok := trustLevelForProjectKey(projects, key); ok {
		return path, level, true
	}
	if !windowsPath {
		return "", "", false
	}
	best := ""
	for path := range projects {
		if !strings.EqualFold(path, key) {
			continue
		}
		if best == "" || path < best {
			best = path
		}
	}
	if best == "" {
		return "", "", false
	}
	return trustLevelForProjectKey(projects, best)
}

func trustLevelForProjectKey(projects map[string]any, key string) (string, string, bool) {
	project, ok := projects[key].(map[string]any)
	if !ok {
		return "", "", false
	}
	level, ok := project["trust_level"].(string)
	if !ok {
		return "", "", false
	}
	return key, level, true
}

// projectConfigLayers returns the project-source layers in server order.
func projectConfigLayers(layers []config.Layer) []config.Layer {
	var out []config.Layer
	for _, layer := range layers {
		if layer.Name.Type == config.LayerSourceProject {
			out = append(out, layer)
		}
	}
	return out
}

// lastDisabledProjectLayer is Rust's
// `project_layers.iter().rev().find(|layer| layer.disabledReason.is_some())`.
func lastDisabledProjectLayer(layers []config.Layer) *config.Layer {
	for index := len(layers) - 1; index >= 0; index-- {
		if layers[index].DisabledReason != nil {
			return &layers[index]
		}
	}
	return nil
}

// projectRootMarkersForTrust mirrors Rust's
// `serde_json::from_value::<Option<Vec<String>>>(config.project_root_markers)
// .unwrap_or_else(default_project_root_markers)`: an absent or null value means
// the defaults, while an explicitly empty list means no discovery at all.
func projectRootMarkersForTrust(values map[string]any) []string {
	raw, present := values["project_root_markers"]
	if !present || raw == nil {
		return config.DefaultProjectRootMarkers()
	}
	return (&config.Config{Values: values}).ProjectRootMarkers()
}

// trustTargetFromDisabledReason extracts `<path>` from Rust's
// "<path>, add <path> as a trusted project in <file>" disabled-folder reason.
func trustTargetFromDisabledReason(reason string) (string, bool) {
	_, addPath, ok := strings.Cut(reason, ", add ")
	if !ok {
		return "", false
	}
	index := strings.LastIndex(addPath, " as a trusted project in ")
	if index < 0 {
		return "", false
	}
	target := strings.TrimSpace(addPath[:index])
	return target, target != ""
}

// disabledProjectDotCodexFolder is Rust's fallback of stripping "/.codex" or
// "\.codex" off a disabled project layer's dotCodexFolder.
func disabledProjectDotCodexFolder(layer *config.Layer) string {
	if layer == nil {
		return ""
	}
	folder := strings.TrimSpace(layer.Name.DotCodexFolder)
	if folder == "" {
		return ""
	}
	if trimmed, ok := strings.CutSuffix(folder, "/.codex"); ok {
		return trimmed
	}
	if trimmed, ok := strings.CutSuffix(folder, `\.codex`); ok {
		return trimmed
	}
	return ""
}

// disabledReasonIsExplicitlyUntrusted is Rust's explicitly_untrusted probe: some
// saved untrusted project owns the disabled reason.
func disabledReasonIsExplicitlyUntrusted(projects map[string]any, disabledReason string) bool {
	if strings.TrimSpace(disabledReason) == "" {
		return false
	}
	for path, raw := range projects {
		project, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if level, _ := project["trust_level"].(string); level != "untrusted" {
			continue
		}
		suffix, ok := strings.CutPrefix(disabledReason, path)
		if ok && strings.HasPrefix(suffix, " is marked as untrusted") {
			return true
		}
	}
	return false
}

// cwdInsideUntrustedProject ports the remote-workspace guard: cwd starts with a
// project path saved as untrusted.
func cwdInsideUntrustedProject(projects map[string]any, cwd string) bool {
	cwdCanonical := config.CanonicalProjectTrustPath(cwd)
	if cwdCanonical == "" {
		return false
	}
	for path, raw := range projects {
		project, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if level, _ := project["trust_level"].(string); level != "untrusted" {
			continue
		}
		projectCanonical := config.CanonicalProjectTrustPath(path)
		if projectCanonical == "" {
			continue
		}
		if cwdCanonical == projectCanonical {
			return true
		}
		prefix := strings.TrimRight(projectCanonical, `/\`)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(cwdCanonical, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func trustPathUsesWindowsConvention(path string) bool {
	return tool.PathConventionForPath(path) == utils.ConventionWindows
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
	// ResumedTask reports a resumed task: Rust shows the existing-task
	// disclosure for an untrusted folder it opens (existing_task = connected &&
	// resumed_thread.is_some()).
	ResumedTask bool
	Root        *cli.RootOptions
}

// ensureRemoteProjectTrust queries the trust decision and, when the folder has
// no decision, prompts (interactive only) and persists the accepted trust
// (Rust #39082). Non-interactive sessions decline without persisting so
// automation is never blocked by an interactive prompt.
//
// #44746: an explicitly untrusted folder is opened restricted and keeps its
// saved decision, and a folder that needs no consent (already trusted, trusted
// project config layer, or projectless) returns Trusted without prompting.
func ensureRemoteProjectTrust(ctx context.Context, client *remoteAppServerTUIClient, req remoteTrustRequest) (TrustStatus, error) {
	host := remoteTrustHostRemote
	if req.Local {
		host = remoteTrustHostLocal
	}
	read, err := readRemoteProjectTrust(ctx, client, req.CWD, host)
	if err != nil || read.Status != TrustStatusUndecided {
		return read.Status, err
	}
	return confirmAndPersistDirectoryTrust(ctx, client, req, read)
}

// confirmAndPersistDirectoryTrust asks for consent for one resolved folder and
// persists an accepted decision. It never persists for a restricted folder.
func confirmAndPersistDirectoryTrust(ctx context.Context, client *remoteAppServerTUIClient, req remoteTrustRequest, read remoteProjectTrustRead) (TrustStatus, error) {
	if read.Status == TrustStatusUntrusted {
		return TrustStatusUntrusted, nil
	}
	if !req.Interactive {
		return TrustStatusDeclined, nil
	}
	confirm := req.Confirm
	if confirm == nil {
		confirm = defaultTrustConfirm
	}
	target := trustTargetForDecision(read.CWD, read.TrustTarget)
	trusted, err := confirm(read.CWD, target)
	if err != nil {
		return TrustStatusUndecided, err
	}
	if !trusted {
		return TrustStatusDeclined, nil
	}
	if err := persistRemoteProjectTrust(ctx, client, target, true); err != nil {
		return TrustStatusUndecided, err
	}
	return TrustStatusTrusted, nil
}

// directoryTrustRequest carries one folder-consent pass (Rust
// onboarding/directory_trust.rs check_directory_trust, #44746).
type directoryTrustRequest struct {
	Client *remoteAppServerTUIClient
	Host   remoteTrustHost
	// CWD is the destination the session resolved to.
	CWD string
	// SavedCWD is the folder a resumed local-daemon task was saved in, when the
	// session attaches to that task instead of CWD.
	SavedCWD string
	// ResumedTask marks a resumed task so an untrusted folder uses the
	// existing-task disclosure.
	ResumedTask bool
	// RereadCWD re-reads the task after consent so a folder that changed while
	// the prompt was open is checked too (Rust thread/read, include_turns false).
	RereadCWD   func(context.Context) (string, error)
	Confirm     TrustConfirmFunc
	Interactive bool
	Root        *cli.RootOptions
}

// directoryTrustResult reports the outcome of one consent pass.
type directoryTrustResult struct {
	Status     TrustStatus
	Persisted  bool
	Restricted bool
	// ExistingTask reports that the restricted folder holds a resumed task, and
	// therefore may retain configuration or hooks loaded while it was trusted.
	ExistingTask bool
	TrustTarget  string
	// ShouldExit reports a declined prompt: Rust quits startup instead of
	// starting the session.
	ShouldExit bool
}

// checkDirectoryTrust ports Rust's check_directory_trust: consent covers the
// destination the picker/resume resolved, plus (for a local daemon resume) the
// task's saved folder. After a persisted decision the task is re-read so a
// folder changed by another client during consent is checked too.
func checkDirectoryTrust(ctx context.Context, req directoryTrustRequest) (directoryTrustResult, error) {
	result := directoryTrustResult{Status: TrustStatusTrusted}
	if req.Client == nil {
		return result, nil
	}
	host := req.Host
	if host == "" {
		host = remoteTrustHostRemote
	}
	trustReq := remoteTrustRequest{
		Confirm:     req.Confirm,
		Interactive: req.Interactive,
		Local:       host == remoteTrustHostLocal,
		ResumedTask: req.ResumedTask,
		Root:        req.Root,
	}
	var pending []string
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		pending = append(pending, cwd)
	}
	if saved := strings.TrimSpace(req.SavedCWD); saved != "" {
		pending = append(pending, saved)
	}
	checked := map[string]bool{}
	for len(pending) > 0 {
		cwd := pending[0]
		pending = pending[1:]
		key := config.CanonicalProjectTrustPath(cwd)
		if checked[key] {
			continue
		}
		checked[key] = true
		read, err := readRemoteProjectTrust(ctx, req.Client, cwd, host)
		if err != nil {
			return result, err
		}
		if read.Status == TrustStatusTrusted {
			continue
		}
		// Rust keeps the existing behavior for a saved untrusted folder on a
		// remote workspace: it is not a startup consent target.
		if host == remoteTrustHostRemote && read.Status == TrustStatusUntrusted {
			continue
		}
		status, err := confirmAndPersistDirectoryTrust(ctx, req.Client, trustReq, read)
		if err != nil {
			return result, err
		}
		result.Status = status
		result.TrustTarget = read.TrustTarget
		switch status {
		case TrustStatusDeclined:
			if req.Interactive {
				result.ShouldExit = true
				return result, nil
			}
			continue
		case TrustStatusUntrusted:
			result.Restricted = true
			result.ExistingTask = req.ResumedTask
			continue
		case TrustStatusTrusted:
			result.Persisted = true
		}
		if host == remoteTrustHostLocal && req.ResumedTask && req.RereadCWD != nil {
			latest, rereadErr := req.RereadCWD(ctx)
			if rereadErr != nil {
				return result, rereadErr
			}
			if latest = strings.TrimSpace(latest); latest != "" {
				pending = append(pending, latest)
			}
		}
	}
	return result, nil
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
// Failures are non-fatal warnings so remote TUI startup stays robust. The
// caller runs it before loading server settings so a persisted decision is part
// of the configuration the session starts with (Rust #44746).
func interactiveRemoteTrustCheck(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, root *cli.RootOptions, interactive bool) TrustStatus {
	if endpoint == nil || root == nil {
		return TrustStatusTrusted
	}
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return TrustStatusUndecided
	}
	defer client.close()
	cwd := strings.TrimSpace(root.Shared.CWD)
	if cwd == "" {
		return TrustStatusTrusted
	}
	host := remoteTrustHostRemote
	if interactiveRemoteEndpointIsLocal(endpoint) {
		host = remoteTrustHostLocal
	}
	result, err := checkDirectoryTrust(reqCtx, directoryTrustRequest{
		Client:      client,
		Host:        host,
		CWD:         cwd,
		Confirm:     defaultTrustConfirm,
		Interactive: interactive,
		Root:        root,
	})
	if err != nil {
		slog.Warn("remote project trust check failed", "error", err)
		return TrustStatusUndecided
	}
	if result.Restricted {
		slog.Warn("remote project folder is restricted; project-local config, hooks, and exec policies stay disabled and saved trust is unchanged")
	} else if result.Status == TrustStatusDeclined {
		slog.Warn("remote project trust declined; project-local config, hooks, and exec policies will not load")
	}
	return result.Status
}
