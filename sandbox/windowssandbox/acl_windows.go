//go:build windows

package windowssandbox

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fileDeleteChild = 0x00000040
	fileAllAccess   = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

	denyReadMask  = windows.FILE_GENERIC_READ | windows.GENERIC_READ
	denyWriteMask = windows.FILE_GENERIC_WRITE |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_EA |
		windows.FILE_WRITE_ATTRIBUTES |
		windows.GENERIC_WRITE |
		windows.DELETE |
		fileDeleteChild
	allowWriteMask = windows.FILE_GENERIC_READ |
		windows.FILE_GENERIC_WRITE |
		windows.FILE_GENERIC_EXECUTE |
		windows.DELETE |
		fileDeleteChild
)

type aclACEKind int

const (
	aclACEKindDenyRead aclACEKind = iota
	aclACEKindDenyWrite
	aclACEKindAllowWrite
)

func AddDenyReadACE(req ACLRequest) error {
	_, err := addACLACE(req, aclACEKindDenyRead)
	return err
}

func AddDenyWriteACE(req ACLRequest) error {
	_, err := addACLACE(req, aclACEKindDenyWrite)
	return err
}

func EnsureAllowWriteACEs(req ACLRequest) error {
	_, err := addACLACE(req, aclACEKindAllowWrite)
	return err
}

func EnsureAllowMaskACEsWithInheritance(req ACLRequest, inheritance uint32) error {
	_, err := addAllowMaskACE(req, inheritance)
	return err
}

// GrantAllowMaskACEsWithInheritance adds missing read/execute grants without
// replacing existing ACEs. Any deny ACE for the same mask is preserved instead
// of introducing an allow that could outrank an inherited deny. It reports
// whether the DACL changed (Rust `ensure_allow_mask_aces_with_inheritance`
// returns the same flag so callers only revalidate after an update).
func GrantAllowMaskACEsWithInheritance(req ACLRequest, inheritance uint32) (bool, error) {
	return grantAllowMaskACE(req, inheritance)
}

// GrantReadExecuteACEs adds read/execute permissions without replacing existing
// ACEs or overriding any deny ACE. It reports whether the DACL changed.
func GrantReadExecuteACEs(req ACLRequest, inheritance uint32) (bool, error) {
	req.Mask = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE
	return GrantAllowMaskACEsWithInheritance(req, inheritance)
}

func PathMaskAllows(req ACLRequest, requireAllBits bool) (bool, error) {
	path, sidBytes, sid, err := validateACLRequest(req)
	if err != nil {
		return false, err
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		return false, err
	}
	defer runtime.KeepAlive(sidBytes)
	defer runtime.KeepAlive(sd)
	if requireAllBits {
		return daclAllowsMaskForSID(dacl, sid, req.Mask), nil
	}
	return daclAllowsAnyMaskForSID(dacl, sid, req.Mask), nil
}

// PathMaskDenied reports whether an explicit deny ACE for the request's SID
// covers any bit of the request mask (Rust `dacl_has_deny_mask`). Callers use
// it to prove a denial survived a repair that only adds allow ACEs.
func PathMaskDenied(req ACLRequest) (bool, error) {
	path, sidBytes, sid, err := validateACLRequest(req)
	if err != nil {
		return false, err
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		return false, err
	}
	defer runtime.KeepAlive(sidBytes)
	defer runtime.KeepAlive(sd)
	return daclHasDeniedMaskForSID(dacl, sid, req.Mask), nil
}

func addACLACE(req ACLRequest, kind aclACEKind) (bool, error) {
	path, sidBytes, sid, err := validateACLRequest(req)
	if err != nil {
		return false, err
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		return false, err
	}

	if aclAlreadyHasACE(dacl, sid, kind) {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return false, nil
	}

	entry := explicitAccessForSID(sid, kind, req.Mask)
	newACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return false, err
	}
	if err := setDACL(path, newACL); err != nil {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		runtime.KeepAlive(newACL)
		runtime.KeepAlive(entry)
		return false, err
	}
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
	runtime.KeepAlive(newACL)
	runtime.KeepAlive(entry)
	return true, nil
}

func addAllowMaskACE(req ACLRequest, inheritance uint32) (bool, error) {
	if req.Mask == 0 {
		return false, ErrInvalidRequest
	}
	path, sidBytes, sid, err := validateACLRequest(req)
	if err != nil {
		return false, err
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		return false, err
	}
	if daclAllowsMaskForSID(dacl, sid, req.Mask) {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return false, nil
	}
	entry := explicitAllowAccessForSID(sid, req.Mask, inheritance)
	newACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return false, err
	}
	if err := setDACL(path, newACL); err != nil {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		runtime.KeepAlive(newACL)
		runtime.KeepAlive(entry)
		return false, err
	}
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
	runtime.KeepAlive(newACL)
	runtime.KeepAlive(entry)
	return true, nil
}

func grantAllowMaskACE(req ACLRequest, inheritance uint32) (bool, error) {
	if req.Mask == 0 {
		return false, ErrInvalidRequest
	}
	path, sidBytes, sid, err := validateACLRequest(req)
	if err != nil {
		return false, err
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		return false, err
	}
	defer runtime.KeepAlive(sidBytes)
	defer runtime.KeepAlive(sd)
	if daclAllowsMaskForSID(dacl, sid, req.Mask) {
		return false, nil
	}
	if daclHasAnyDenyMask(dacl, req.Mask|windows.GENERIC_READ|windows.GENERIC_WRITE|windows.GENERIC_EXECUTE) {
		return false, nil
	}
	// A root-only grant must never turn a null DACL (which already allows
	// everyone) into a restrictive one (Rust `inheritance == 0 && p_dacl.is_null()`).
	if dacl == nil && inheritance == 0 {
		return false, nil
	}
	entry := explicitGrantAccessForSID(sid, req.Mask, inheritance)
	newACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		return false, err
	}
	if err := setDACL(path, newACL); err != nil {
		runtime.KeepAlive(newACL)
		runtime.KeepAlive(entry)
		return false, err
	}
	runtime.KeepAlive(newACL)
	runtime.KeepAlive(entry)
	return true, nil
}

func validateACLRequest(req ACLRequest) (string, []byte, *windows.SID, error) {
	path := strings.TrimSpace(req.Path)
	if path == "" || strings.TrimSpace(req.SID) == "" {
		return "", nil, nil, ErrInvalidRequest
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, nil, err
	}
	if info == nil {
		return "", nil, nil, ErrInvalidRequest
	}
	sidBytes, err := SIDBytesFromString(req.SID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	sid := sidPointerFromBytes(sidBytes)
	if sid == nil || !sid.IsValid() {
		return "", nil, nil, ErrInvalidRequest
	}
	return path, sidBytes, sid, nil
}

// fileDACL reads the DACL through a handle instead of the named-object API so
// runtime paths beyond the legacy Windows path limit stay readable (Rust
// `acl::fetch_dacl_handle`, which opens the target with `OpenOptions`).
func fileDACL(path string) (*windows.SECURITY_DESCRIPTOR, *windows.ACL, error) {
	handle, err := openACLHandle(path, windows.READ_CONTROL, "open ACL target (READ_CONTROL)")
	if err != nil {
		return nil, nil, err
	}
	defer windows.CloseHandle(handle)
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, nil, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		if err == windows.ERROR_OBJECT_NOT_FOUND {
			return sd, nil, nil
		}
		return nil, nil, err
	}
	return sd, dacl, nil
}

// setDACL replaces the DACL through a handle that requests ACL-write access, so
// the update also works beyond the legacy Windows path limit. Only callers that
// actually need to change the DACL reach this, which keeps no-op repairs
// read-only (Rust `acl::ensure_allow_mask_aces_with_inheritance_impl`).
func setDACL(path string, dacl *windows.ACL) error {
	handle, err := openACLHandle(path, windows.READ_CONTROL|windows.WRITE_DAC, "open ACL target for update (READ_CONTROL | WRITE_DAC)")
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// openACLHandle opens a path for ACL reads and updates. Requesting the security
// rights directly on the handle (with backup semantics so directories open too)
// is what lets the handle-based security APIs repair long runtime paths.
//
// Deliberately never MAXIMUM_ALLOWED (Rust #51822): that access mask conflicts
// with a runtime file another process holds open with FILE_SHARE_READ, so
// repairing an active node.exe failed with a sharing violation.
func openACLHandle(path string, access uint32, operation string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(extendedACLPath(path))
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("%s %s: %w", operation, path, err)
	}
	handle, err := windows.CreateFile(
		name,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("%s %s: %w", operation, path, err)
	}
	return handle, nil
}

func explicitAccessForSID(sid *windows.SID, kind aclACEKind, overrideMask uint32) windows.EXPLICIT_ACCESS {
	var mode windows.ACCESS_MODE = windows.SET_ACCESS
	mask := uint32(allowWriteMask)
	if kind == aclACEKindDenyRead || kind == aclACEKindDenyWrite {
		mode = windows.DENY_ACCESS
		if kind == aclACEKindDenyRead {
			mask = denyReadMask
		} else {
			mask = denyWriteMask
		}
	}
	if overrideMask != 0 {
		mask = overrideMask
	}
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(mask),
		AccessMode:        mode,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func explicitAllowAccessForSID(sid *windows.SID, mask uint32, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(mask),
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func explicitGrantAccessForSID(sid *windows.SID, mask uint32, inheritance uint32) windows.EXPLICIT_ACCESS {
	entry := explicitAllowAccessForSID(sid, mask, inheritance)
	entry.AccessMode = windows.GRANT_ACCESS
	return entry
}

func aclAlreadyHasACE(dacl *windows.ACL, sid *windows.SID, kind aclACEKind) bool {
	if dacl == nil {
		return false
	}
	switch kind {
	case aclACEKindDenyRead:
		return daclHasDeniedMaskForSID(dacl, sid, denyReadMask)
	case aclACEKindDenyWrite:
		return daclHasDeniedMaskForSID(dacl, sid, denyWriteMask)
	case aclACEKindAllowWrite:
		return daclAllowsMaskForSID(dacl, sid, allowWriteMask)
	default:
		return false
	}
}

func daclAllowsMaskForSID(dacl *windows.ACL, sid *windows.SID, desiredMask uint32) bool {
	return scanDACL(dacl, func(ace *windows.ACCESS_ALLOWED_ACE) bool {
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || isInheritOnlyACE(ace) {
			return false
		}
		if !windows.EqualSid(aceSID(ace), sid) {
			return false
		}
		mask := mapFileGenericMask(uint32(ace.Mask))
		return mask&desiredMask == desiredMask
	})
}

func daclAllowsAnyMaskForSID(dacl *windows.ACL, sid *windows.SID, desiredMask uint32) bool {
	return scanDACL(dacl, func(ace *windows.ACCESS_ALLOWED_ACE) bool {
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || isInheritOnlyACE(ace) {
			return false
		}
		if !windows.EqualSid(aceSID(ace), sid) {
			return false
		}
		mask := mapFileGenericMask(uint32(ace.Mask))
		return mask&desiredMask != 0
	})
}

func daclHasDeniedMaskForSID(dacl *windows.ACL, sid *windows.SID, desiredMask uint32) bool {
	return scanDACL(dacl, func(ace *windows.ACCESS_ALLOWED_ACE) bool {
		if ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE || isInheritOnlyACE(ace) {
			return false
		}
		if !windows.EqualSid(aceSID(ace), sid) {
			return false
		}
		mask := mapFileGenericMask(uint32(ace.Mask))
		return mask&desiredMask != 0
	})
}

func daclHasAnyDenyMask(dacl *windows.ACL, desiredMask uint32) bool {
	return scanDACL(dacl, func(ace *windows.ACCESS_ALLOWED_ACE) bool {
		if ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			return false
		}
		return mapFileGenericMask(uint32(ace.Mask))&desiredMask != 0
	})
}

func scanDACL(dacl *windows.ACL, match func(*windows.ACCESS_ALLOWED_ACE) bool) bool {
	if dacl == nil {
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace == nil {
			continue
		}
		if match(ace) {
			return true
		}
	}
	return false
}

func isInheritOnlyACE(ace *windows.ACCESS_ALLOWED_ACE) bool {
	const inheritOnlyACE = 0x08
	return ace.Header.AceFlags&inheritOnlyACE != 0
}

func aceSID(ace *windows.ACCESS_ALLOWED_ACE) *windows.SID {
	return (*windows.SID)(unsafe.Pointer(&ace.SidStart))
}

func mapFileGenericMask(mask uint32) uint32 {
	if mask&windows.GENERIC_READ != 0 {
		mask = (mask &^ windows.GENERIC_READ) | windows.FILE_GENERIC_READ
	}
	if mask&windows.GENERIC_WRITE != 0 {
		mask = (mask &^ windows.GENERIC_WRITE) | windows.FILE_GENERIC_WRITE
	}
	if mask&windows.GENERIC_EXECUTE != 0 {
		mask = (mask &^ windows.GENERIC_EXECUTE) | windows.FILE_GENERIC_EXECUTE
	}
	if mask&windows.GENERIC_ALL != 0 {
		mask = (mask &^ windows.GENERIC_ALL) | fileAllAccess
	}
	return mask
}
