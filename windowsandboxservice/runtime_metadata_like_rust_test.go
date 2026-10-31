//go:build windows

package windowsandboxservice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// metadataTestToken opens a real primary token for the current process.
func metadataTestToken(t *testing.T) windows.Token {
	t.Helper()
	var token windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_IMPERSONATE,
		&token,
	); err != nil {
		t.Skipf("open the current process token: %v", err)
	}
	t.Cleanup(func() { _ = token.Close() })
	return token
}

// metadataTestSID returns a SID that is never the current user's logon SID.
func metadataTestSID(t *testing.T) string {
	t.Helper()
	return "S-1-5-32-545"
}

// metadataTestRecord builds one registered-Core record naming userSID.
func metadataTestRecord(userSID string) *windowssandbox.InstallationRecord {
	alias := "C:\\managed\\alias.exe"
	return &windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: `C:\Users\owner`,
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{{
				Account:   windowssandbox.SandboxRuntimeAccountOffline,
				UserSID:   userSID,
				AliasPath: &alias,
			}},
		},
	}
}

// metadataTestRoots points the two AppData roots at fresh temp directories.
func metadataTestRoots(t *testing.T) []string {
	t.Helper()
	local := t.TempDir()
	roaming := t.TempDir()
	previousKnown := runtimeKnownFolderPath
	previousSave := runtimeSaveMetadataRecord
	t.Cleanup(func() {
		runtimeKnownFolderPath = previousKnown
		runtimeSaveMetadataRecord = previousSave
	})
	runtimeKnownFolderPath = func(_ windows.Token, folder *windows.GUID, _ uint32) (string, error) {
		switch *folder {
		case knownFolderLocalAppData:
			return local, nil
		case knownFolderRoamingAppData:
			return roaming, nil
		default:
			return "", errors.New("unexpected Known Folder")
		}
	}
	runtimeSaveMetadataRecord = func(*windowssandbox.InstallationRecord) error { return nil }
	return []string{local, roaming}
}

func metadataACECount(t *testing.T, root string, sidString string) int {
	t.Helper()
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatalf("encode %s: %v", root, err)
	}
	handle, err := windows.CreateFile(
		rootPtr,
		windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		t.Fatalf("open %s: %v", root, err)
	}
	defer windows.CloseHandle(handle)
	acl, err := readRuntimeMetadataACL(handle)
	if err != nil {
		t.Fatalf("readRuntimeMetadataACL(%s) error = %v", root, err)
	}
	if acl == nil {
		t.Fatalf("%s has no DACL", root)
	}
	sid, err := windows.StringToSid(sidString)
	if err != nil {
		t.Fatalf("parse SID %s: %v", sidString, err)
	}
	matches := 0
	pointer := acl.pointer()
	winACL, err := acl.windowsACL()
	if err != nil {
		t.Fatalf("windowsACL() error = %v", err)
	}
	for index := uint32(0); index < uint32(pointer.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(winACL, index, &ace); err != nil {
			t.Fatalf("GetAce(%d) error = %v", index, err)
		}
		if ace.Header.AceType != 0 || ace.Header.AceFlags != 0 || ace.Mask != metadataMask {
			continue
		}
		if windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), sid) {
			matches++
		}
	}
	return matches
}

// TestGrantRuntimeMetadataInsertsOneExactACEAndRemoveDropsItLikeRust edits real
// directory DACLs: one explicit FILE_READ_ATTRIBUTES ACE per root, inserted
// ahead of inherited entries, granted idempotently and removed again.
func TestGrantRuntimeMetadataInsertsOneExactACEAndRemoveDropsItLikeRust(t *testing.T) {
	token := metadataTestToken(t)
	sid := metadataTestSID(t)
	roots := metadataTestRoots(t)
	record := metadataTestRecord(sid)

	if err := GrantRuntimeMetadata(token, sid, record); err != nil {
		t.Fatalf("GrantRuntimeMetadata() error = %v", err)
	}
	for _, root := range roots {
		if count := metadataACECount(t, root, sid); count != 1 {
			t.Fatalf("%s metadata ACE count = %d", root, count)
		}
	}
	if len(record.Runtime.MetadataRoots) != 2 {
		t.Fatalf("metadata roots = %v", record.Runtime.MetadataRoots)
	}

	if err := GrantRuntimeMetadata(token, sid, record); err != nil {
		t.Fatalf("second GrantRuntimeMetadata() error = %v", err)
	}
	for _, root := range roots {
		if count := metadataACECount(t, root, sid); count != 1 {
			t.Fatalf("%s metadata ACE count after re-grant = %d", root, count)
		}
	}
	if len(record.Runtime.MetadataRoots) != 2 {
		t.Fatalf("metadata roots after re-grant = %v", record.Runtime.MetadataRoots)
	}

	if err := RemoveRuntimeMetadata(token, record); err != nil {
		t.Fatalf("RemoveRuntimeMetadata() error = %v", err)
	}
	for _, root := range roots {
		if count := metadataACECount(t, root, sid); count != 0 {
			t.Fatalf("%s metadata ACE count after removal = %d", root, count)
		}
	}
	if err := RemoveRuntimeMetadata(token, record); err != nil {
		t.Fatalf("second RemoveRuntimeMetadata() error = %v", err)
	}
}

func TestGrantRuntimeMetadataRequiresRecordedAccountLikeRust(t *testing.T) {
	token := metadataTestToken(t)
	metadataTestRoots(t)
	record := metadataTestRecord("S-1-5-21-9-9-9-1001")
	err := GrantRuntimeMetadata(token, "S-1-5-21-9-9-9-2002", record)
	if err == nil || err.Error() != "AppData metadata access requires a recorded managed account" {
		t.Fatalf("GrantRuntimeMetadata(unrecorded account) error = %v", err)
	}
}

func TestGrantRuntimeMetadataRecordsRootBeforeGrantingLikeRust(t *testing.T) {
	token := metadataTestToken(t)
	sid := metadataTestSID(t)
	roots := metadataTestRoots(t)
	record := metadataTestRecord(sid)
	failure := errors.New("persist provenance")
	runtimeSaveMetadataRecord = func(*windowssandbox.InstallationRecord) error { return failure }
	if err := GrantRuntimeMetadata(token, sid, record); !errors.Is(err, failure) {
		t.Fatalf("GrantRuntimeMetadata() error = %v", err)
	}
	for _, root := range roots {
		if count := metadataACECount(t, root, sid); count != 0 {
			t.Fatalf("%s metadata ACE count without provenance = %d", root, count)
		}
	}
}

func TestRemoveRuntimeMetadataSkipsMissingRootsLikeRust(t *testing.T) {
	token := metadataTestToken(t)
	sid := metadataTestSID(t)
	metadataTestRoots(t)
	record := metadataTestRecord(sid)
	record.Runtime.MetadataRoots = []string{filepath.Join(t.TempDir(), "removed-root")}
	if err := RemoveRuntimeMetadata(token, record); err != nil {
		t.Fatalf("RemoveRuntimeMetadata(missing root) error = %v", err)
	}
}

func TestRemoveRuntimeMetadataReportsForeignFailuresLikeRust(t *testing.T) {
	token := metadataTestToken(t)
	sid := metadataTestSID(t)
	metadataTestRoots(t)
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	record := metadataTestRecord(sid)
	record.Runtime.MetadataRoots = []string{file}
	err := RemoveRuntimeMetadata(token, record)
	if err == nil || !strings.HasPrefix(err.Error(), "AppData metadata cleanup: ") {
		t.Fatalf("RemoveRuntimeMetadata(file) error = %v", err)
	}
}
