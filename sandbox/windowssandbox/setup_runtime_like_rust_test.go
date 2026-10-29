package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWrapperRuntimeSelectionUsesTheParentLikeRust ports
// `wrapper_runtime_selection_uses_the_parent_not_environment_overrides`: an
// inherited or shell-supplied spelling never overrides the captured parent
// selection.
func TestWrapperRuntimeSelectionUsesTheParentLikeRust(t *testing.T) {
	for _, registeredCore := range []bool{false, true} {
		env := map[string]string{"codex_windows_registered_core": "1"}
		ApplyWrapperRuntimeEnvForSelection(env, registeredCore)
		if registeredCore {
			if len(env) != 1 || env[registeredCoreEnv] != "1" {
				t.Fatalf("registered env = %#v, want only %s=1", env, registeredCoreEnv)
			}
			continue
		}
		if len(env) != 0 {
			t.Fatalf("legacy env = %#v, want the override removed", env)
		}
	}
}

// TestResolveExeForRuntimeRegisteredDoesNotMaterializeLikeRust ports
// `registered_request_does_not_materialize_or_replace_a_missing_source`.
func TestResolveExeForRuntimeRegisteredDoesNotMaterializeLikeRust(t *testing.T) {
	tmp := t.TempDir()
	executable := filepath.Join(tmp, "codex.exe")
	home := filepath.Join(tmp, "home")
	for _, content := range [][]byte{nil, []byte("fixture")} {
		if content != nil {
			if err := os.WriteFile(executable, content, 0o600); err != nil {
				t.Fatalf("WriteFile(executable) error = %v", err)
			}
		}
		got, err := ResolveExeForRuntime(executable, home, SetupRuntimeRegistered)
		if err != nil {
			t.Fatalf("ResolveExeForRuntime(registered) error = %v", err)
		}
		if got != executable {
			t.Fatalf("ResolveExeForRuntime(registered) = %q, want %q", got, executable)
		}
		if _, err := os.Stat(HelperBinDir(home)); !os.IsNotExist(err) {
			t.Fatalf("helper bin dir exists after registered resolution: %v", err)
		}
	}
}

// TestResolveExeForRuntimeLegacyMaterializesTheSameSourceLikeRust ports
// `legacy_request_materializes_the_same_source`.
func TestResolveExeForRuntimeLegacyMaterializesTheSameSourceLikeRust(t *testing.T) {
	tmp := t.TempDir()
	executable := filepath.Join(tmp, "codex.exe")
	home := filepath.Join(tmp, "home")
	if err := os.WriteFile(executable, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(executable) error = %v", err)
	}
	destination := filepath.Join(HelperBinDir(home), "codex.exe")
	got, err := ResolveExeForRuntime(executable, home, SetupRuntimeLegacy)
	if err != nil {
		t.Fatalf("ResolveExeForRuntime(legacy) error = %v", err)
	}
	if got != destination {
		t.Fatalf("ResolveExeForRuntime(legacy) = %q, want %q", got, destination)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}
	if string(data) != "fixture" {
		t.Fatalf("materialized content = %q", data)
	}
}

// TestHelperReadRootsOnlyCreateALegacyBinLikeRust ports
// `helper_read_roots_only_create_a_legacy_bin`.
func TestHelperReadRootsOnlyCreateALegacyBinLikeRust(t *testing.T) {
	tmp := t.TempDir()
	for _, runtime := range []SetupRuntime{SetupRuntimeRegistered, SetupRuntimeLegacy} {
		home := filepath.Join(tmp, string(runtime))
		roots := GatherHelperReadRootsForRuntime(home, runtime)
		if !runtime.IsLegacy() {
			if len(roots) != 0 {
				t.Fatalf("registered helper roots = %#v", roots)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("registered runtime created codex home: %v", err)
			}
			continue
		}
		if len(roots) != 1 || roots[0] != HelperBinDir(home) {
			t.Fatalf("legacy helper roots = %#v", roots)
		}
		if info, err := os.Stat(HelperBinDir(home)); err != nil || !info.IsDir() {
			t.Fatalf("legacy helper bin dir missing: %v", err)
		}
	}
}

// TestRegisteredPayloadPreservesExplicitBinReadRootLikeRust ports
// `registered_payload_preserves_explicit_bin_read_root`.
func TestRegisteredPayloadPreservesExplicitBinReadRootLikeRust(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	bin := HelperBinDir(home)
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	readRoots, writeRoots := BuildPayloadRootsForRuntime(&SandboxSetupRequest{
		CodexHome: home,
	}, SetupRootOverrides{ReadRoots: []string{bin}, ReadRootsSet: true}, SetupRuntimeRegistered)
	if len(writeRoots) != 0 {
		t.Fatalf("writeRoots = %#v", writeRoots)
	}
	if len(readRoots) != 1 || !containsCanonical(readRoots, bin) {
		t.Fatalf("readRoots = %#v, want explicit bin root", readRoots)
	}
}
