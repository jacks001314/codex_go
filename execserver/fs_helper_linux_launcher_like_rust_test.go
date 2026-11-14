package execserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFSHelperReadableRootsIncludeLinuxSandboxLauncherLikeRust(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "bin", "codex")
	linuxHelper := filepath.Join(root, "aliases", "codex-linux-sandbox")
	roots := fsHelperReadableRoots(helper, linuxHelper)
	if len(roots) != 2 || roots[0] != filepath.Clean(helper) || roots[1] != filepath.Clean(linuxHelper) {
		t.Fatalf("readable roots = %#v", roots)
	}
	for _, forbidden := range []string{filepath.Dir(helper), filepath.Dir(linuxHelper)} {
		for _, granted := range roots {
			if granted == forbidden {
				t.Fatalf("helper granted parent directory %q", forbidden)
			}
		}
	}
	if duplicate := fsHelperReadableRoots(helper, helper); len(duplicate) != 1 || duplicate[0] != filepath.Clean(helper) {
		t.Fatalf("duplicate roots = %#v", duplicate)
	}
}

func TestFSHelperPlanCarriesLinuxSandboxLauncherLikeRust(t *testing.T) {
	cwd := t.TempDir()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	linuxHelper := filepath.Join(t.TempDir(), "codex-linux-sandbox")
	if err := os.WriteFile(linuxHelper, []byte("launcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots"}},"access":"write"}]},"network":"enabled"}`)
	for _, legacy := range []bool{false, true} {
		ctx := &FileSystemSandboxContext{Permissions: raw, CWD: cwd, UseLegacyLandlock: legacy, codexLinuxSandboxExe: linuxHelper}
		required, profile, profileJSON, policyCWD, roots, err := prepareFSSandboxForOperation(ctx, MethodFSWriteFile)
		if err != nil || !required {
			t.Fatalf("legacy=%v prepare: required=%v err=%v", legacy, required, err)
		}
		plan, err := fsSandboxRunPlan(ctx, profile, profileJSON, policyCWD, roots, "", helper, []string{helper})
		if err != nil {
			t.Fatalf("legacy=%v plan: %v", legacy, err)
		}
		if plan.CodexLinuxSandboxExe != filepath.Clean(linuxHelper) {
			t.Fatalf("legacy=%v linux sandbox exe = %q", legacy, plan.CodexLinuxSandboxExe)
		}
		var wire struct {
			FileSystem struct {
				Entries []struct {
					Access string `json:"access"`
					Path   struct {
						Path string `json:"path"`
					} `json:"path"`
				} `json:"entries"`
			} `json:"file_system"`
		}
		if err := json.Unmarshal([]byte(plan.PermissionProfileJSON), &wire); err != nil {
			t.Fatal(err)
		}
		granted := false
		for _, entry := range wire.FileSystem.Entries {
			if entry.Access == "read" && filepath.Clean(entry.Path.Path) == filepath.Clean(linuxHelper) {
				granted = true
			}
			if entry.Path.Path != "" && filepath.Clean(entry.Path.Path) == filepath.Dir(linuxHelper) {
				t.Fatalf("legacy=%v granted launcher parent directory", legacy)
			}
		}
		if granted == legacy {
			t.Fatalf("legacy=%v linux launcher read grant = %v", legacy, granted)
		}
	}
}

func TestServerAppliesLinuxSandboxLauncherToFSSandboxContext(t *testing.T) {
	server := NewServer()
	server.SetCodexLinuxSandboxExe(`/opt/codex/codex-linux-sandbox`)
	context := &FileSystemSandboxContext{}
	server.applyFSSandboxRuntimeOptions(context)
	if context.codexLinuxSandboxExe != `/opt/codex/codex-linux-sandbox` {
		t.Fatalf("context launcher = %q", context.codexLinuxSandboxExe)
	}
}
