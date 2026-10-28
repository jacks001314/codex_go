package appserverdaemon

import (
	"reflect"
	"testing"
)

// TestRequiredPackageArtifactsIncludeAdvertisedExecServerLikeRust pins Rust
// #57590: a package that advertises `execServer` must carry it, while packages
// that predate the key remain complete.
func TestRequiredPackageArtifactsIncludeAdvertisedExecServerLikeRust(t *testing.T) {
	advertised := "bin/codex-exec-server.exe"
	manifest := &daemonPackageManifest{
		Entrypoint:   "bin/codex.exe",
		ExecServer:   &advertised,
		ResourcesDir: "codex-resources",
		PathDir:      "codex-path",
	}
	want := []string{
		"bin/codex.exe",
		"bin/codex-code-mode-host.exe",
		"codex-path/rg.exe",
		"bin/codex-exec-server.exe",
		"codex-resources/codex-command-runner.exe",
		"codex-resources/codex-windows-sandbox-setup.exe",
	}
	if got := requiredPackageArtifacts(manifest, "windows"); !reflect.DeepEqual(got, want) {
		t.Fatalf("windows artifacts = %#v, want %#v", got, want)
	}

	empty := ""
	fallback := &daemonPackageManifest{Entrypoint: "bin/codex", ExecServer: &empty, PathDir: "codex-path"}
	wantLinux := []string{"bin/codex", "bin/codex-code-mode-host", "codex-path/rg", "bin/codex-exec-server", "codex-resources/bwrap"}
	if got := requiredPackageArtifacts(fallback, "linux"); !reflect.DeepEqual(got, wantLinux) {
		t.Fatalf("linux artifacts = %#v, want %#v", got, wantLinux)
	}

	legacy := &daemonPackageManifest{Entrypoint: "bin/codex", PathDir: "codex-path"}
	wantLegacy := []string{"bin/codex", "bin/codex-code-mode-host", "codex-path/rg"}
	if got := requiredPackageArtifacts(legacy, "darwin"); !reflect.DeepEqual(got, wantLegacy) {
		t.Fatalf("legacy artifacts = %#v, want %#v", got, wantLegacy)
	}
}
