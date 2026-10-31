//go:build windows

package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWriteFileAtomicallyReplacesHardLinkWithoutWritingToItsTarget(t *testing.T) {
	temp := t.TempDir()
	victim := filepath.Join(temp, "victim")
	output := filepath.Join(temp, "sandbox_users.json")
	if err := os.WriteFile(victim, []byte("unchanged"), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	if err := os.Link(victim, output); err != nil {
		t.Fatalf("Link(victim, output) error = %v", err)
	}
	if err := WriteFileAtomically(output, []byte("new contents")); err != nil {
		t.Fatalf("WriteFileAtomically() error = %v", err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "unchanged" {
		t.Fatalf("hard-link target = %q, %v; want unchanged", got, err)
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "new contents" {
		t.Fatalf("output = %q, %v; want new contents", got, err)
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, %v; want 2", entries, err)
	}
}

func TestWriteFileAtomicallyFailsWithoutTruncatingOrLeavingTemporaryFile(t *testing.T) {
	temp := t.TempDir()
	victim := filepath.Join(temp, "victim")
	output := filepath.Join(temp, "setup_error.json")
	if err := os.WriteFile(victim, []byte("unchanged"), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	if err := os.Link(victim, output); err != nil {
		t.Fatalf("Link(victim, output) error = %v", err)
	}
	name, err := windows.UTF16PtrFromString(output)
	if err != nil {
		t.Fatalf("UTF16PtrFromString() error = %v", err)
	}
	held, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("hold output error = %v", err)
	}
	defer windows.CloseHandle(held)

	if err := WriteFileAtomically(output, []byte("new contents")); err == nil {
		t.Fatal("replacing a destination held without share-delete must fail")
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "unchanged" {
		t.Fatalf("hard-link target = %q, %v; want unchanged", got, err)
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "unchanged" {
		t.Fatalf("output = %q, %v; want unchanged", got, err)
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, %v; want 2 (no temporary file)", entries, err)
	}
}

func TestCreateTemporaryFileInDirCannotBeWrittenOrReplacedWhileHeld(t *testing.T) {
	temp := t.TempDir()
	file, directory, err := CreateTemporaryFileInDir(temp, ".log")
	if err != nil {
		t.Fatalf("CreateTemporaryFileInDir() error = %v", err)
	}
	defer windows.CloseHandle(directory)
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 1 {
		file.Close()
		t.Fatalf("entries = %v, %v", entries, err)
	}
	path := filepath.Join(temp, entries[0].Name())
	if _, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		file.Close()
		t.Fatal("fresh temporary file must not be writable while held")
	}
	if err := os.Remove(path); err == nil {
		file.Close()
		t.Fatal("fresh temporary file must not be removable while held")
	}
	if err := os.Rename(path, filepath.Join(temp, "moved")); err == nil {
		file.Close()
		t.Fatal("fresh temporary file must not be renamable while held")
	}
	if _, err := file.Write([]byte("log contents")); err != nil {
		file.Close()
		t.Fatalf("write temporary file error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temporary file error = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "log contents" {
		t.Fatalf("temporary file contents = %q, %v", got, err)
	}
}

func TestWriteFileAtomicallyRejectsUnsafePaths(t *testing.T) {
	temp := t.TempDir()
	unsafePaths := []string{
		"relative.json",
		temp + string(filepath.Separator) + ".." + string(filepath.Separator) + "escape.json",
		filepath.Join(temp, "users.json") + ":stream",
	}
	for _, path := range unsafePaths {
		if err := WriteFileAtomically(path, []byte("data")); err == nil {
			t.Fatalf("WriteFileAtomically(%q) unexpectedly succeeded", path)
		}
	}
	if _, err := os.Stat(filepath.Join(temp, "users.json")); !os.IsNotExist(err) {
		t.Fatalf("unsafe write created users.json: %v", err)
	}
}

func TestWriteFileAtomicallyRejectsJunctionParent(t *testing.T) {
	temp := t.TempDir()
	target := filepath.Join(temp, "target")
	alias := filepath.Join(temp, "linked")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error = %v", err)
	}
	createDirectoryJunction(t, target, alias)

	err := WriteFileAtomically(filepath.Join(alias, "sandbox_users.json"), []byte("data"))
	if err == nil {
		t.Fatal("junction parent must be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(target, "sandbox_users.json")); !os.IsNotExist(statErr) {
		t.Fatalf("junction write leaked into target: %v", statErr)
	}
}

func TestWriteFileAtomicallyCreatesNewOutput(t *testing.T) {
	temp := t.TempDir()
	path := filepath.Join(temp, "setup_marker.json")
	if err := WriteFileAtomically(path, []byte("contents")); err != nil {
		t.Fatalf("WriteFileAtomically() error = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "contents" {
		t.Fatalf("output = %q, %v", got, err)
	}
	if err := WriteFileAtomically(path, []byte("replaced")); err != nil {
		t.Fatalf("WriteFileAtomically(replace) error = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "replaced" {
		t.Fatalf("replaced output = %q, %v", got, err)
	}
}

func TestOpenProvisioningSetupLogCreatesFreshLog(t *testing.T) {
	baseDir := t.TempDir()
	first, err := OpenProvisioningSetupLog(baseDir)
	if err != nil {
		t.Fatalf("OpenProvisioningSetupLog() error = %v", err)
	}
	if _, err := first.WriteString("first line\n"); err != nil {
		first.Close()
		t.Fatalf("write first log error = %v", err)
	}
	first.Close()
	second, err := OpenProvisioningSetupLog(baseDir)
	if err != nil {
		t.Fatalf("OpenProvisioningSetupLog(second) error = %v", err)
	}
	second.Close()

	entries, err := os.ReadDir(baseDir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("logs = %v, %v; want 2 fresh logs", entries, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "sandbox-") || !strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("log name = %q", entry.Name())
		}
	}
}

func TestPrepareProvisioningSetupMarkerRetainsProtectedHandle(t *testing.T) {
	home := t.TempDir()
	file, err := PrepareProvisioningSetupMarker(home, setupTestRealUser(t))
	if err != nil {
		t.Fatalf("PrepareProvisioningSetupMarker() error = %v", err)
	}
	markerPath := SetupMarkerPath(home)
	info, err := os.Stat(markerPath)
	if err != nil {
		file.Close()
		t.Fatalf("marker stat error = %v", err)
	}
	if info.Size() != 0 {
		file.Close()
		t.Fatalf("unfinished marker size = %d, want 0", info.Size())
	}
	if _, err := os.OpenFile(markerPath, os.O_WRONLY, 0o600); err == nil {
		file.Close()
		t.Fatal("prepared marker must stay exclusively held")
	}
	want := &SetupMarker{Version: SetupVersion, OfflineUsername: OfflineUsername, OnlineUsername: OnlineUsername}
	if err := WriteProvisioningSetupMarker(file, want); err != nil {
		file.Close()
		t.Fatalf("WriteProvisioningSetupMarker() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close marker error = %v", err)
	}
	got, err := ReadSetupMarker(home)
	if err != nil {
		t.Fatalf("ReadSetupMarker() error = %v", err)
	}
	if got == nil || got.OfflineUsername != OfflineUsername || got.OnlineUsername != OnlineUsername {
		t.Fatalf("marker = %#v", got)
	}
	sd, err := windows.GetNamedSecurityInfo(markerPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("security descriptor Control() error = %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("marker DACL is not protected")
	}
}

// TestProvisioningSetupMarkerDACLMatchesRustSDDL pins the creation-time marker
// security descriptor to Rust's "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;<user>)"
// SDDL: a protected DACL with exactly SYSTEM, Administrators and the real user.
func TestProvisioningSetupMarkerDACLMatchesRustSDDL(t *testing.T) {
	home := t.TempDir()
	realUser := setupTestRealUser(t)
	file, err := PrepareProvisioningSetupMarker(home, realUser)
	if err != nil {
		t.Fatalf("PrepareProvisioningSetupMarker() error = %v", err)
	}
	defer file.Close()
	descriptor, err := windows.GetNamedSecurityInfo(SetupMarkerPath(home), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("security descriptor Control() error = %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("marker DACL is not protected")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("security descriptor DACL() error = %v", err)
	}
	if dacl == nil {
		t.Fatal("marker DACL is nil")
	}
	if got := int(dacl.AceCount); got != 3 {
		t.Fatalf("marker ACE count = %d, want 3", got)
	}
	realUserSID, found, err := lookupLocalUserSID(realUser)
	if err != nil || !found {
		t.Fatalf("lookupLocalUserSID(%q) = %q, %v, %v", realUser, realUserSID, found, err)
	}
	wantSIDs := []string{"S-1-5-18", "S-1-5-32-544", realUserSID}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatalf("GetAce(%d) error = %v", index, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("ACE %d type = %d, want access allowed", index, ace.Header.AceType)
		}
		// SDDL "GA" is stored as the file-specific FILE_ALL_ACCESS mask.
		const fileAllAccess = 0x1F01FF
		if ace.Mask != fileAllAccess {
			t.Fatalf("ACE %d mask = %#x, want FILE_ALL_ACCESS", index, ace.Mask)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if got := sid.String(); got != wantSIDs[index] {
			t.Fatalf("ACE %d SID = %q, want %q", index, got, wantSIDs[index])
		}
	}
}

func setupTestRealUser(t *testing.T) string {
	t.Helper()
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	account, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatalf("LookupAccount() error = %v", err)
	}
	if _, found, err := lookupLocalUserSID(account); err != nil || !found {
		t.Skipf("local user %q is not resolvable: %v", account, err)
	}
	return account
}
