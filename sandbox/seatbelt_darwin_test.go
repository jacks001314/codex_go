//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeSeatbeltEnforcesWorkspaceAndMetadata(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	metadata := filepath.Join(workspace, ".git")
	if err := os.Mkdir(metadata, 0o755); err != nil {
		t.Fatalf("create protected metadata directory: %v", err)
	}
	allowedPath := filepath.Join(workspace, "allowed.txt")
	blockedPath := filepath.Join(outside, "blocked.txt")
	metadataPath := filepath.Join(metadata, "blocked.txt")
	profile := WorkspaceWritePermissionProfile()
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		ResolvedPermissionProfile:   &profile,
		ResolvedPermissionProfileID: "native-seatbelt-test",
		CWD:                         workspace,
		Command: []string{"/bin/sh", "-c", `
set -eu
test "$CODEX_SANDBOX" = seatbelt
printf allowed > "$ALLOW_PATH"
if printf blocked > "$BLOCK_PATH"; then exit 21; fi
if printf metadata > "$METADATA_PATH"; then exit 22; fi
`},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan: %v", err)
	}
	if len(plan.Command) == 0 || plan.Command[0] != macOSSeatbeltExecutable {
		t.Fatalf("native command is not seatbelt-wrapped: %#v", plan.Command)
	}
	command := exec.Command(plan.Command[0], plan.Command[1:]...)
	command.Dir = plan.CWD
	command.Env = append(os.Environ(),
		"ALLOW_PATH="+allowedPath,
		"BLOCK_PATH="+blockedPath,
		"METADATA_PATH="+metadataPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run native seatbelt command: %v\n%s", err, output)
	}
	if contents, err := os.ReadFile(allowedPath); err != nil || string(contents) != "allowed" {
		t.Fatalf("allowed workspace write = %q, %v", contents, err)
	}
	for _, path := range []string{blockedPath, metadataPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("seatbelt allowed protected write %s: %v", path, err)
		}
	}
}
