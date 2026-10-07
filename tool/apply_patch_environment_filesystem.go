package tool

import (
	"context"
	"io/fs"
	"os"
	"time"

	"codex_go/applypatch"
	"codex_go/execserver"
)

// environmentApplyPatchFileSystem adapts a selected turn environment's
// EnvironmentFileSystem to the surface apply_patch drives
// (applypatch.FileSystem). Rust passes the selected
// `TurnEnvironment`'s `Arc<dyn ExecutorFileSystem>` straight into
// `apply_patch_with_options` / `verify_apply_patch_args`
// (codex-rs/apply-patch/src/lib.rs:329, invocation.rs:168, Rust #20647
// `78421face0`), so a patch destined for a remote executor is verified and
// applied on that executor instead of the host process filesystem. Go splits
// the context and sandbox out of the interface, so they are carried here.
type environmentApplyPatchFileSystem struct {
	ctx     context.Context
	fs      EnvironmentFileSystem
	sandbox *execserver.FileSystemSandboxContext
}

// newEnvironmentApplyPatchFileSystem returns the applypatch filesystem for a
// resolved environment, or nil when no environment filesystem was resolved
// (applypatch then uses the host process filesystem).
func newEnvironmentApplyPatchFileSystem(ctx context.Context, fileSystem EnvironmentFileSystem) applypatch.FileSystem {
	if fileSystem == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return environmentApplyPatchFileSystem{ctx: ctx, fs: fileSystem}
}

func (a environmentApplyPatchFileSystem) ReadFile(path string) ([]byte, error) {
	return a.fs.ReadFile(a.ctx, path, a.sandbox)
}

func (a environmentApplyPatchFileSystem) WriteFile(path string, data []byte, _ os.FileMode) error {
	return a.fs.WriteFile(a.ctx, path, data, a.sandbox)
}

func (a environmentApplyPatchFileSystem) MkdirAll(path string, _ os.FileMode) error {
	return a.fs.CreateDirectory(a.ctx, path, true, a.sandbox)
}

func (a environmentApplyPatchFileSystem) Remove(path string) error {
	return a.fs.Remove(a.ctx, path, false, false, a.sandbox)
}

// Stat and Lstat both report the entry's metadata; the wire protocol carries a
// single getMetadata operation (execserver FSGetMetadataParams) whose response
// already distinguishes a directory, a regular file and a symlink, which is
// exactly what apply_patch's `info.Mode().IsRegular()` checks need.
func (a environmentApplyPatchFileSystem) Stat(path string) (os.FileInfo, error) {
	return a.metadata(path)
}

func (a environmentApplyPatchFileSystem) Lstat(path string) (os.FileInfo, error) {
	return a.metadata(path)
}

func (a environmentApplyPatchFileSystem) metadata(path string) (os.FileInfo, error) {
	metadata, err := a.fs.GetMetadata(a.ctx, path, a.sandbox)
	if err != nil {
		// A remote miss arrives as the exec server's NotFound code, so map both
		// the in-process os.ErrNotExist and the wire form onto os.ErrNotExist
		// for applypatch's existence check.
		if execserver.IsFSNotExistError(err) {
			return nil, &fs.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
		}
		return nil, err
	}
	return environmentFileInfo{metadata: metadata}, nil
}

// environmentFileInfo is the os.FileInfo view of a wire getMetadata response.
// The reported permission bits are nominal (apply_patch hardcodes the modes it
// writes); only the entry type matters to the caller.
type environmentFileInfo struct {
	metadata *execserver.FSGetMetadataResponse
}

func (i environmentFileInfo) Name() string { return "" }

func (i environmentFileInfo) Size() int64 {
	if i.metadata == nil {
		return 0
	}
	return i.metadata.Size
}

func (i environmentFileInfo) Mode() os.FileMode {
	if i.metadata == nil {
		return 0
	}
	switch {
	case i.metadata.IsDirectory:
		return os.ModeDir | 0o755
	case i.metadata.IsFile:
		return 0o600
	default:
		// A symlink the metadata could not resolve through, matching os.Stat
		// reporting a non-regular entry for a dangling link.
		return os.ModeSymlink | 0o777
	}
}

func (i environmentFileInfo) ModTime() time.Time {
	if i.metadata == nil {
		return time.Time{}
	}
	return time.UnixMilli(i.metadata.ModifiedAtMS)
}

func (i environmentFileInfo) IsDir() bool {
	return i.metadata != nil && i.metadata.IsDirectory
}

func (i environmentFileInfo) Sys() any { return nil }
