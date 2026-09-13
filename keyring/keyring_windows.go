//go:build windows

package keyring

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Rust parity: the `keyring` crate's Windows Credential Manager backend
// (credential type generic, target name "<account>.<service>", UTF-16LE secret
// blob, enterprise persistence). Matching the mapping keeps credentials written
// by one implementation readable by the other.

const (
	credTypeGeneric       = 1
	credPersistEnterprise = 3
	// errorNotFound is Win32 ERROR_NOT_FOUND, returned by CredReadW/CredDeleteW
	// when the target has no credential.
	errorNotFound           = syscall.Errno(1168)
	credWindowsCommentValue = "codex keyring store"
)

var (
	advapi32    = syscall.NewLazyDLL("advapi32.dll")
	credWriteW  = advapi32.NewProc("CredWriteW")
	credReadW   = advapi32.NewProc("CredReadW")
	credDeleteW = advapi32.NewProc("CredDeleteW")
	credFree    = advapi32.NewProc("CredFree")
)

// osKeyringAvailable reports whether the Windows credential store is usable.
const osKeyringAvailable = true

type osKeyringStore struct{}

type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

// credentialW mirrors Win32 CREDENTIALW.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func (osKeyringStore) Save(service string, account string, secret string) error {
	if err := requireIdentifier(service, account); err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(WindowsTargetName(service, account))
	if err != nil {
		return err
	}
	user, err := syscall.UTF16PtrFromString(account)
	if err != nil {
		return err
	}
	comment, err := syscall.UTF16PtrFromString(credWindowsCommentValue)
	if err != nil {
		return err
	}
	blob := EncodeWindowsSecret(secret)
	var blobPointer *byte
	if len(blob) > 0 {
		blobPointer = &blob[0]
	}
	credential := credentialW{
		Type:               credTypeGeneric,
		TargetName:         target,
		Comment:            comment,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     blobPointer,
		Persist:            credPersistEnterprise,
		UserName:           user,
	}
	result, _, callErr := credWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return fmt.Errorf("CredWriteW failed: %w", callErr)
	}
	return nil
}

func (osKeyringStore) Load(service string, account string) (string, error) {
	if err := requireIdentifier(service, account); err != nil {
		return "", err
	}
	target, err := syscall.UTF16PtrFromString(WindowsTargetName(service, account))
	if err != nil {
		return "", err
	}
	var credential *credentialW
	result, _, callErr := credReadW.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&credential)),
	)
	if result == 0 {
		if callErr == errorNotFound {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("CredReadW failed: %w", callErr)
	}
	if credential == nil {
		return "", ErrNotFound
	}
	defer credFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential.CredentialBlobSize == 0 {
		return "", nil
	}
	blob := unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)
	return DecodeWindowsSecret(append([]byte(nil), blob...))
}

func (osKeyringStore) Delete(service string, account string) (bool, error) {
	if err := requireIdentifier(service, account); err != nil {
		return false, err
	}
	target, err := syscall.UTF16PtrFromString(WindowsTargetName(service, account))
	if err != nil {
		return false, err
	}
	result, _, callErr := credDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0)
	if result != 0 {
		return true, nil
	}
	if callErr == errorNotFound {
		return false, nil
	}
	return false, fmt.Errorf("CredDeleteW failed: %w", callErr)
}
