//go:build windows

package windowssandbox

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

const (
	restrictedTokenDisableMaxPrivilege = 0x01
	restrictedTokenLUAToken            = 0x04
	restrictedTokenWriteRestricted     = 0x08
)

func ConvertStringSIDToSID(value string) (*LocalSID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ErrInvalidRequest
	}
	bytes, err := SIDBytesFromString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return &LocalSID{
		String: StringFromSIDBytes(bytes),
		Bytes:  bytes,
	}, nil
}

func GetCurrentTokenForRestriction() (uintptr, error) {
	const access = windows.TOKEN_DUPLICATE |
		windows.TOKEN_QUERY |
		windows.TOKEN_ASSIGN_PRIMARY |
		windows.TOKEN_ADJUST_DEFAULT |
		windows.TOKEN_ADJUST_SESSIONID |
		windows.TOKEN_ADJUST_PRIVILEGES

	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), access, &token); err != nil {
		return 0, err
	}
	return uintptr(token), nil
}

func CloseTokenHandle(token uintptr) error {
	if token == 0 {
		return nil
	}
	return windows.Token(token).Close()
}

func CreateReadonlyTokenWithCapsFrom(token uintptr, caps []string) (uintptr, error) {
	return createTokenWithCapsFrom(windows.Token(token), caps)
}

func CreateWorkspaceWriteTokenWithCapsFrom(token uintptr, caps []string) (uintptr, error) {
	return createTokenWithCapsFrom(windows.Token(token), caps)
}

func CreateReadonlyTokenWithCapsAndUserFrom(token uintptr, caps []string) (uintptr, error) {
	return createTokenWithCapsAndUserFrom(windows.Token(token), caps)
}

func CreateWorkspaceWriteTokenWithCapsAndUserFrom(token uintptr, caps []string) (uintptr, error) {
	return createTokenWithCapsAndUserFrom(windows.Token(token), caps)
}

func createTokenWithCapsFrom(baseToken windows.Token, caps []string) (uintptr, error) {
	return createTokenWithCapsAndExtraSIDsFrom(baseToken, caps, nil)
}

func createTokenWithCapsAndUserFrom(baseToken windows.Token, caps []string) (uintptr, error) {
	userSID, err := getUserSIDBytes(baseToken)
	if err != nil {
		return 0, err
	}
	return createTokenWithCapsAndExtraSIDsFrom(baseToken, caps, [][]byte{userSID})
}

func createTokenWithCapsAndExtraSIDsFrom(baseToken windows.Token, caps []string, extraRestrictingSIDs [][]byte) (uintptr, error) {
	if baseToken == 0 || len(caps) == 0 {
		return 0, ErrInvalidRequest
	}
	capBuffers, capEntries, err := sidAttributesFromStrings(caps)
	if err != nil {
		return 0, err
	}
	logonSID, err := getLogonSIDBytes(baseToken)
	if err != nil {
		return 0, err
	}
	everyoneSID, err := worldSIDBytes()
	if err != nil {
		return 0, err
	}

	restricting := make([]windows.SIDAndAttributes, 0, len(capEntries)+len(extraRestrictingSIDs)+2)
	restricting = append(restricting, capEntries...)
	for _, sidBytes := range extraRestrictingSIDs {
		restricting = append(restricting, windows.SIDAndAttributes{Sid: sidPointerFromBytes(sidBytes)})
	}
	restricting = append(restricting,
		windows.SIDAndAttributes{Sid: sidPointerFromBytes(logonSID)},
		windows.SIDAndAttributes{Sid: sidPointerFromBytes(everyoneSID)},
	)

	newToken, err := createRestrictedTokenWithRequiredFlags(baseToken, restricting)
	if err != nil {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = newToken.Close()
		}
	}()

	daclSIDs := make([]*windows.SID, 0, len(capEntries)+2)
	daclSIDs = append(daclSIDs, sidPointerFromBytes(logonSID), sidPointerFromBytes(everyoneSID))
	for _, entry := range capEntries {
		daclSIDs = append(daclSIDs, entry.Sid)
	}
	if err := setTokenDefaultDACL(newToken, daclSIDs); err != nil {
		return 0, fmt.Errorf("SetTokenInformation(TokenDefaultDacl): %w", err)
	}
	if err := enableSinglePrivilege(newToken, "SeChangeNotifyPrivilege"); err != nil {
		return 0, fmt.Errorf("AdjustTokenPrivileges(SeChangeNotifyPrivilege): %w", err)
	}
	runtimeKeepAliveSIDBuffers(capBuffers, logonSID, everyoneSID)
	for _, sidBytes := range extraRestrictingSIDs {
		runtime.KeepAlive(sidBytes)
	}
	ok = true
	return uintptr(newToken), nil
}

type sidBuffer struct {
	bytes []byte
	sid   *windows.SID
}

func sidAttributesFromStrings(values []string) ([]sidBuffer, []windows.SIDAndAttributes, error) {
	buffers := make([]sidBuffer, 0, len(values))
	attrs := make([]windows.SIDAndAttributes, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, nil, ErrInvalidRequest
		}
		bytes, err := SIDBytesFromString(value)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		sid := sidPointerFromBytes(bytes)
		if sid == nil || !sid.IsValid() {
			return nil, nil, ErrInvalidRequest
		}
		buffers = append(buffers, sidBuffer{bytes: bytes, sid: sid})
		attrs = append(attrs, windows.SIDAndAttributes{Sid: sid})
	}
	return buffers, attrs, nil
}

func sidPointerFromBytes(bytes []byte) *windows.SID {
	if len(bytes) == 0 {
		return nil
	}
	return (*windows.SID)(unsafe.Pointer(&bytes[0]))
}

func createRestrictedToken(baseToken windows.Token, flags uint32, restricting []windows.SIDAndAttributes) (windows.Token, error) {
	if len(restricting) == 0 {
		return 0, ErrInvalidRequest
	}
	var newToken windows.Token
	r1, _, e1 := procCreateRestrictedToken.Call(
		uintptr(baseToken),
		uintptr(flags),
		0,
		0,
		0,
		0,
		uintptr(len(restricting)),
		uintptr(unsafe.Pointer(&restricting[0])),
		uintptr(unsafe.Pointer(&newToken)),
	)
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return 0, e1
		}
		return 0, windows.GetLastError()
	}
	return newToken, nil
}

func createRestrictedTokenWithRequiredFlags(baseToken windows.Token, restricting []windows.SIDAndAttributes) (windows.Token, error) {
	flags := uint32(restrictedTokenDisableMaxPrivilege | restrictedTokenLUAToken | restrictedTokenWriteRestricted)
	token, err := createRestrictedToken(baseToken, flags, restricting)
	if err == nil {
		return token, nil
	}
	if err == windows.ERROR_INVALID_PARAMETER {
		return 0, fmt.Errorf("%w: WRITE_RESTRICTED CreateRestrictedToken rejected: %w", ErrHostUnsupported, err)
	}
	return 0, err
}

func getLogonSIDBytes(token windows.Token) ([]byte, error) {
	if sid, ok := scanTokenGroupsForLogonSID(token); ok {
		return sid, nil
	}
	linked, err := token.GetLinkedToken()
	if err == nil && linked != 0 {
		defer linked.Close()
		if sid, ok := scanTokenGroupsForLogonSID(linked); ok {
			return sid, nil
		}
	}
	return nil, fmt.Errorf("%w: token logon SID not present", ErrInvalidRequest)
}

func getUserSIDBytes(token windows.Token) ([]byte, error) {
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return nil, fmt.Errorf("%w: token user SID not present", ErrInvalidRequest)
	}
	return copySIDBytes(user.User.Sid), nil
}

func scanTokenGroupsForLogonSID(token windows.Token) ([]byte, bool) {
	groups, err := token.GetTokenGroups()
	if err != nil {
		return nil, false
	}
	for _, group := range groups.AllGroups() {
		if (group.Attributes & windows.SE_GROUP_LOGON_ID) != windows.SE_GROUP_LOGON_ID {
			continue
		}
		bytes := copySIDBytes(group.Sid)
		if len(bytes) != 0 {
			return bytes, true
		}
	}
	return nil, false
}

func worldSIDBytes() ([]byte, error) {
	sid, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return nil, err
	}
	return copySIDBytes(sid), nil
}

type tokenDefaultDACLInfo struct {
	DefaultDACL *windows.ACL
}

func setTokenDefaultDACL(token windows.Token, sids []*windows.SID) error {
	if len(sids) == 0 {
		return nil
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(sids))
	for _, sid := range sids {
		if sid == nil || !sid.IsValid() {
			return ErrInvalidRequest
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	info := tokenDefaultDACLInfo{DefaultDACL: acl}
	err = windows.SetTokenInformation(
		token,
		windows.TokenDefaultDacl,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	runtimeKeepAliveACL(acl, entries, sids)
	return err
}

func enableSinglePrivilege(token windows.Token, name string) error {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, namePtr, &luid); err != nil {
		return err
	}
	privileges := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{{
			Luid:       luid,
			Attributes: windows.SE_PRIVILEGE_ENABLED,
		}},
	}
	if err := windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil); err != nil {
		return err
	}
	if err := windows.GetLastError(); err != nil {
		return err
	}
	return nil
}

func runtimeKeepAliveSIDBuffers(capBuffers []sidBuffer, logonSID []byte, everyoneSID []byte) {
	for _, buffer := range capBuffers {
		runtime.KeepAlive(buffer.sid)
		runtime.KeepAlive(buffer.bytes)
	}
	runtime.KeepAlive(logonSID)
	runtime.KeepAlive(everyoneSID)
}

func runtimeKeepAliveACL(acl *windows.ACL, entries []windows.EXPLICIT_ACCESS, sids []*windows.SID) {
	runtime.KeepAlive(acl)
	runtime.KeepAlive(entries)
	runtime.KeepAlive(sids)
}
