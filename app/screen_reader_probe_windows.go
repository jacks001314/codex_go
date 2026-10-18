//go:build windows

package app

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const spiGetScreenReader = 0x0046

var screenReaderUser32 = windows.NewLazySystemDLL("user32.dll")
var procSystemParametersInfoW = screenReaderUser32.NewProc("SystemParametersInfoW")

func screenReaderProbe() bool {
	var enabled uint32
	ret, _, _ := procSystemParametersInfoW.Call(
		spiGetScreenReader,
		0,
		uintptr(unsafe.Pointer(&enabled)),
		0,
	)
	if ret != 0 && enabled != 0 {
		return true
	}
	return narratorRunningInCurrentSession()
}

func narratorRunningInCurrentSession() bool {
	currentSession, err := processSession(uint32(os.Getpid()))
	if err != nil {
		return false
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return false
	}
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name, "Narrator.exe") {
			if session, err := processSession(entry.ProcessID); err == nil && session == currentSession {
				return true
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			return false
		}
	}
}

func processSession(pid uint32) (uint32, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(pid, &session); err != nil {
		return 0, err
	}
	return session, nil
}
