//go:build windows

package windowssandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReadCleanupLocalUserFlagsForCurrentUser(t *testing.T) {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	account, _, _, err := tokenUser.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatalf("LookupAccount() error = %v", err)
	}
	flags, found, err := readCleanupLocalUserFlags(account)
	if err != nil {
		t.Fatalf("readCleanupLocalUserFlags(%q) error = %v", account, err)
	}
	if !found {
		t.Fatalf("current local account %q was not found", account)
	}
	_ = flags
}

func TestSandboxLogonSessionRemainsIgnoresNonSandboxUsers(t *testing.T) {
	remains, err := sandboxLogonSessionRemains(&disabledSandboxUsers{})
	if err != nil {
		t.Skipf("host cannot enumerate logon sessions: %v", err)
	}
	if remains {
		t.Fatal("a session without a sandbox SID was treated as a sandbox token")
	}
}

func TestDisabledSandboxUsersDisableAndRestoreLikeRust(t *testing.T) {
	originalRead := readSandboxCleanupUserFlags
	originalWrite := writeSandboxCleanupUserFlags
	originalResolve := resolveSandboxCleanupUserSID
	defer func() {
		readSandboxCleanupUserFlags = originalRead
		writeSandboxCleanupUserFlags = originalWrite
		resolveSandboxCleanupUserSID = originalResolve
	}()

	readSandboxCleanupUserFlags = func(name string) (uint32, bool, error) {
		if name == OfflineUsername {
			return 0x10, true, nil
		}
		return 0, false, nil
	}
	resolveSandboxCleanupUserSID = func(name string) (string, bool, error) {
		return "S-1-5-21-10-20-30-" + name, true, nil
	}
	writes := map[string][]uint32{}
	writeSandboxCleanupUserFlags = func(name string, flags uint32) error {
		writes[name] = append(writes[name], flags)
		return nil
	}

	users := &disabledSandboxUsers{}
	if err := users.disable(); err != nil {
		t.Fatalf("disable() error = %v", err)
	}
	if len(users.users) != 1 || users.users[0].name != OfflineUsername {
		t.Fatalf("disabled users = %#v", users.users)
	}
	if got := writes[OfflineUsername]; len(got) != 1 || got[0] != 0x12 {
		t.Fatalf("disable writes = %#v, want [0x12]", got)
	}
	if err := users.restore(); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	if got := writes[OfflineUsername]; len(got) != 2 || got[1] != 0x10 {
		t.Fatalf("restore writes = %#v, want original flags", got)
	}
}

func TestPrepareSandboxCleanupRestoresFlagsWhenProcessStopFailsLikeRust(t *testing.T) {
	originalRead := readSandboxCleanupUserFlags
	originalWrite := writeSandboxCleanupUserFlags
	originalResolve := resolveSandboxCleanupUserSID
	originalStop := stopSandboxCleanupProcesses
	defer func() {
		readSandboxCleanupUserFlags = originalRead
		writeSandboxCleanupUserFlags = originalWrite
		resolveSandboxCleanupUserSID = originalResolve
		stopSandboxCleanupProcesses = originalStop
	}()

	readSandboxCleanupUserFlags = func(name string) (uint32, bool, error) {
		return 0x10, name == OfflineUsername, nil
	}
	resolveSandboxCleanupUserSID = func(name string) (string, bool, error) {
		return "S-1-5-21-10-20-30-" + name, true, nil
	}
	var writes []uint32
	writeSandboxCleanupUserFlags = func(name string, flags uint32) error {
		writes = append(writes, flags)
		return nil
	}
	stopSandboxCleanupProcesses = func(users *disabledSandboxUsers) error {
		return errors.New("sandbox process did not exit")
	}

	_, err := prepareSandboxCleanupForRemoval()
	if err == nil || !strings.Contains(err.Error(), "sandbox process did not exit") {
		t.Fatalf("prepare error = %v", err)
	}
	if len(writes) != 2 || writes[0] != 0x12 || writes[1] != 0x10 {
		t.Fatalf("account flags writes = %#v, want disable then restore", writes)
	}
}

func TestFinishSandboxCleanupRemovesOnlyPinnedSandboxDirectoriesLikeRust(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{SandboxDir(home), SandboxSecretsDir(home), SandboxBinDir(home)} {
		if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "nested", "file.txt"), []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	keep := filepath.Join(home, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile(keep) error = %v", err)
	}
	var lines []string
	steps := sandboxCleanupSteps{
		removeWFPFilters:    func() error { return nil },
		removeFirewallRules: func() error { return nil },
		unhideUsers:         func() error { return nil },
		removeUsers:         func(func(string)) error { return nil },
		removeGroup:         func() error { return nil },
	}
	if err := finishSandboxCleanup(&home, func(line string) { lines = append(lines, line) }, nil, steps); err != nil {
		t.Fatalf("finishSandboxCleanup() error = %v", err)
	}
	for _, dir := range []string{SandboxDir(home), SandboxSecretsDir(home), SandboxBinDir(home)} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sandbox directory %s still exists", dir)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanup removed a non-sandbox file: %v", err)
	}
	if !containsString(lines, "removed .sandbox") || !containsString(lines, "removed .sandbox-bin") {
		t.Fatalf("cleanup report = %#v", lines)
	}
}

func TestFinishSandboxCleanupContinuesAfterDesktopFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	stepsRun := 0
	steps := sandboxCleanupSteps{
		removeWFPFilters:    func() error { stepsRun++; return nil },
		removeFirewallRules: func() error { stepsRun++; return nil },
		unhideUsers:         func() error { stepsRun++; return nil },
		removeUsers:         func(func(string)) error { stepsRun++; return nil },
		removeGroup:         func() error { stepsRun++; return nil },
	}
	err := finishSandboxCleanup(&home, func(string) {}, func() error {
		return errors.New("desktop cleanup failed")
	}, steps)
	if err == nil || !strings.Contains(err.Error(), "desktop cleanup failed") {
		t.Fatalf("finishSandboxCleanup() error = %v", err)
	}
	if stepsRun != 4 {
		t.Fatalf("cleanup steps run = %d, want 4 (group retained after failure)", stepsRun)
	}
}

func TestFinishSandboxCleanupRetainsGroupAfterEarlierFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	groupRemoved := false
	var lines []string
	steps := sandboxCleanupSteps{
		removeWFPFilters:    func() error { return errors.New("wfp failed") },
		removeFirewallRules: func() error { return nil },
		unhideUsers:         func() error { return nil },
		removeUsers:         func(func(string)) error { return nil },
		removeGroup: func() error {
			groupRemoved = true
			return nil
		},
	}
	err := finishSandboxCleanup(&home, func(line string) { lines = append(lines, line) }, nil, steps)
	if err == nil || !strings.Contains(err.Error(), "wfp failed") {
		t.Fatalf("finishSandboxCleanup() error = %v", err)
	}
	if groupRemoved {
		t.Fatal("sandbox group was removed after an earlier failure")
	}
	if !containsString(lines, "retaining sandbox group until cleanup finishes") {
		t.Fatalf("cleanup report = %#v", lines)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
