package execserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// LocalShellSnapshotExecutor owns the executor-side shell snapshot cache for
// launches the tool runs in this process, mirroring the Rust local
// environment's exec backend (#39957). The cache belongs to the executor rather
// than to a turn, so a capture warmed at turn start serves every later launch
// with the same scope (#42137).
type LocalShellSnapshotExecutor struct {
	mu        sync.Mutex
	snapshots *shellSnapshotManager
	closed    bool
}

// NewLocalShellSnapshotExecutor returns an executor with its own bounded cache.
func NewLocalShellSnapshotExecutor() *LocalShellSnapshotExecutor {
	return &LocalShellSnapshotExecutor{snapshots: newShellSnapshotManager()}
}

// defaultLocalShellSnapshotExecutor is the process-wide local executor state,
// the Go counterpart of the per-environment executor Rust keeps for local
// launches.
var defaultLocalShellSnapshotExecutor = NewLocalShellSnapshotExecutor()

// LocalShellSnapshots returns the process-wide local executor's snapshot state.
func LocalShellSnapshots() *LocalShellSnapshotExecutor {
	return defaultLocalShellSnapshotExecutor
}

// LocalShellSnapshotV2Supported reports whether the local executor advertises
// the shell snapshot v2 capability (Rust's `shell_snapshot_v2_supported` for a
// local environment, environment_selection.rs:864).
func LocalShellSnapshotV2Supported() bool {
	return shellSnapshotPlatformSupported
}

func (e *LocalShellSnapshotExecutor) manager() (*shellSnapshotManager, error) {
	if e == nil {
		return nil, requestError(-32603, "local shell snapshot executor is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, requestError(-32603, "local shell snapshot executor is closed")
	}
	if e.snapshots == nil {
		e.snapshots = newShellSnapshotManager()
	}
	return e.snapshots, nil
}

// Prepare rewrites a local launch to restore the scope's executor-owned
// snapshot, applying the same process/start rewrite the exec server performs
// (execserver/shell_snapshot.go `rewrite`). The returned params carry the
// rewritten argv and the environment with any in-memory replay chunks.
func (e *LocalShellSnapshotExecutor) Prepare(ctx context.Context, params *ExecParams, captureEnv map[string]string) (*ExecParams, error) {
	if params == nil {
		return nil, requestError(-32602, "process/start params are required")
	}
	manager, err := e.manager()
	if err != nil {
		return nil, err
	}
	return manager.rewrite(ctx, params, captureEnv, nil)
}

// Prewarm captures the scope's snapshot without starting the command, mirroring
// Rust's `prewarm_shell_snapshot` (#42137). A later launch for the same scope
// reuses the capture instead of running a startup shell first.
func (e *LocalShellSnapshotExecutor) Prewarm(ctx context.Context, params *ExecParams, captureEnv map[string]string) error {
	if params == nil || params.ShellSnapshot == nil {
		return nil
	}
	manager, err := e.manager()
	if err != nil {
		return err
	}
	if !shellSnapshotPlatformSupported {
		return requestError(-32602, "shell snapshots are unsupported on this platform")
	}
	request := params.ShellSnapshot
	scope := strings.TrimSpace(request.ScopeID)
	if scope == "" || len(scope) > maxShellSnapshotScopeBytes {
		return requestError(-32602, fmt.Sprintf("shell snapshot scope must be non-empty and at most %d bytes", maxShellSnapshotScopeBytes))
	}
	shellType, ok := shellSnapshotType(request.Shell)
	if !ok {
		return requestError(-32602, fmt.Sprintf("shell snapshots are unsupported for shell `%s`", request.Shell.Name))
	}
	if shellSnapshotScriptIndex(params.Argv, request.Shell) < 0 {
		return requestError(-32602, "shell snapshot requires a shell -c or -lc invocation")
	}
	_, err = manager.snapshotEntry(ctx, params, shellType, captureEnv, nil)
	return err
}

// Close drops the executor's cache. A closed executor rejects further use,
// mirroring the executor shutting down with its environment.
func (e *LocalShellSnapshotExecutor) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	manager := e.snapshots
	e.snapshots = nil
	e.closed = true
	e.mu.Unlock()
	if manager != nil {
		manager.close()
	}
}
