//go:build windows

package windowssandbox

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

func ApplyDenyReadACLs(paths []string, sid string) ([]string, error) {
	if sid == "" {
		return nil, ErrInvalidRequest
	}
	planned := PlanDenyReadACLPaths(paths)
	applied := make([]string, 0, len(planned))
	addedInThisCall := make([]string, 0, len(planned))
	seenApplied := map[string]bool{}
	for _, path := range planned {
		added, err := applyDenyReadACLPath(path, sid)
		if err != nil {
			for _, addedPath := range addedInThisCall {
				_ = RevokeACE(ACLRequest{Path: addedPath, SID: sid})
			}
			return nil, err
		}
		if added {
			addedInThisCall = append(addedInThisCall, path)
		}
		pushPlannedDenyReadPath(&applied, seenApplied, path)
	}
	return applied, nil
}

func applyDenyReadACLPath(path string, sid string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return false, fmt.Errorf("create deny-read path %s: %w", path, err)
		}
	}
	added, err := addACLACE(ACLRequest{Path: path, SID: sid}, aclACEKindDenyRead)
	if err != nil {
		return false, fmt.Errorf("apply deny-read ACE to %s: %w", path, err)
	}
	return added, nil
}

func RevokeACE(req ACLRequest) error {
	path, sidBytes, sid, err := validateACLRequest(req)
	if err != nil {
		return err
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		return err
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessMode:  windows.REVOKE_ACCESS,
		Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
	newACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, newACL, nil)
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
	runtime.KeepAlive(newACL)
	runtime.KeepAlive(entry)
	return err
}
