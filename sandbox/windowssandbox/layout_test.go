package windowssandbox

import (
	"errors"
	"runtime"
	"testing"
)

func TestPortLayoutMirrorsRustWindowsSandboxModules(t *testing.T) {
	layout := PortLayout()
	if len(layout) < 50 {
		t.Fatalf("PortLayout length = %d, want root modules and nested Rust helpers", len(layout))
	}
	byRustPath := map[string]PortModule{}
	for _, module := range layout {
		if module.RustPath == "" || module.GoPath == "" || module.Package == "" {
			t.Fatalf("incomplete module entry: %+v", module)
		}
		if module.Status == PortStatusScaffold || module.Status == PortStatusStub {
			t.Fatalf("module %s still has incomplete status %s", module.RustPath, module.Status)
		}
		byRustPath[module.RustPath] = module
	}
	for _, rustPath := range []string{
		"src/lib.rs",
		"src/setup.rs",
		"src/wfp.rs",
		"src/token.rs",
		"src/bin/setup_main/main.rs",
		"src/bin/command_runner/main.rs",
		"src/elevated/ipc_framed.rs",
		"src/unified_exec/backends/elevated.rs",
		"src/wfp/filter_specs.rs",
	} {
		if _, ok := byRustPath[rustPath]; !ok {
			t.Fatalf("missing Rust module mapping for %s", rustPath)
		}
	}
}

func TestCaptureRequiresExplicitBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows capture backend is implemented; integration requires provisioned sandbox state")
	}
	_, err := RunWindowsSandboxCapture(&CaptureRequest{
		PermissionProfileID: "workspace",
		WorkspaceRoots:      []string{`C:\repo`},
		CodexHome:           `C:\Users\codex\.codex`,
		Command:             []string{"cmd", "/c", "echo hi"},
		CWD:                 `C:\repo`,
	})
	if !errors.Is(err, ErrWindowsOnly) && !errors.Is(err, ErrBackendNotImplemented) {
		t.Fatalf("RunWindowsSandboxCapture() error = %v, want explicit unsupported backend", err)
	}
}

func TestResolveWindowsDenyReadPathsNormalizesAndDeduplicates(t *testing.T) {
	got := ResolveWindowsDenyReadPaths([]string{"/repo/../secret", "/secret", ""})
	if len(got) != 1 || got[0] == "" {
		t.Fatalf("ResolveWindowsDenyReadPaths() = %#v", got)
	}
}
