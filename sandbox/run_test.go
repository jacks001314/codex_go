package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	json "github.com/goccy/go-json"
)

func TestBuildCommandRunPlanAllowsFullAccessProfile(t *testing.T) {
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		PermissionProfile: ":danger-full-access",
		Command:           []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if plan.RequiresPlatformSandbox {
		t.Fatalf("RequiresPlatformSandbox = true, reason %q", plan.UnsupportedReason)
	}
	if plan.PermissionProfileID != ":danger-full-access" || plan.PermissionProfile == nil || !plan.PermissionProfile.Disabled {
		t.Fatalf("plan profile = %+v id %q", plan.PermissionProfile, plan.PermissionProfileID)
	}
}

func TestBuildCommandRunPlanRejectsSandboxedProfilesWithoutBackend(t *testing.T) {
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		PermissionProfile: ":workspace",
		Command:           []string{"go", "version"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if runtime.GOOS == "linux" {
		if plan.RequiresPlatformSandbox || len(plan.Command) == 0 || plan.Command[0] != "codex-linux-sandbox" {
			t.Fatalf("linux plan = %+v", plan)
		}
		return
	}
	if runtime.GOOS == "windows" {
		if plan.RequiresPlatformSandbox || len(plan.Command) == 0 || plan.Command[0] != "go" {
			t.Fatalf("windows plan = %+v", plan)
		}
		return
	}
	if !plan.RequiresPlatformSandbox || !strings.Contains(plan.UnsupportedReason, `permission profile ":workspace"`) {
		t.Fatalf("plan = %+v", plan)
	}
	if err := plan.UnsupportedError(); !errors.Is(err, ErrPlatformSandboxUnsupported) {
		t.Fatalf("UnsupportedError() = %v, want ErrPlatformSandboxUnsupported", err)
	}
}

func TestBuildCommandRunPlanValidatesSandboxStateJSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		want  string
	}{
		{name: "invalid JSON", state: "{", want: "sandbox state JSON is invalid"},
		{name: "missing profile", state: fmt.Sprintf(`{"sandbox_cwd":%q}`, t.TempDir()), want: "missing permission_profile"},
		{name: "missing cwd", state: `{"permission_profile":{"type":"disabled"}}`, want: "missing sandbox_cwd"},
		{name: "relative cwd", state: `{"permission_profile":{"type":"disabled"},"sandbox_cwd":"relative"}`, want: "sandbox state cwd is not native to this host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildCommandRunPlan(&CommandRunRequest{
				SandboxStateJSON: tc.state,
				Command:          []string{"go", "version"},
			})
			if !errors.Is(err, ErrInvalidSandboxRunRequest) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BuildCommandRunPlan() error = %v, want ErrInvalidSandboxRunRequest containing %q", err, tc.want)
			}
		})
	}
}

func TestBuildCommandRunPlanAppliesSandboxStateReadableRootsAsReadOnly(t *testing.T) {
	readableRoot := filepath.Join(t.TempDir(), "docs")
	sandboxExe := filepath.Join(t.TempDir(), "custom-codex-linux-sandbox")
	cwd := t.TempDir()
	state := fmt.Sprintf(`{
		"permission_profile": {
			"type": "managed",
			"file_system": {
				"type": "restricted",
				"entries": [
					{"path": {"type": "special", "value": {"kind": "minimal"}}, "access": "read"}
				]
			},
			"network": "enabled"
		},
		"sandbox_cwd": %q,
		"codex_linux_sandbox_exe": %q,
		"use_legacy_landlock": true
	}`, cwd, sandboxExe)
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		SandboxStateJSON:     state,
		SandboxReadableRoots: []string{readableRoot},
		Command:              []string{"go", "version"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	access := permissionProfilePathAccess(t, plan.PermissionProfileJSON, readableRoot)
	if access != string(FileSystemAccessRead) {
		t.Fatalf("readable root was widened to write: %s", plan.PermissionProfileJSON)
	}
	if plan.CWD != filepath.Clean(cwd) {
		t.Fatalf("CWD = %q, want sandbox state cwd %q", plan.CWD, filepath.Clean(cwd))
	}
	if plan.CodexLinuxSandboxExe != filepath.Clean(sandboxExe) {
		t.Fatalf("CodexLinuxSandboxExe = %q, want %q", plan.CodexLinuxSandboxExe, filepath.Clean(sandboxExe))
	}
	if !plan.UseLegacyLandlock {
		t.Fatalf("UseLegacyLandlock = false, want sandbox state value true")
	}
	if runtime.GOOS == "linux" {
		if plan.RequiresPlatformSandbox || len(plan.Command) == 0 || plan.Command[0] != filepath.Clean(sandboxExe) {
			t.Fatalf("linux plan = %+v", plan)
		}
		if !containsSandboxArg(plan.Command, "--use-legacy-landlock") {
			t.Fatalf("linux command missing legacy landlock flag: %#v", plan.Command)
		}
		return
	}
	if runtime.GOOS == "windows" {
		if plan.RequiresPlatformSandbox || len(plan.Command) == 0 || plan.Command[0] != "go" {
			t.Fatalf("windows plan = %+v", plan)
		}
		return
	}
	if !plan.RequiresPlatformSandbox || !strings.Contains(plan.UnsupportedReason, "sandbox state") {
		t.Fatalf("non-linux plan = %+v", plan)
	}
}

func TestBuildCommandRunPlanUsesRequestLegacyLandlockWithoutSandboxState(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		ResolvedPermissionProfile:   &profile,
		ResolvedPermissionProfileID: "dev",
		UseLegacyLandlock:           true,
		Command:                     []string{"go", "version"},
		CWD:                         t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if !plan.UseLegacyLandlock {
		t.Fatalf("UseLegacyLandlock = false, want request value true")
	}
	if runtime.GOOS == "linux" && !containsSandboxArg(plan.Command, "--use-legacy-landlock") {
		t.Fatalf("linux command missing legacy landlock flag: %#v", plan.Command)
	}
}

func TestBuildCommandRunPlanUsesRequestLinuxSandboxHelper(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "codex-linux-sandbox")
	profile := WorkspaceWritePermissionProfile()
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		ResolvedPermissionProfile:   &profile,
		ResolvedPermissionProfileID: "dev",
		CodexLinuxSandboxExe:        helper,
		Command:                     []string{"go", "version"},
		CWD:                         t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if plan.CodexLinuxSandboxExe != filepath.Clean(helper) {
		t.Fatalf("CodexLinuxSandboxExe = %q, want %q", plan.CodexLinuxSandboxExe, filepath.Clean(helper))
	}
	if runtime.GOOS == "linux" && (len(plan.Command) == 0 || plan.Command[0] != filepath.Clean(helper)) {
		t.Fatalf("linux command = %#v, want helper %q", plan.Command, filepath.Clean(helper))
	}
}

func TestBuildCommandRunPlanAllowsNetworkForProxy(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		ResolvedPermissionProfile:   &profile,
		ResolvedPermissionProfileID: "dev",
		UseLegacyLandlock:           true,
		AllowNetworkForProxy:        true,
		Command:                     []string{"go", "version"},
		CWD:                         t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if !plan.AllowNetworkForProxy {
		t.Fatalf("AllowNetworkForProxy = false, want true")
	}
	if runtime.GOOS == "linux" {
		if !containsSandboxArg(plan.Command, "--allow-network-for-proxy") {
			t.Fatalf("linux command missing managed proxy flag: %#v", plan.Command)
		}
		if containsSandboxArg(plan.Command, "--use-legacy-landlock") {
			t.Fatalf("linux command should not combine legacy landlock with managed proxy routing: %#v", plan.Command)
		}
	}
}

func TestBuildCommandRunPlanSandboxStateLegacyLandlockOverridesRequest(t *testing.T) {
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		SandboxStateJSON:  fmt.Sprintf(`{"permission_profile":{"type":"disabled"},"sandbox_cwd":%q}`, t.TempDir()),
		UseLegacyLandlock: true,
		Command:           []string{"go", "version"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if plan.UseLegacyLandlock {
		t.Fatalf("UseLegacyLandlock = true, want sandbox state default false")
	}
}

func TestBuildCommandRunPlanSkipsSandboxStateReadableRootWithExistingEffectiveAccess(t *testing.T) {
	cwd := t.TempDir()
	stateData, err := json.Marshal(map[string]any{
		"permission_profile": map[string]any{
			"type": "managed",
			"file_system": map[string]any{
				"type": "restricted",
				"entries": []any{
					map[string]any{
						"path": map[string]any{
							"type": "special",
							"value": map[string]any{
								"kind": "project_roots",
							},
						},
						"access": string(FileSystemAccessRead),
					},
				},
			},
			"network": string(NetworkRestricted),
		},
		"sandbox_cwd": cwd,
	})
	if err != nil {
		t.Fatalf("Marshal sandbox state error = %v", err)
	}
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		SandboxStateJSON:     string(stateData),
		SandboxReadableRoots: []string{cwd},
		Command:              []string{"go", "version"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if count := permissionProfilePathEntryCount(t, plan.PermissionProfileJSON, cleanRunPath(cwd)); count != 0 {
		t.Fatalf("readable root already covered by project_roots was appended %d time(s): %s", count, plan.PermissionProfileJSON)
	}
}

func permissionProfilePathAccess(t *testing.T, raw string, path string) string {
	t.Helper()
	var wire struct {
		FileSystem struct {
			Entries []struct {
				Path struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"path"`
				Access string `json:"access"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatalf("Unmarshal permission profile JSON error = %v", err)
	}
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Type == "path" && entry.Path.Path == path {
			return entry.Access
		}
	}
	t.Fatalf("path %q not found in permission profile JSON %s", path, raw)
	return ""
}

func permissionProfilePathEntryCount(t *testing.T, raw string, path string) int {
	t.Helper()
	var wire struct {
		FileSystem struct {
			Entries []struct {
				Path struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"path"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatalf("Unmarshal permission profile JSON error = %v", err)
	}
	count := 0
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Type == "path" && cleanRunPath(entry.Path.Path) == path {
			count++
		}
	}
	return count
}

func TestBuildCommandRunPlanRejectsDisabledSandboxStateNetworkOverride(t *testing.T) {
	_, err := BuildCommandRunPlan(&CommandRunRequest{
		SandboxStateJSON:      fmt.Sprintf(`{"permission_profile":{"type":"disabled"},"sandbox_cwd":%q}`, t.TempDir()),
		SandboxDisableNetwork: true,
		Command:               []string{"go", "version"},
	})
	if !errors.Is(err, ErrInvalidSandboxRunRequest) || !strings.Contains(err.Error(), "cannot be applied to a disabled permission profile") {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
}

func TestResolvePermissionProfileAliases(t *testing.T) {
	for _, id := range []string{"danger-full-access", ":danger-full-access", "full-access"} {
		profile, normalized, err := ResolvePermissionProfile(id)
		if err != nil {
			t.Fatalf("ResolvePermissionProfile(%q) error = %v", id, err)
		}
		if normalized != id || profile == nil || !profile.Disabled {
			t.Fatalf("ResolvePermissionProfile(%q) = %+v %q", id, profile, normalized)
		}
	}
}

func TestLinuxSandboxHelperReportsPlatformState(t *testing.T) {
	var stderr strings.Builder
	code := RunLinuxSandboxHelper(nil, nil, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	message := stderr.String()
	if runtime.GOOS == "linux" {
		if !strings.Contains(message, "No command specified") && !strings.Contains(message, "no command specified") {
			t.Fatalf("stderr = %q", message)
		}
		return
	}
	if !strings.Contains(message, "only supported on Linux") {
		t.Fatalf("stderr = %q", message)
	}
}

func containsSandboxArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
