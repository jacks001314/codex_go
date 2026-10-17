package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"

	"codex_go/execserver"
	"codex_go/utils"
)

// MultiEnvCopyFileToolName is Rust's `TOOL_NAME` in the multi-environment
// file-copy extension (codex-rs/ext/multi-env-copy-file/src/tool.rs, upstream
// #54674 `e7de1f4440`). The host installs the extension only when the
// `multi_env_copy_file` feature is enabled (upstream #54741 `6d8b4c7479`).
const MultiEnvCopyFileToolName = "multi_env_copy_file"

// DefaultMultiEnvCopyFileDescription is Rust's `TOOL_DESCRIPTION`.
const DefaultMultiEnvCopyFileDescription = "Copy a file between two different environments without loading it into context. Returns bytes copied after completion. Interrupted copies may leave files partially written."

// multiEnvCopyFileArgs mirrors Rust's `CopyFileArgs`, a
// `#[serde(deny_unknown_fields)]` struct whose four fields are required and not
// `Option`: pointers are used so a missing field is rejected the way serde
// rejects it (the message describes the same rejection, not serde's exact text).
type multiEnvCopyFileArgs struct {
	SourceEnvironmentID *string `json:"source_environment_id"`
	SourcePath          *string `json:"source_path"`
	TargetEnvironmentID *string `json:"target_environment_id"`
	TargetPath          *string `json:"target_path"`
}

// MultiEnvCopyFileOptions carries the turn's environment filesystems, the same
// host-resolved surface apply_patch / view_image / request_permissions use
// (Rust `environment.fs()`; codex-rs/core/src/environment_selection.rs).
type MultiEnvCopyFileOptions struct {
	EnvironmentFileSystems EnvironmentFileSystemProvider
}

// MultiEnvCopyFileHandler copies one file between two selected environments
// without loading its bytes into model context
// (codex-rs/ext/multi-env-copy-file/src/tool.rs `CopyFileTool`).
type MultiEnvCopyFileHandler struct {
	options MultiEnvCopyFileOptions
}

func NewMultiEnvCopyFileHandler(options MultiEnvCopyFileOptions) *MultiEnvCopyFileHandler {
	return &MultiEnvCopyFileHandler{options: options}
}

func (h *MultiEnvCopyFileHandler) Spec() Spec {
	properties := map[string]any{
		"source_environment_id": map[string]any{"type": "string", "description": "Source environment ID from <environment_context>."},
		"source_path":           map[string]any{"type": "string", "description": "Absolute file path in the source environment. Parent directories must exist."},
		"target_environment_id": map[string]any{"type": "string", "description": "Target environment ID from <environment_context>."},
		"target_path":           map[string]any{"type": "string", "description": "Absolute file path in the target environment. Parent directories must exist."},
	}
	return Spec{
		Name:        PlainName(MultiEnvCopyFileToolName),
		Description: DefaultMultiEnvCopyFileDescription,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             []string{"source_environment_id", "source_path", "target_environment_id", "target_path"},
			"additionalProperties": false,
		},
	}
}

func (h *MultiEnvCopyFileHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if invocation.Payload.Kind != PayloadFunction {
		return nil, RespondToModel("multi_env_copy_file handler received unsupported payload")
	}
	var args multiEnvCopyFileArgs
	if err := invocation.DecodeStrictArguments(&args); err != nil {
		return nil, RespondToModel(err.Error())
	}
	if err := requireMultiEnvCopyFileArgs(&args); err != nil {
		return nil, RespondToModel(err.Error())
	}
	// Rust leaves copies within one environment to tools like `cp`, which already
	// handle different paths to the same file, including through hard links and
	// symlinks.
	if *args.SourceEnvironmentID == *args.TargetEnvironmentID {
		return nil, RespondToModel("source and target environment IDs must differ. To copy within one environment, use a shell command.")
	}
	var provider EnvironmentFileSystemProvider
	if h != nil {
		provider = h.options.EnvironmentFileSystems
	}
	source, sourcePath, err := resolveMultiEnvCopyFileEndpoint(provider, *args.SourceEnvironmentID, *args.SourcePath)
	if err != nil {
		return nil, RespondToModel(err.Error())
	}
	target, targetPath, err := resolveMultiEnvCopyFileEndpoint(provider, *args.TargetEnvironmentID, *args.TargetPath)
	if err != nil {
		return nil, RespondToModel(err.Error())
	}
	// The target writer pulls one block at a time when it supports streaming
	// writes; an older executor buffers this stream and sends a single write
	// (Rust `ExecutorFileSystem::read_file_stream` / `write_file_stream`).
	contents, release, err := source.ReadFileStream(ctx, sourcePath, nil)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("could not open copy source: %s", fileSystemErrorKind(err)))
	}
	if release != nil {
		defer func() { _ = release() }()
	}
	written, err := target.WriteFileStream(ctx, targetPath, execserver.FileWriteChunkSource(contents), nil)
	if err != nil {
		// Report only the I/O error kind: an executor connection error can carry
		// credentials from a signed websocket URL in its message.
		return nil, RespondToModel(fmt.Sprintf("copy failed; target may be truncated or partially written: %s", fileSystemErrorKind(err)))
	}
	body, err := json.Marshal(map[string]any{"bytes_copied": written})
	if err != nil {
		return nil, err
	}
	return &Output{Success: true, Body: string(body)}, nil
}

var _ Executor = (*MultiEnvCopyFileHandler)(nil)

// requireMultiEnvCopyFileArgs rejects a missing required argument the way serde
// rejects Rust's all-required `CopyFileArgs`.
func requireMultiEnvCopyFileArgs(args *multiEnvCopyFileArgs) error {
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"source_environment_id", args.SourceEnvironmentID},
		{"source_path", args.SourcePath},
		{"target_environment_id", args.TargetEnvironmentID},
		{"target_path", args.TargetPath},
	} {
		if field.value == nil {
			return fmt.Errorf("missing field `%s`", field.name)
		}
	}
	return nil
}

// resolveMultiEnvCopyFileEndpoint is Rust's `resolve_endpoint` closure: the id
// must resolve to an environment this turn can reach, the path must be absolute
// in that environment's own path convention, and the resolved path is the
// converted path rendered back into the environment's native spelling.
func resolveMultiEnvCopyFileEndpoint(provider EnvironmentFileSystemProvider, environmentID string, path string) (EnvironmentFileSystem, string, error) {
	var fileSystem EnvironmentFileSystem
	if provider != nil {
		if candidate, ok := provider.FileSystemFor(environmentID); ok && candidate != nil {
			fileSystem = candidate
		}
	}
	if fileSystem == nil {
		return nil, "", fmt.Errorf("unknown or unavailable environment ID: %s", environmentID)
	}
	cwd := utils.NewLegacyAppPathString(strings.TrimSpace(fileSystem.CWD()))
	convention, ok := cwd.InferAbsolutePathConvention()
	if !ok {
		return nil, "", fmt.Errorf("invalid working directory path for environment %s", environmentID)
	}
	requested := utils.NewLegacyAppPathString(path)
	uri, err := requested.ToPathURI(convention)
	if err != nil {
		return nil, "", multiEnvCopyFileAbsolutePathError(convention, environmentID)
	}
	rendered, err := utils.LegacyAppPathStringFromURI(uri, convention)
	if err != nil {
		return nil, "", multiEnvCopyFileAbsolutePathError(convention, environmentID)
	}
	return fileSystem, rendered.Value, nil
}

func multiEnvCopyFileAbsolutePathError(convention utils.PathConvention, environmentID string) error {
	name := "POSIX"
	if convention == utils.ConventionWindows {
		name = "Windows"
	}
	return fmt.Errorf("provide an absolute %s file path for environment %s", name, environmentID)
}

// fileSystemErrorKind renders an error the way Rust's `io::ErrorKind` Display
// does. The copy tool reports only `error.kind()`, never the error message,
// because an exec-server connection error can carry credentials from a signed
// websocket URL. The names below are Rust's; an unrecognised error reports
// "other" exactly like `io::ErrorKind::Other`.
func fileSystemErrorKind(err error) string {
	if err == nil {
		return "other"
	}
	// A connection the executor lost mid-call surfaces as Rust's `broken pipe`.
	if execserver.IsRetryableRecoveryError(err) {
		return "broken pipe"
	}
	// An exec-server failure carries its Rust error kind as a wire code, because
	// the message is the executor's OS error text.
	if code, ok := execserver.FSErrorCode(err); ok {
		switch code {
		case execserver.FSNotExistErrorCode:
			return "entity not found"
		case execserver.FSPermissionErrorCode:
			return "permission denied"
		}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "entity not found"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrExist):
		return "entity already exists"
	case errors.Is(err, fs.ErrInvalid):
		return "invalid input parameter"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "operation interrupted"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno.Error() {
		case "is a directory":
			return "is a directory"
		case "not a directory":
			return "not a directory"
		case "directory not empty":
			return "directory not empty"
		case "no space left on device":
			return "no storage space"
		case "read-only file system":
			return "read-only filesystem"
		case "broken pipe":
			return "broken pipe"
		case "connection reset by peer":
			return "connection reset"
		case "invalid argument":
			return "invalid input parameter"
		}
	}
	return "other"
}
