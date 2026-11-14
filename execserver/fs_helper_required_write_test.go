package execserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFilesystemRequiredSandboxRejectsWriteWithoutMutation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows disabled sandbox selection")
	}
	root := t.TempDir()
	existing := filepath.Join(root, "existing.txt")
	missing := filepath.Join(root, "missing.txt")
	if err := os.WriteFile(existing, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := &FileSystemSandboxContext{
		Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
		CWD:         root, WorkspaceRoots: []string{}, WindowsSandboxLevel: "disabled",
	}
	required, _, _, _, _, err := prepareFSSandboxForOperation(ctx, MethodFSWriteFile)
	if err != nil || !required {
		t.Fatalf("required=%v err=%v", required, err)
	}
	for _, target := range []string{existing, missing} {
		_, err := writeFile(&FSWriteFileParams{Path: target, DataBase64: base64.StdEncoding.EncodeToString([]byte("changed")), Sandbox: ctx})
		var failure *requestFailure
		if !errors.As(err, &failure) || failure.code != -32602 || !strings.Contains(failure.message, "filesystem sandbox cannot be enforced") {
			t.Fatalf("write %s: %v", target, err)
		}
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "original" {
		t.Fatalf("existing changed: %q err=%v", contents, err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected write created target: %v", err)
	}
	// A fully unrestricted filesystem does not need a helper, even when the
	// Windows sandbox is disabled.
	ctx.Permissions = json.RawMessage(`{"type":"managed","file_system":{"type":"unrestricted"},"network":"restricted"}`)
	if _, err := writeFile(&FSWriteFileParams{Path: missing, DataBase64: base64.StdEncoding.EncodeToString([]byte("allowed")), Sandbox: ctx}); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(missing)
	if err != nil || string(contents) != "allowed" {
		t.Fatalf("unrestricted write: %q err=%v", contents, err)
	}
}
