package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
)

// TestApplyPatchRoutesToSelectedRemoteEnvironmentLikeRust covers Rust #20647
// (`78421face0`, "Route process tools to selected environments"): the
// apply_patch handler resolves the turn environment and then verifies and
// applies the patch through that environment's `ExecutorFileSystem`
// (`codex-rs/core/src/tools/handlers/apply_patch.rs:334` calls
// `verify_apply_patch_args(args, cwd, fs, sandbox)` ->
// `codex-rs/apply-patch/src/invocation.rs:168`; the committed write goes through
// `apply_patch_with_options(..., fs, ...)` ->
// `codex-rs/apply-patch/src/lib.rs:329`). The Rust coverage this mirrors is
// `apply_patch_freeform_routes_to_selected_remote_environment`
// (codex-rs/core/tests/suite/remote_env.rs:4198).
//
// The patch's target exists only inside the remote environment's directory: the
// local directory never contains it. Both the preflight read (which must not
// stat/read the host filesystem) and the committed write therefore have to run
// over the exec-server wire. A regression that reads the target locally during
// verification fails here as "failed to find expected lines" because the local
// file is missing.
func TestApplyPatchRoutesToSelectedRemoteEnvironmentLikeRust(t *testing.T) {
	serverURL, stop := startEnvironmentFileSystemExecServer(t)
	defer stop()

	remoteDir := t.TempDir()
	localDir := t.TempDir()
	const targetName = "remote-only.txt"
	if err := os.WriteFile(filepath.Join(remoteDir, targetName), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write remote file: %v", err)
	}
	// The local environment must not have the target, so a local read cannot
	// satisfy verification.
	if _, err := os.Stat(filepath.Join(localDir, targetName)); !os.IsNotExist(err) {
		t.Fatalf("local target unexpectedly exists (err = %v)", err)
	}

	provider := NewUnifiedExecEnvironmentFileSystems([]UnifiedExecEnvironment{{
		ID:            "remote-x",
		CWD:           remoteDir,
		ExecServerURL: serverURL,
	}}, localDir)

	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:                  localDir,
		IncludeEnvironmentID: true,
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: provider,
	})

	patch := "*** Begin Patch\n" +
		"*** Environment ID: remote-x\n" +
		"*** Update File: " + targetName + "\n" +
		"@@\n" +
		"-before\n" +
		"+after\n" +
		"*** Add File: created-remotely.txt\n" +
		"+created\n" +
		"*** End Patch\n"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := executor.Execute(ctx, &Invocation{
		CallID:   "remote-apply",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("Execute output = %+v, want success", output)
	}

	// Verify then apply both ran on the executor: the remote file changed and the
	// new file exists remotely.
	updated, err := os.ReadFile(filepath.Join(remoteDir, targetName))
	if err != nil {
		t.Fatalf("read remote target: %v", err)
	}
	if string(updated) != "after\n" {
		t.Fatalf("remote target = %q, want %q", updated, "after\n")
	}
	if created, err := os.ReadFile(filepath.Join(remoteDir, "created-remotely.txt")); err != nil || string(created) != "created\n" {
		t.Fatalf("remote created file = %q, %v, want %q", created, err, "created\n")
	}

	// The write was recorded on the environment filesystem, not the local one.
	if _, err := os.Stat(filepath.Join(localDir, targetName)); !os.IsNotExist(err) {
		t.Fatalf("local target was created (err = %v), want it untouched", err)
	}
	if _, err := os.Stat(filepath.Join(localDir, "created-remotely.txt")); !os.IsNotExist(err) {
		t.Fatalf("local created file is present (err = %v), want it untouched", err)
	}
}

// TestApplyPatchLocalEnvironmentUnchangedLikeRust guards the default path: with
// no environment filesystem provider the patch keeps running against the host
// process filesystem (the pre-#20647 behavior), so existing callers are
// unaffected by the injected filesystem.
func TestApplyPatchLocalEnvironmentUnchangedLikeRust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})
	patch := "*** Begin Patch\n*** Update File: local.txt\n@@\n-before\n+after\n*** End Patch\n"
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "local-apply",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("Execute output = %+v, want success", output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "local.txt"))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("local file = %q, %v, want %q", data, err, "after\n")
	}
	if !strings.Contains(output.Body, "local.txt") {
		t.Fatalf("summary = %q, want it to mention local.txt", output.Body)
	}
}

// fakeEnvironmentFileSystem is a remote environment filesystem whose contents
// exist only in memory, so a call that silently falls back to the host process
// filesystem cannot satisfy it. It records every mutation so the test can assert
// the write landed on the environment.
type fakeEnvironmentFileSystem struct {
	cwd     string
	files   map[string][]byte
	dirs    map[string]bool
	writes  []string
	removes []string
}

func newFakeEnvironmentFileSystem(cwd string) *fakeEnvironmentFileSystem {
	return &fakeEnvironmentFileSystem{
		cwd:   cwd,
		files: map[string][]byte{"/remote-x-workspace/remote-only.txt": []byte("before\n")},
		dirs:  map[string]bool{"/remote-x-workspace": true},
	}
}

func (f *fakeEnvironmentFileSystem) CWD() string { return f.cwd }

func (f *fakeEnvironmentFileSystem) GetMetadata(_ context.Context, path string, _ *execserver.FileSystemSandboxContext) (*execserver.FSGetMetadataResponse, error) {
	if data, ok := f.files[path]; ok {
		return &execserver.FSGetMetadataResponse{IsFile: true, Size: int64(len(data))}, nil
	}
	if f.dirs[path] {
		return &execserver.FSGetMetadataResponse{IsDirectory: true}, nil
	}
	return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrNotExist}
}

func (f *fakeEnvironmentFileSystem) ReadFile(_ context.Context, path string, _ *execserver.FileSystemSandboxContext) ([]byte, error) {
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
}

func (f *fakeEnvironmentFileSystem) WriteFile(_ context.Context, path string, data []byte, _ *execserver.FileSystemSandboxContext) error {
	f.files[path] = append([]byte(nil), data...)
	f.writes = append(f.writes, path)
	return nil
}

func (f *fakeEnvironmentFileSystem) CreateDirectory(_ context.Context, path string, _ bool, _ *execserver.FileSystemSandboxContext) error {
	f.dirs[path] = true
	return nil
}

func (f *fakeEnvironmentFileSystem) Remove(_ context.Context, path string, _ bool, _ bool, _ *execserver.FileSystemSandboxContext) error {
	delete(f.files, path)
	f.removes = append(f.removes, path)
	return nil
}

type fakeEnvironmentFileSystemProvider struct {
	fileSystem EnvironmentFileSystem
}

func (p fakeEnvironmentFileSystemProvider) FileSystemFor(environmentID string) (EnvironmentFileSystem, bool) {
	if environmentID == "remote-x" {
		return p.fileSystem, true
	}
	return nil, false
}

// TestApplyPatchPreflightReadsSelectedEnvironmentFileSystemLikeRust is the
// decisive form of the routing test: the selected environment's filesystem holds
// the patch target and nothing on the host process filesystem does. Rust's
// verification goes through `fs` (`verify_apply_patch_args(args, cwd, fs,
// sandbox)`, codex-rs/apply-patch/src/invocation.rs:168), so a preflight that
// stats or reads the host filesystem cannot verify this patch at all. The test
// asserts verify + apply both succeed and that the mutation was recorded on the
// environment (the local disk never sees the file).
func TestApplyPatchPreflightReadsSelectedEnvironmentFileSystemLikeRust(t *testing.T) {
	remote := newFakeEnvironmentFileSystem("/remote-x-workspace")
	localDir := t.TempDir()

	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:                  localDir,
		IncludeEnvironmentID: true,
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: fakeEnvironmentFileSystemProvider{fileSystem: remote},
	})

	patch := "*** Begin Patch\n" +
		"*** Environment ID: remote-x\n" +
		"*** Update File: remote-only.txt\n" +
		"@@\n" +
		"-before\n" +
		"+after\n" +
		"*** Add File: created-remotely.txt\n" +
		"+created\n" +
		"*** End Patch\n"

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "remote-fake-apply",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("Execute output = %+v, want success", output)
	}
	if got := string(remote.files["/remote-x-workspace/remote-only.txt"]); got != "after\n" {
		t.Fatalf("environment target = %q, want %q", got, "after\n")
	}
	if got := string(remote.files["/remote-x-workspace/created-remotely.txt"]); got != "created\n" {
		t.Fatalf("environment created file = %q, want %q", got, "created\n")
	}
	if len(remote.writes) != 2 {
		t.Fatalf("environment writes = %v, want two writes", remote.writes)
	}
	// Nothing leaked onto the host process filesystem.
	for _, name := range []string{"remote-only.txt", "created-remotely.txt"} {
		if _, err := os.Stat(filepath.Join(localDir, name)); !os.IsNotExist(err) {
			t.Fatalf("local %s exists (err = %v), want the patch confined to the environment", name, err)
		}
	}
}
