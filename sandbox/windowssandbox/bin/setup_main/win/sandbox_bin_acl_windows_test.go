//go:build windows

package win

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// TestLockSandboxBinDirBlocksInheritedWriteForRunnerFilesLikeRust pins Rust
// #42330: .sandbox-bin usually lives inside the workspace, so without a
// protected DACL the workspace write capability would inherit WRITE/DELETE on
// the runner binaries and the sandbox could replace its own launcher.
func TestLockSandboxBinDirBlocksInheritedWriteForRunnerFilesLikeRust(t *testing.T) {
	for _, mode := range []setupMode{setupModeFull, setupModeProvision} {
		t.Run(string(mode), func(t *testing.T) {
			workspace := t.TempDir()
			sandboxBin := windowssandbox.SandboxBinDir(workspace)
			workspaceSID, err := windowssandbox.WorkspaceWriteCapabilitySIDForRoot(workspace, workspace)
			if err != nil {
				t.Fatalf("WorkspaceWriteCapabilitySIDForRoot() error = %v", err)
			}
			if err := windowssandbox.EnsureAllowWriteACEs(windowssandbox.ACLRequest{Path: workspace, SID: workspaceSID}); err != nil {
				t.Fatalf("EnsureAllowWriteACEs() error = %v", err)
			}
			if err := os.MkdirAll(sandboxBin, 0o700); err != nil {
				t.Fatalf("MkdirAll(%s) error = %v", sandboxBin, err)
			}
			existingRunner := filepath.Join(sandboxBin, "existing-runner.exe")
			if err := os.WriteFile(existingRunner, []byte("existing"), 0o700); err != nil {
				t.Fatalf("WriteFile(existing runner) error = %v", err)
			}
			assertInheritedWrite(t, existingRunner, workspaceSID, true)

			sandboxGroupSID, err := ResolveSID("Authenticated Users")
			if err != nil {
				t.Fatalf("ResolveSID(Authenticated Users) error = %v", err)
			}
			realUser := os.Getenv("USERNAME")
			if realUser == "" {
				realUser = "Administrators"
			}
			payload := &setupPayload{CodexHome: workspace, RealUser: realUser, Mode: mode}
			if err := lockSandboxBinDir(payload, sandboxGroupSID, io.Discard); err != nil {
				t.Fatalf("lockSandboxBinDir() error = %v", err)
			}
			newRunner := filepath.Join(sandboxBin, "new-runner.exe")
			if err := os.WriteFile(newRunner, []byte("new"), 0o700); err != nil {
				t.Fatalf("WriteFile(new runner) error = %v", err)
			}
			for _, path := range []string{sandboxBin, existingRunner, newRunner} {
				assertInheritedWrite(t, path, workspaceSID, false)
				allowed, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{
					Path: path,
					SID:  windowssandbox.StringFromSIDBytes(sandboxGroupSID),
					Mask: uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE),
				}, true)
				if err != nil {
					t.Fatalf("PathMaskAllows(sandbox group, %s) error = %v", path, err)
				}
				if !allowed {
					t.Fatalf("sandbox group lost read/execute on %s", path)
				}
			}
		})
	}
}

func assertInheritedWrite(t *testing.T, path string, sid string, want bool) {
	t.Helper()
	allowed, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{
		Path: path,
		SID:  sid,
		Mask: uint32(windows.FILE_GENERIC_WRITE | windows.DELETE),
	}, false)
	if err != nil {
		t.Fatalf("PathMaskAllows(%s) error = %v", path, err)
	}
	if allowed != want {
		t.Fatalf("workspace capability write/delete on %s = %t, want %t", path, allowed, want)
	}
}
