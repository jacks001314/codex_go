package execserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

func TestFSHelperPermissionBoundaryLikeRust(t *testing.T) {
	cwd := t.TempDir()
	workspace := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots"}},"access":"write"}]},"network":"enabled"}`)
	for _, legacy := range []bool{false, true} {
		for _, roots := range [][]string{nil, {}, {workspace}} {
			ctx := &FileSystemSandboxContext{Permissions: raw, CWD: cwd, WorkspaceRoots: roots, UseLegacyLandlock: legacy}
			required, profile, profileJSON, policyCWD, nativeRoots, err := prepareFSSandboxForOperation(ctx, MethodFSWriteFile)
			if err != nil || !required {
				t.Fatalf("prepare: required=%v, err=%v", required, err)
			}
			if nativeRoots == nil || len(nativeRoots) != len(roots) {
				t.Errorf("workspace roots = %v, want explicit %v", nativeRoots, roots)
			}
			plan, err := fsSandboxRunPlan(ctx, profile, profileJSON, policyCWD, nativeRoots, "", executable, []string{executable})
			if err != nil {
				t.Fatal(err)
			}
			if plan.PermissionProfile.AllowsNetwork() {
				t.Error("helper inherited network access")
			}
			var wire struct {
				Network    string `json:"network"`
				FileSystem struct {
					Entries []struct {
						Access string `json:"access"`
						Path   struct {
							Type string `json:"type"`
							Path string `json:"path"`
						} `json:"path"`
					} `json:"entries"`
				} `json:"file_system"`
			}
			if err := json.Unmarshal([]byte(plan.PermissionProfileJSON), &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Network != string(sandbox.NetworkRestricted) {
				t.Errorf("wire network = %q", wire.Network)
			}
			writes := 0
			helperRead := false
			for _, entry := range wire.FileSystem.Entries {
				if entry.Access == "write" {
					writes++
					if len(roots) == 0 || filepath.Clean(entry.Path.Path) != filepath.Clean(workspace) {
						t.Errorf("unexpected writable entry: %+v", entry)
					}
				}
				if entry.Access == "read" && filepath.Clean(entry.Path.Path) == filepath.Clean(executable) {
					helperRead = true
				}
			}
			if writes != len(roots) {
				t.Errorf("write entries = %d, want %d", writes, len(roots))
			}
			if helperRead == legacy {
				t.Errorf("helper runtime read grant = %v, legacy Landlock = %v", helperRead, legacy)
			}
			if !profile.AllowsNetwork() {
				t.Error("preparation mutated the caller permission profile")
			}
		}
	}

}
