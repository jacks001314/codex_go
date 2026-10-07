package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"codex_go/execserver"
	"codex_go/tool"
)

// remoteEnvironmentFileSystemFake is an in-memory environment filesystem whose
// contents exist nowhere on the host, so a handler that is not wired to the
// selected environment cannot satisfy it. It records reads and writes.
type remoteEnvironmentFileSystemFake struct {
	cwd     string
	files   map[string][]byte
	writes  []string
	reads   []string
	present bool
}

func newRemoteEnvironmentFileSystemFake(cwd string) *remoteEnvironmentFileSystemFake {
	return &remoteEnvironmentFileSystemFake{cwd: cwd, files: map[string][]byte{}}
}

func (f *remoteEnvironmentFileSystemFake) CWD() string { return f.cwd }

// resolve mirrors the real provider: a relative path resolves against the
// environment's working directory before the filesystem sees it.
func (f *remoteEnvironmentFileSystemFake) resolve(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(f.cwd, path)
}

func (f *remoteEnvironmentFileSystemFake) GetMetadata(_ context.Context, path string, _ *execserver.FileSystemSandboxContext) (*execserver.FSGetMetadataResponse, error) {
	if data, ok := f.files[f.resolve(path)]; ok {
		return &execserver.FSGetMetadataResponse{IsFile: true, Size: int64(len(data))}, nil
	}
	return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrNotExist}
}

func (f *remoteEnvironmentFileSystemFake) ReadFile(_ context.Context, path string, _ *execserver.FileSystemSandboxContext) ([]byte, error) {
	resolved := f.resolve(path)
	f.reads = append(f.reads, resolved)
	if data, ok := f.files[resolved]; ok {
		return data, nil
	}
	return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
}

func (f *remoteEnvironmentFileSystemFake) WriteFile(_ context.Context, path string, data []byte, _ *execserver.FileSystemSandboxContext) error {
	resolved := f.resolve(path)
	f.files[resolved] = append([]byte(nil), data...)
	f.writes = append(f.writes, resolved)
	return nil
}

func (f *remoteEnvironmentFileSystemFake) CreateDirectory(_ context.Context, _ string, _ bool, _ *execserver.FileSystemSandboxContext) error {
	return nil
}

func (f *remoteEnvironmentFileSystemFake) Remove(_ context.Context, path string, _ bool, _ bool, _ *execserver.FileSystemSandboxContext) error {
	delete(f.files, f.resolve(path))
	return nil
}

type remoteEnvironmentFileSystemFakeProvider struct {
	fileSystem *remoteEnvironmentFileSystemFake
}

func (p remoteEnvironmentFileSystemFakeProvider) FileSystemFor(environmentID string) (tool.EnvironmentFileSystem, bool) {
	if environmentID == "remote-x" {
		return p.fileSystem, true
	}
	return nil, false
}

func environmentFileSystemTestRegistryOptions(t *testing.T, fileSystem *remoteEnvironmentFileSystemFake) *ToolRegistryOptions {
	t.Helper()
	cwd := t.TempDir()
	options := DefaultToolRegistryOptions(cwd)
	options.EnableUnifiedExec = true
	options.EnableApplyPatch = true
	options.EnableRequestPermissions = true
	options.RequestPermissionsReviewer = readyRequestPermissionsReviewer
	options.SelectedEnvironmentIDs = []string{"remote-x"}
	options.EnvironmentWaiter = readyEnvironmentWaiter{}
	options.StableEnvironmentTools = true
	options.ApplyPatch.IncludeEnvironmentID = true
	options.ViewImage = &tool.ViewImageOptions{CWD: cwd}
	options.EnvironmentFileSystems = remoteEnvironmentFileSystemFakeProvider{fileSystem: fileSystem}
	return options
}

// TestBuildToolRegistryWiresApplyPatchToEnvironmentFileSystemsLikeRust proves the
// turn registry threads ToolRegistryOptions.EnvironmentFileSystems into the
// apply_patch handler, so the handler patches the selected environment instead
// of the host filesystem (Rust #20647 `78421face0`; the patch handler resolves
// the environment and executes on its `ExecutorFileSystem`,
// codex-rs/core/src/tools/handlers/apply_patch.rs:334).
func TestBuildToolRegistryWiresApplyPatchToEnvironmentFileSystemsLikeRust(t *testing.T) {
	fileSystem := newRemoteEnvironmentFileSystemFake("/remote-x-workspace")
	fileSystem.files["/remote-x-workspace/target.txt"] = []byte("before\n")
	options := environmentFileSystemTestRegistryOptions(t, fileSystem)

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.PlainName(tool.DefaultApplyPatchToolName))
	if !ok {
		t.Fatalf("apply_patch was not registered")
	}
	patch := "*** Begin Patch\n*** Environment ID: remote-x\n*** Update File: target.txt\n@@\n-before\n+after\n*** End Patch\n"
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "wired-apply",
		ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: patch},
	})
	if err != nil {
		t.Fatalf("apply_patch Execute error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("apply_patch output = %+v, want success", output)
	}
	if got := string(fileSystem.files["/remote-x-workspace/target.txt"]); got != "after\n" {
		t.Fatalf("environment file = %q, want the patched content %q", got, "after\n")
	}
	if _, err := os.Stat(filepath.Join(options.Shell.Validation.CWD, "target.txt")); !os.IsNotExist(err) {
		t.Fatalf("local target exists (err = %v), want the patch confined to the environment", err)
	}
}

// TestBuildToolRegistryWiresViewImageToEnvironmentFileSystemsLikeRust proves the
// same wiring for view_image: the registry copies
// ToolRegistryOptions.EnvironmentFileSystems onto the handler that reads the
// image (Rust #20647; codex-rs/core/src/tools/handlers/view_image.rs:145).
func TestBuildToolRegistryWiresViewImageToEnvironmentFileSystemsLikeRust(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	fileSystem := newRemoteEnvironmentFileSystemFake("/remote-x-workspace")
	fileSystem.files["/remote-x-workspace/picture.png"] = encoded.Bytes()
	options := environmentFileSystemTestRegistryOptions(t, fileSystem)

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.PlainName(tool.ViewImageToolName))
	if !ok {
		t.Fatalf("view_image was not registered")
	}
	arguments, err := json.Marshal(map[string]any{"path": "picture.png", "environment_id": "remote-x"})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "wired-view",
		ToolName: tool.PlainName(tool.ViewImageToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: string(arguments)},
	})
	if err != nil {
		t.Fatalf("view_image Execute error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("view_image output = %+v, want success", output)
	}
	if len(fileSystem.reads) == 0 || fileSystem.reads[0] != "/remote-x-workspace/picture.png" {
		t.Fatalf("environment reads = %v, want the selected environment's image", fileSystem.reads)
	}
}

// TestBuildToolRegistryWiresRequestPermissionsEnvironmentLikeRust proves the turn
// registry hands request_permissions the same environment filesystems, so the
// review path receives the resolved environment id (Rust #20647 `78421face0`,
// Rust #25858 `e29071e4c9`; the handler resolves the turn environment before
// calling `session.request_permissions_for_environment`).
func TestBuildToolRegistryWiresRequestPermissionsEnvironmentLikeRust(t *testing.T) {
	fileSystem := newRemoteEnvironmentFileSystemFake("/remote-x-workspace")
	options := environmentFileSystemTestRegistryOptions(t, fileSystem)
	var reviewedEnvironmentID string
	options.RequestPermissionsReviewer = func(_ context.Context, _, _, _, environmentID, _ string, _ map[string]any) (tool.RequestPermissionsDecision, error) {
		reviewedEnvironmentID = environmentID
		return tool.RequestPermissionsDecision{Approved: true}, nil
	}

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.PlainName(tool.RequestPermissionsToolName))
	if !ok {
		t.Fatalf("request_permissions was not registered")
	}
	arguments, err := json.Marshal(map[string]any{
		"reason":         "need the environment",
		"environment_id": "remote-x",
		"permissions":    map[string]any{"network": true},
	})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "wired-permissions",
		ToolName: tool.PlainName(tool.RequestPermissionsToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: string(arguments)},
	})
	if err != nil {
		t.Fatalf("request_permissions Execute error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("request_permissions output = %+v, want success", output)
	}
	if reviewedEnvironmentID != "remote-x" {
		t.Fatalf("reviewed environment = %q, want %q (the selected environment)", reviewedEnvironmentID, "remote-x")
	}
}
