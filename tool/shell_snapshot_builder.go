package tool

// The session's shell-snapshot producer.
//
// Rust parity: codex-rs/core/src/shell_snapshot.rs. A session captures the
// user's shell state once, keeps the snapshot file for as long as the session
// lives, and removes it when the session ends; the file is written to a
// temporary path and only renamed into place after sourcing it succeeded, so a
// half-written snapshot can never be replayed.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex_go/envutil"
	"codex_go/sandbox"
	"codex_go/shell"
)

// DefaultSnapshotTimeout bounds a capture or validation run, mirroring Rust's
// SNAPSHOT_TIMEOUT.
const DefaultSnapshotTimeout = 10 * time.Second

// SnapshotCaptureRunner runs one capture command and returns its standard
// output. The permission profile is the one the snapshot is captured under, so
// the captured state reflects what the launch itself can read.
type SnapshotCaptureRunner func(
	ctx context.Context,
	command []string,
	cwd string,
	env map[string]string,
	profile *sandbox.PermissionProfile,
	profileID string,
) ([]byte, error)

// SnapshotBuilderOptions configures one session's snapshot producer.
type SnapshotBuilderOptions struct {
	CodexHome string
	SessionID string
	// Runner runs the capture and validation commands. It defaults to
	// RunSandboxedSnapshotCommand.
	Runner SnapshotCaptureRunner
	// Timeout bounds each capture run; DefaultSnapshotTimeout when zero.
	Timeout time.Duration
}

// SnapshotCaptureRequest describes the launch whose shell state is needed.
type SnapshotCaptureRequest struct {
	ShellType           ShellType
	ShellPath           string
	CWD                 string
	AllowLoginShell     bool
	PermissionProfile   *sandbox.PermissionProfile
	PermissionProfileID string
}

// SnapshotProviderRequest describes a launch asking for its session's snapshot.
// The provider decides whether this launch is one Rust would replay a snapshot
// for (feature enabled, the session's own shell and directory, a local
// environment, Direct shell mode), and returns "" when it is not.
type SnapshotProviderRequest struct {
	ShellType           ShellType
	ShellPath           string
	CWD                 string
	AllowLoginShell     bool
	PermissionProfile   *sandbox.PermissionProfile
	PermissionProfileID string
	EnvironmentID       string
	Remote              bool
}

// snapshotExplicitOverrides returns the policy-driven environment overrides that
// must win after a snapshot is sourced (Rust passes `explicit_env_overrides`).
func snapshotExplicitOverrides(req *ShellRequest) map[string]string {
	if req == nil || req.EnvPolicy == nil {
		return nil
	}
	return req.EnvPolicy.Set
}

// SnapshotBuilder captures snapshots for one session and keeps them alive until
// Close. It is safe for concurrent use: a launch that needs a missing snapshot
// captures it, and other launches wait for that capture instead of starting a
// second one.
type SnapshotBuilder struct {
	mu        sync.Mutex
	options   SnapshotBuilderOptions
	snapshots map[string]*ShellSnapshotFile
	cleaned   bool
}

// ShellSnapshotFile is a captured snapshot on disk. Closing it removes the file,
// mirroring Rust's `Drop for ShellSnapshotFile`.
type ShellSnapshotFile struct {
	path string
}

// Path returns the snapshot's location, or "" for no snapshot.
func (f *ShellSnapshotFile) Path() string {
	if f == nil {
		return ""
	}
	return f.path
}

// Close removes the snapshot file.
func (f *ShellSnapshotFile) Close() error {
	if f == nil || strings.TrimSpace(f.path) == "" {
		return nil
	}
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// NewSnapshotBuilder returns a builder for one session, or nil when the session
// cannot hold a snapshot (no codex home).
func NewSnapshotBuilder(options SnapshotBuilderOptions) *SnapshotBuilder {
	if strings.TrimSpace(options.CodexHome) == "" || strings.TrimSpace(options.SessionID) == "" {
		return nil
	}
	if options.Runner == nil {
		options.Runner = RunSandboxedSnapshotCommand
	}
	if options.Timeout <= 0 {
		options.Timeout = DefaultSnapshotTimeout
	}
	return &SnapshotBuilder{options: options, snapshots: map[string]*ShellSnapshotFile{}}
}

// Snapshot returns the session's snapshot for the request, capturing it on first
// use. It returns nil when the shell has no snapshot support or the capture
// failed; callers then run the command without a snapshot, exactly like Rust's
// fail-open capture.
func (b *SnapshotBuilder) Snapshot(ctx context.Context, request SnapshotCaptureRequest) *ShellSnapshotFile {
	if b == nil {
		return nil
	}
	key := snapshotCaptureKey(request)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupStaleSnapshotsLocked()
	if existing := b.snapshots[key]; existing != nil {
		if _, err := os.Stat(existing.path); err == nil {
			return existing
		}
		delete(b.snapshots, key)
	}
	created := b.captureLocked(ctx, request)
	if created == nil {
		return nil
	}
	b.snapshots[key] = created
	return created
}

// Close removes every snapshot this session captured.
func (b *SnapshotBuilder) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	snapshots := b.snapshots
	b.snapshots = map[string]*ShellSnapshotFile{}
	b.mu.Unlock()
	for _, snapshot := range snapshots {
		_ = snapshot.Close()
	}
}

// cleanupStaleSnapshotsLocked removes this session's leaked snapshots once. Rust
// additionally drops snapshots whose rollout is gone or stale; that pass needs
// the rollout store, so Go keeps the age-based helper it already had.
func (b *SnapshotBuilder) cleanupStaleSnapshotsLocked() {
	if b.cleaned {
		return
	}
	b.cleaned = true
	_, _ = shell.CleanupStaleSnapshots(b.options.CodexHome, b.options.SessionID, time.Now())
}

func (b *SnapshotBuilder) captureLocked(ctx context.Context, request SnapshotCaptureRequest) *ShellSnapshotFile {
	startup := shell.SnapshotStartupInteractive
	if !request.AllowLoginShell {
		startup = shell.SnapshotStartupNonInteractive
	}
	captureType := snapshotShellType(request)
	script, ok := shell.SnapshotCaptureScript(captureType, shell.SnapshotCaptureOptions{
		Startup:      startup,
		Declarations: true,
	})
	if !ok {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, b.options.Timeout)
	defer cancel()
	env := snapshotCaptureEnv()
	captured, err := b.options.Runner(runCtx, snapshotExecArgs(request, script), request.CWD, env, request.PermissionProfile, request.PermissionProfileID)
	if err != nil {
		return nil
	}
	decoded := shell.ParseCapturedSnapshot(captureType, captured)
	if decoded == nil {
		return nil
	}
	return b.writeSnapshot(runCtx, request, env, decoded)
}

// writeSnapshot writes the rendered replay text beside its temporary file,
// sources it to prove it is usable, and only then renames it into place
// (Rust's write_shell_snapshot plus validate_snapshot).
func (b *SnapshotBuilder) writeSnapshot(
	ctx context.Context,
	request SnapshotCaptureRequest,
	env map[string]string,
	decoded *shell.CapturedSnapshot,
) *ShellSnapshotFile {
	path, tempPath := shell.SnapshotPath(
		b.options.CodexHome,
		b.options.SessionID,
		snapshotShellType(request),
		time.Now().UnixNano(),
	)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil
	}
	if err := os.WriteFile(tempPath, []byte(decoded.RenderScript()), 0o600); err != nil {
		return nil
	}
	validation := "set -e; . \"" + tempPath + "\""
	if _, err := b.options.Runner(ctx, snapshotExecArgs(request, validation), request.CWD, env, request.PermissionProfile, request.PermissionProfileID); err != nil {
		_ = os.Remove(tempPath)
		return nil
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return nil
	}
	return &ShellSnapshotFile{path: path}
}

// snapshotShellType resolves the capture dialect for a launch. Go's tool-level
// type folds `sh` into bash, while a POSIX sh needs its own capture script
// (Rust's ShellType::Sh, which already adapts when the sh is bash-backed), so
// the shell's own name decides.
func snapshotShellType(request SnapshotCaptureRequest) shell.ShellType {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(request.ShellPath)))
	base = strings.TrimSuffix(base, ".exe")
	switch {
	case base == "sh", base == "dash", base == "ash", strings.HasPrefix(base, "dash-"), strings.HasPrefix(base, "ash-"):
		return shell.ShellSh
	default:
		return shell.ShellType(request.ShellType)
	}
}

// snapshotExecArgs mirrors Rust's Shell::derive_exec_args for snapshot work: a
// login run asks the shell for its login startup, a non-login run does not.
func snapshotExecArgs(request SnapshotCaptureRequest, script string) []string {
	flag := "-c"
	if request.AllowLoginShell {
		flag = "-lc"
	}
	return []string{request.ShellPath, flag, script}
}

// snapshotCaptureKey identifies a captured snapshot, mirroring Rust's
// ShellSnapshotCacheKey: the shell, its startup mode, the launch directory and
// the policy the capture ran under.
func snapshotCaptureKey(request SnapshotCaptureRequest) string {
	policy := request.PermissionProfileID
	if request.PermissionProfile != nil {
		if encoded, err := sandbox.RuntimePermissionProfileJSON(*request.PermissionProfile); err == nil {
			policy = request.PermissionProfileID + "\x00" + encoded
		}
	}
	return strings.Join([]string{
		string(request.ShellType),
		request.ShellPath,
		request.CWD,
		boolKey(request.AllowLoginShell),
		policy,
	}, "\x01")
}

func boolKey(value bool) string {
	if value {
		return "login"
	}
	return "non-login"
}

// snapshotCaptureEnv is the environment a capture starts from: the parent
// process environment without the launch-context variables Rust scrubs.
func snapshotCaptureEnv() map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	return envutil.ScrubMap(env)
}

// RunSandboxedSnapshotCommand is the default capture runner: it applies the
// launch's permission profile to the capture command, so a shell that may not
// read its own configuration cannot capture it either (Rust's
// ShellSnapshotSandbox::run).
func RunSandboxedSnapshotCommand(
	ctx context.Context,
	command []string,
	cwd string,
	env map[string]string,
	profile *sandbox.PermissionProfile,
	profileID string,
) ([]byte, error) {
	if len(command) == 0 {
		return nil, errors.New("snapshot command is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	plan, planErr := sandbox.BuildCommandRunPlan(&sandbox.CommandRunRequest{
		ResolvedPermissionProfile:   profile,
		ResolvedPermissionProfileID: profileID,
		CWD:                         cwd,
		Command:                     append([]string(nil), command...),
	})
	if planErr != nil {
		return nil, planErr
	}
	if err := plan.UnsupportedError(); err != nil {
		return nil, err
	}
	runCommand := plan.Command
	if len(runCommand) == 0 {
		runCommand = command
	}
	process := exec.CommandContext(ctx, runCommand[0], runCommand[1:]...)
	process.Dir = plan.CWD
	if process.Dir == "" {
		process.Dir = cwd
	}
	if plan.PermissionProfileID != "" {
		env = cloneEnvMap(env)
		env["CODEX_PERMISSION_PROFILE"] = plan.PermissionProfileID
	}
	process.Env = envSlice(env)
	var stdout, stderr strings.Builder
	process.Stdout = &stdout
	process.Stderr = &stderr
	if err := process.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, err
		}
		return nil, errors.New("snapshot command failed: " + strings.TrimSpace(stderr.String()))
	}
	return []byte(stdout.String()), nil
}

func cloneEnvMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
