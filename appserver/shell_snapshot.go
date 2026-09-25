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
	"path/filepath"
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/session"
	"codex_go/tool"
)

// shellSnapshotCaptureRunner runs a session's snapshot captures. It is a
// variable so a test can stand in for a real POSIX shell and its sandbox.
var shellSnapshotCaptureRunner tool.SnapshotCaptureRunner

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
	return func(ctx context.Context, request tool.SnapshotProviderRequest) string {
		if !shellSnapshotLaunchEligible(request) {
			return ""
		}
		// Rust only replays the session's snapshot for the session's own
		// directory; another directory has no captured state to restore.
		if threadCWD != "" && !sameDirectory(request.CWD, threadCWD) {
			return ""
		}
		snapshot := builder.Snapshot(ctx, tool.SnapshotCaptureRequest{
			ShellType:           request.ShellType,
			ShellPath:           request.ShellPath,
			CWD:                 request.CWD,
			AllowLoginShell:     request.AllowLoginShell,
			PermissionProfile:   request.PermissionProfile,
			PermissionProfileID: request.PermissionProfileID,
		})
		return snapshot.Path()
	}
}

// shellSnapshotLaunchEligible mirrors the launch shape Rust's turn environment
// accepts: a local environment, one of the POSIX shells the capture supports,
// and a login command (Rust allows a bare `-c` only for brokered captures, which
// Go does not have yet).
func shellSnapshotLaunchEligible(request tool.SnapshotProviderRequest) bool {
	if request.Remote || !request.AllowLoginShell {
		return false
	}
	switch request.ShellType {
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
