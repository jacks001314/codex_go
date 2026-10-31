package appserver

// Session shell snapshots.
//
// Rust parity: codex-rs/core/src/session/session.rs (one ShellSnapshot per
// session, gated on Feature::ShellSnapshot) and
// core/src/session/turn_context.rs (the per-launch gates that decide whether a
// command replays it). A session captures the user's shell state once, reuses
// the captured file for every login-shell launch in the session's own
// directory, and removes it when the session ends.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/execpolicy"
	execserverclient "codex_go/execserver"
	"codex_go/features"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/shell"
	"codex_go/telemetry"
	"codex_go/tool"
)

// shellSnapshotCaptureRunner runs a session's snapshot captures. It is a
// variable so a test can stand in for a real POSIX shell and its sandbox.
var shellSnapshotCaptureRunner tool.SnapshotCaptureRunner

// shellSnapshotPruneLookup reports the rollout age of a session, which the
// snapshot prune needs to tell a live session's snapshots from a dead one's
// (Rust's `find_thread_path_by_id_str` plus the rollout's metadata).
func (r *RuntimeRouter) shellSnapshotPruneLookup() shell.SnapshotPruneLookup {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	store := r.services.ThreadRouter.store
	return func(sessionID string) (time.Time, bool) {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return time.Time{}, false
		}
		path, err := store.Path(session.ThreadID(sessionID))
		if err != nil || strings.TrimSpace(path) == "" {
			return time.Time{}, false
		}
		info, err := os.Stat(path)
		if err != nil {
			return time.Time{}, false
		}
		return info.ModTime(), true
	}
}

// shellSnapshotProviderForTurn returns the snapshot provider this turn's shell
// executor asks, or nil when the session has no snapshot.
//
// Rust keeps two independent mechanisms: the session file snapshot
// (`Feature::ShellSnapshot`) and the executor-owned snapshot requested through
// `shell_snapshot_request` (`Feature::ShellSnapshotV2`). Both may be enabled, so
// the provider answers with the file path, the executor request, or both.
func (r *RuntimeRouter) shellSnapshotProviderForTurn(threadID string, cfg *config.Config) func(context.Context, tool.SnapshotProviderRequest) tool.SnapshotProviderResult {
	if r == nil || cfg == nil {
		return nil
	}
	v2 := r.shellSnapshotV2ProviderForTurn(threadID, cfg)
	if !features.Enabled(cfg.FeatureSettings(), "shell_snapshot") {
		return v2
	}
	v1 := r.shellSnapshotV1ProviderForTurn(threadID, cfg)
	if v1 == nil || v2 == nil {
		if v1 != nil {
			return v1
		}
		return v2
	}
	return func(ctx context.Context, request tool.SnapshotProviderRequest) tool.SnapshotProviderResult {
		result := v1(ctx, request)
		executorSnapshot := v2(ctx, request)
		if executorSnapshot.Remote != nil {
			// Rust #39957 scopes the executor snapshot to the thread and the
			// selected environment, so it supersedes the v1 remote request.
			result.Remote = executorSnapshot.Remote
		}
		result.Local = executorSnapshot.Local
		return result
	}
}

// shellSnapshotV1ProviderForTurn returns the session file snapshot provider. The
// feature gate is Rust's `Feature::ShellSnapshot`; the session's builder is
// created on first use and closed when the thread unloads.
func (r *RuntimeRouter) shellSnapshotV1ProviderForTurn(threadID string, cfg *config.Config) func(context.Context, tool.SnapshotProviderRequest) tool.SnapshotProviderResult {
	if r == nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "shell_snapshot") {
		return nil
	}

	builder := r.shellSnapshotBuilderForThread(threadID)
	if builder == nil {
		return nil
	}
	threadCWD := r.shellSnapshotThreadCWD(threadID)
	protected := shellSnapshotProtected(cfg)
	// Rust starts the session's snapshot capture as soon as the turn environments
	// resolve, so the first command does not wait for it. Protected captures (an
	// active credential broker) stay lazy and sandboxed.
	if !protected {
		r.prewarmShellSnapshot(builder, threadID, threadCWD, cfg)
	}
	return func(ctx context.Context, request tool.SnapshotProviderRequest) tool.SnapshotProviderResult {
		if !shellSnapshotLaunchEligible(request) {
			return tool.SnapshotProviderResult{}
		}
		// Rust's session file snapshot is local-only
		// (runtimes/unified_exec.rs skips it for `environment_is_remote`); a
		// remote environment only replays the executor-owned snapshot, which the
		// `shell_snapshot_v2` gate requests after checking the capability.
		if request.Remote {
			return tool.SnapshotProviderResult{}
		}
		if protected && (!request.Protected || request.PermissionProfile == nil) {
			// Rust only creates a protected snapshot once the credential broker
			// is Ready and the launch has a sandbox to capture under. Starting or
			// unavailable broker state leaves the launch without a snapshot.
			return tool.SnapshotProviderResult{}
		}
		// Rust only replays the session's snapshot for the session's own
		// directory; another directory has no captured state to restore. A
		// protected (brokered) session is exempt, exactly like Rust's
		// `req.cwd != selection.cwd && !brokered` gate.
		if threadCWD != "" && !sameDirectory(request.CWD, threadCWD) && !protected {
			return tool.SnapshotProviderResult{}
		}
		// Every launch in the session's directory replays the one session
		// snapshot, captured with a login shell and without a sandbox (Rust's
		// start_shell_snapshot_task plus the non-broker branch of
		// TurnEnvironment::shell_snapshot, which ignores the launch's sandbox).
		captureRequest := tool.SnapshotCaptureRequest{
			ShellType:         request.ShellType,
			ShellPath:         request.ShellPath,
			CWD:               request.CWD,
			AllowLoginShell:   true,
			EnvironmentPolicy: request.EnvironmentPolicy,
		}
		if protected {
			brokerCapture := r.shellSnapshotBrokerCaptureForThread(threadID, request.EnvironmentID)
			if brokerCapture == nil {
				return tool.SnapshotProviderResult{}
			}
			// Rust captures a protected snapshot per command shape: its shell,
			// working directory, login mode, sandbox and environment policy.
			captureRequest.AllowLoginShell = request.AllowLoginShell
			captureRequest.PermissionProfile = request.PermissionProfile
			captureRequest.PermissionProfileID = request.PermissionProfileID
			captureRequest.Protected = true
			captureRequest.Env = brokerCapture.Env
			captureRequest.SourceEnv = brokerCapture.SourceEnv
			captureRequest.CredentialPairs = brokerCapture.CredentialPairs
			captureRequest.CredentialEnvKeys = brokerCapture.CredentialEnvKeys
			captureRequest.CredentialTextUnsafe = brokerCapture.CredentialTextUnsafe
		}
		// The availability state is read before the capture, like Rust's peek.
		state := r.shellSnapshotCommandState(request, protected, builder, captureRequest)
		started := time.Now()
		snapshot, reason := builder.Snapshot(ctx, captureRequest)
		wait := time.Since(started)
		r.recordShellSnapshot(wait, reason, "execution", string(request.ShellType))
		path := snapshot.Path()
		if state == "" {
			return tool.SnapshotProviderResult{Path: path, Env: snapshot.BrokeredEnv()}
		}
		if path == "" && reason != "" {
			// Rust tags a snapshot that cannot be made available `unavailable`.
			// Go's prewarm cache only ever holds successful captures, so a
			// capture/validation failure is the state a launch observes there.
			state = "unavailable"
		}
		return tool.SnapshotProviderResult{
			Path: path,
			Env:  snapshot.BrokeredEnv(),
			ObserveCommandOutcome: func(used bool) {
				r.recordShellSnapshotCommand(wait, state, used)
			},
		}
	}
}

// localShellSnapshotV2Supported reports the local executor's shell snapshot v2
// capability. It is a variable so a test can stand in for a POSIX host.
var localShellSnapshotV2Supported = execserverclient.LocalShellSnapshotV2Supported

// shellSnapshotV2ProviderForTurn mirrors Rust `shell_snapshot_request`
// (core/src/unified_exec/shell_snapshot.rs:169-196): the executor-owned snapshot
// is requested only when the `shell_snapshot_v2` feature is on, the selected
// environment advertises the capability, the launch runs in the environment's
// own directory, and the resolved shell is a POSIX login shell. The scope is
// `thread:environment`, like Rust's `shell_snapshot_request` scope id.
func (r *RuntimeRouter) shellSnapshotV2ProviderForTurn(threadID string, cfg *config.Config) func(context.Context, tool.SnapshotProviderRequest) tool.SnapshotProviderResult {
	if r == nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "shell_snapshot_v2") {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	threadCWD := r.shellSnapshotThreadCWD(threadID)
	r.prewarmShellSnapshotV2(threadID, threadCWD, cfg)
	return func(_ context.Context, request tool.SnapshotProviderRequest) tool.SnapshotProviderResult {
		if !request.AllowLoginShell || !shellSnapshotShellSupported(request.ShellType) {
			return tool.SnapshotProviderResult{}
		}
		// Rust compares the launch directory with the environment's selection
		// directory; a command in another directory has no captured state.
		if threadCWD != "" && !sameDirectory(request.CWD, threadCWD) {
			return tool.SnapshotProviderResult{}
		}
		if !r.shellSnapshotV2EnvironmentSupported(request) {
			return tool.SnapshotProviderResult{}
		}
		environmentID := strings.TrimSpace(request.EnvironmentID)
		if environmentID == "" {
			environmentID = execserverclient.LocalEnvironmentID
		}
		snapshot := &execserverclient.ShellSnapshotRequest{
			ScopeID: threadID + ":" + environmentID,
			Shell: execserverclient.ShellInfo{
				Name: string(request.ShellType),
				Path: strings.TrimSpace(request.ShellPath),
			},
		}
		if request.Remote {
			return tool.SnapshotProviderResult{Remote: snapshot}
		}
		return tool.SnapshotProviderResult{Local: snapshot}
	}
}

// shellSnapshotV2PrewarmTimeout bounds one speculative capture, mirroring Rust
// SNAPSHOT_TIMEOUT.
const shellSnapshotV2PrewarmTimeout = 10 * time.Second

// shellSnapshotV2Prewarm runs one speculative executor capture. It is a
// variable so tests can observe the prewarm without a POSIX shell.
var shellSnapshotV2Prewarm = func(ctx context.Context, request *tool.ShellRequest) error {
	return tool.PrewarmLocalShellSnapshot(ctx, request)
}

// prewarmShellSnapshotV2 mirrors Rust `Session::prewarm_shell_snapshots`
// (core/src/unified_exec/shell_snapshot.rs:32-152): once a turn's tools and
// permissions are fixed, speculate the local environment's capture so the first
// exec_command does not pay for it. Remote and deferred environments, managed
// networking, an untrusted project and UnlessTrusted approval stay on the lazy
// capture path.
func (r *RuntimeRouter) prewarmShellSnapshotV2(threadID string, threadCWD string, cfg *config.Config) {
	if r == nil || cfg == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	// Rust requires the exec_command tool to be registered for this turn.
	if !features.Enabled(cfg.FeatureSettings(), "unified_exec") {
		return
	}
	if !localShellSnapshotV2Supported() || !cfg.AllowLoginShell() {
		return
	}
	if strings.TrimSpace(threadCWD) == "" {
		threadCWD = r.shellSnapshotThreadCWD(threadID)
	}
	if strings.TrimSpace(threadCWD) == "" {
		return
	}
	if r.shellSnapshotV2PrewarmBlocked(threadID, threadCWD, cfg) {
		return
	}
	shellType, shellPath, remote := r.sessionShellForThread(threadID)
	if remote || strings.TrimSpace(shellPath) == "" || !shellSnapshotShellSupported(shellType) {
		return
	}
	// Rust speculates once per session (`state.shell_snapshot_prewarm`); later
	// turns reuse the executor's cached capture instead of starting another
	// speculative launch.
	r.shellSnapshotsMu.Lock()
	if r.shellSnapshotV2Prewarmed == nil {
		r.shellSnapshotV2Prewarmed = map[string]struct{}{}
	}
	if _, started := r.shellSnapshotV2Prewarmed[threadID]; started {
		r.shellSnapshotsMu.Unlock()
		return
	}
	r.shellSnapshotV2Prewarmed[threadID] = struct{}{}
	r.shellSnapshotsMu.Unlock()
	request := &tool.ShellRequest{
		CWD:      threadCWD,
		ThreadID: threadID,
		Shell:    tool.ShellInvocation{Shell: &tool.Shell{Type: shellType, Path: shellPath}, UseLoginShell: true},
		LocalShellSnapshot: &execserverclient.ShellSnapshotRequest{
			ScopeID: threadID + ":" + execserverclient.LocalEnvironmentID,
			Shell:   execserverclient.ShellInfo{Name: string(shellType), Path: shellPath},
		},
	}
	if table := r.shellSnapshotEnvironmentPolicyTable(cfg); len(table) > 0 {
		request.EnvPolicy = execpolicy.EnvPolicyFromShellEnvironmentPolicy(table, threadCWD)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), shellSnapshotV2PrewarmTimeout)
		defer cancel()
		_ = shellSnapshotV2Prewarm(ctx, request)
	}()
}

// shellSnapshotV2PrewarmBlocked reports the turn settings Rust refuses to
// speculate under: an untrusted project, UnlessTrusted approval, or a managed
// network (a capture shell must not run before the proxy is in place).
func (r *RuntimeRouter) shellSnapshotV2PrewarmBlocked(threadID string, cwd string, cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	if policy, ok := parseTurnApprovalPolicy(cfg.Values["approval_policy"]); ok && policy == sandbox.ApprovalUnlessTrusted {
		return true
	}
	if level, ok := cfg.ProjectTrustLevelForLookup(config.ProjectTrustLookupFromNativePath(cwd)); ok && strings.EqualFold(level, "untrusted") {
		return true
	}
	r.managedNetworksMu.Lock()
	managed := r.managedNetworks[threadID] != nil
	r.managedNetworksMu.Unlock()
	if managed {
		return true
	}
	featuresTable, ok := cfg.Values["features"].(map[string]any)
	if !ok {
		return false
	}
	switch value := featuresTable["network_proxy"].(type) {
	case bool:
		return value
	case map[string]any:
		enabled, ok := value["enabled"].(bool)
		return ok && enabled
	}
	return false
}

// shellSnapshotV2EnvironmentSupported reads
// `turn_environment.shell_snapshot_v2_supported`: the local executor advertises
// it on POSIX hosts, and a remote environment advertises it through its cached
// environment info.
func (r *RuntimeRouter) shellSnapshotV2EnvironmentSupported(request tool.SnapshotProviderRequest) bool {
	if !request.Remote {
		return localShellSnapshotV2Supported()
	}
	if r == nil || r.services.Environment == nil {
		return false
	}
	info, ok := r.services.Environment.CachedInfo(strings.TrimSpace(request.EnvironmentID))
	return ok && info != nil && info.Capabilities.ShellSnapshotV2
}

// shellSnapshotBrokerCapture is the credential-broker state a protected
// snapshot captures under: the virtualized child environment, the original
// environment used to mark stripped credentials, and the broker's mappings.
type shellSnapshotBrokerCapture struct {
	Env                  map[string]string
	SourceEnv            map[string]string
	CredentialPairs      []network.CredentialMaskPair
	CredentialEnvKeys    []string
	CredentialTextUnsafe func(string) bool
}

// shellSnapshotBrokerCaptureForThread returns the protected-capture state for
// this thread, or nil when the broker is not ready. Rust rebuilds a protected
// snapshot whenever the broker's configuration changes; Go keys its cache by
// the resulting credential state instead.
func (r *RuntimeRouter) shellSnapshotBrokerCaptureForThread(threadID string, environmentID string) *shellSnapshotBrokerCapture {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	r.managedNetworksMu.Lock()
	prepared := r.managedNetworks[threadID]
	r.managedNetworksMu.Unlock()
	if prepared == nil {
		return nil
	}
	source := prepared.SnapshotSourceEnv()
	if source == nil {
		return nil
	}
	env, _, err := prepared.PrepareForEnvironment(environmentID)
	if err != nil || env == nil {
		return nil
	}
	pairs := prepared.PrepareSnapshotCredentialMask(source)
	return &shellSnapshotBrokerCapture{
		Env:                  env,
		SourceEnv:            source,
		CredentialPairs:      pairs,
		CredentialEnvKeys:    prepared.SnapshotCredentialEnvKeys(),
		CredentialTextUnsafe: prepared.SnapshotTextContainsCredential,
	}
}

// shellSnapshotCommandState returns the availability state Rust #51347 tags an
// eligible launch's command observation with, or "" when the launch is outside
// the metric denominator. It mirrors SnapshotMetrics::start
// (core/src/tools/runtimes/unified_exec/snapshot_metrics.rs): unix, a local
// environment, the ShellSnapshot feature (the provider exists only then), a
// Direct shell mode and a POSIX login shell. Rust's per-launch snapshot source
// exclusion (`req.shell_snapshot.is_some()`) has no Go counterpart, and Go keys
// its prewarm cache by the capture request, so Rust's "cached under another
// policy" state is reported after a failed capture instead.
func (r *RuntimeRouter) shellSnapshotCommandState(request tool.SnapshotProviderRequest, protected bool, builder *tool.SnapshotBuilder, captureRequest tool.SnapshotCaptureRequest) string {
	if runtime.GOOS == "windows" || request.Remote || request.ShellMode != tool.UnifiedExecShellModeDirect {
		return ""
	}
	if !request.AllowLoginShell || !shellSnapshotShellSupported(request.ShellType) {
		return ""
	}
	if protected {
		// Protected snapshots use a separate lazy capture, not the prewarm task.
		return "protected"
	}
	if builder.PrewarmedSnapshot(captureRequest) {
		return "prewarm_ready"
	}
	return "prewarm_pending"
}

// shellSnapshotPrewarmTimeout bounds a prewarm capture, matching the capture
// timeout the builder applies.
const shellSnapshotPrewarmTimeout = tool.DefaultSnapshotTimeout

// shellSnapshotProtected reports whether this session's snapshots are protected
// and therefore captured lazily: Rust skips the prewarm while the credential
// broker is configured and enabled (`ShellSnapshot::should_rebuild_inherited`).
func shellSnapshotProtected(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return config.CredentialBrokerProjectStateForValues(cfg.Values) == config.CredentialBrokerProjectEnabled
}

// prewarmShellSnapshot captures the session's snapshot in the background, the way
// Rust spawns `start_shell_snapshot_task` when an environment resolves. It is
// best effort: a shell that cannot be snapshotted, a remote environment or a
// failed capture simply leaves the launch without a snapshot.
func (r *RuntimeRouter) prewarmShellSnapshot(builder *tool.SnapshotBuilder, threadID string, cwd string, cfg *config.Config) {
	if r == nil || builder == nil {
		return
	}
	policyTable := r.shellSnapshotEnvironmentPolicyTable(cfg)
	shellType, shellPath, remote := r.sessionShellForThread(threadID)
	if remote || shellPath == "" || !shellSnapshotShellSupported(shellType) {
		return
	}
	if cwd == "" {
		cwd = r.shellSnapshotThreadCWD(threadID)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), shellSnapshotPrewarmTimeout)
		defer cancel()
		started := time.Now()
		_, reason, attempted := builder.PrewarmSnapshot(ctx, tool.SnapshotCaptureRequest{
			ShellType:         shellType,
			ShellPath:         shellPath,
			CWD:               cwd,
			AllowLoginShell:   true,
			EnvironmentPolicy: policyTable,
		})
		// Rust keeps one prewarm handle per session; later turns reuse the
		// result without another capture or telemetry sample (#42137). A
		// capture cancelled by thread unload is not a failure either.
		if attempted && !builder.Closed() {
			r.recordShellSnapshot(time.Since(started), reason, "prewarm", string(shellType))
		}
	}()
}

// shellSnapshotEnvironmentPolicyTable returns the thread-level
// `shell_environment_policy` table, which is the inferred policy for the primary
// environment (Rust's `inferred_environment_config`). The prewarm captures the
// session snapshot under it, and a launch whose environment resolves a different
// policy captures its own.
func (r *RuntimeRouter) shellSnapshotEnvironmentPolicyTable(cfg *config.Config) map[string]any {
	if cfg == nil {
		return nil
	}
	table, ok := cfg.Values["shell_environment_policy"].(map[string]any)
	if !ok {
		return nil
	}
	return cloneShellEnvironmentPolicy(table)
}

// recordShellSnapshot mirrors Rust's shell-snapshot telemetry
// (core/src/shell_snapshot.rs): one duration sample and one count per capture
// attempt, both tagged with the capture version and whether it succeeded, and
// the count additionally tagged with the failure reason.
func (r *RuntimeRouter) recordShellSnapshot(duration time.Duration, reason tool.SnapshotFailureReason, purpose string, shellName string) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	success := "true"
	if reason != "" {
		success = "false"
	}
	r.services.TurnMetrics.RecordDuration(telemetry.ShellSnapshotDurationMetric, duration, map[string]string{
		"version": "v1",
		"success": success,
	})
	tags := map[string]string{"version": "v1", "success": success}
	if reason != "" {
		tags["failure_reason"] = string(reason)
	}
	r.services.TurnMetrics.Counter(telemetry.ShellSnapshotCountMetric, 1, tags)
	r.shellSnapshotCaptured(duration, reason, purpose, shellName)
}

// shellSnapshotCaptured emits the exec-server v2 capture observation. Local
// execution has no separate exec-server telemetry owner, so the host turn
// metrics client observes the same capture attempt that produced the v1 core
// signal (Rust exec-server telemetry, #40620). Rust #43454 tags every v2 sample
// with the capture purpose, attempt, shell and sandbox so failures and retries
// are distinguishable, and reports a bounded reason on the duration too. Go's
// host-side builder captures once per purpose (no exec-server retry loop), so
// the attempt label is always "1", and local captures run unsandboxed ("none").
func (r *RuntimeRouter) shellSnapshotCaptured(duration time.Duration, reason tool.SnapshotFailureReason, purpose string, shellName string) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	success := "true"
	if reason != "" {
		success = "false"
	}
	tags := map[string]string{
		"version": "v2",
		"success": success,
		"purpose": purpose,
		"attempt": "1",
		"shell":   shellName,
		"sandbox": "none",
	}
	if reason != "" {
		tags["failure_reason"] = string(reason)
	}
	r.services.TurnMetrics.RecordDuration(telemetry.ShellSnapshotDurationMetric, duration, tags)
	r.services.TurnMetrics.Counter(telemetry.ShellSnapshotCountMetric, 1, tags)
}

// recordShellSnapshotCommand emits one command-level snapshot observation
// (Rust #51347): a count and a wait duration for an eligible command
// preparation, tagged with the capture version, the snapshot availability state
// and whether the replay wrapper was selected (`used`) or normal shell startup
// was used (`fallback`). Unlike recordShellSnapshot, which measures capture
// attempts, this measures what a command did with the session's snapshot.
func (r *RuntimeRouter) recordShellSnapshotCommand(wait time.Duration, state string, used bool) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	if wait < 0 {
		wait = 0
	}
	outcome := "fallback"
	if used {
		outcome = "used"
	}
	tags := map[string]string{"version": "v1", "state": state, "outcome": outcome}
	r.services.TurnMetrics.Counter(telemetry.ShellSnapshotCommandMetric, 1, tags)
	r.services.TurnMetrics.RecordDuration(telemetry.ShellSnapshotCommandWaitMetric, wait, tags)
}

// shellSnapshotLaunchEligible mirrors the launch shape Rust's turn environment
// accepts: a local environment, one of the POSIX shells the capture supports,
// and a login command (Rust allows a bare `-c` only for brokered captures, which
// Go does not have yet).
func shellSnapshotLaunchEligible(request tool.SnapshotProviderRequest) bool {
	if !request.AllowLoginShell && !request.Protected {
		return false
	}
	return shellSnapshotShellSupported(request.ShellType)
}

// shellSnapshotShellSupported reports whether the capture supports this shell:
// the POSIX shells Rust snapshots (an `sh` launch is folded into bash at the
// tool layer, and the capture script adapts to a bash-backed sh).
func shellSnapshotShellSupported(shellType tool.ShellType) bool {
	switch shellType {
	case tool.ShellBash, tool.ShellZsh:
		return true
	default:
		return false
	}
}

// shellSnapshotBuilderForThread returns the thread's builder, creating it on
// first use. A thread without a codex home or id cannot hold snapshots.
func (r *RuntimeRouter) shellSnapshotBuilderForThread(threadID string) *tool.SnapshotBuilder {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.shellSnapshotsMu.Lock()
	defer r.shellSnapshotsMu.Unlock()
	if existing := r.shellSnapshots[threadID]; existing != nil {
		return existing
	}
	builder := tool.NewSnapshotBuilder(tool.SnapshotBuilderOptions{
		CodexHome: strings.TrimSpace(r.codexHomeForRollout()),
		SessionID: threadID,
		Runner:    shellSnapshotCaptureRunner,
		Prune:     r.shellSnapshotPruneLookup(),
	})
	if builder == nil {
		return nil
	}
	if r.shellSnapshots == nil {
		r.shellSnapshots = map[string]*tool.SnapshotBuilder{}
	}
	r.shellSnapshots[threadID] = builder
	return builder
}

// shellSnapshotThreadCWD reports the thread's recorded working directory, which
// is the one Rust compares a launch against before replaying the snapshot.
func (r *RuntimeRouter) shellSnapshotThreadCWD(threadID string) string {
	if r == nil {
		return ""
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), false, false)
	if err != nil || record == nil {
		return ""
	}
	return strings.TrimSpace(record.Metadata.CWD)
}

// closeThreadShellSnapshots removes a closing thread's snapshot files, mirroring
// Rust dropping the session's ShellSnapshotFile.
func (r *RuntimeRouter) closeThreadShellSnapshots(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.shellSnapshotsMu.Lock()
	builder := r.shellSnapshots[threadID]
	delete(r.shellSnapshots, threadID)
	delete(r.shellSnapshotV2Prewarmed, threadID)
	r.shellSnapshotsMu.Unlock()
	if builder != nil {
		builder.Close()
	}
}

// closeShellSnapshots removes every remaining snapshot at router shutdown.
func (r *RuntimeRouter) closeShellSnapshots() {
	if r == nil {
		return
	}
	r.shellSnapshotsMu.Lock()
	builders := r.shellSnapshots
	r.shellSnapshots = map[string]*tool.SnapshotBuilder{}
	r.shellSnapshotsMu.Unlock()
	for _, builder := range builders {
		builder.Close()
	}
}

// sameDirectory compares two launch directories after cleaning, so a trailing
// separator or a redundant segment does not look like another directory.
func sameDirectory(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
