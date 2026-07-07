//go:build windows

package win

import (
	"fmt"
	"io"
	"unsafe"

	"codex_go/internal/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
)

const (
	nerrSuccess          uint32 = 0
	errorAliasExists     uint32 = 1379
	nerrGroupExists      uint32 = 2223
	userPrivUser         uint32 = 1
	ufScript             uint32 = 0x0001
	ufDontExpirePassword uint32 = 0x10000
)

var (
	modnetapi32                 = windows.NewLazySystemDLL("netapi32.dll")
	procNetLocalGroupAdd        = modnetapi32.NewProc("NetLocalGroupAdd")
	procNetLocalGroupAddMembers = modnetapi32.NewProc("NetLocalGroupAddMembers")
	procNetUserAdd              = modnetapi32.NewProc("NetUserAdd")
	procNetUserSetInfo          = modnetapi32.NewProc("NetUserSetInfo")
)

type localGroupInfo1 struct {
	Name    *uint16
	Comment *uint16
}

type localGroupMembersInfo3 struct {
	DomainAndName *uint16
}

type userInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

type userInfo1003 struct {
	Password *uint16
}

func EnsureLocalUser(name string, userPassword string, log io.Writer) error {
	nameW, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	passwordW, err := windows.UTF16PtrFromString(userPassword)
	if err != nil {
		return err
	}
	info := userInfo1{
		Name:     nameW,
		Password: passwordW,
		Priv:     userPrivUser,
		Flags:    ufScript | ufDontExpirePassword,
	}
	status, _ := netUserAdd(&info)
	if status != nerrSuccess {
		passwordInfo := userInfo1003{Password: passwordW}
		updateStatus := netUserSetInfo(nameW, &passwordInfo)
		if updateStatus != nerrSuccess {
			logSetupLine(log, fmt.Sprintf("NetUserSetInfo failed for %s code %d", name, updateStatus))
			return fmt.Errorf("failed to create/update user %s, code %d/%d", name, status, updateStatus)
		}
	}
	if usersGroup, err := LookupAccountNameForSID("S-1-5-32-545"); err == nil {
		groupW, err := windows.UTF16PtrFromString(usersGroup)
		if err == nil {
			_ = netLocalGroupAddMembers(groupW, nameW)
		}
	} else {
		logSetupLine(log, "LookupAccountSidW failed for Users SID; skipping Users group membership")
	}
	return nil
}

func EnsureLocalGroup(name string, comment string, log io.Writer) error {
	nameW, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	commentW, err := windows.UTF16PtrFromString(comment)
	if err != nil {
		return err
	}
	info := localGroupInfo1{Name: nameW, Comment: commentW}
	status, parmErr := netLocalGroupAdd(&info)
	if status != nerrSuccess && status != errorAliasExists && status != nerrGroupExists {
		logSetupLine(log, fmt.Sprintf("NetLocalGroupAdd failed for %s code %d parm_err=%d", name, status, parmErr))
		return fmt.Errorf("failed to create local group %s, code %d", name, status)
	}
	return nil
}

func EnsureLocalGroupMember(groupName string, memberName string) error {
	groupW, err := windows.UTF16PtrFromString(groupName)
	if err != nil {
		return err
	}
	memberW, err := windows.UTF16PtrFromString(memberName)
	if err != nil {
		return err
	}
	_ = netLocalGroupAddMembers(groupW, memberW)
	return nil
}

func ResolveSandboxUsersGroupSID() ([]byte, error) {
	return ResolveSID(SandboxUsersGroup)
}

func ResolveSID(name string) ([]byte, error) {
	if sidString, ok := wellKnownSIDString(name); ok {
		return windowssandbox.SIDBytesFromString(sidString)
	}
	sid, _, _, err := windows.LookupSID("", name)
	if err != nil {
		return nil, err
	}
	return copySIDForSetupUsers(sid), nil
}

func LookupAccountNameForSID(sidString string) (string, error) {
	sid, err := windows.StringToSid(sidString)
	if err != nil {
		return "", err
	}
	account, _, _, err := sid.LookupAccount("")
	return account, err
}

func wellKnownSIDString(name string) (string, bool) {
	switch name {
	case "Administrators":
		return "S-1-5-32-544", true
	case "Users":
		return "S-1-5-32-545", true
	case "Authenticated Users":
		return "S-1-5-11", true
	case "Everyone":
		return "S-1-1-0", true
	case "SYSTEM":
		return "S-1-5-18", true
	default:
		return "", false
	}
}

func netLocalGroupAdd(info *localGroupInfo1) (uint32, uint32) {
	var parmErr uint32
	r1, _, _ := procNetLocalGroupAdd.Call(
		0,
		1,
		uintptr(unsafe.Pointer(info)),
		uintptr(unsafe.Pointer(&parmErr)),
	)
	return uint32(r1), parmErr
}

func netLocalGroupAddMembers(groupName *uint16, memberName *uint16) uint32 {
	member := localGroupMembersInfo3{DomainAndName: memberName}
	r1, _, _ := procNetLocalGroupAddMembers.Call(
		0,
		uintptr(unsafe.Pointer(groupName)),
		3,
		uintptr(unsafe.Pointer(&member)),
		1,
	)
	return uint32(r1)
}

func netUserAdd(info *userInfo1) (uint32, uint32) {
	var parmErr uint32
	r1, _, _ := procNetUserAdd.Call(
		0,
		1,
		uintptr(unsafe.Pointer(info)),
		uintptr(unsafe.Pointer(&parmErr)),
	)
	return uint32(r1), parmErr
}

func netUserSetInfo(name *uint16, info *userInfo1003) uint32 {
	r1, _, _ := procNetUserSetInfo.Call(
		0,
		uintptr(unsafe.Pointer(name)),
		1003,
		uintptr(unsafe.Pointer(info)),
		0,
	)
	return uint32(r1)
}

func copySIDForSetupUsers(sid *windows.SID) []byte {
	if sid == nil || !sid.IsValid() {
		return nil
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(sid)), sid.Len())
	out := make([]byte, len(data))
	copy(out, data)
	return out
}
