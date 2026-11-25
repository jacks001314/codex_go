//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeSeatbeltEnforcesWorkspaceAndMetadata(t *testing.T) {
	base := nativeSeatbeltFixtureOutsideScratch(t)
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	t.Run("outside_scratch", func(t *testing.T) {
		workspace := filepath.Join(base, "workspace")
		if err := os.Mkdir(workspace, 0700); err != nil {
			t.Fatal(err)
		}
		assertNativeSeatbeltWrites(t, workspace, outside)
	})
	// Also exercise the deferred metadata denies against broader tmp grants.
	t.Run("scratch_metadata", func(t *testing.T) {
		assertNativeSeatbeltWrites(t, t.TempDir(), outside)
	})
}

func nativeSeatbeltFixtureOutsideScratch(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp", os.TempDir()} {
		grant, err = normalizeSeatbeltTopLevelAlias(grant)
		if err != nil {
			t.Fatal(err)
		}
		if seatbeltSubpathWithin(cwd, grant) {
			t.Skipf("source directory %s is under default writable scratch/TMPDIR %s; native denial fixture needs a project checkout outside these grants", cwd, grant)
		}
	}
	base, err := os.MkdirTemp(cwd, ".codex-seatbelt-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	return base
}

func assertNativeSeatbeltWrites(t *testing.T, workspace, outside string) {
	t.Helper()
	t.Logf("workspace=%s outside=%s TMPDIR=%q os.TempDir=%s", workspace, outside, os.Getenv("TMPDIR"), os.TempDir())
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
