package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestPrepareLegacySpawnContextAppliesOfflineNetworkEnv(t *testing.T) {
	codexHome := t.TempDir()
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	req := &CaptureRequest{
		PermissionProfileID: "workspace-write",
		WorkspaceRoots:      []string{cwd},
		CodexHome:           codexHome,
		Command:             []string{"cmd.exe", "/c", "echo hi"},
		CWD:                 cwd,
		Env:                 map[string]string{"OUT": "/dev/null"},
	}

	context, err := PrepareLegacySpawnContext(req, SpawnPrepOptions{InheritPath: true})
	if err != nil {
		t.Fatalf("PrepareLegacySpawnContext() error = %v", err)
	}
	if context.Env["OUT"] != "NUL" {
		t.Fatalf("OUT = %q, want NUL", context.Env["OUT"])
	}
	if context.Env["SBX_NONET_ACTIVE"] != "1" {
		t.Fatalf("SBX_NONET_ACTIVE = %q, want 1", context.Env["SBX_NONET_ACTIVE"])
	}
	pathHead, _, _ := strings.Cut(context.Env["PATH"], ";")
	if filepath.Base(pathHead) != ".sbx-denybin" {
		t.Fatalf("PATH = %q, want denybin prefix", context.Env["PATH"])
	}
	if _, err := os.Stat(filepath.Join(pathHead, "ssh.bat")); err != nil {
		t.Fatalf("expected denybin ssh.bat at PATH head: %v", err)
	}
	if context.LogsBaseDir != SandboxDir(codexHome) {
		t.Fatalf("LogsBaseDir = %q, want sandbox dir", context.LogsBaseDir)
	}
	if _, err := os.Stat(CurrentLogFilePathForBaseDir(context.LogsBaseDir)); err != nil {
		t.Fatalf("expected sandbox log file: %v", err)
	}
}

func TestRootCapabilitySIDsDedupAndMatchingPreferSpecificRoot(t *testing.T) {
	codexHome := t.TempDir()
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	rootSIDs, err := RootCapabilitySIDs(codexHome, root, []string{child, root, child})
	if err != nil {
		t.Fatalf("RootCapabilitySIDs() error = %v", err)
	}
	if len(rootSIDs) != 2 {
		t.Fatalf("RootCapabilitySIDs() len = %d, want 2: %#v", len(rootSIDs), rootSIDs)
	}
	best := MatchingRootCapability(filepath.Join(child, "file.txt"), rootSIDs)
	if best == nil || CanonicalPathKey(best.Root) != CanonicalPathKey(child) {
		t.Fatalf("MatchingRootCapability() = %#v, want child root", best)
	}
	denied := DenyRootCapabilitiesForPath(filepath.Join(root, ".git"), rootSIDs)
	if len(denied) == 0 {
		t.Fatalf("DenyRootCapabilitiesForPath() returned no SIDs")
	}
}

func TestLegacySessionCapabilityRootsUsesEffectiveWriteRoots(t *testing.T) {
	workspace := t.TempDir()
	tempDir := t.TempDir()
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	roots := LegacySessionCapabilityRoots(permissions, workspace, map[string]string{"TEMP": tempDir, "TMP": tempDir}, t.TempDir())
	if len(roots) != 2 {
		t.Fatalf("LegacySessionCapabilityRoots() = %#v, want workspace and temp roots", roots)
	}
}
