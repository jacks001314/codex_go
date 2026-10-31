//go:build windows

package windowssandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coresandbox "codex_go/sandbox"
	"golang.org/x/sys/windows"
)

func TestPrepareLaunchDesktopDefault(t *testing.T) {
	desktop, err := PrepareLaunchDesktop(false)
	if err != nil {
		t.Fatalf("PrepareLaunchDesktop(false) error = %v", err)
	}
	if desktop.StartupName != `Winsta0\Default` {
		t.Fatalf("StartupName = %q, want default desktop", desktop.StartupName)
	}
}

func TestCreateLaunchDesktopRejectsEmptyName(t *testing.T) {
	if _, err := CreateLaunchDesktop(""); err == nil {
		t.Fatalf("CreateLaunchDesktop(\"\") error = nil, want error")
	}
}

func TestPrepareLaunchDesktopPrivateCreatesAndCloses(t *testing.T) {
	desktop, err := PrepareLaunchDesktop(true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Skipf("host does not allow private desktop creation: %v", err)
		}
		t.Fatalf("PrepareLaunchDesktop(true) error = %v", err)
	}
	if desktop.Handle == 0 {
		t.Fatalf("private desktop handle is zero")
	}
	if desktop.StartupName == "" || desktop.StartupName == `Winsta0\Default` {
		t.Fatalf("private StartupName = %q", desktop.StartupName)
	}
	if err := desktop.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestPrivateDesktopNameValidationLikeRust covers Rust #40672's rejection of
// interactive desktops, injected separators/NULs, and overlong nonces.
func TestPrivateDesktopNameValidationLikeRust(t *testing.T) {
	for _, name := range []string{
		"Default",
		`Winsta0\Default`,
		"CodexSandboxDesktop-",
		`CodexSandboxDesktop-abcd\Default`,
		"CodexSandboxDesktop-abcd\x00Default",
		"CodexSandboxDesktop-0123456789abcdef0123456789abcdef0",
	} {
		if _, err := OpenPrivateLaunchDesktop(name); err == nil {
			t.Fatalf("OpenPrivateLaunchDesktop(%q) error = nil, want rejection", name)
		}
	}
}

// TestOpeningMissingPrivateDesktopDoesNotCreateLikeRust ensures a validated but
// missing name fails instead of falling back to the default desktop.
func TestOpeningMissingPrivateDesktopDoesNotCreateLikeRust(t *testing.T) {
	name := "CodexSandboxDesktop-0123456789abcdef0123456789abcdef"
	if _, err := OpenPrivateLaunchDesktop(name); err == nil {
		t.Fatalf("opening missing private desktop %q succeeded", name)
	}
}

// TestSharedPrivateDesktopSurvivesLaunchHandlesAndConcurrentRequestsLikeRust
// covers Rust #40672: concurrent equivalent requests share one parent-owned
// desktop, and closing a launch handle does not destroy it.
func TestSharedPrivateDesktopSurvivesLaunchHandlesAndConcurrentRequestsLikeRust(t *testing.T) {
	account, err := currentAccountName()
	if err != nil {
		t.Fatalf("currentAccountName() error = %v", err)
	}
	policy := DesktopPolicy{NetworkEnabled: false, ReadRoots: []string{}, WriteRoots: []string{}}
	names := make([]string, 4)
	errs := make([]error, 4)
	var wait sync.WaitGroup
	for index := range names {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			names[index], errs[index] = SharedPrivateDesktopForUser(account, policy)
		}(index)
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("shared desktop %d error = %v", index, err)
		}
		if names[index] != names[0] {
			t.Fatalf("names = %#v, want one shared desktop", names)
		}
	}
	desktop, err := OpenPrivateLaunchDesktop(names[0])
	if err != nil {
		t.Fatalf("OpenPrivateLaunchDesktop(shared) error = %v", err)
	}
	if err := desktop.Close(); err != nil {
		t.Fatalf("Close(shared) error = %v", err)
	}
	again, err := SharedPrivateDesktopForUser(strings.ToLower(account), policy)
	if err != nil || again != names[0] {
		t.Fatalf("lowercase account = %q, %v; want %q", again, err, names[0])
	}
	changed, err := SharedPrivateDesktopForUser(account, DesktopPolicy{NetworkEnabled: true, ReadRoots: []string{}, WriteRoots: []string{}})
	if err != nil {
		t.Fatalf("changed policy error = %v", err)
	}
	if changed == names[0] {
		t.Fatalf("different network policy reused desktop %q", names[0])
	}
	changedDesktop, err := OpenPrivateLaunchDesktop(changed)
	if err != nil {
		t.Fatalf("OpenPrivateLaunchDesktop(changed) error = %v", err)
	}
	_ = changedDesktop.Close()
}

// TestLegacyDesktopReusesOnlyEquivalentPermissionsLikeRust covers the legacy
// backend cache key: the same restricted account and policy share a desktop,
// while additional deny-write paths select another one.
func TestLegacyDesktopReusesOnlyEquivalentPermissionsLikeRust(t *testing.T) {
	workspace := t.TempDir()
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{workspace})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	token, err := GetCurrentTokenForRestriction()
	if err != nil {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v", err)
	}
	defer CloseTokenHandle(token)
	security := &LegacySessionSecurity{Token: token}
	first, err := PrepareLegacyLaunchDesktop(security, permissions, workspace, map[string]string{}, nil, "")
	if err != nil {
		t.Fatalf("PrepareLegacyLaunchDesktop() error = %v", err)
	}
	name := first.StartupName
	_ = first.Close()
	second, err := PrepareLegacyLaunchDesktop(security, permissions, workspace, map[string]string{}, nil, "")
	if err != nil || second.StartupName != name {
		t.Fatalf("equivalent legacy desktop = %q, %v; want %q", second.StartupName, err, name)
	}
	_ = second.Close()
	denyPath := filepath.Join(workspace, "denied")
	changed, err := PrepareLegacyLaunchDesktop(security, permissions, workspace, map[string]string{}, []string{denyPath}, "")
	if err != nil {
		t.Fatalf("PrepareLegacyLaunchDesktop(deny write) error = %v", err)
	}
	if changed.StartupName == name {
		t.Fatalf("additional deny-write path reused desktop %q", name)
	}
	_ = changed.Close()
}

// TestLegacyDesktopLaunchesUser32ProcessesLikeRust guards the legacy desktop
// ACL. Rust's PrivateDesktop::create grants the caller's logon SID, and that
// SID is one of the restricted legacy token's restricting SIDs. Without the
// grant, processes that initialise USER32 (pwsh, powershell, where, whoami)
// cannot open their desktop and exit with STATUS_DLL_INIT_FAILED
// (0xC0000142) while cmd.exe still runs.
func TestLegacyDesktopLaunchesUser32ProcessesLikeRust(t *testing.T) {
	whereExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "where.exe")
	if _, err := os.Stat(whereExe); err != nil {
		t.Skipf("where.exe unavailable: %v", err)
	}
	workspace := t.TempDir()
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{workspace})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	base, err := GetCurrentTokenForRestriction()
	if err != nil {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v", err)
	}
	defer CloseTokenHandle(base)
	const capability = "S-1-5-21-10-20-30-40"
	restricted, err := CreateWorkspaceWriteTokenWithCapsFrom(base, []string{capability})
	if err != nil {
		if errors.Is(err, ErrHostUnsupported) {
			t.Skipf("restricted tokens unsupported on this host: %v", err)
		}
		t.Fatalf("CreateWorkspaceWriteTokenWithCapsFrom() error = %v", err)
	}
	defer CloseTokenHandle(restricted)
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	security := &LegacySessionSecurity{
		Token:         restricted,
		WriteRootSIDs: []RootCapabilitySID{{Root: workspace, SID: capability}},
	}
	desktop, err := PrepareLegacyLaunchDesktop(security, permissions, workspace, env, nil, "")
	if err != nil {
		t.Fatalf("PrepareLegacyLaunchDesktop() error = %v", err)
	}
	defer desktop.Close()
	handles, err := SpawnProcessWithPipesWithToken(PipeSpawnRequest{
		Token:      restricted,
		Command:    []string{whereExe, "cmd"},
		CWD:        workspace,
		Env:        env,
		StdinMode:  StdinModeClosed,
		StderrMode: StderrModeSeparate,
		Desktop:    desktop,
	})
	if err != nil {
		t.Fatalf("SpawnProcessWithPipesWithToken() error = %v", err)
	}
	defer handles.Close()
	var stdout []byte
	done, err := ReadHandleLoop(handles.StdoutRead, func(chunk []byte) {
		stdout = append(stdout, chunk...)
	})
	if err != nil {
		t.Fatalf("ReadHandleLoop() error = %v", err)
	}
	outcome, err := WaitCreatedProcess(handles.Process, nil, CancellationToken{})
	if err != nil {
		t.Fatalf("WaitCreatedProcess() error = %v", err)
	}
	if outcome != ProcessWaitExited {
		t.Fatalf("WaitCreatedProcess() outcome = %v, want exited", outcome)
	}
	<-done
	exitCode, err := CreatedProcessExitCode(handles.Process)
	if err != nil {
		t.Fatalf("CreatedProcessExitCode() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("where.exe exit code = %d (0x%X), stdout=%q; want 0", exitCode, uint32(exitCode), string(stdout))
	}
	if !strings.Contains(strings.ToLower(string(stdout)), "cmd.exe") {
		t.Fatalf("where.exe output = %q, want the cmd.exe path", string(stdout))
	}
}

// TestElevatedDesktopPolicySeparatesEffectivePermissionsLikeRust covers Rust
// #40672's reuse key: root order, duplicates and `..` spellings normalize,
// while every effective permission change selects a separate desktop.
func TestElevatedDesktopPolicySeparatesEffectivePermissionsLikeRust(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	readable := filepath.Join(root, "readable")
	writable := filepath.Join(root, "writable")
	for _, path := range []string{workspace, readable, writable} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}
	account, err := currentAccountName()
	if err != nil {
		t.Fatalf("currentAccountName() error = %v", err)
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{workspace})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	request := &SandboxSetupRequest{
		CodexHome:     filepath.Join(root, "codex-home"),
		CommandCWD:    workspace,
		Env:           map[string]string{},
		ProxyEnforced: true,
		Permissions:   permissions,
	}
	base := SetupRootOverrides{
		ReadRoots:     []string{readable},
		ReadRootsSet:  true,
		WriteRoots:    []string{workspace, writable},
		WriteRootsSet: true,
	}
	desktopFor := func(overrides SetupRootOverrides, capabilitySIDs []string, proxySID string) string {
		t.Helper()
		policy, err := NewElevatedDesktopPolicy(request, overrides, capabilitySIDs, proxySID)
		if err != nil {
			t.Fatalf("NewElevatedDesktopPolicy() error = %v", err)
		}
		name, err := SharedPrivateDesktopForUser(account, policy)
		if err != nil {
			t.Fatalf("SharedPrivateDesktopForUser() error = %v", err)
		}
		return name
	}
	sids := []string{"S-1-5-21-10-20-30-40", "S-1-5-21-10-20-30-41"}
	proxySID := "S-1-5-21-50-60-70-80"
	name := desktopFor(base, sids, proxySID)
	equivalent := SetupRootOverrides{
		ReadRoots:     []string{readable, readable},
		ReadRootsSet:  true,
		WriteRoots:    []string{filepath.Join(writable, "..", "writable"), workspace, writable},
		WriteRootsSet: true,
	}
	if got := desktopFor(equivalent, []string{sids[1], sids[0], sids[1]}, proxySID); got != name {
		t.Fatalf("equivalent policy desktop = %q, want %q", got, name)
	}
	for _, tc := range []struct {
		name      string
		overrides SetupRootOverrides
		sids      []string
		proxySID  string
	}{
		{"read roots", SetupRootOverrides{ReadRoots: []string{workspace}, ReadRootsSet: true, WriteRoots: base.WriteRoots, WriteRootsSet: true}, sids, proxySID},
		{"write roots", SetupRootOverrides{ReadRoots: base.ReadRoots, ReadRootsSet: true, WriteRoots: []string{workspace}, WriteRootsSet: true}, sids, proxySID},
		{"deny reads", SetupRootOverrides{ReadRoots: base.ReadRoots, ReadRootsSet: true, WriteRoots: base.WriteRoots, WriteRootsSet: true, DenyReadPaths: []string{readable}}, sids, proxySID},
		{"deny writes", SetupRootOverrides{ReadRoots: base.ReadRoots, ReadRootsSet: true, WriteRoots: base.WriteRoots, WriteRootsSet: true, DenyWritePaths: []string{writable}}, sids, proxySID},
		{"capability SIDs", base, sids[:1], proxySID},
		{"proxy SID", base, sids, "S-1-5-21-50-60-70-81"},
	} {
		changed := desktopFor(tc.overrides, tc.sids, tc.proxySID)
		if changed == name {
			t.Fatalf("%s reused desktop %q", tc.name, name)
		}
		desktop, err := OpenPrivateLaunchDesktop(changed)
		if err != nil {
			t.Fatalf("OpenPrivateLaunchDesktop(%s) error = %v", tc.name, err)
		}
		_ = desktop.Close()
	}
}

func currentAccountName() (string, error) {
	buffer := make([]uint16, 256)
	length := uint32(len(buffer))
	if err := windows.GetUserNameEx(windows.NameSamCompatible, &buffer[0], &length); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:length]), nil
}
