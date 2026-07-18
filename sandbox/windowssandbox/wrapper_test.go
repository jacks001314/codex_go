package windowssandbox

import (
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
