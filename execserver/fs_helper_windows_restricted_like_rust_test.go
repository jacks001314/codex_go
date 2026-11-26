//go:build windows

package execserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
)

// Rust #61138/#61276 route filesystem helpers through the sandbox manager with
// SandboxablePreference::Require, so on Windows the helper must really execute
// under the restricted-token wrapper. The other Windows helper tests assert the
// generated command; this one runs it against a real workspace boundary.
func TestFSHelperRunsUnderWindowsRestrictedTokenLikeRust(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	insidePath := filepath.Join(workspace, "inside.txt")
	if err := os.WriteFile(insidePath, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	userHome := filepath.Join(workspace, "user")
	if err := os.MkdirAll(userHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", userHome)
	if profile := strings.TrimSpace(os.Getenv("USERPROFILE")); profile == "" {
		t.Skip("a user profile is required to build the sandbox token")
	}
	permissions := json.RawMessage(fmt.Sprintf(
		`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":%q},"access":"write"}]},"network":"restricted"}`,
		workspace,
	))
	ctx := &FileSystemSandboxContext{
		Permissions:         permissions,
		CWD:                 workspace,
		WorkspaceRoots:      []string{workspace},
		WindowsSandboxLevel: string(windowssandbox.WindowsSandboxLevelRestrictedToken),
	}

	// A write-restricted policy forces the sandboxed helper, so this write proves
	// the restricted-token wrapper executed rather than only being generated.
	insideWrite := filepath.Join(workspace, "written.txt")
	required, err := runSandboxedFSOperation(context.Background(), ctx, MethodFSWriteFile, FSWriteFileParams{
		Path:       insideWrite,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("payload")),
	}, &FSWriteFileResponse{})
	if err != nil {
		if isWindowsSandboxUnavailableForTest(err) {
			t.Skipf("restricted-token backend unavailable on this host: %v", err)
		}
		t.Fatalf("sandboxed helper write inside the workspace = %v", err)
	}
	if !required {
		t.Fatal("helper write took the unsandboxed path")
	}
	if written, err := os.ReadFile(insideWrite); err != nil || string(written) != "payload" {
		t.Fatalf("workspace write = %q, %v", written, err)
	}

	// Rust #58195 omits the sandbox context for a read the policy does not
	// restrict, so this read keeps the direct path while the write above ran
	// inside the restricted token.
	if required, err := runSandboxedFSOperation(context.Background(), ctx, MethodFSReadFile, FSReadFileParams{Path: insidePath}, &FSReadFileResponse{}); err != nil || required {
		t.Fatalf("unrestricted read required=%v err=%v, want the direct path", required, err)
	}
	if data, err := os.ReadFile(insidePath); err != nil || string(data) != "inside" {
		t.Fatalf("workspace read = %q, %v", data, err)
	}

	outsideWrite := filepath.Join(outside, "escape.txt")
	_, err = runSandboxedFSOperation(context.Background(), ctx, MethodFSWriteFile, FSWriteFileParams{
		Path:       outsideWrite,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("escape")),
	}, &FSWriteFileResponse{})
	if err == nil {
		t.Fatal("restricted-token helper wrote outside the workspace")
	}
	if _, statErr := os.Stat(outsideWrite); !os.IsNotExist(statErr) {
		t.Fatalf("outside path changed: %v", statErr)
	}
}

// isWindowsSandboxUnavailableForTest reports the executor-level "cannot be
// enforced here" diagnostic so a host without a provisioned sandbox skips.
func isWindowsSandboxUnavailableForTest(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cannot be enforced on this executor")
}
