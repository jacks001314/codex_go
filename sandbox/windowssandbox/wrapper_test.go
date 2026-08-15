package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestCreateWindowsSandboxCommandArgsForPermissionProfile(t *testing.T) {
	profile := coresandbox.WorkspaceWritePermissionProfile()
	got, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:                          []string{"cmd", "/c", "echo hi"},
		CommandCWD:                       `C:\repo`,
		WorkspaceRoots:                   []string{`C:\repo`},
		Env:                              map[string]string{"TEMP": `C:\tmp`},
		PermissionProfile:                &profile,
		WindowsSandboxLevel:              WindowsSandboxLevelRestrictedToken,
		ProxyEnforced:                    true,
		ProxySettingsMode:                ProxySettingsPreserve,
		ReadRootsOverride:                []string{`C:\read`},
		ReadRootsOverrideSet:             true,
		ReadRootsIncludePlatformDefaults: true,
		WriteRootsOverride:               []string{`C:\write`},
		WriteRootsOverrideSet:            true,
		DenyReadPathsOverride:            []string{`C:\repo\.env`},
		DenyWritePathsOverride:           []string{`C:\repo\readonly`},
		CodexHome:                        `C:\Users\codex\.codex`,
	})
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	if got[0] != CodexWindowsSandboxArg1 || got[len(got)-3] != "cmd" {
		t.Fatalf("args = %#v", got)
	}
	parsed, err := ParseWindowsSandboxWrapperArgs(got)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxWrapperArgs() error = %v", err)
	}
	if parsed.CodexHome != `C:\Users\codex\.codex` || parsed.CommandCWD != `C:\repo` {
		t.Fatalf("parsed paths = home %q cwd %q", parsed.CodexHome, parsed.CommandCWD)
	}
	if len(parsed.Command) != 3 || parsed.Command[0] != "cmd" {
		t.Fatalf("parsed command = %#v", parsed.Command)
	}
	if !parsed.ProxyEnforced || parsed.ProxySettingsMode != ProxySettingsPreserve || !parsed.ReadRootsIncludePlatformDefaults {
		t.Fatalf("parsed flags = %+v", parsed.WindowsSandboxCommandArgsRequest)
	}
	capture := wrapperCaptureRequest(parsed)
	if !capture.ProxyEnforced || capture.ProxySettingsMode != ProxySettingsPreserve {
		t.Fatalf("capture proxy = %+v", capture)
	}
	if !capture.ReadRootsOverrideSet || len(capture.ReadRootsOverride) != 1 || capture.ReadRootsOverride[0] != `C:\read` {
		t.Fatalf("capture read roots = %#v set=%v", capture.ReadRootsOverride, capture.ReadRootsOverrideSet)
	}
	if !capture.WriteRootsOverrideSet || len(capture.WriteRootsOverride) != 1 || capture.WriteRootsOverride[0] != `C:\write` {
		t.Fatalf("capture write roots = %#v set=%v", capture.WriteRootsOverride, capture.WriteRootsOverrideSet)
	}
	if len(capture.DenyReadPaths) != 1 || len(capture.DenyWritePaths) != 1 {
		t.Fatalf("capture denies = read %#v write %#v", capture.DenyReadPaths, capture.DenyWritePaths)
	}
}

func TestCreateWindowsSandboxCommandArgsResolvesDenyReadPathsFromProfileLikeRust(t *testing.T) {
	tmp := t.TempDir()
	exact := filepath.Join(tmp, "secrets.txt")
	nested := filepath.Join(tmp, "app", ".env")
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, path := range []string{exact, nested} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []coresandbox.FileSystemSandboxEntry{
		{Path: coresandbox.FileSystemPath{Type: "path", Path: exact}, Access: coresandbox.FileSystemAccessDeny},
		{Path: coresandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.Join(tmp, "**", "*.env")}, Access: coresandbox.FileSystemAccessDeny},
	}
	got, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd", "/c", "echo hi"},
		CommandCWD:          tmp,
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelElevated,
		CodexHome:           `C:\Users\codex\.codex`,
	})
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	parsed, err := ParseWindowsSandboxWrapperArgs(got)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxWrapperArgs() error = %v", err)
	}
	if len(parsed.DenyReadPathsOverride) != 2 {
		t.Fatalf("deny read paths = %#v, want exact path and glob match resolved from profile", parsed.DenyReadPathsOverride)
	}
	joined := strings.Join(parsed.DenyReadPathsOverride, "\n")
	if !strings.Contains(joined, filepath.Clean(exact)) || !strings.Contains(joined, filepath.Clean(nested)) {
		t.Fatalf("deny read paths = %#v, want %q and %q", parsed.DenyReadPathsOverride, exact, nested)
	}
}

func TestCreateWindowsSandboxCommandArgsRejectsRestrictedTokenWithDenyReadLikeRust(t *testing.T) {
	profile := coresandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []coresandbox.FileSystemSandboxEntry{
		{Path: coresandbox.FileSystemPath{Type: "path", Path: `C:\repo\.env`}, Access: coresandbox.FileSystemAccessDeny},
	}
	_, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd", "/c", "echo hi"},
		CommandCWD:          `C:\repo`,
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
		CodexHome:           `C:\Users\codex\.codex`,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot enforce split filesystem read restrictions") {
		t.Fatalf("error = %v, want restricted-token fail-closed for deny-read", err)
	}
}

func TestParseWindowsSandboxWrapperArgsRejectsRelativeCodexHome(t *testing.T) {
	profile := coresandbox.ReadOnlyPermissionProfile()
	args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd"},
		CommandCWD:          `C:\repo`,
		Env:                 map[string]string{},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
		CodexHome:           `C:\Users\codex\.codex`,
	})
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	for i, arg := range args {
		if arg == codexHomeFlag {
			args[i+1] = "relative"
			break
		}
	}
	if _, err := ParseWindowsSandboxWrapperArgs(args); err == nil {
		t.Fatalf("ParseWindowsSandboxWrapperArgs() error = nil, want failure")
	}
}
