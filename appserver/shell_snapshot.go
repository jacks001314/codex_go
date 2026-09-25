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
	"strings"
	"time"

	"codex_go/config"
	"codex_go/features"
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
// executor asks, or nil when the session has no snapshot. The feature gate is
// Rust's `Feature::ShellSnapshot`; the session's builder is created on first use
// and closed when the thread unloads.
func (r *RuntimeRouter) shellSnapshotProviderForTurn(threadID string, cfg *config.Config) func(context.Context, tool.SnapshotProviderRequest) string {
	if r == nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "shell_snapshot") {
		return nil
	}

	builder := r.shellSnapshotBuilderForThread(threadID)
	if builder == nil {
		return nil
	}
	threadCWD := r.shellSnapshotThreadCWD(threadID)
	// Rust starts the session's snapshot capture as soon as the turn environments
	// resolve, so the first command does not wait for it. Protected captures (an
	// active credential broker) stay lazy and sandboxed.
	if !shellSnapshotProtected(cfg) {
		r.prewarmShellSnapshot(builder, threadID, threadCWD, cfg)
	}
	return func(ctx context.Context, request tool.SnapshotProviderRequest) string {
		if !shellSnapshotLaunchEligible(request) {
			return ""
		}
		// Rust only replays the session's snapshot for the session's own
		// directory; another directory has no captured state to restore.
		if threadCWD != "" && !sameDirectory(request.CWD, threadCWD) {
			return ""
		}
		started := time.Now()
		// Every launch in the session's directory replays the one session
		// snapshot, captured with a login shell and without a sandbox (Rust's
		// start_shell_snapshot_task plus the non-broker branch of
		// TurnEnvironment::shell_snapshot, which ignores the launch's sandbox).
		snapshot, reason := builder.Snapshot(ctx, tool.SnapshotCaptureRequest{
			ShellType:         request.ShellType,
			ShellPath:         request.ShellPath,
			CWD:               request.CWD,
			AllowLoginShell:   true,
			EnvironmentPolicy: request.EnvironmentPolicy,
		})
		r.recordShellSnapshot(time.Since(started), reason)
		return snapshot.Path()
	}
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
		_, reason := builder.Snapshot(ctx, tool.SnapshotCaptureRequest{
			ShellType:         shellType,
			ShellPath:         shellPath,
			CWD:               cwd,
			AllowLoginShell:   true,
			EnvironmentPolicy: policyTable,
		})
		r.recordShellSnapshot(time.Since(started), reason)
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
func (r *RuntimeRouter) recordShellSnapshot(duration time.Duration, reason tool.SnapshotFailureReason) {
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
}

// shellSnapshotLaunchEligible mirrors the launch shape Rust's turn environment
// accepts: a local environment, one of the POSIX shells the capture supports,
// and a login command (Rust allows a bare `-c` only for brokered captures, which
// Go does not have yet).
func shellSnapshotLaunchEligible(request tool.SnapshotProviderRequest) bool {
	if request.Remote || !request.AllowLoginShell {
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
