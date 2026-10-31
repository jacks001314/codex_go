//go:build windows

package windowsandboxservice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

const (
	// metadataMask is FILE_READ_ATTRIBUTES: no listing, no contents and no
	// inheritance, which is what packaged Known Folder lookups verify.
	metadataMask = windows.FILE_READ_ATTRIBUTES
	// inheritedACE separates explicit ACEs from ACL entries Windows added.
	inheritedACE = 0x10
	// daclSecurityInformation writes only the DACL of the retained handle.
	daclSecurityInformation = 0x00000004
	// The preserved control bits keep the owner's DACL protection state.
	seDACLDefaulted     = 0x0008
	seDACLAutoInherited = 0x0400
	seDACLProtected     = 0x1000
	// metadataACLRevisionMin and metadataACLRevisionDS bound the revisions the
	// editor understands (ACL_REVISION .. ACL_REVISION_DS).
	metadataACLRevisionMin = 2
	metadataACLRevisionDS  = 4
	// metadataRootAccess is what editing one owner-authorized root needs.
	metadataRootAccess = windows.READ_CONTROL | windows.WRITE_DAC | windows.FILE_READ_ATTRIBUTES | windows.FILE_TRAVERSE
)

var (
	modRuntimeAdvapi32               = windows.NewLazySystemDLL("advapi32.dll")
	modRuntimeNtdll                  = windows.NewLazySystemDLL("ntdll.dll")
	procInitializeSecurityDescriptor = modRuntimeAdvapi32.NewProc("InitializeSecurityDescriptor")
	procSetSecurityDescriptorDacl    = modRuntimeAdvapi32.NewProc("SetSecurityDescriptorDacl")
	procSetSecurityDescriptorControl = modRuntimeAdvapi32.NewProc("SetSecurityDescriptorControl")
	procNtSetSecurityObject          = modRuntimeNtdll.NewProc("NtSetSecurityObject")
	procRtlNtStatusToDosError        = modRuntimeNtdll.NewProc("RtlNtStatusToDosError")
)

// runtimeSecurityDescriptor is the absolute SECURITY_DESCRIPTOR layout.
type runtimeSecurityDescriptor struct {
	Revision byte
	Sbz1     byte
	Control  uint16
	padding  uint32
	Owner    uintptr
	Group    uintptr
	Sacl     uintptr
	Dacl     uintptr
}

// runtimeACLHeader is the ACL layout x/sys keeps unexported.
type runtimeACLHeader struct {
	Revision uint8
	Sbz1     uint8
	Size     uint16
	AceCount uint16
	Sbz2     uint16
}

// runtimeMetadataACL is a mutable copy of one directory DACL.
type runtimeMetadataACL struct {
	bytes   []byte
	control uint16
}

// pointer exposes the copied DACL header.
func (acl *runtimeMetadataACL) pointer() *runtimeACLHeader {
	if acl == nil || len(acl.bytes) < int(unsafe.Sizeof(runtimeACLHeader{})) {
		return nil
	}
	return (*runtimeACLHeader)(unsafe.Pointer(&acl.bytes[0]))
}

// windowsACL exposes the copied DACL to the Win32 ACE helpers.
func (acl *runtimeMetadataACL) windowsACL() (*windows.ACL, error) {
	pointer := acl.pointer()
	if pointer == nil {
		return nil, errors.New("missing AppData DACL")
	}
	if pointer.Size != uint16(len(acl.bytes)) || pointer.Size < uint16(unsafe.Sizeof(runtimeACLHeader{})) {
		return nil, errors.New("invalid AppData DACL size")
	}
	return (*windows.ACL)(unsafe.Pointer(pointer)), nil
}

// runtimeKnownFolderPath is the seam grant/remove resolve roots through.
var runtimeKnownFolderPath = knownFolderPath

// runtimeSaveMetadataRecord persists provenance before any ACE mutation.
var runtimeSaveMetadataRecord = windowssandbox.SaveInstallation

// GrantRuntimeMetadata gives the managed account metadata access to the
// owner's two AppData roots (Rust registered_runtime/metadata.rs::grant). No
// file contents or descendants are granted, and each owner-authorized root is
// recorded before its ACE is written.
func GrantRuntimeMetadata(ownerToken windows.Token, userSID string, record *windowssandbox.InstallationRecord) error {
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return err
	}
	recorded := false
	for _, account := range runtime.Accounts {
		if account.UserSID == userSID {
			recorded = true
			break
		}
	}
	if !recorded {
		return errors.New("AppData metadata access requires a recorded managed account")
	}
	sid, err := windows.StringToSid(userSID)
	if err != nil {
		return err
	}
	for _, folder := range []*windows.GUID{&knownFolderLocalAppData, &knownFolderRoamingAppData} {
		path, err := runtimeKnownFolderPath(ownerToken, folder, ownerFolderFlags)
		if err != nil {
			return err
		}
		if err := editRuntimeMetadataRootACL(ownerToken, path, func(acl *runtimeMetadataACL) (bool, error) {
			if !metadataRootRecorded(record, path) {
				runtime.MetadataRoots = append(runtime.MetadataRoots, path)
				if err := runtimeSaveMetadataRecord(record); err != nil {
					return false, err
				}
			}
			if _, found, err := runtimeMetadataACEIndex(acl, sid); err != nil {
				return false, err
			} else if found {
				return false, nil
			}
			if err := insertRuntimeMetadataACE(acl, sid); err != nil {
				return false, err
			}
			return true, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// RemoveRuntimeMetadata drops the exact grants from every recorded root before
// the accounts are deleted (Rust metadata.rs::remove). A root that no longer
// exists is skipped; every other failure is reported.
func RemoveRuntimeMetadata(ownerToken windows.Token, record *windowssandbox.InstallationRecord) error {
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return err
	}
	if len(runtime.Accounts) == 0 {
		return nil
	}
	var failures []string
	for _, path := range runtime.MetadataRoots {
		err := editRuntimeMetadataRootACL(ownerToken, path, func(acl *runtimeMetadataACL) (bool, error) {
			changed := false
			for _, account := range runtime.Accounts {
				sid, err := windows.StringToSid(account.UserSID)
				if err != nil {
					return false, err
				}
				for {
					index, found, err := runtimeMetadataACEIndex(acl, sid)
					if err != nil {
						return false, err
					}
					if !found {
						break
					}
					if err := deleteRuntimeMetadataACE(acl, index); err != nil {
						return false, err
					}
					changed = true
				}
			}
			return changed, nil
		})
		if err != nil && !metadataRootMissing(err) {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("AppData metadata cleanup: %s", strings.Join(failures, "; "))
	}
	return nil
}

func metadataRootRecorded(record *windowssandbox.InstallationRecord, path string) bool {
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return false
	}
	for _, root := range runtime.MetadataRoots {
		if root == path {
			return true
		}
	}
	return false
}

// metadataRootMissing mirrors Rust's io::ErrorKind::NotFound skip.
func metadataRootMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, syscall.ERROR_PATH_NOT_FOUND)
}

// editRuntimeMetadataRootACL opens one root as the owner and edits its DACL in
// place; a null DACL already allows metadata and is never replaced.
func editRuntimeMetadataRootACL(
	ownerToken windows.Token,
	path string,
	edit func(*runtimeMetadataACL) (bool, error),
) error {
	var pins []windows.Handle
	var handle windows.Handle
	err := withOwnerImpersonation(ownerToken, func() error {
		if err := pinExistingAncestors(path, &pins); err != nil {
			return err
		}
		opened, err := windowssandbox.OpenDirectoryNoReparse(
			path,
			metadataRootAccess,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			windowssandbox.DirectoryOpenExisting,
		)
		if err != nil {
			return err
		}
		handle = opened
		return nil
	})
	defer closeHandles(pins)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	acl, err := readRuntimeMetadataACL(handle)
	if err != nil {
		return err
	}
	if acl == nil {
		return nil
	}
	changed, err := edit(acl)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return writeRuntimeMetadataACL(handle, acl)
}

// readRuntimeMetadataACL copies one handle's DACL so it can be edited.
func readRuntimeMetadataACL(handle windows.Handle) (*runtimeMetadataACL, error) {
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, fmt.Errorf("read AppData DACL: %w", err)
	}
	// x/sys copies the descriptor onto the Go heap, so it is not LocalFree'd.
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return nil, fmt.Errorf("read AppData DACL: %w", err)
	}
	if dacl == nil {
		return nil, nil
	}
	header := (*runtimeACLHeader)(unsafe.Pointer(dacl))
	if header.Revision < metadataACLRevisionMin || header.Revision > metadataACLRevisionDS {
		return nil, errors.New("invalid AppData DACL")
	}
	size := int(header.Size)
	buffer := make([]byte, size)
	copy(buffer, unsafe.Slice((*byte)(unsafe.Pointer(dacl)), size))
	control, _, err := descriptor.Control()
	if err != nil {
		return nil, fmt.Errorf("read AppData DACL control: %w", err)
	}
	return &runtimeMetadataACL{bytes: buffer, control: uint16(control)}, nil
}

// runtimeMetadataACEIndex finds the exact explicit FILE_READ_ATTRIBUTES ACE.
func runtimeMetadataACEIndex(acl *runtimeMetadataACL, sid *windows.SID) (uint32, bool, error) {
	pointer := acl.pointer()
	winACL, err := acl.windowsACL()
	if err != nil {
		return 0, false, err
	}
	if pointer == nil {
		return 0, false, errors.New("missing AppData DACL")
	}
	for index := uint32(0); index < uint32(pointer.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(winACL, index, &ace); err != nil {
			return 0, false, fmt.Errorf("read AppData ACE: %w", err)
		}
		if ace == nil {
			return 0, false, errors.New("read AppData ACE: empty ACE")
		}
		if ace.Header.AceType != 0 || ace.Header.AceFlags != 0 {
			continue
		}
		if ace.Mask != metadataMask {
			continue
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if aceSID.IsValid() && windows.EqualSid(aceSID, sid) {
			return index, true, nil
		}
	}
	return 0, false, nil
}

// insertRuntimeMetadataACE inserts the ACE ahead of any inherited entry.
func insertRuntimeMetadataACE(acl *runtimeMetadataACL, sid *windows.SID) error {
	pointer := acl.pointer()
	winACL, err := acl.windowsACL()
	if err != nil {
		return err
	}
	if pointer == nil {
		return errors.New("missing AppData DACL")
	}
	aceSize := 8 + int(windows.GetLengthSid(sid))
	size := int(pointer.Size)
	if size+aceSize > 0xFFFF {
		return errors.New("AppData DACL is full")
	}
	position := size
	for index := uint32(0); index < uint32(pointer.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(winACL, index, &ace); err != nil {
			return fmt.Errorf("read AppData ACE order: %w", err)
		}
		if ace == nil {
			return errors.New("read AppData ACE order: empty ACE")
		}
		if ace.Header.AceFlags&inheritedACE != 0 {
			position = int(uintptr(unsafe.Pointer(ace)) - uintptr(unsafe.Pointer(pointer)))
			break
		}
	}
	grown := make([]byte, size+aceSize)
	copy(grown, acl.bytes[:position])
	entry := grown[position : position+aceSize]
	entry[0] = 0
	entry[1] = 0
	binary.LittleEndian.PutUint16(entry[2:], uint16(aceSize))
	binary.LittleEndian.PutUint32(entry[4:], uint32(metadataMask))
	sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(sid)), int(windows.GetLengthSid(sid)))
	copy(entry[8:], sidBytes)
	copy(grown[position+aceSize:], acl.bytes[position:size])
	acl.bytes = grown
	updated := acl.pointer()
	updated.Size = uint16(len(grown))
	updated.AceCount++
	return nil
}

// deleteRuntimeMetadataACE removes one ACE and keeps the DACL compact.
func deleteRuntimeMetadataACE(acl *runtimeMetadataACL, index uint32) error {
	pointer := acl.pointer()
	winACL, err := acl.windowsACL()
	if err != nil {
		return err
	}
	if pointer == nil {
		return errors.New("missing AppData DACL")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(winACL, index, &ace); err != nil {
		return fmt.Errorf("read AppData ACE: %w", err)
	}
	if ace == nil {
		return errors.New("read AppData ACE: empty ACE")
	}
	size := int(pointer.Size)
	offset := int(uintptr(unsafe.Pointer(ace)) - uintptr(unsafe.Pointer(pointer)))
	aceSize := int(ace.Header.AceSize)
	if offset < 0 || aceSize <= 0 || offset+aceSize > size {
		return errors.New("AppData ACE exceeds ACL")
	}
	copied := copy(acl.bytes[offset:], acl.bytes[offset+aceSize:size])
	acl.bytes = acl.bytes[:offset+copied]
	updated := acl.pointer()
	updated.Size = uint16(len(acl.bytes))
	updated.AceCount--
	return nil
}

// writeRuntimeMetadataACL writes only this handle's DACL, preserving the
// owner's protection and auto-inheritance control bits without reapplying
// inheritable ACEs to children (Rust uses NtSetSecurityObject for the same
// reason).
func writeRuntimeMetadataACL(handle windows.Handle, acl *runtimeMetadataACL) error {
	dacl := acl.pointer()
	if dacl == nil {
		return errors.New("missing AppData DACL")
	}
	var descriptor runtimeSecurityDescriptor
	initialized, _, callErr := procInitializeSecurityDescriptor.Call(uintptr(unsafe.Pointer(&descriptor)), 1)
	if initialized == 0 {
		return fmt.Errorf("initialize AppData security descriptor: %w", callErr)
	}
	defaulted := uintptr(0)
	if acl.control&seDACLDefaulted != 0 {
		defaulted = 1
	}
	set, _, callErr := procSetSecurityDescriptorDacl.Call(
		uintptr(unsafe.Pointer(&descriptor)),
		1,
		uintptr(unsafe.Pointer(dacl)),
		defaulted,
	)
	if set == 0 {
		return fmt.Errorf("set AppData DACL: %w", callErr)
	}
	preserved := uint16(seDACLProtected | seDACLAutoInherited)
	applied, _, callErr := procSetSecurityDescriptorControl.Call(
		uintptr(unsafe.Pointer(&descriptor)),
		uintptr(preserved),
		uintptr(acl.control&preserved),
	)
	if applied == 0 {
		return fmt.Errorf("preserve AppData DACL control: %w", callErr)
	}
	status, _, _ := procNtSetSecurityObject.Call(
		uintptr(handle),
		daclSecurityInformation,
		uintptr(unsafe.Pointer(&descriptor)),
	)
	if int32(uint32(status)) < 0 {
		dos, _, _ := procRtlNtStatusToDosError.Call(status)
		return fmt.Errorf("write AppData DACL: %w", syscall.Errno(uint32(dos)))
	}
	return nil
}
