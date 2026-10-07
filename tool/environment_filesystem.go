package tool

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/execserver"
)

// environmentFileSystemConnectTimeout is the exec-server connection budget for a
// filesystem call, matching the shell family's remote dial
// (tool/unified_exec.go `execRemote`) and the environment-backed OpenAI file
// client (appserver/openai_file_environment.go `environmentConnectTimeout`).
const environmentFileSystemConnectTimeout = 10 * time.Second

// EnvironmentFileSystem is the Go counterpart of the subset of Rust's
// `ExecutorFileSystem` (codex-rs/file-system/src/lib.rs:629) that the handlers
// reached through `resolve_tool_environment` need.
//
// Rust hands apply_patch / view_image / request_permissions the selected
// `TurnEnvironment`'s `environment.get_filesystem()`
// (codex-rs/exec-server/src/environment.rs:1171): the in-process filesystem for
// the implicit local environment, and an exec-server client (`RemoteFileSystem`)
// for a selected remote environment (`Environment::is_remote`, :833). Rust #20647
// (`78421face0`, "Route process tools to selected environments") is the origin
// of that routing; the handler-side use is
// codex-rs/core/src/tools/handlers/{apply_patch,view_image}.rs.
//
// Go carries the environment working directory with the filesystem, so a relative
// `path` resolves against `CWD()` inside the implementation - the client-side
// equivalent of Rust's `turn_environment.cwd().join(&path)`
// (codex-rs/core/src/tools/handlers/view_image.rs:154, which joins before the
// `fs` call because Rust's trait takes an absolute `PathUri`). Go's wire
// filesystem methods take a plain string path plus a
// `FileSystemSandboxContext` (execserver/server.go:570/644), so the join has to
// happen on the client side.
type EnvironmentFileSystem interface {
	// GetMetadata reports whether the path exists and its file/directory shape.
	GetMetadata(ctx context.Context, path string, sandbox *execserver.FileSystemSandboxContext) (*execserver.FSGetMetadataResponse, error)
	// ReadFile returns the file's bytes.
	ReadFile(ctx context.Context, path string, sandbox *execserver.FileSystemSandboxContext) ([]byte, error)
	// WriteFile replaces the file's contents (Rust `ExecutorFileSystem::write_file`,
	// codex-rs/file-system/src/lib.rs:629).
	WriteFile(ctx context.Context, path string, data []byte, sandbox *execserver.FileSystemSandboxContext) error
	// CreateDirectory creates the directory, recursively when recursive is set
	// (Rust `ExecutorFileSystem::create_directory`).
	CreateDirectory(ctx context.Context, path string, recursive bool, sandbox *execserver.FileSystemSandboxContext) error
	// Remove deletes the path (Rust `ExecutorFileSystem::remove`).
	Remove(ctx context.Context, path string, force bool, recursive bool, sandbox *execserver.FileSystemSandboxContext) error
	// CWD is the environment working directory relative paths resolve against.
	CWD() string
}

// EnvironmentFileSystemProvider resolves a turn environment id to its
// filesystem. `FileSystemFor` reports ok=false for an id the turn never resolved
// to a usable executor, which mirrors Rust's ready-only lookup
// (`TurnEnvironmentSnapshot::turn_environments().find(|environment|
// environment.selection.environment_id == id)`, see
// tool.ResolveToolEnvironment).
type EnvironmentFileSystemProvider interface {
	FileSystemFor(environmentID string) (EnvironmentFileSystem, bool)
}

// NewUnifiedExecEnvironmentFileSystems builds the provider for a turn from the
// same host-resolved usable executors the shell family dials
// (`ShellExecutorOptions.UnifiedExecEnvironments`, tool/shell.go:150). An
// environment with a resolved executor (a WebSocket/stdio/noise transport,
// `UnifiedExecEnvironment.Remote`) reads and writes through that exec server; an
// id with no resolved executor is not usable, exactly like Rust's ready-only
// `turn_environments()`. The empty id and the implicit local id resolve to the
// in-process filesystem rooted at `localCWD`.
func NewUnifiedExecEnvironmentFileSystems(environments []UnifiedExecEnvironment, localCWD string) EnvironmentFileSystemProvider {
	byID := make(map[string]UnifiedExecEnvironment, len(environments))
	for i := range environments {
		id := strings.TrimSpace(environments[i].ID)
		if id == "" || id == execserver.LocalEnvironmentID {
			continue
		}
		byID[id] = environments[i]
	}
	return &unifiedExecEnvironmentFileSystems{
		byID:  byID,
		local: localEnvironmentFileSystem{cwd: localCWD},
	}
}

type unifiedExecEnvironmentFileSystems struct {
	byID  map[string]UnifiedExecEnvironment
	local EnvironmentFileSystem
}

func (p *unifiedExecEnvironmentFileSystems) FileSystemFor(environmentID string) (EnvironmentFileSystem, bool) {
	if p == nil {
		return nil, false
	}
	id := strings.TrimSpace(environmentID)
	if id == "" || id == execserver.LocalEnvironmentID {
		if p.local == nil {
			return nil, false
		}
		return p.local, true
	}
	environment, ok := p.byID[id]
	if !ok {
		return nil, false
	}
	if !environment.Remote() {
		// A configured environment without a resolved transport still runs
		// in-process; keep its own cwd.
		return localEnvironmentFileSystem{cwd: environment.CWD}, true
	}
	return remoteEnvironmentFileSystem{environment: environment}, true
}

// localEnvironmentFileSystem backs the in-process environment. It reuses
// execserver.LocalFileSystem so the local path speaks the same wire parameter
// shapes (and honours the same sandbox context) as an executor, keeping the
// local and remote implementations interchangeable for the handlers.
type localEnvironmentFileSystem struct {
	cwd string
}

func (f localEnvironmentFileSystem) CWD() string { return f.cwd }

func (f localEnvironmentFileSystem) GetMetadata(_ context.Context, path string, sandbox *execserver.FileSystemSandboxContext) (*execserver.FSGetMetadataResponse, error) {
	return execserver.LocalFileSystem{}.GetMetadata(&execserver.FSGetMetadataParams{
		Path:    f.resolve(path),
		Sandbox: sandbox,
	})
}

func (f localEnvironmentFileSystem) ReadFile(_ context.Context, path string, sandbox *execserver.FileSystemSandboxContext) ([]byte, error) {
	response, err := execserver.LocalFileSystem{}.ReadFile(&execserver.FSReadFileParams{
		Path:    f.resolve(path),
		Sandbox: sandbox,
	})
	if err != nil {
		return nil, err
	}
	return decodeBase64File(response.DataBase64)
}

func (f localEnvironmentFileSystem) WriteFile(_ context.Context, path string, data []byte, _ *execserver.FileSystemSandboxContext) error {
	resolved := f.resolve(path)
	if dir := filepath.Dir(resolved); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(resolved, data, 0o600)
}

func (f localEnvironmentFileSystem) CreateDirectory(_ context.Context, path string, _ bool, _ *execserver.FileSystemSandboxContext) error {
	return os.MkdirAll(f.resolve(path), 0o755)
}

func (f localEnvironmentFileSystem) Remove(_ context.Context, path string, _ bool, _ bool, _ *execserver.FileSystemSandboxContext) error {
	return os.Remove(f.resolve(path))
}

func (f localEnvironmentFileSystem) resolve(path string) string {
	return resolveEnvironmentPath(f.cwd, path)
}

// remoteEnvironmentFileSystem backs a selected exec-server environment. It dials
// the same transports the shell family uses (tool/unified_exec.go:605) and
// issues the executor's filesystem methods
// (execserver/client.go FSGetMetadata/FSReadFile).
type remoteEnvironmentFileSystem struct {
	environment UnifiedExecEnvironment
	// dial overrides the transport for tests; nil dials the environment.
	dial func(ctx context.Context) (*execserver.Client, error)
}

func (f remoteEnvironmentFileSystem) CWD() string { return f.environment.CWD }

func (f remoteEnvironmentFileSystem) GetMetadata(ctx context.Context, path string, sandbox *execserver.FileSystemSandboxContext) (*execserver.FSGetMetadataResponse, error) {
	client, err := f.client(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.FSGetMetadata(ctx, &execserver.FSGetMetadataParams{
		Path:    f.resolve(path),
		Sandbox: sandbox,
	})
}

func (f remoteEnvironmentFileSystem) ReadFile(ctx context.Context, path string, sandbox *execserver.FileSystemSandboxContext) ([]byte, error) {
	client, err := f.client(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	response, err := client.FSReadFile(ctx, &execserver.FSReadFileParams{
		Path:    f.resolve(path),
		Sandbox: sandbox,
	})
	if err != nil {
		return nil, err
	}
	return decodeBase64File(response.DataBase64)
}

func (f remoteEnvironmentFileSystem) WriteFile(ctx context.Context, path string, data []byte, sandbox *execserver.FileSystemSandboxContext) error {
	client, err := f.client(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = client.FSWriteFile(ctx, &execserver.FSWriteFileParams{
		Path:       f.resolve(path),
		DataBase64: base64.StdEncoding.EncodeToString(data),
		Sandbox:    sandbox,
	})
	return err
}

func (f remoteEnvironmentFileSystem) CreateDirectory(ctx context.Context, path string, recursive bool, sandbox *execserver.FileSystemSandboxContext) error {
	client, err := f.client(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = client.FSCreateDirectory(ctx, &execserver.FSCreateDirectoryParams{
		Path:      f.resolve(path),
		Recursive: &recursive,
		Sandbox:   sandbox,
	})
	return err
}

func (f remoteEnvironmentFileSystem) Remove(ctx context.Context, path string, force bool, recursive bool, sandbox *execserver.FileSystemSandboxContext) error {
	client, err := f.client(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = client.FSRemove(ctx, &execserver.FSRemoveParams{
		Path:      f.resolve(path),
		Force:     &force,
		Recursive: &recursive,
		Sandbox:   sandbox,
	})
	return err
}

func (f remoteEnvironmentFileSystem) resolve(path string) string {
	return resolveEnvironmentPath(f.CWD(), path)
}

func (f remoteEnvironmentFileSystem) client(ctx context.Context) (*execserver.Client, error) {
	if f.dial != nil {
		return f.dial(ctx)
	}
	return dialEnvironmentFileSystemClient(ctx, f.environment)
}

func dialEnvironmentFileSystemClient(ctx context.Context, environment UnifiedExecEnvironment) (*execserver.Client, error) {
	// Rust #48575: an initial connection to a provisioned environment waits out
	// a resuming executor for a fixed five-minute window, so the connect budget
	// and the dial option both follow the environment's provisioned flag.
	connectCtx, cancel := context.WithTimeout(ctx, environment.ConnectBudget(environmentFileSystemConnectTimeout))
	defer cancel()
	options := execserver.DialClientOptions{
		ClientName:   "codex-go-environment-fs",
		HTTPHeaders:  environment.ExecServerHTTPHeaders.Clone(),
		StdioCommand: environment.ExecServerStdioCommand,
		Provisioned:  environment.Provisioned,
	}
	if environment.NoiseProvider != nil {
		return execserver.DialNoiseRendezvousClient(connectCtx, environment.NoiseProvider, options)
	}
	return execserver.DialClientWithOptions(connectCtx, environment.ExecServerURL, options)
}

// decodeBase64File decodes the executor's base64 file payload
// (execserver.FSReadFileResponse.DataBase64).
func decodeBase64File(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// resolveEnvironmentPath joins a model-visible path against an environment
// working directory. An absolute path, an empty path or an unknown cwd is left
// untouched (the executor resolves a path it receives against its own cwd).
func resolveEnvironmentPath(cwd, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}
